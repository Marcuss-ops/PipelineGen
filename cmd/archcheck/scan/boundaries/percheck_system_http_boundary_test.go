package boundaries

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func scanSystemHTTP(t *testing.T, root string) *report.Report {
	t.Helper()
	r := &report.Report{}
	ScanSystemHTTPBoundary(root, &policy.Policy{}, r)
	return r
}

func systemHTTPViolations(r *report.Report) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == systemHTTPBoundaryRule {
			out = append(out, v)
		}
	}
	return out
}

func TestScanSystemHTTPBoundaryFlagsCapabilityImport(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/system/bad.go", `package system

import "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"

var _ httpserver.BuildContext
`)
	if got := systemHTTPViolations(scanSystemHTTP(t, tmp)); len(got) != 1 {
		t.Fatalf("violations = %d, want 1: %+v", len(got), got)
	}
}

func TestScanSystemHTTPBoundaryAllowsHealthRouterDirection(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/platform/httpserver/routes.go", `package httpserver

import health "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system/health"

var _ health.Service
`)
	writeGoFile(t, tmp, "internal/capabilities/system/clean.go", "package system\n")
	writeGoFile(t, tmp, "internal/capabilities/system/bad_test.go", `package system

import "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"

var _ httpserver.BuildContext
`)
	if got := systemHTTPViolations(scanSystemHTTP(t, tmp)); len(got) != 0 {
		t.Fatalf("router→health and test files must not fire, got %+v", got)
	}
}

func TestScanSystemHTTPBoundaryRepoTreeIsClean(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("repository root not resolvable: %v", err)
	}
	if got := systemHTTPViolations(scanSystemHTTP(t, root)); len(got) != 0 {
		t.Fatalf("system capability imported platform/httpserver: %+v", got)
	}
}
