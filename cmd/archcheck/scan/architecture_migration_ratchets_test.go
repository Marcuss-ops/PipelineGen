package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const testDomainJobImport = "github.com/Marcuss-ops/PipelineGen/internal/domain/" + "job"

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

func TestScanUnknownInternalRootsUsesRegisteredMigration(t *testing.T) {
	root := t.TempDir()
	writeHotspotRegistry(t, root, `{
  "version": 1,
  "hotspots": [],
  "root_migrations": [{
    "path": "internal/youtube",
    "owner": "internal/application/youtube",
    "deadline": "2026-08-31",
    "targets": ["internal/application/youtube", "internal/infrastructure/youtube"]
  }]
}`)
	if err := os.MkdirAll(filepath.Join(root, "internal", "youtube"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := &report.Report{}
	ScanUnknownInternalRoots(root, &policy.Policy{}, r)
	if len(r.Violations) != 1 {
		t.Fatalf("expected one governed migration result, got %#v", r.Violations)
	}
	if r.Violations[0].MatchedRule != "not_in_legacy_or_target_internal_roots" || r.Violations[0].Severity != "warn" {
		t.Fatalf("expected governed legacy-shape warning, got %#v", r.Violations[0])
	}
}

func TestCollectDomainJobImportsProductionOnly(t *testing.T) {
	root := t.TempDir()
	fixture := fmt.Sprintf("package jobs\nimport job %q\nvar _ = job.Status(\"\")\n", testDomainJobImport)
	writeTestGoFile(t, root, "internal/application/jobs/service.go", fixture)
	writeTestGoFile(t, root, "internal/application/jobs/service_test.go", fixture)
	writeTestGoFile(t, root, "cmd/archcheck/scan/self.go", "package scan\n"+fixture)

	sites := collectDomainJobImports(root, testDomainJobImport)
	if len(sites) != 1 {
		t.Fatalf("expected one production import, got %d: %#v", len(sites), sites)
	}
	if sites[0].File != "internal/application/jobs/service.go" {
		t.Fatalf("unexpected import site: %#v", sites[0])
	}
}

func TestParseDomainJobAddedImportsBlocksOnlyProductionAdditions(t *testing.T) {
	diff := fmt.Sprintf(`diff --git a/internal/application/jobs/new.go b/internal/application/jobs/new.go
new file mode 100644
--- /dev/null
+++ b/internal/application/jobs/new.go
@@ -0,0 +1,4 @@
+package jobs
+import job %q
+var _ = job.Status("")
+
diff --git a/internal/application/jobs/new_test.go b/internal/application/jobs/new_test.go
new file mode 100644
--- /dev/null
+++ b/internal/application/jobs/new_test.go
@@ -0,0 +1,2 @@
+package jobs
+import job %q
`, testDomainJobImport, testDomainJobImport)

	hits := parseDomainJobAddedImports(diff, testDomainJobImport)
	if len(hits) != 1 {
		t.Fatalf("expected one blocked addition, got %d: %#v", len(hits), hits)
	}
	if hits[0].File != "internal/application/jobs/new.go" || hits[0].Line != 2 {
		t.Fatalf("unexpected blocked addition: %#v", hits[0])
	}
}

func TestAddedLineImportsDomainJobRejectsCommentsAndCanonicalPath(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{fmt.Sprintf("job %q", testDomainJobImport), true},
		{fmt.Sprintf("%q", testDomainJobImport+"/workspace"), true},
		{fmt.Sprintf("// %q", testDomainJobImport), false},
		{`job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"`, false},
	}
	for _, tc := range cases {
		if got := addedLineImportsDomainJob(tc.line, testDomainJobImport); got != tc.want {
			t.Fatalf("addedLineImportsDomainJob(%q)=%v want %v", tc.line, got, tc.want)
		}
	}
}

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
