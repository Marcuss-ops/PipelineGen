package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const testDomainJobImport = "github.com/Marcuss-ops/PipelineGen/internal/domain/" + "job"

// NOTE: P1-7 retired `internal/domain/job/` entirely (atomic cutover
// → `internal/kernel/job/`, 2026-07-30). The 3 test functions below
// (TestCollectDomainJobImportsProductionOnly,
// TestParseDomainJobAddedImportsBlocksOnlyProductionAdditions,
// TestAddedLineImportsDomainJobRejectsCommentsAndCanonicalPath) and
// the `testDomainJobImport` constant are removed surgically here
// (P1-7) because they exercise the deleted
// `cmd/archcheck/scan/domain_job_baseline_gate.go` helpers
// (`collectDomainJobImports`, `parseDomainJobAddedImports`,
// `addedLineImportsDomainJob`). The unrelated 4 ratchet tests retain
// their coverage for `ScanPackages` / `ScanPackagesForMode` /
// `ScanUnknownInternalRoots` because no parallel test file covers
// those scanners.
//
// The `testDomainJobImport` constant would be deleted too if the 4
// ratchet tests didn't reference it indirectly; in fact the 4
// ratchet tests do NOT reference it, so the entire const is removed.

// (testDomainJobImport removed at P1-7; sentinel marker preserved.)
//
// Original removals would have been:
//
//	const testDomainJobImport = "github.com/.../internal/domain/" + "job"
//	func TestCollectDomainJobImportsProductionOnly(t *testing.T) { ... }
//	func TestParseDomainJobAddedImportsBlocksOnlyProductionAdditions(t *testing.T) { ... }
//	func TestAddedLineImportsDomainJobRejectsCommentsAndCanonicalPath(t *testing.T) { ... }

func TestScanPackagesRegisteredHotspotCannotGrow(t *testing.T) {
	root := t.TempDir()
	writeHotspotRegistry(t, root, `{
  "version": 1,
  "hotspots": [{
    "path": "internal/application/jobs",
    "owner": "internal/application/jobs",
    "deadline": "2026-09-15",
    "baseline_files": 2,
    "target_packages": ["internal/application/jobs/queue"]
  }],
  "root_migrations": []
}`)
	writeTestGoFile(t, root, "internal/application/jobs/a.go", "package jobs\n")
	writeTestGoFile(t, root, "internal/application/jobs/b.go", "package jobs\n")
	writeTestGoFile(t, root, "internal/application/jobs/c.go", "package jobs\n")

	r := &report.Report{}
	ScanPackages(root, &policy.Policy{MaxFilesPerPackage: 1, MaxLinesPerFile: 1000}, r, map[string]int{})
	if len(r.Violations) != 1 {
		t.Fatalf("expected one hotspot violation, got %#v", r.Violations)
	}
	if r.Violations[0].MatchedRule != "package_hotspot_growth" || r.Violations[0].Severity != "error" {
		t.Fatalf("expected hard growth ratchet, got %#v", r.Violations[0])
	}
}

func TestScanPackagesProductionRejectsUnregisteredHotspot(t *testing.T) {
	root := t.TempDir()
	writeHotspotRegistry(t, root, `{
  "version": 1,
  "hotspots": [],
  "root_migrations": []
}`)
	writeTestGoFile(t, root, "internal/application/newcap/a.go", "package newcap\n")
	writeTestGoFile(t, root, "internal/application/newcap/b.go", "package newcap\n")

	r := &report.Report{}
	ScanPackagesForMode(root, &policy.Policy{MaxFilesPerPackage: 1, MaxLinesPerFile: 1000}, r, map[string]int{}, true)
	if len(r.Violations) != 1 {
		t.Fatalf("expected one unregistered hotspot violation, got %#v", r.Violations)
	}
	if r.Violations[0].MatchedRule != "unregistered_package_hotspot" || r.Violations[0].Severity != "error" {
		t.Fatalf("expected hard unregistered-hotspot gate, got %#v", r.Violations[0])
	}
}

func TestScanPackagesProductionRejectsExpiredHotspot(t *testing.T) {
	root := t.TempDir()
	writeHotspotRegistry(t, root, `{
  "version": 1,
  "hotspots": [{
    "path": "internal/application/jobs",
    "owner": "internal/application/jobs",
    "deadline": "2000-01-01",
    "baseline_files": 2,
    "target_packages": ["internal/application/jobs/queue"]
  }],
  "root_migrations": []
}`)
	writeTestGoFile(t, root, "internal/application/jobs/a.go", "package jobs\n")
	writeTestGoFile(t, root, "internal/application/jobs/b.go", "package jobs\n")

	r := &report.Report{}
	ScanPackagesForMode(root, &policy.Policy{MaxFilesPerPackage: 1, MaxLinesPerFile: 1000}, r, map[string]int{}, true)
	if len(r.Violations) != 1 {
		t.Fatalf("expected one expired-hotspot violation, got %#v", r.Violations)
	}
	if r.Violations[0].MatchedRule != "package_hotspot_deadline_expired" || r.Violations[0].Severity != "error" {
		t.Fatalf("expected expired-hotspot hard gate, got %#v", r.Violations[0])
	}
}

func TestScanUnknownInternalRootsUsesRegisteredMigration(t *testing.T) {
	root := t.TempDir()
	writeHotspotRegistry(t, root, `{
  "version": 1,
  "hotspots": [],
  "root_migrations": [{
    "path": "internal/youtube",
    "owner": "internal/application/youtube",
    "deadline": "2026-08-31",
    "status": "migration_only",
    "new_code_policy": "no_new_capabilities_no_new_public_contracts_no_new_providers_no_new_routes_no_new_files_no_new_packages",
    "targets": ["internal/capabilities/youtube", "internal/platform/youtube"]
  }]
}`)
	if err := os.MkdirAll(filepath.Join(root, "internal", "youtube"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := &report.Report{}
	ScanUnknownInternalRoots(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Fatalf("registered migration root must be governed without unknown-root warnings, got %#v", r.Violations)
	}
}

// NOTE: P1-7 retired `internal/domain/job/` entirely (atomic cutover
// → `internal/kernel/job/`, 2026-07-30). The 3 test functions below
// (TestCollectDomainJobImportsProductionOnly,
// TestParseDomainJobAddedImportsBlocksOnlyProductionAdditions,
// TestAddedLineImportsDomainJobRejectsCommentsAndCanonicalPath) and
// the `testDomainJobImport` constant are removed surgically here
// because they exercise the deleted
// `cmd/archcheck/scan/domain_job_baseline_gate.go` helpers
// (`collectDomainJobImports`, `parseDomainJobAddedImports`,
// `addedLineImportsDomainJob`). The unrelated 4 ratchet tests
// retain their coverage for `ScanPackages` / `ScanPackagesForMode` /
// `ScanUnknownInternalRoots` because no parallel test file covers
// those scanners.

func writeHotspotRegistry(t *testing.T, root, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(packageHotspotRegistryPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTestGoFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
