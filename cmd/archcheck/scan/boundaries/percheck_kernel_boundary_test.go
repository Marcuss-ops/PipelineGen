package boundaries

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func TestScanKernelBoundaryRejectsTechnologyAndOuterLayerImports(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "kernel", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "bad.go")
	const source = `package demo
import (
 "database/sql"
 "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
 "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
)
`
	if err := os.WriteFile(file, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	var r report.Report
	ScanKernelBoundary(root, nil, &r)
	if len(r.Violations) != 3 {
		t.Fatalf("expected 3 violations, got %#v", r.Violations)
	}
}

func TestScanKernelBoundaryIgnoresAllowedImports(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "kernel", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "ok.go")
	const source = `package demo
import (
 "fmt"
 "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)
`
	if err := os.WriteFile(file, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	var r report.Report
	ScanKernelBoundary(root, nil, &r)
	if len(r.Violations) != 0 {
		t.Fatalf("expected no violations, got %#v", r.Violations)
	}
}
