package sqlite

// The verifier's exclusion rules, pinned against the shapes the live database
// actually produced on 2026-09-28:
//
//   - a table whose home moved to another SQLite plane (migration 265 moved the
//     execution plane to `migrations/sqlite_jobs/`, and the data-plane archival
//     script renamed the primary copies to legacy_*), which must NOT be reported
//     on the primary database;
//   - a table an APPLIED ledger row drops through a file that is no longer in
//     the corpus (253_drop_assembly_sessions.sql), where the ledger is the only
//     surviving statement of intent;
//   - an index that is missing, or present under the declared name but owned by
//     a different table — the silent-skip shape that left the restored
//     `job_checkpoints` without `idx_job_checkpoints_job`.
//
// Every rule is a SUPPRESSION rule, so each test needs its positive control:
// the un-suppressed case must still be reported, or the rule would just make the
// whole check quiet.

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap/zaptest"
)

// migratedDB applies a real migration directory and returns the handle plus the
// discovered corpus.
func migratedDB(t *testing.T, rel, scope string) (*sql.DB, string, []migrationFile) {
	t.Helper()
	migrationsDir, err := filepath.Abs(rel)
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "verify.sqlite")
	if err := RunMigrationsOnDB(dbPath, zaptest.NewLogger(t), migrationsDir, scope); err != nil {
		t.Fatalf("RunMigrationsOnDB: %v", err)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	migrations, err := discoverMigrations(migrationsDir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	return db, migrationsDir, migrations
}

// tableGap returns the report entry for a table, if any.
func tableGap(t *testing.T, db *sql.DB, migrations []migrationFile, scope, table string) (declaredTable, bool) {
	t.Helper()
	applied, err := loadAppliedMigrations(db)
	if err != nil {
		t.Fatalf("load applied: %v", err)
	}
	live, err := liveTableNames(db)
	if err != nil {
		t.Fatalf("live tables: %v", err)
	}
	for _, gap := range missingDeclaredTables(migrations, applied, live, scope) {
		if gap.Table == table {
			return gap, true
		}
	}
	return declaredTable{}, false
}

// TestVerifyDoesNotReportAnArchivedExecutionPlaneTable pins the sanctioned-move
// rule with a table the live database really lost that way: `job_events` is
// declared by the primary corpus AND by `migrations/sqlite_jobs/`, and the
// archival moved the primary copy to legacy_job_events.
func TestVerifyDoesNotReportAnArchivedExecutionPlaneTable(t *testing.T) {
	const rel = "../../../migrations/sqlite"
	db, _, migrations := migratedDB(t, rel, "primary")

	// Prove the table is really declared by the primary corpus (otherwise the
	// test would pass for the wrong reason) and that it is on the list under
	// test.
	declaredByPrimary := false
	for _, m := range migrations {
		if !migrationAppliesToTargetDB(m.scope, "primary") {
			continue
		}
		for _, name := range declaredTablesIn(readMigrationFile(t, m.path)) {
			if name == "job_events" {
				declaredByPrimary = true
			}
		}
	}
	if !declaredByPrimary {
		t.Skip("the primary corpus no longer declares job_events — the rule has no subject")
	}
	if !executionPlaneArchivedTables["job_events"] {
		t.Skip("job_events is no longer treated as archived; the rule has no subject")
	}

	if _, err := db.Exec(`DROP TABLE job_events`); err != nil {
		t.Fatalf("drop job_events: %v", err)
	}
	if gap, reported := tableGap(t, db, migrations, "primary", "job_events"); reported {
		t.Fatalf("job_events is sanctioned-archived out of primary and must not be reported, got %+v", gap)
	}
}

// TestSanctionedArchiveListMatchesTheJobsPlaneCorpus keeps the explicit archive
// list anchored: every name it suppresses must be a table the jobs plane really
// owns. A name that no plane declares would turn the list into a place to hide
// drift.
func TestSanctionedArchiveListMatchesTheJobsPlaneCorpus(t *testing.T) {
	jobsDir, err := filepath.Abs("../../../migrations/sqlite_jobs")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	jobsMigrations, err := discoverMigrations(jobsDir)
	if err != nil {
		t.Fatalf("discover jobs plane: %v", err)
	}
	ownedByJobsPlane := map[string]bool{}
	for _, m := range jobsMigrations {
		for _, name := range declaredTablesIn(readMigrationFile(t, m.path)) {
			ownedByJobsPlane[name] = true
		}
	}
	for name := range executionPlaneArchivedTables {
		if !ownedByJobsPlane[name] {
			t.Errorf("%q is treated as archived out of primary but the jobs plane does not declare it", name)
		}
	}
}

// TestVerifyStillReportsAPrimaryOnlyTable is the positive control for the rule
// above: a table the primary history owns and no sibling plane declares must be
// reported when it disappears.
func TestVerifyStillReportsAPrimaryOnlyTable(t *testing.T) {
	const rel = "../../../migrations/sqlite"
	db, _, migrations := migratedDB(t, rel, "primary")
	if _, err := db.Exec(`DROP TABLE job_checkpoints`); err != nil {
		t.Fatalf("drop job_checkpoints: %v", err)
	}
	gap, reported := tableGap(t, db, migrations, "primary", "job_checkpoints")
	if !reported {
		t.Fatal("job_checkpoints is owned by the primary corpus and must be reported when absent")
	}
	if gap.Version == 0 || gap.Filename == "" {
		t.Fatalf("the report must name the declaring file and version, got %+v", gap)
	}
	t.Logf("positive control: table=%s declared_by=%s version=%d", gap.Table, gap.Filename, gap.Version)
}

// TestVerifyHonoursALedgerRowWhoseFileIsGone pins the 253 case: the migration
// that dropped assembly_sessions was removed from the corpus, so only the ledger
// records the intent — and that intent is enough to stop reporting the table.
func TestVerifyHonoursALedgerRowWhoseFileIsGone(t *testing.T) {
	const rel = "../../../migrations/sqlite"
	db, _, migrations := migratedDB(t, rel, "primary")

	// The corpus must NOT contain the drop file, or this test would be testing
	// the SQL-visible rule instead.
	for _, m := range migrations {
		if m.filename == "253_drop_assembly_sessions.sql" {
			t.Skip("253_drop_assembly_sessions.sql is back in the corpus — nothing to pin")
		}
	}

	// The live database carries this ledger row (the newest applied version whose
	// file was removed from the tree); a fresh DB has no such row. Insert it and
	// drop the table to reproduce the archived shape.
	if _, err := db.Exec(`INSERT INTO schema_migrations
		(version, migration_id, filename, checksum, checksum_sha256, applied_at)
		VALUES (253, 900253, '253_drop_assembly_sessions.sql', 'legacy', 'legacy', '2026-08-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert the historical ledger row: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE assembly_sessions`); err != nil {
		t.Fatalf("drop assembly_sessions: %v", err)
	}
	if gap, reported := tableGap(t, db, migrations, "primary", "assembly_sessions"); reported {
		t.Fatalf("assembly_sessions is dropped by an applied ledger row whose file is gone; not drift, got %+v", gap)
	}

	// Positive control: with the same table absent but NO ledger row naming a
	// drop, the checker must still complain.
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE filename = '253_drop_assembly_sessions.sql'`); err != nil {
		t.Fatalf("remove the ledger row: %v", err)
	}
	gap, reported := tableGap(t, db, migrations, "primary", "assembly_sessions")
	if !reported {
		t.Fatal("without the ledger row nothing records the drop; the absence must be reported")
	}
	t.Logf("positive control: table=%s declared_by=%s version=%d", gap.Table, gap.Filename, gap.Version)
}

// indexGap returns the report entry for an index, if any.
func indexGap(t *testing.T, db *sql.DB, migrations []migrationFile, scope, index string) (declaredIndex, bool) {
	t.Helper()
	applied, err := loadAppliedMigrations(db)
	if err != nil {
		t.Fatalf("load applied: %v", err)
	}
	live, err := liveTableNames(db)
	if err != nil {
		t.Fatalf("live tables: %v", err)
	}
	owners, err := liveIndexOwners(db)
	if err != nil {
		t.Fatalf("live index owners: %v", err)
	}
	for _, gap := range missingDeclaredIndexes(migrations, applied, live, owners, scope) {
		if gap.Index == index {
			return gap, true
		}
	}
	return declaredIndex{}, false
}

// TestVerifyReportsAMissingIndex is the production regression: 272 recreated the
// table and the canonical index was silently skipped because the name was taken.
func TestVerifyReportsAMissingIndex(t *testing.T) {
	const rel = "../../../migrations/sqlite"
	db, _, migrations := migratedDB(t, rel, "primary")
	if _, err := db.Exec(`DROP INDEX idx_job_checkpoints_job`); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	gap, reported := indexGap(t, db, migrations, "primary", "idx_job_checkpoints_job")
	if !reported {
		t.Fatal("a declared index that the live schema lost must be reported")
	}
	if gap.Table != "job_checkpoints" || gap.Version == 0 || gap.Filename == "" {
		t.Fatalf("the report must name the table, file and version, got %+v", gap)
	}
	t.Logf("missing index: %s on %s declared_by=%s version=%d", gap.Index, gap.Table, gap.Filename, gap.Version)
}

// TestVerifyReportsAnIndexStolenByAnotherTable is the exact silent-skip shape:
// the declared name EXISTS, so a name-only check would be satisfied, but it is
// attached to the wrong table and the declared table has no access path at all.
func TestVerifyReportsAnIndexStolenByAnotherTable(t *testing.T) {
	const rel = "../../../migrations/sqlite"
	db, _, migrations := migratedDB(t, rel, "primary")

	// Reproduce the quarantine: park the index on a table with the same shape.
	if _, err := db.Exec(`CREATE TABLE legacy_job_checkpoints (
		job_id TEXT NOT NULL, stage TEXT NOT NULL, unit_id TEXT NOT NULL,
		input_fingerprint TEXT NOT NULL, status TEXT NOT NULL,
		artifact_sha256 TEXT NOT NULL DEFAULT '', artifact_uri TEXT NOT NULL DEFAULT '',
		processor_version TEXT NOT NULL, completed_at TEXT NOT NULL,
		PRIMARY KEY(job_id, stage, unit_id))`); err != nil {
		t.Fatalf("create the quarantined copy: %v", err)
	}
	if _, err := db.Exec(`DROP INDEX idx_job_checkpoints_job`); err != nil {
		t.Fatalf("free the name: %v", err)
	}
	if _, err := db.Exec(`CREATE INDEX idx_job_checkpoints_job ON legacy_job_checkpoints(job_id, completed_at)`); err != nil {
		t.Fatalf("steal the name: %v", err)
	}
	// The silent no-op that caused the production defect.
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_job_checkpoints_job ON job_checkpoints(job_id, completed_at)`); err != nil {
		t.Fatalf("the colliding statement must not error: %v", err)
	}
	owners, err := liveIndexOwners(db)
	if err != nil {
		t.Fatalf("owners: %v", err)
	}
	if owners["idx_job_checkpoints_job"] != "legacy_job_checkpoints" {
		t.Fatalf("the collision was not reproduced; owner=%q", owners["idx_job_checkpoints_job"])
	}

	gap, reported := indexGap(t, db, migrations, "primary", "idx_job_checkpoints_job")
	if !reported {
		t.Fatal("an index declared on job_checkpoints but owned by another table must be reported")
	}
	if gap.Table != "job_checkpoints" {
		t.Fatalf("the report must name the declaring table, got %+v", gap)
	}
	t.Logf("stolen index: %s declared_on=%s owned_by=%s", gap.Index, gap.Table, owners[gap.Index])
}

