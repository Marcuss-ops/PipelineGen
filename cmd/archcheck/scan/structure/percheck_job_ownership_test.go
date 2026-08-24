package structure

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func writeJobOwnershipFixture(t *testing.T, root, jobs string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "architecture", "ownership"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, jobOwnershipSourcePath), []byte(jobs), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeRuntimeFixture(t *testing.T, root, source string) {
	t.Helper()
	path := filepath.Join(root, "internal", "application", "runtime", "runtime.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package runtime\n\n"+source+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanJobOwnership_PassesDeclaredRuntimeAndHandler(t *testing.T) {
	root := t.TempDir()
	writeJobOwnershipFixture(t, root, "job_handler_map:\n  - job_type: catalog.sync\n")
	writeRuntimeFixture(t, root, `var registered = JobPolicy{Type: TypeCatalogSync}
func bind() { svc.RegisterHandler(appjobs.TypeCatalogSync, appjobs.HandlerFunc(func() {})) }`)

	r := &report.Report{}
	ScanJobOwnership(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Fatalf("expected valid ownership mapping, got violations: %+v", r.Violations)
	}
}

func TestScanJobOwnership_PassesCanonicalNestedJobType(t *testing.T) {
	root := t.TempDir()
	writeJobOwnershipFixture(t, root, "job_handler_map:\n  - job_type: catalog.sync\n")
	writeRuntimeFixture(t, root, `var registered = JobPolicy{Completion: CompletionDeclaration{JobType: TypeCatalogSync}}
func bind() { svc.RegisterHandler(appjobs.TypeCatalogSync, appjobs.HandlerFunc(func() {})) }`)

	r := &report.Report{}
	ScanJobOwnership(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Fatalf("expected canonical nested JobType to pass, got violations: %+v", r.Violations)
	}
}

func TestScanJobOwnership_RejectsMissingRuntimeRegistry(t *testing.T) {
	root := t.TempDir()
	writeJobOwnershipFixture(t, root, "job_handler_map:\n  - job_type: catalog.sync\n")
	writeRuntimeFixture(t, root, `func bind() { svc.RegisterHandler(appjobs.TypeCatalogSync, appjobs.HandlerFunc(func() {})) }`)

	r := &report.Report{}
	ScanJobOwnership(root, &policy.Policy{}, r)
	if !hasJobOwnershipMatch(r, "runtime_registry_missing") {
		t.Fatalf("expected runtime_registry_missing, got %+v", r.Violations)
	}
}

func TestScanJobOwnership_RejectsNilRuntimeHandler(t *testing.T) {
	root := t.TempDir()
	writeJobOwnershipFixture(t, root, "job_handler_map:\n  - job_type: catalog.sync\n")
	writeRuntimeFixture(t, root, `var registered = JobPolicy{Type: TypeCatalogSync}
func bind() { svc.RegisterHandler(appjobs.TypeCatalogSync, nil) }`)

	r := &report.Report{}
	ScanJobOwnership(root, &policy.Policy{}, r)
	if !hasJobOwnershipMatch(r, "runtime_handler_missing") {
		t.Fatalf("expected nil handler to be rejected, got %+v", r.Violations)
	}
}

func TestScanJobOwnership_RejectsMissingRuntimeHandler(t *testing.T) {
	root := t.TempDir()
	writeJobOwnershipFixture(t, root, "job_handler_map:\n  - job_type: catalog.sync\n")
	writeRuntimeFixture(t, root, `var registered = JobPolicy{Type: TypeCatalogSync}`)

	r := &report.Report{}
	ScanJobOwnership(root, &policy.Policy{}, r)
	if !hasJobOwnershipMatch(r, "runtime_handler_missing") {
		t.Fatalf("expected runtime_handler_missing, got %+v", r.Violations)
	}
}

func TestScanJobOwnership_RejectsUnmappedDeclaration(t *testing.T) {
	root := t.TempDir()
	writeJobOwnershipFixture(t, root, "job_handler_map:\n  - job_type: stale.job\n")

	r := &report.Report{}
	ScanJobOwnership(root, &policy.Policy{}, r)
	if !hasJobOwnershipMatch(r, "unmapped_runtime_type") {
		t.Fatalf("expected unmapped_runtime_type, got %+v", r.Violations)
	}
}

func hasJobOwnershipMatch(r *report.Report, match string) bool {
	for _, violation := range r.Violations {
		if violation.Rule == jobOwnershipRule && violation.MatchedRule == match {
			return true
		}
	}
	return false
}
