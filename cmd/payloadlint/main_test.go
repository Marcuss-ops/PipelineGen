package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const coherentEnvelope = `{
  "version": 2,
  "preset": "custom",
  "correlation_id": "lint-ok",
  "final_job": false,
  "items": [{
    "id": "item-1",
    "title": "Coherent",
    "language": "it",
    "source": {"type": "text", "topic": "t", "source_text": "testo"},
    "script_params": {"target_words": 120, "images_per_scene": 3,
      "segments": [{"topic": "a", "target_words": 60}, {"topic": "b", "target_words": 60}]},
    "audio": {"mode": "COMBINED_TIMELINE", "mix_policy": "VOICEOVER_ONLY"},
    "media_plan": {"extraction": {"image_search_mode": "explicit_then_fallback",
      "entity_images": {"enabled": true}}},
    "output": {"render": {"enabled": true, "drive_subfolder_name": "lint-ok"},
      "save_to_db": true}
  }]
}`

func writeLintFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestLintDirAcceptsCoherentEnvelopeAndSkipsJobDump(t *testing.T) {
	dir := t.TempDir()
	writeLintFile(t, dir, "request-ok.json", coherentEnvelope)
	writeLintFile(t, dir, "job-dump.json", `{"id":"job-1","status":"COMPLETED","current_payload":{}}`)

	report, err := lintDir(dir)
	if err != nil {
		t.Fatalf("lintDir: %v", err)
	}
	if !report.OK || report.Checked != 1 || report.Failed != 0 || report.Skipped != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestLintDirRejectsUnknownContractField(t *testing.T) {
	dir := t.TempDir()
	writeLintFile(t, dir, "request-bad.json", `{
	  "version": 2, "preset": "custom",
	  "items": [{"source": {"type": "text", "topic": "t"},
	    "output": {"drive_subfolder_name": "wrong-level"}}]
	}`)
	report, err := lintDir(dir)
	if err != nil {
		t.Fatalf("lintDir: %v", err)
	}
	if report.OK || report.Failed != 1 {
		t.Fatalf("report = %+v", report)
	}
	if got := strings.Join(report.Payloads[0].Errors, " "); !strings.Contains(got, `unknown field "drive_subfolder_name"`) {
		t.Fatalf("error = %q", got)
	}
}

func TestLintDirRejectsSemanticContradictions(t *testing.T) {
	dir := t.TempDir()
	writeLintFile(t, dir, "request-contradictory.json", `{
	  "version": 2, "preset": "custom",
	  "items": [{
	    "id": "bad",
	    "source": {"type": "text", "topic": "t"},
	    "script_params": {"target_words": 1800, "images_per_scene": 3,
	      "segments": [{"topic": "only", "target_words": 60}]},
	    "audio": {"mode": "COMBINED_TIMELINE"},
	    "media_plan": {"extraction": {"image_search_mode": "disabled",
	        "entity_images": {"enabled": true}},
	      "planner": {"max_images_per_scene": 2}},
	    "max_phrase_overlays": 99,
	    "output": {"audio": {"mode": "COMBINED_TIMELINE"}}
	  }]
	}`)
	report, err := lintDir(dir)
	if err != nil {
		t.Fatalf("lintDir: %v", err)
	}
	if report.OK || report.Failed != 1 {
		t.Fatalf("report = %+v", report)
	}
	got := strings.Join(report.Payloads[0].Errors, "\n")
	for _, want := range []string{
		"disabled forbids every image search",
		"audio.mode is set twice",
		"max_phrase_overlays=99 exceeds",
		"images_per_scene=3 disagrees",
		"target_words=1800 disagrees",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestLintDirWarnsOnDuplicateForceRefreshAndStrictFails(t *testing.T) {
	dir := t.TempDir()
	writeLintFile(t, dir, "request-warning.json", strings.Replace(
		coherentEnvelope, `"target_words": 120,`, `"target_words": 120, "force_refresh": true,`, 1))

	report, err := lintDir(dir)
	if err != nil {
		t.Fatalf("lintDir: %v", err)
	}
	if !report.OK || report.Failed != 0 {
		t.Fatalf("non-strict report = %+v", report)
	}
	if len(report.Payloads) != 1 || len(report.Payloads[0].Warnings) != 1 {
		t.Fatalf("warnings = %+v", report.Payloads)
	}
	if got := report.Payloads[0].Warnings[0]; !strings.Contains(got, "FORCE_REFRESH_DUPLICATE") {
		t.Fatalf("warning = %q", got)
	}

	strict, err := lintDirWithOptions(dir, true)
	if err != nil {
		t.Fatalf("lintDirWithOptions: %v", err)
	}
	if strict.OK || strict.Failed != 1 {
		t.Fatalf("strict report = %+v", strict)
	}
}

func TestLintDirEnforcesV3SingleOwner(t *testing.T) {
	canonicalV3 := strings.Replace(coherentEnvelope, `"version": 2`, `"version": 3`, 1)
	clean := t.TempDir()
	writeLintFile(t, clean, "request-v3.json", canonicalV3)
	report, err := lintDir(clean)
	if err != nil {
		t.Fatalf("lintDir: %v", err)
	}
	if !report.OK || report.Checked != 1 {
		t.Fatalf("canonical V3 payload must stay clean: %+v", report)
	}

	duplicate := strings.Replace(canonicalV3,
		`"output": {"render"`,
		`"output": {"audio": {"mode": "COMBINED_TIMELINE"}, "render"`, 1)
	dirty := t.TempDir()
	writeLintFile(t, dirty, "request-v3-dup.json", duplicate)
	report, err = lintDir(dirty)
	if err != nil {
		t.Fatalf("lintDir: %v", err)
	}
	if report.OK || report.Failed != 1 {
		t.Fatalf("a V3 body with two audio owners must fail: %+v", report)
	}
	if got := strings.Join(report.Payloads[0].Errors, " "); !strings.Contains(got, "items[0].output.audio") {
		t.Fatalf("error = %q", got)
	}
}

func TestCanonicalReferencePayloadIsClean(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "ops", "jobs", "canonical")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("outer workspace canonical payload not present: %v", err)
	}
	report, err := lintDir(dir)
	if err != nil {
		t.Fatalf("lintDir: %v", err)
	}
	if !report.OK || report.Failed != 0 {
		t.Fatalf("canonical payload must stay clean: %+v", report)
	}
	if report.Checked == 0 {
		t.Fatal("canonical directory contained no envelope")
	}
}

func TestRunAcceptsPositionalDirectory(t *testing.T) {
	dir := t.TempDir()
	writeLintFile(t, dir, "request-ok.json", coherentEnvelope)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--json", dir}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, exitOK, stderr.String())
	}
	var report lintReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if !report.OK || report.Checked != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunJSONFailureExitsOne(t *testing.T) {
	dir := t.TempDir()
	writeLintFile(t, dir, "request-bad.json", `{"version":2,"preset":"news","items":[]}`)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--dir", dir, "--json"}, &stdout, &stderr)
	if code != exitFailure {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, exitFailure, stderr.String())
	}
	var report lintReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("output is not JSON: %v (%q)", err, stdout.String())
	}
	if report.OK || report.Failed != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunRequiresAnExistingDirectory(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--dir", filepath.Join(t.TempDir(), "missing")}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "payloadlint:") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