// TestMigrationHistory_OnlyTablesWithALiveClaimAreReported is the general form of
// the ordering rule, evaluated against the real corpus with an EMPTY live schema
// (the strictest possible view): every table the report claims must have a
// declaration as its last statement, and the rebuild scratch tables — created and
// renamed away inside one migration file, which is how the live primary database
// produced its final twelve reports — must not be claimed at all.
func TestMigrationHistory_OnlyTablesWithALiveClaimAreReported(t *testing.T) {
	const rel = "../../../migrations/sqlite"
	db, _, migrations := migratedDB(t, rel, "primary")
	applied, err := loadAppliedMigrations(db)
	if err != nil {
		t.Fatalf("load applied: %v", err)
	}
	own := schemaOwnershipFor(migrations, applied, "primary")

	reported := map[string]bool{}
	for _, gap := range missingDeclaredTables(migrations, applied, map[string]bool{}, "primary") {
		reported[gap.Table] = true
		if own.removedAt[gap.Table].after(own.declaredAt[gap.Table]) {
			t.Fatalf("%q is claimed present although the corpus ends with a removal of it", gap.Table)
		}
	}

	// Concrete names the live database surfaced. Each is a scratch table whose
	// final statement inside its own migration is the rename away.
	for _, scratch := range []string{"jobs_new", "voiceovers_new", "asset_text_tracks_new", "_voiceover_text_hash_audit"} {
		if !anyMigrationDeclares(migrations, scratch) {
			continue
		}
		if reported[scratch] {
			t.Errorf("rebuild scratch table %q is created only to be renamed away and must not be reported", scratch)
		}
	}
	t.Logf("claimed=%d scratch suppressed", len(reported))
}

