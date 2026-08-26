package boundaries

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func TestScanAPIInfrastructureImportsAllowsCurrentRoots(t *testing.T) {
	root := t.TempDir()
	for _, currentRoot := range []string{"app", "kernel", "capabilities", "platform"} {
		if err := os.MkdirAll(filepath.Join(root, "internal", currentRoot), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	report := &report.Report{}
	ScanAPIInfrastructureImports(root, &policy.Policy{}, report)
	if len(report.Violations) != 0 {
		t.Fatalf("current roots produced violations: %#v", report.Violations)
	}
}

func TestScanAPIInfrastructureImportsRejectsRetiredRoots(t *testing.T) {
	root := t.TempDir()
	for _, retiredRoot := range []string{"api", "application", "domain", "infrastructure"} {
		if err := os.MkdirAll(filepath.Join(root, "internal", retiredRoot), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	report := &report.Report{}
	ScanAPIInfrastructureImports(root, &policy.Policy{}, report)
	if len(report.Violations) != 4 {
		t.Fatalf("violations = %d, want 4: %#v", len(report.Violations), report.Violations)
	}
	for _, violation := range report.Violations {
		if violation.Rule != "percheck_legacy_root_ban" || violation.Severity != "error" {
			t.Fatalf("unexpected violation: %#v", violation)
		}
	}
}
