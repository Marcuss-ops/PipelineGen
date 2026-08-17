// Package scan — percheck_duration_probe_ssot_test.go pins the
// forward-prevention contract for the raw ffprobe/ffmpeg exec ban.
package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func durationProbeTestReport() *report.Report {
	return &report.Report{Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}}}
}

func durationProbeWriteTree(t *testing.T, dir string, files map[string]string) {
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

func durationProbeViolations(r *report.Report) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == durationProbeSSOTRule {
			out = append(out, v)
		}
	}
	return out
}

func TestDurationProbeSSOT_RawFfprobeViolates(t *testing.T) {
	dir := t.TempDir()
	durationProbeWriteTree(t, dir, map[string]string{
		"internal/foo/probe.go": `package foo
import "os/exec"
var _ = exec.Command("ffprobe", "-v", "error", "x")
`,
	})
	r := durationProbeTestReport()
	ScanDurationProbeSSOT(dir, &policy.Policy{}, r)
	if got := len(durationProbeViolations(r)); got != 1 {
		t.Fatalf("want 1 violation, got %d: %+v", got, r.Violations)
	}
}

func TestDurationProbeSSOT_RawFfmpegContextViolates(t *testing.T) {
	dir := t.TempDir()
	durationProbeWriteTree(t, dir, map[string]string{
		"internal/foo/probe.go": `package foo
import ("context"; "os/exec")
var _ = exec.CommandContext(context.Background(), "ffmpeg", "-i", "x")
`,
	})
	r := durationProbeTestReport()
	ScanDurationProbeSSOT(dir, &policy.Policy{}, r)
	if got := len(durationProbeViolations(r)); got != 1 {
		t.Fatalf("want 1 violation, got %d: %+v", got, r.Violations)
	}
}

func TestDurationProbeSSOT_CanonicalCapabilityExempt(t *testing.T) {
	dir := t.TempDir()
	durationProbeWriteTree(t, dir, map[string]string{
		"internal/platform/media/rustexec/executor.go": `package rustexec
import "os/exec"
var _ = exec.Command("ffmpeg", "-i", "x")
`,
	})
	r := durationProbeTestReport()
	ScanDurationProbeSSOT(dir, &policy.Policy{}, r)
	if got := len(durationProbeViolations(r)); got != 0 {
		t.Fatalf("want 0 violations in canonical media capability, got %d: %+v", got, r.Violations)
	}
}

func TestDurationProbeSSOT_TestFilesExempt(t *testing.T) {
	dir := t.TempDir()
	durationProbeWriteTree(t, dir, map[string]string{
		"internal/foo/probe_test.go": `package foo
import "os/exec"
var _ = exec.Command("ffprobe", "x")
`,
	})
	r := durationProbeTestReport()
	ScanDurationProbeSSOT(dir, &policy.Policy{}, r)
	if got := len(durationProbeViolations(r)); got != 0 {
		t.Fatalf("want 0 violations in test files, got %d", got)
	}
}
