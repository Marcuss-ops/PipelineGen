package governance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func TestObservabilityOperationSSOTRejectsSecondWriter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "internal", "foo", "writer.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package foo\nvar q = `INSERT INTO performance_operations`\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &report.Report{}
	ScanObservabilityOperationSSOT(dir, &policy.Policy{}, r)
	if len(r.Violations) != 1 || r.Violations[0].Rule != observabilityOperationSSOTRule {
		t.Fatalf("violations = %+v", r.Violations)
	}
}

func TestObservabilityOperationSSOTAllowsCanonicalProjectionStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "internal", "platform", "sqlite", "performance", "operations.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package performance\nvar q = `INSERT INTO performance_operations`\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &report.Report{}
	ScanObservabilityOperationSSOT(dir, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Fatalf("canonical store was rejected: %+v", r.Violations)
	}
}