// anyMigrationDeclares reports whether any in-scope migration creates the name.
func anyMigrationDeclares(migrations []migrationFile, name string) bool {
	for _, m := range migrations {
		if !migrationAppliesToTargetDB(m.scope, "primary") {
			continue
		}
		content, err := os.ReadFile(m.path)
		if err != nil {
			continue
		}
		for _, declared := range declaredTablesIn(string(content)) {
			if declared == name {
				return true
			}
		}
	}
	return false
}

// TestVerifyAcceptsADeliberateIndexRename guards the false-positive class: a
// migration that drops an index and creates it under a new name (or the same name
// on the same table) must be silent.
func TestVerifyAcceptsADeliberateIndexRename(t *testing.T) {
	const rel = "../../../migrations/sqlite"
	db, _, migrations := migratedDB(t, rel, "primary")
	if gaps := missingDeclaredIndexesFor(t, db, migrations, "primary"); len(gaps) != 0 {
		t.Fatalf("a freshly migrated database must report no index drift, got %v", gaps)
	}
}

func missingDeclaredIndexesFor(t *testing.T, db *sql.DB, migrations []migrationFile, scope string) []declaredIndex {
	t.Helper()
	applied, err := loadAppliedMigrations(db)
	if err != nil {
		t.Fatalf("load applied: %v", err)
	}
	live, err := liveTableNames(db)
	if err != nil {
		t.Fatalf("live tables: %v", err)
	}
	owners, err := liveIndexOwners(db)
	if err != nil {
		t.Fatalf("owners: %v", err)
	}
	return missingDeclaredIndexes(migrations, applied, live, owners, scope)
}

func readMigrationFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}
