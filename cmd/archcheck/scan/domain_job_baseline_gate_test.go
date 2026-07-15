package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func TestScanDomainJobImportBaselineRejectsGrowth(t *testing.T) {
	root := t.TempDir()
	writeDomainJobMigration(t, root, 0)
	writeTestGoFile(t, root, "internal/application/assets/consumer.go", fmt.Sprintf(
		"package assets\nimport job %q\nvar _ = job.Status(\"\")\n",
		testDomainJobImport,
	))

	r := &report.Report{}
	ScanDomainJobBaselineRatchet(root, &policy.Policy{}, r, true)
	if len(r.Violations) != 1 {
		t.Fatalf("expected one baseline violation, got %#v", r.Violations)
	}
	got := r.Violations[0]
	if got.MatchedRule != "domain_job_import_baseline_exceeded" || got.Severity != "error" {
		t.Fatalf("unexpected baseline violation: %#v", got)
	}
	if got.ActualCount != 1 || got.AllowedCount != 0 {
		t.Fatalf("unexpected counts: %#v", got)
	}
}

func TestScanDomainJobImportBaselineAllowsRegisteredCount(t *testing.T) {
	root := t.TempDir()
	writeDomainJobMigration(t, root, 1)
	writeTestGoFile(t, root, "internal/application/assets/consumer.go", fmt.Sprintf(
		"package assets\nimport job %q\nvar _ = job.Status(\"\")\n",
		testDomainJobImport,
	))

	r := &report.Report{}
	ScanDomainJobBaselineRatchet(root, &policy.Policy{}, r, true)
	if len(r.Violations) != 0 {
		t.Fatalf("expected registered baseline to pass, got %#v", r.Violations)
	}
}

func writeDomainJobMigration(t *testing.T, root string, baseline int) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(domainJobMigrationPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf(`{
  "version": 1,
  "id": "TEST-JOB-MIGRATION",
  "status": "in_progress",
  "owner": "internal/kernel/job",
  "deadline": "2026-09-30",
  "compatibility_import": %q,
  "canonical_import": "github.com/Marcuss-ops/PipelineGen/internal/kernel/job",
  "reported_baseline_imports": %d,
  "migration_order": ["internal/application/assets"],
  "contract_exit": "delete bridge at zero"
}`, testDomainJobImport, baseline)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
