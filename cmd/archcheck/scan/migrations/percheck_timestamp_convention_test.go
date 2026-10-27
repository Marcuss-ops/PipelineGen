package migrations

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeTimestampFixture writes a minimal clean tree for the timestamp gate.
func writeTimestampFixture(t *testing.T) string {
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
	write(filepath.Join("docs", "migrations", "TIMESTAMP_CONVENTION.md"),
		"# test\nDEFAULT ("+canonicalTimestampDefault+")\n")
	write(filepath.Join("docs", "migrations", "timestamp-convention-allowlist.txt"),
		"# test allowlist\nmigrations/sqlite/100_old_style.sql\n")
	write(filepath.Join("migrations", "sqlite", "100_old_style.sql"),
		"CREATE TABLE t (created_at TEXT NOT NULL DEFAULT (datetime('now')));\n")
	write(filepath.Join("migrations", "sqlite", "101_clean.sql"),
		"CREATE TABLE t2 (\n    id TEXT PRIMARY KEY,\n    created_at TEXT NOT NULL DEFAULT ("+canonicalTimestampDefault+"),\n    finished_at TEXT\n);\n")
	return root
}

func timestampRules(rep *report.Report) map[string]int {
	out := map[string]int{}
	for _, v := range rep.Violations {
		if v.Rule == timestampConventionRule {
			out[v.MatchedRule]++
		}
	}
	return out
}

func TestScanTimestampConventionAcceptsCleanTree(t *testing.T) {
	root := writeTimestampFixture(t)
	r := &report.Report{}
	ScanTimestampConvention(root, &policy.Policy{}, r)
	for _, v := range r.Violations {
		t.Fatalf("unexpected violation: %#v", v)
	}
}

func TestScanTimestampConventionRejectsMixedFormatDefault(t *testing.T) {
	root := writeTimestampFixture(t)
	path := filepath.Join(root, "migrations", "sqlite", "102_bad.sql")
	if err := os.WriteFile(path, []byte("CREATE TABLE t3 (created_at TEXT NOT NULL DEFAULT (datetime('now')));\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &report.Report{}
	ScanTimestampConvention(root, &policy.Policy{}, r)
	if got := timestampRules(r)["timestamp_mixed_format_default"]; got != 1 {
		t.Fatalf("timestamp_mixed_format_default = %d, want 1 (report=%+v)", got, r.Violations)
	}
}

func TestScanTimestampConventionRejectsEmptyStringDefault(t *testing.T) {
	root := writeTimestampFixture(t)
	path := filepath.Join(root, "migrations", "sqlite", "103_bad_empty.sql")
	if err := os.WriteFile(path, []byte("CREATE TABLE t4 (lease_expiry TEXT NOT NULL DEFAULT '');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &report.Report{}
	ScanTimestampConvention(root, &policy.Policy{}, r)
	if got := timestampRules(r)["timestamp_empty_string_default"]; got != 1 {
		t.Fatalf("timestamp_empty_string_default = %d, want 1 (report=%+v)", got, r.Violations)
	}
}

// A DEFAULT ” on a NON-timestamp column is legitimate and must pass.
func TestScanTimestampConventionIgnoresNonTimestampColumns(t *testing.T) {
	root := writeTimestampFixture(t)
	path := filepath.Join(root, "migrations", "sqlite", "104_ok_empty.sql")
	if err := os.WriteFile(path, []byte("CREATE TABLE t5 (name TEXT NOT NULL DEFAULT '', note TEXT NOT NULL DEFAULT '');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &report.Report{}
	ScanTimestampConvention(root, &policy.Policy{}, r)
	for _, v := range r.Violations {
		t.Fatalf("unexpected violation for non-timestamp columns: %#v", v)
	}
}

func TestScanTimestampConventionRejectsStaleAllowlistEntry(t *testing.T) {
	root := writeTimestampFixture(t)
	// Clean the allowlisted file: its entry goes stale.
	path := filepath.Join(root, "migrations", "sqlite", "100_old_style.sql")
	if err := os.WriteFile(path, []byte("CREATE TABLE t (id TEXT);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &report.Report{}
	ScanTimestampConvention(root, &policy.Policy{}, r)
	if got := timestampRules(r)["timestamp_allowlist_stale"]; got != 1 {
		t.Fatalf("timestamp_allowlist_stale = %d, want 1 (report=%+v)", got, r.Violations)
	}
}

func TestScanTimestampConventionFailsClosedOnMissingDoc(t *testing.T) {
	root := writeTimestampFixture(t)
	if err := os.Remove(filepath.Join(root, "docs", "migrations", "TIMESTAMP_CONVENTION.md")); err != nil {
		t.Fatal(err)
	}
	r := &report.Report{}
	ScanTimestampConvention(root, &policy.Policy{}, r)
	if got := timestampRules(r)["timestamp_doc_missing"]; got != 1 {
		t.Fatalf("timestamp_doc_missing = %d, want 1 (report=%+v)", got, r.Violations)
	}
}
