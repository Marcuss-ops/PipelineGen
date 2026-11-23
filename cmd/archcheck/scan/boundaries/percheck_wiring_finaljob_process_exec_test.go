package boundaries

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func scanWiringFinalJobExec(t *testing.T, root string) *report.Report {
	t.Helper()
	r := &report.Report{}
	ScanWiringFinalJobProcessExec(root, &policy.Policy{}, r)
	return r
}

func wiringFinalJobExecViolations(r *report.Report) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == wiringFinalJobExecRule {
			out = append(out, v)
		}
	}
	return out
}

func TestScanWiringFinalJobProcessExecFlagsRootExec(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/app/wiring/ffmpeg.go", `package wiring

import "os/exec"

var _ = exec.Command
`)
	if got := wiringFinalJobExecViolations(scanWiringFinalJobExec(t, tmp)); len(got) != 1 {
		t.Fatalf("violations = %d, want 1: %+v", len(got), got)
	}
}

func TestScanWiringFinalJobProcessExecIgnoresTestsAndSubpackages(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/app/wiring/fixture_test.go", `package wiring

import "os/exec"

var _ = exec.Command
`)
	writeGoFile(t, tmp, "internal/app/modules/rendering/handlers.go", `package rendering

import "os/exec"

var _ = exec.Command
`)
	writeGoFile(t, tmp, "internal/app/wiring/clean.go", "package wiring\n")
	if got := wiringFinalJobExecViolations(scanWiringFinalJobExec(t, tmp)); len(got) != 0 {
		t.Fatalf("tests and subpackages must not fire, got %+v", got)
	}
}

func TestScanWiringFinalJobProcessExecRepoTreeIsClean(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("repository root not resolvable: %v", err)
	}
	if got := wiringFinalJobExecViolations(scanWiringFinalJobExec(t, root)); len(got) != 0 {
		t.Fatalf("wiring root imports os/exec: %+v", got)
	}
}
