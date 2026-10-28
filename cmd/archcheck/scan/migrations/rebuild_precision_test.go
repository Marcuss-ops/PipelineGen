package migrations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// A rebuild migration re-creates the table's TRIGGERS. A trigger body is
// runtime logic (it fires per row change): its UPDATE text is not a migration
// rewriting a vocabulary in place. Positive control in the same test: the
// identical UPDATE statement OUTSIDE a trigger is still a state-rewrite
// error — the suppression must not swallow real migration-time rewrites.
func TestScanStateMatrixIgnoresTriggerBodyRuntimeLogic(t *testing.T) {
	const trigger = `CREATE TRIGGER trg_media_assets_asset_state_projection_insert
AFTER INSERT ON media_assets
BEGIN
    UPDATE media_assets
    SET asset_state = CASE
        WHEN NEW.lifecycle_state IN ('DELETED') THEN 'FAILED_PERMANENT'
        WHEN NEW.index_state = 'INDEXED' THEN 'INDEXED'
        ELSE 'DISCOVERED'
    END
    WHERE id = NEW.id;
END;
`
	root := writeStateMatrixFixture(t)
	if err := os.WriteFile(
		filepath.Join(root, "migrations", "sqlite", "104_trigger_rebuild.sql"),
		[]byte(trigger), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	r := &report.Report{}
	ScanStateMatrix(root, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.MatchedRule == "state_rewrite_migration" {
			t.Fatalf("trigger-body runtime logic misclassified as state rewrite: %#v", v)
		}
	}

	// Positive control: the same UPDATE at migration top level IS a rewrite.
	if err := os.WriteFile(
		filepath.Join(root, "migrations", "sqlite", "105_top_level_rewrite.sql"),
		[]byte("UPDATE media_assets\n    SET asset_state = CASE WHEN 1 THEN 'DISCOVERED' ELSE 'DISCOVERED' END\n    WHERE id = 'x';\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	r2 := &report.Report{}
	ScanStateMatrix(root, &policy.Policy{}, r2)
	found := false
	for _, v := range r2.Violations {
		if v.MatchedRule == "state_rewrite_migration" {
			found = true
		}
	}
	if !found {
		t.Fatalf("top-level state rewrite not flagged: %#v", r2.Violations)
	}
}

// A SQLite table rebuild must re-declare the whole table (there is no ALTER
// COLUMN): a FAITHFUL carry-forward of a historical timestamp shape introduces
// no new timestamp debt and must not require an allowlist entry — including
// when the rebuilt table is created under a scratch name and renamed into
// place. Positive control: a genuinely NEW timestamp column with DEFAULT ”
// is still an error.
func TestScanTimestampConventionAllowsFaithfulRebuildCarryForward(t *testing.T) {
	root := writeTimestampFixture(t)
	// Historical declaration (pre-convention shape), allowlisted.
	if err := os.WriteFile(
		filepath.Join(root, "migrations", "sqlite", "102_history.sql"),
		[]byte("CREATE TABLE media_assets (\n    id TEXT PRIMARY KEY,\n    deleted_at TEXT NOT NULL DEFAULT ''\n);\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	// Allowlist must cover the historical declaration.
	allowPath := filepath.Join(root, "docs", "migrations", "timestamp-convention-allowlist.txt")
	allowBody, err := os.ReadFile(allowPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(allowPath, append(allowBody, []byte("migrations/sqlite/102_history.sql\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	// Faithful rebuild: scratch name + rename into place, same shape.
	rebuild := "CREATE TABLE media_assets_103 (\n    id TEXT PRIMARY KEY,\n    deleted_at TEXT NOT NULL DEFAULT ''\n);\n" +
		"INSERT INTO media_assets_103 (id, deleted_at) SELECT id, deleted_at FROM media_assets;\n" +
		"DROP TABLE media_assets;\n" +
		"ALTER TABLE media_assets_103 RENAME TO media_assets;\n"
	if err := os.WriteFile(
		filepath.Join(root, "migrations", "sqlite", "103_rebuild.sql"),
		[]byte(rebuild), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	r := &report.Report{}
	ScanTimestampConvention(root, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if strings.Contains(v.File, "103_rebuild.sql") {
			t.Fatalf("faithful rebuild carry-forward flagged as new debt: %#v", v)
		}
	}

	// Positive control: a NEW timestamp column with DEFAULT '' still offends.
	if err := os.WriteFile(
		filepath.Join(root, "migrations", "sqlite", "104_new_column.sql"),
		[]byte("ALTER TABLE media_assets ADD COLUMN expires_at TEXT NOT NULL DEFAULT '';\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	r2 := &report.Report{}
	ScanTimestampConvention(root, &policy.Policy{}, r2)
	found := false
	for _, v := range r2.Violations {
		if v.MatchedRule == "timestamp_empty_string_default" && strings.Contains(v.File, "104_new_column.sql") {
			found = true
		}
	}
	if !found {
		t.Fatalf("new empty-string timestamp default not flagged: %#v", r2.Violations)
	}
}
