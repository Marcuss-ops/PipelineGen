package boundaries

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func TestScanSQLiteAssetsClipsDuplicateBan_AllowsIdempotencyAdapter(t *testing.T) {
	root := t.TempDir()
	writeSQLiteClipsFixture(t, root, "idempotency_repository.go", "package clips\n")
	writeSQLiteClipsFixture(t, root, "idempotency_repository_test.go", "package clips\n")

	r := &report.Report{}
	ScanSQLiteAssetsClipsDuplicateBan(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Fatalf("allowed idempotency files produced violations: %#v", r.Violations)
	}
}

func TestScanSQLiteAssetsClipsDuplicateBan_RejectsNewImplementationFile(t *testing.T) {
	root := t.TempDir()
	writeSQLiteClipsFixture(t, root, "clips_repository.go", "package clips\n\ntype ClipsRepository struct{}\n")

	r := &report.Report{}
	ScanSQLiteAssetsClipsDuplicateBan(root, &policy.Policy{}, r)
	if len(r.Violations) != 1 || r.Violations[0].Rule != sqliteAssetsClipsDuplicateRule {
		t.Fatalf("want one duplicate-file violation, got %#v", r.Violations)
	}
	if !strings.Contains(r.Violations[0].Note, "canonical parent assets") {
		t.Fatalf("unexpected note: %q", r.Violations[0].Note)
	}
}

func TestScanSQLiteAssetsClipsDuplicateBan_RejectsRetiredImport(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "app", "wire.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package app\n\nimport _ \""+retiredSQLiteClipsImportPath+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &report.Report{}
	ScanSQLiteAssetsClipsDuplicateBan(root, &policy.Policy{}, r)
	if len(r.Violations) != 1 || r.Violations[0].Rule != sqliteAssetsClipsImportRule {
		t.Fatalf("want one retired-import violation, got %#v", r.Violations)
	}
	if r.Violations[0].Line != 3 {
		t.Fatalf("want import line 3, got %#v", r.Violations[0])
	}
}

func writeSQLiteClipsFixture(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(retiredSQLiteClipsDir), name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
