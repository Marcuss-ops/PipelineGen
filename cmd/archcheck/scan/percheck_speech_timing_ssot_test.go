// Package scan — percheck_speech_timing_ssot_test.go pins the
// forward-prevention contract for the SpeechTimingArtifact builder ban.
package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func speechTimingTestReport() *report.Report {
	return &report.Report{Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}}}
}

func speechTimingWriteTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, contents := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %q: %v", full, err)
		}
	}
}

func speechTimingViolations(r *report.Report) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == speechTimingSSOTRule {
			out = append(out, v)
		}
	}
	return out
}

func TestSpeechTimingSSOT_DirectLiteralViolates(t *testing.T) {
	dir := t.TempDir()
	speechTimingWriteTree(t, dir, map[string]string{
		"internal/foo/timing.go": `package foo
var _ = audio.SpeechTimingArtifact{Words: nil}
`,
	})
	r := speechTimingTestReport()
	ScanSpeechTimingSSOT(dir, &policy.Policy{}, r)
	if got := len(speechTimingViolations(r)); got != 1 {
		t.Fatalf("want 1 violation, got %d: %+v", got, r.Violations)
	}
}

func TestSpeechTimingSSOT_CanonicalBuilderExempt(t *testing.T) {
	dir := t.TempDir()
	speechTimingWriteTree(t, dir, map[string]string{
		"internal/capabilities/audio/speech_artifact.go": `package audio
func BuildSpeechTimingArtifact() *SpeechTimingArtifact { return &SpeechTimingArtifact{} }
`,
	})
	r := speechTimingTestReport()
	ScanSpeechTimingSSOT(dir, &policy.Policy{}, r)
	if got := len(speechTimingViolations(r)); got != 0 {
		t.Fatalf("want 0 violations in canonical builder, got %d: %+v", got, r.Violations)
	}
}

func TestSpeechTimingSSOT_TestFilesExempt(t *testing.T) {
	dir := t.TempDir()
	speechTimingWriteTree(t, dir, map[string]string{
		"internal/foo/timing_test.go": `package foo
var _ = audio.SpeechTimingArtifact{}
`,
	})
	r := speechTimingTestReport()
	ScanSpeechTimingSSOT(dir, &policy.Policy{}, r)
	if got := len(speechTimingViolations(r)); got != 0 {
		t.Fatalf("want 0 violations in test files, got %d", got)
	}
}
