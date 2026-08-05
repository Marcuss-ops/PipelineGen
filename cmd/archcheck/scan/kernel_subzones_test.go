package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func TestScanKernelSubzoneIntegrity_AllDeclaredAndPresent(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"asset", "observability"} {
		if err := os.MkdirAll(filepath.Join(root, "internal", "kernel", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	r := &report.Report{}
	ScanKernelSubzoneIntegrity(root, &policy.Policy{KernelSubzones: []string{"asset", "observability"}}, r)

	if len(r.Violations) != 0 {
		t.Fatalf("complete declared kernel layout must pass, got %#v", r.Violations)
	}
}

func TestScanKernelSubzoneIntegrity_RejectsPresentUndeclaredSubzone(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "kernel", "observability"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := &report.Report{}
	ScanKernelSubzoneIntegrity(root, &policy.Policy{KernelSubzones: []string{"asset"}}, r)

	if !hasKernelViolation(r, kernelSubzoneUndeclaredRule, "internal/kernel/observability") {
		t.Fatalf("present undeclared subzone must fail closed, got %#v", r.Violations)
	}
}

func TestScanKernelSubzoneIntegrity_RejectsDeclaredMissingSubzone(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "kernel", "asset"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := &report.Report{}
	ScanKernelSubzoneIntegrity(root, &policy.Policy{KernelSubzones: []string{"asset", "observability"}}, r)

	if !hasKernelViolation(r, kernelSubzoneMissingRule, "internal/kernel/observability") {
		t.Fatalf("declared missing subzone must fail closed, got %#v", r.Violations)
	}
}

func TestScanKernelSubzoneIntegrity_RejectsMissingKernelRoot(t *testing.T) {
	root := t.TempDir()

	r := &report.Report{}
	ScanKernelSubzoneIntegrity(root, &policy.Policy{KernelSubzones: []string{"asset"}}, r)

	if !hasKernelViolation(r, kernelSubzoneMissingRule, "internal/kernel/asset") {
		t.Fatalf("missing internal/kernel root must fail closed, got %#v", r.Violations)
	}
}

func hasKernelViolation(r *report.Report, rule, directory string) bool {
	for _, violation := range r.Violations {
		if violation.Rule == rule && violation.Directory == directory && violation.Severity == "error" {
			return true
		}
	}
	return false
}
