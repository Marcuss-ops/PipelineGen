package sqlite

// The migration-history contract: a table declared by a migration that the
// ledger records as APPLIED must exist in the database. The ledger proves the
// file ran; it does not prove the schema survived.
//
// Regression source (production, 2026-09-28): `data/media/media.db.sqlite`
// carried a schema_migrations row for version 216 whose checksum matched
// 216_job_checkpoints.sql byte-for-byte, while `job_checkpoints` was absent
// from that database. Nothing noticed — the runner skips an applied file, and
// its checksum check only guards the FILE, never the live schema — so every
// durable checkpoint write failed with "no such table". The repair is a NEW
// numbered migration (272), because editing 216 is invisible to the runner.
//
// These tests pin both halves: the drift is REPORTED, and a legitimately
// dropped table is not (no false positives on deprecation migrations).

import (
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap/zaptest"
)

// driftScenario applies a real migration directory to a fresh DB, then removes
// one table to reproduce "applied but absent". It returns the db path and the
// declared table the caller should expect to see reported.
func driftScenario(t *testing.T, migrationsRelPath, scope, declFile string) (string, string) {
	t.Helper()
	migrationsDir, err := filepath.Abs(migrationsRelPath)
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	// The table each scenario removes must be declared by a file the scenario's
	// ledger records as applied — that IS the drift.
	content, err := os.ReadFile(filepath.Join(migrationsDir, declFile))
	if err != nil {
		t.Fatalf("read %s: %v", declFile, err)
	}
	tables := declaredTablesIn(string(content))
	if len(tables) == 0 {
		t.Fatalf("%s declares no CREATE TABLE — the scenario cannot be built", declFile)
	}
	table := tables[0]

	dbPath := filepath.Join(t.TempDir(), "drift.sqlite")
	if err := RunMigrationsOnDB(dbPath, zaptest.NewLogger(t), migrationsDir, scope); err != nil {
		t.Fatalf("RunMigrationsOnDB: %v", err)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("DROP TABLE " + table); err != nil {
		t.Fatalf("simulate the drift (drop %s): %v", table, err)
	}
	return dbPath, table
}

// assertDriftIsReported is the catch: re-running the verifier on the drifted
// database must name the table AND the file that declares it, so an operator
// knows the repair is a forward migration. It also pins WHICH declarer is
// named: the newest numbered one in the history, never the baseline. Naming the
// baseline would be useless in production (267 concatenated files, and editing
// it is invisible to the ledger) — the actionable pointer is the most recent
// migration that promised the table.
func assertDriftIsReported(t *testing.T, dbPath, migrationsRelPath, scope, table string) {
	t.Helper()
	migrationsDir, err := filepath.Abs(migrationsRelPath)
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	migrations, err := discoverMigrations(migrationsDir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	applied, err := loadAppliedMigrations(db)
	if err != nil {
		t.Fatalf("load applied: %v", err)
	}
	live, err := liveTableNames(db)
	if err != nil {
		t.Fatalf("live tables: %v", err)
	}

	// Recomputed from the tree, so the assertion follows the history instead of
	// hard-coding a filename that the next migration would invalidate.
	newestDeclaringVersion, declaringFiles := 0, map[int]string{}
	for _, m := range migrations {
		if !migrationAppliesToTargetDB(m.scope, scope) {
			continue
		}
		if _, ok := applied[m.version]; !ok {
			continue
		}
		content, err := os.ReadFile(m.path)
		if err != nil {
			t.Fatalf("read %s: %v", m.filename, err)
		}
		for _, name := range declaredTablesIn(string(content)) {
			if name != table {
				continue
			}
			declaringFiles[m.version] = m.filename
			if m.version > newestDeclaringVersion {
				newestDeclaringVersion = m.version
			}
		}
	}
	if newestDeclaringVersion == 0 {
		t.Fatalf("scenario is broken: no applied migration declares %s", table)
	}

	gaps := missingDeclaredTables(migrations, applied, live, scope)
	var found *declaredTable
	for i := range gaps {
		if gaps[i].Table == table {
			found = &gaps[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("a table declared by an APPLIED migration is missing and was NOT reported; gaps=%v", gaps)
	}
	if found.Filename == "" {
		t.Fatalf("the report must name the declaring file, got %+v", *found)
	}
	if want := declaringFiles[newestDeclaringVersion]; found.Filename != want {
		t.Fatalf("the report must name the newest declarer %q (version %d), got %+v", want, newestDeclaringVersion, *found)
	}
	if found.Version == 0 {
		t.Fatalf("the report fell back to the baseline despite a numbered declarer existing, got %+v", *found)
	}
	// The named file must really declare the table — a pointer an operator
	// cannot trust is worse than none.
	content, err := os.ReadFile(filepath.Join(migrationsDir, found.Filename))
	if err != nil {
		t.Fatalf("read the reported file %s: %v", found.Filename, err)
	}
	if !slices.Contains(declaredTablesIn(string(content)), table) {
		t.Fatalf("the reported file %s does not declare %s", found.Filename, table)
	}
	t.Logf("reported: table=%s declared_by=%s version=%d (newest of %d declaring files)",
		found.Table, found.Filename, found.Version, len(declaringFiles))
}

// TestMigrationHistory_PrimaryPlane_ReportsAnAppliedTableThatIsMissing is the
// production shape on the plane where it happened.
func TestMigrationHistory_PrimaryPlane_ReportsAnAppliedTableThatIsMissing(t *testing.T) {
	const rel = "../../../migrations/sqlite"
	dbPath, table := driftScenario(t, rel, "primary", "216_job_checkpoints.sql")
	assertDriftIsReported(t, dbPath, rel, "primary", table)
}

// TestMigrationHistory_JobsPlane_ReportsAnAppliedTableThatIsMissing is the same
// contract for the split jobs history.
func TestMigrationHistory_JobsPlane_ReportsAnAppliedTableThatIsMissing(t *testing.T) {
	const rel = "../../../migrations/sqlite_jobs"
	dbPath, table := driftScenario(t, rel, "jobs", "001_jobs_plane.sql")
	assertDriftIsReported(t, dbPath, rel, "jobs", table)
}

// TestMigrationHistory_CleanDatabaseReportsNothing is the other half of the
// contract: the verifier must be silent on a database whose applied migrations
// match its schema, or it would be noise an operator learns to ignore.
func TestMigrationHistory_CleanDatabaseReportsNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rel   string
		scope string
	}{
		{name: "primary", rel: "../../../migrations/sqlite", scope: "primary"},
		{name: "jobs", rel: "../../../migrations/sqlite_jobs", scope: "jobs"},
		{name: "observability", rel: "../../../migrations/sqlite", scope: "observability"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			migrationsDir, err := filepath.Abs(tc.rel)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			dbPath := filepath.Join(t.TempDir(), "clean.sqlite")
			if err := RunMigrationsOnDB(dbPath, zaptest.NewLogger(t), migrationsDir, tc.scope); err != nil {
				t.Fatalf("RunMigrationsOnDB: %v", err)
			}
			db, err := sql.Open("sqlite3", dbPath)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer db.Close()

			migrations, err := discoverMigrations(migrationsDir)
			if err != nil {
				t.Fatalf("discover: %v", err)
			}
			applied, err := loadAppliedMigrations(db)
			if err != nil {
				t.Fatalf("load applied: %v", err)
			}
			live, err := liveTableNames(db)
			if err != nil {
				t.Fatalf("live tables: %v", err)
			}
			if gaps := missingDeclaredTables(migrations, applied, live, tc.scope); len(gaps) != 0 {
				t.Fatalf("a freshly migrated %s database must report no gaps, got %v", tc.name, gaps)
			}
			// The wiring must be inert on a clean database too.
			if err := verifyDeclaredTables(db, migrationsDir, tc.scope, zaptest.NewLogger(t)); err != nil {
				t.Fatalf("verifyDeclaredTables on a clean DB: %v", err)
			}
		})
	}
}

// TestMigrationHistory_DroppedTablesAreNotReported guards the false-positive
// class: a table whose LAST statement in the corpus is a drop or a rename is
// EXPECTED to be absent, and reporting it would make the check untrustworthy.
//
// The expectation is order-aware, not name-based. A bare "the name appears in
// some DROP" reading is wrong twice over: this history rebuilds tables (create
// <t>_new, copy, drop <t>, rename <t>_new -> <t>) and re-creates others later, so
// a name can be dropped and declared again. Those tables ARE expected to exist.
func TestMigrationHistory_DroppedTablesAreNotReported(t *testing.T) {
	migrationsDir, err := filepath.Abs("../../../migrations/sqlite")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	migrations, err := discoverMigrations(migrationsDir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	// Independent re-derivation of the corpus intent: newest declaring version
	// and newest removing version per table.
	declaredAt := map[string]int{}
	removedAt := map[string]int{}
	for _, m := range migrations {
		if !migrationAppliesToTargetDB(m.scope, "primary") {
			continue
		}
		content := readMigrationFile(t, m.path)
		for _, name := range declaredTablesIn(content) {
			if m.version > declaredAt[name] {
				declaredAt[name] = m.version
			}
		}
		for _, name := range removedTablesIn(content) {
			if m.version > removedAt[name] {
				removedAt[name] = m.version
			}
		}
	}
	droppedForGood := map[string]bool{}
	for name, removed := range removedAt {
		if removed > declaredAt[name] {
			droppedForGood[name] = true
		}
	}
	if len(droppedForGood) == 0 {
		t.Skip("no migration in this history drops a table for good — nothing to guard")
	}

	// Apply the real history, then claim every declared table is applied and the
	// live schema has NONE of them: only the tables with a declared reason may be
	// withheld.
	dbPath := filepath.Join(t.TempDir(), "removed.sqlite")
	if err := RunMigrationsOnDB(dbPath, zaptest.NewLogger(t), migrationsDir, "primary"); err != nil {
		t.Fatalf("RunMigrationsOnDB: %v", err)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	applied, err := loadAppliedMigrations(db)
	if err != nil {
		t.Fatalf("load applied: %v", err)
	}
	gaps := missingDeclaredTables(migrations, applied, map[string]bool{}, "primary")
	reported := map[string]bool{}
	for _, g := range gaps {
		reported[g.Table] = true
	}
	for name := range droppedForGood {
		if reported[name] {
			t.Fatalf("table %q was dropped for good (drop at v%d, newest declaration v%d) and must not be reported as drift", name, removedAt[name], declaredAt[name])
		}
	}

	// The guard must not be vacuous: at least one declared table with a live
	// claim has to come back as a gap. Which tables those are is
	// history-dependent, so the witness is chosen from the tree.
	own := schemaOwnershipFor(migrations, applied, "primary")
	witnesses := make([]string, 0, len(declaredAt))
	for name := range declaredAt {
		if declaredAt[name] >= removedAt[name] && !own.expectedAbsent(name) {
			witnesses = append(witnesses, name)
		}
	}
	sort.Strings(witnesses)
	if len(witnesses) == 0 {
		t.Fatalf("no declared table has a live claim — the scenario proves nothing")
	}
	if !reported[witnesses[0]] {
		t.Fatalf("table %q is last declared at v%d (removed never / earlier) yet it was treated as present when the live schema is empty (reported=%d)", witnesses[0], declaredAt[witnesses[0]], len(reported))
	}
	t.Logf("declared=%d dropped-for-good=%d reported=%d witness=%s", len(declaredAt), len(droppedForGood), len(reported), witnesses[0])
}
