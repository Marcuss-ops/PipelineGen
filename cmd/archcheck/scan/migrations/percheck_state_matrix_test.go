package migrations

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeStateMatrixFixture writes a minimal matrix + allowlist tree the gate
// can run against.
func writeStateMatrixFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join("docs", "migrations"),
		filepath.Join("migrations", "sqlite"),
		filepath.Join("migrations", "sqlite_jobs"),
		filepath.Join("migrations", "postgres"),
	} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join("docs", "migrations", "STATE_MATRIX.md"),
		"# test\n\n<!-- state-matrix:begin -->\n"+
			"| table | column | planes | default | values | casing | terminal |\n"+
			"|---|---|---|---|---|---|---|\n"+
			"| jobs | status | sqlite,sqlite_jobs | - | QUEUED,FAILED | UPPER | FAILED |\n"+
			"| media_assets | lifecycle_state | postgres | ACTIVE | - | UPPER | DELETED |\n"+
			"<!-- state-matrix:end -->\n")
	write(filepath.Join("docs", "migrations", "state-rewrite-allowlist.txt"),
		"# test allowlist\nmigrations/sqlite/100_legacy_rewrite.sql\n")
	write(filepath.Join("migrations", "sqlite", "100_legacy_rewrite.sql"),
		"UPDATE jobs SET status = 'FAILED' WHERE status = 'QUEUED';\n")
	write(filepath.Join("migrations", "sqlite", "101_good.sql"),
		"CREATE TABLE t (id TEXT);\nUPDATE t SET ready = 1 WHERE status = 'x';\n")
	write(filepath.Join("migrations", "postgres", "001_media.sql"),
		"CREATE TABLE media_assets (\n    lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE'\n);\n")
	return root
}

func TestScanStateMatrixAcceptsCleanTree(t *testing.T) {
	root := writeStateMatrixFixture(t)
	r := &report.Report{}
	ScanStateMatrix(root, &policy.Policy{}, r)
	for _, v := range r.Violations {
		t.Fatalf("unexpected violation: %#v", v)
	}
}

func TestScanStateMatrixRejectsRewriteMigration(t *testing.T) {
	root := writeStateMatrixFixture(t)
	path := filepath.Join(root, "migrations", "sqlite", "102_bad_rewrite.sql")
	if err := os.WriteFile(path, []byte("UPDATE jobs SET status = 'CANCELLED' WHERE id = 'x';\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &report.Report{}
	ScanStateMatrix(root, &policy.Policy{}, r)
	found := false
	for _, v := range r.Violations {
		if v.MatchedRule == "state_rewrite_migration" && v.Severity == "error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected state_rewrite_migration error, got %#v", r.Violations)
	}
}

// A WHERE predicate on a state column is not a rewrite and must pass.
func TestScanStateMatrixIgnoresWherePredicates(t *testing.T) {
	root := writeStateMatrixFixture(t)
	path := filepath.Join(root, "migrations", "sqlite", "103_predicate.sql")
	body := "UPDATE jobs SET progress = 1 WHERE status = 'RUNNING' AND lifecycle_state = 'ACTIVE';\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &report.Report{}
	ScanStateMatrix(root, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.MatchedRule == "state_rewrite_migration" {
			t.Fatalf("WHERE predicate misclassified as rewrite: %#v", v)
		}
	}
}

func TestScanStateMatrixRejectsStaleAllowlistEntry(t *testing.T) {
	root := writeStateMatrixFixture(t)
	// Remove the rewrite from the allowlisted file: the entry goes stale.
	path := filepath.Join(root, "migrations", "sqlite", "100_legacy_rewrite.sql")
	if err := os.WriteFile(path, []byte("CREATE TABLE t (id TEXT);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &report.Report{}
	ScanStateMatrix(root, &policy.Policy{}, r)
	found := false
	for _, v := range r.Violations {
		if v.MatchedRule == "state_rewrite_allowlist_stale" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected stale allowlist violation, got %#v", r.Violations)
	}
}

func TestScanStateMatrixRejectsPostgresDrift(t *testing.T) {
	root := writeStateMatrixFixture(t)
	path := filepath.Join(root, "migrations", "postgres", "002_drift.sql")
	body := "ALTER TABLE media_assets ADD COLUMN lifecycle_state TEXT NOT NULL DEFAULT 'ready';\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &report.Report{}
	ScanStateMatrix(root, &policy.Policy{}, r)
	found := false
	for _, v := range r.Violations {
		if v.MatchedRule == "state_matrix_pg_default_drift" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected postgres default drift violation, got %#v", r.Violations)
	}
}

func TestScanStateMatrixFailsClosedOnMissingMatrix(t *testing.T) {
	root := writeStateMatrixFixture(t)
	if err := os.Remove(filepath.Join(root, "docs", "migrations", "STATE_MATRIX.md")); err != nil {
		t.Fatal(err)
	}
	r := &report.Report{}
	ScanStateMatrix(root, &policy.Policy{}, r)
	found := false
	for _, v := range r.Violations {
		if v.MatchedRule == "state_matrix_unreadable" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected fail-closed unreadable-matrix violation, got %#v", r.Violations)
	}
}
