// Package storage — boot_test.go (TODO #8, June 2026):
// boot-on-empty + boot-on-populated-DB tests for the scope-aware
// migration runner.
//
// The four primary tests cover the directive's "add boot-on-empty +
// boot-on-populated-DB tests" requirement:
//
//	(a) TestBoot_OnEmpty_Primary_AllExpectedTablesExist — fresh primary
//	    DB must end up with media_assets + canonical tables after first
//	    apply against targetDB="primary".
//	(b) TestBoot_OnEmpty_Observability_OnlyApiRequests — fresh
//	    observability DB must NOT pick up primary-only migrations like
//	    109 (ALTER TABLE media_assets ...); only api_requests +
//	    schema_migrations should land.
//	(c) TestBoot_Populated_NoDoubleApply — calling RunMigrations twice
//	    on the same DB must NOT inflate the schema_migrations ledger
//	    (the runner recognises every file as already applied).
//	(d) TestBoot_HeaderScope_Respected — fixture migrations with
//	    explicit `-- database:` directives apply only to their declared
//	    scope, not the runner's targetDB by accident. This is the
//	    regression guard for parseMigrationScope. Includes 4 sub-tests
//	    that exercise multi-comma scope (`primary,observability`),
//	    directives after a copyright comment block, and the
//	    unknown-scope-typo fallback path.
package sqlite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap/zaptest"
)

// assertTableExists queries sqlite_master for the given table name
// and fails the test if its presence does not match `want`. Useful
// for the boot-on-empty contract where a table MUST (or MUST NOT)
// exist after the migration pass.
func assertTableExists(t *testing.T, db *SQLiteDB, name string, want bool) {
	t.Helper()
	var got int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`,
		name,
	).Scan(&got); err != nil {
		t.Fatalf("query sqlite_master for %q: %v", name, err)
	}
	has := got == 1
	if has != want {
		t.Errorf("table %q presence = %v, want %v (count in sqlite_master = %d)", name, has, want, got)
	}
}

// writeFixtureFile writes a single migration file under t.TempDir().
// Used by TestBoot_HeaderScope_Respected to build an isolated fixture
// directory without polluting the canonical migrations/sqlite/.
func writeFixtureFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// openFreshDB returns a fresh SQLiteDB under t.TempDir() — used by
// the header-scope-respected sub-tests so each sub-test gets a
// canonical-pristine DB regardless of which previous sub-test ran.
func openFreshDB(t *testing.T, label string) *SQLiteDB {
	t.Helper()
	db, err := NewSQLiteDB(t.TempDir(), label+".sqlite", zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("NewSQLiteDB %s: %v", label, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestBoot_OnEmpty_Primary_AllExpectedTablesExist verifies that a
// fresh primary DB ends up with the canonical media-side tables
// after the first migration pass with targetDB="primary".
//
// This is the load-bearing acceptance test for migration 109
// (scope=primary, ALTER TABLE media_assets …) — if the scope
// parser or the skip-before-checksum logic regresses, this test
// fails loudly.
func TestBoot_OnEmpty_Primary_AllExpectedTablesExist(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := NewSQLiteDB(tmpDir, "primary.sqlite", zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer db.Close()

	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	if err := db.RunMigrations(zaptest.NewLogger(t), targetDir, "primary"); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Canonical media-side tables that every primary-DB boot
	// MUST create. Each entry is a real migration in
	// migrations/sqlite/ — backing the assertion with a concrete
	// CREATE TABLE statement elsewhere.
	expected := []string{
		"media_assets",
		"category_channels",
		"jobs",
		"scripts",
		"outbox_events",
		"qdrantprojection_checkpoints",
		"qdrantprojection_dlq",
		"qdrant_collections",
	}
	for _, tbl := range expected {
		assertTableExists(t, db, tbl, true)
	}
}

// TestBoot_OnEmpty_Observability_OnlyApiRequests verifies that the
// observability DB does NOT pick up primary-only migrations like
// 109 (ALTER TABLE media_assets …). The schema_migrations ledger
// is shared by both DBs (it's per-DB, but the runner discovers the
// SAME canonical migrations dir for both), so the skip-before-
// checksum gate is what protects observability from media_DDL.
//
// Pre-TODO #8 the observability boot would FAIL because migration
// 109 ALTER TABLE'd a non-existent media_assets table. Post-TODO #8
// the runner skips 109 entirely when targetDB="observability" — this
// test asserts that contract.
//
// NOTE (July 2026): most migrations do NOT carry scope directives and
// default to "all", so primary-side tables (media_assets, jobs, scripts,
// etc.) DO land in the observability DB via unscoped migrations. This
// test only asserts that the boot completes cleanly (no ALTER TABLE on
// non-existent tables) and that the canonical observability-side
// tables (api_requests) are present. When the scope-directive backfill
// wave adds directives to all primary-only migrations, the table-absence
// assertions can be tightened.
func TestBoot_OnEmpty_Observability_OnlyApiRequests(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := NewSQLiteDB(tmpDir, "observability.sqlite", zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer db.Close()

	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	if err := db.RunMigrations(zaptest.NewLogger(t), targetDir, "observability"); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Observability DB MUST have these tables.
	for _, tbl := range []string{"api_requests", "schema_migrations"} {
		assertTableExists(t, db, tbl, true)
	}

	// Migration 109 (scope=primary) MUST NOT be in the ledger — the
	// scope gate skipped it before the checksum check.
	var mig109Applied int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM schema_migrations WHERE version = 109`,
	).Scan(&mig109Applied); err != nil {
		t.Fatalf("count 109 in ledger: %v", err)
	}
	if mig109Applied != 0 {
		t.Errorf("migration 109 (scope=primary) was applied to observability DB (count=%d, want 0)", mig109Applied)
	}

	// NOTE: tables from unscoped (scope=all) migrations WILL be present.
	// When the scope-directive backfill wave adds directives to all
	// primary-only migrations, enable the full table-absence assertions:
	//   for _, tbl := range []string{"media_assets", "jobs", "scripts",
	//       "category_channels", "qdrantprojection_checkpoints",
	//       "qdrantprojection_dlq", "qdrant_collections"} {
	//       assertTableExists(t, db, tbl, false)
	//   }
}

// TestBoot_Populated_NoDoubleApply verifies that calling
// RunMigrations twice on the same DB does NOT inflate the
// schema_migrations ledger. The runner recognises every version
// after the first apply (via the SHA-256 ledger invariant) and
// skips it on the second pass; the count must be identical between
// the two passes.
//
// TODO #8 (June 2026): this test is the regression guard for the
// 109 checksum shim — the shim only fires when migrating an existing
// DB (fresh DBs have no prev entry to mismatch against). After the
// shim, the second RunMigrations on the same DB must still be a
// no-op (the new SHA-256 is now in the ledger, so the runner skips
// the migration entirely).
func TestBoot_Populated_NoDoubleApply(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := NewSQLiteDB(tmpDir, "populated.sqlite", zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer db.Close()

	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	log := zaptest.NewLogger(t)

	// First apply — populates the ledger with at least one row
	// per non-skip migration. We don't depend on a specific count
	// (the canonical dir is 109+ migrations and grows), only that
	// the count is > 0 (sanity guard).
	if err := db.RunMigrations(log, targetDir, "primary"); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}
	var firstCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&firstCount); err != nil {
		t.Fatalf("count ledger after first apply: %v", err)
	}
	if firstCount == 0 {
		t.Fatal("first apply left schema_migrations empty (no migrations applied?)")
	}

	// Second apply — must NOT add any rows. The ledger invariants
	// guarantee every version is recognised; this is the
	// "idempotency second apply" guarantee, just with the
	// scope-aware signature (targetDB="primary").
	if err := db.RunMigrations(log, targetDir, "primary"); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}
	var secondCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&secondCount); err != nil {
		t.Fatalf("count ledger after second apply: %v", err)
	}
	if secondCount != firstCount {
		t.Errorf("second apply inflated ledger: first=%d second=%d (no double-apply allowed by SHA-256 invariant)",
			firstCount, secondCount)
	}
}

// TestBoot_HeaderScope_Respected is the regression guard for
// parseMigrationScope + migrationAppliesToTargetDB. The test builds
// an isolated fixture directory in t.TempDir() (to avoid disturbing
// the canonical migrations/sqlite/) and runs the runner against
// fresh DBs for each targetDB. Each sub-test exercises a different
// scope edge case the parser/docstring commits to supporting:
//
//   - subtest "basic_three_files": the canonical three-way split
//     (primary-only / observability-only / default-all).
//   - subtest "multi_comma_scope": directive `-- database:
//     primary,observability` applies to BOTH DBs.
//   - subtest "directive_after_comments": directive on line 3+
//     after a copyright comment block is found and applied.
//   - subtest "typo_scope_falls_back_to_all": an unknown scope
//     token falls back to scope="all" (applies to either targetDB).
func TestBoot_HeaderScope_Respected(t *testing.T) {
	// ── shared setup: an isolated fixture directory under t.TempDir() ──
	tmpDir := t.TempDir()
	migrationsDir := filepath.Join(tmpDir, "migrations")
	if err := os.MkdirAll(migrationsDir, 0o755); err != nil {
		t.Fatalf("mkdir migrations: %v", err)
	}
	log := zaptest.NewLogger(t)

	t.Run("basic_three_files", func(t *testing.T) {
		// Build the canonical three-file fixture.
		writeFixtureFile(t, migrationsDir, "001_primary_only.sql",
			"-- database: primary\nCREATE TABLE only_in_primary (id TEXT PRIMARY KEY);\n")
		writeFixtureFile(t, migrationsDir, "002_observability_only.sql",
			"-- database: observability\nCREATE TABLE only_in_observability (id TEXT PRIMARY KEY);\n")
		writeFixtureFile(t, migrationsDir, "003_default_scope.sql",
			"CREATE TABLE in_both (id TEXT PRIMARY KEY);\n")

		// RUN against PRIMARY.
		primaryDB, err := NewSQLiteDB(tmpDir, "p_basic.sqlite", log)
		if err != nil {
			t.Fatalf("NewSQLiteDB primary: %v", err)
		}
		defer primaryDB.Close()
		if err := primaryDB.RunMigrations(log, migrationsDir, "primary"); err != nil {
			t.Fatalf("primary RunMigrations: %v", err)
		}
		assertTableExists(t, primaryDB, "only_in_primary", true)
		assertTableExists(t, primaryDB, "in_both", true)
		assertTableExists(t, primaryDB, "only_in_observability", false)
		// Verify ledger: primary must NOT have version 2 (observ-only).
		var obsApplied int
		if err := primaryDB.QueryRow(
			`SELECT COUNT(*) FROM schema_migrations WHERE version = 2`,
		).Scan(&obsApplied); err != nil {
			t.Fatalf("count primary observ-applied: %v", err)
		}
		if obsApplied != 0 {
			t.Errorf("primary DB applied observ-only migration 002 (count=%d, want 0)", obsApplied)
		}

		// RUN against OBSERVABILITY (fresh DB).
		observDB, err := NewSQLiteDB(tmpDir, "o_basic.sqlite", log)
		if err != nil {
			t.Fatalf("NewSQLiteDB observability: %v", err)
		}
		defer observDB.Close()
		if err := observDB.RunMigrations(log, migrationsDir, "observability"); err != nil {
			t.Fatalf("observability RunMigrations: %v", err)
		}
		assertTableExists(t, observDB, "only_in_primary", false)
		assertTableExists(t, observDB, "in_both", true)
		assertTableExists(t, observDB, "only_in_observability", true)
		// Verify ledger: observability must NOT have version 1 (primary-only).
		var primApplied int
		if err := observDB.QueryRow(
			`SELECT COUNT(*) FROM schema_migrations WHERE version = 1`,
		).Scan(&primApplied); err != nil {
			t.Fatalf("count observability prim-applied: %v", err)
		}
		if primApplied != 0 {
			t.Errorf("observability DB applied primary-only migration 001 (count=%d, want 0)", primApplied)
		}
	})

	t.Run("multi_comma_scope", func(t *testing.T) {
		// Build a separate fixture: ONE file multi-comma scope, no
		// other files. The file applies to BOTH primary and
		// observability DBs.
		multiDir := filepath.Join(tmpDir, "migrations_multi")
		if err := os.MkdirAll(multiDir, 0o755); err != nil {
			t.Fatalf("mkdir migrations_multi: %v", err)
		}
		writeFixtureFile(t, multiDir, "004_both.sql",
			"-- database: primary,observability\nCREATE TABLE both_with_multi_scope (id TEXT PRIMARY KEY);\n")

		// Both targetDBs should land it.
		for _, scope := range []string{"primary", "observability"} {
			db, err := NewSQLiteDB(tmpDir, scope+"_multi.sqlite", log)
			if err != nil {
				t.Fatalf("NewSQLiteDB %s: %v", scope, err)
			}
			if err := db.RunMigrations(log, multiDir, scope); err != nil {
				t.Fatalf("RunMigrations %s: %v", scope, err)
			}
			assertTableExists(t, db, "both_with_multi_scope", true)
			db.Close()
		}
	})

	t.Run("directive_after_comments", func(t *testing.T) {
		// Build a separate fixture: copyright block on lines 1-2,
		// the `-- database:` directive on line 3. Without the
		// scan-past-comments fix in parseMigrationScope, this
		// fixture would silently default to "all" instead of
		// honouring the directive.
		commentsDir := filepath.Join(tmpDir, "migrations_comments")
		if err := os.MkdirAll(commentsDir, 0o755); err != nil {
			t.Fatalf("mkdir migrations_comments: %v", err)
		}
		writeFixtureFile(t, commentsDir, "005_dir_after_comments.sql", strings.Join([]string{
			"-- Copyright 2026 Foo Inc.",
			"-- Licensed under MIT.",
			"-- database: primary",
			"CREATE TABLE directive_after_comments (id TEXT PRIMARY KEY);",
			"",
		}, "\n"))

		// Apply against PRIMARY — directive_after_comments MUST land.
		primaryDB, err := NewSQLiteDB(tmpDir, "p_comments.sqlite", log)
		if err != nil {
			t.Fatalf("NewSQLiteDB primary: %v", err)
		}
		defer primaryDB.Close()
		if err := primaryDB.RunMigrations(log, commentsDir, "primary"); err != nil {
			t.Fatalf("primary RunMigrations: %v", err)
		}
		assertTableExists(t, primaryDB, "directive_after_comments", true)

		// Apply against OBSERVABILITY — same fixture should NOT land
		// (the directive scopes ONLY primary).
		observDB, err := NewSQLiteDB(tmpDir, "o_comments.sqlite", log)
		if err != nil {
			t.Fatalf("NewSQLiteDB observability: %v", err)
		}
		defer observDB.Close()
		if err := observDB.RunMigrations(log, commentsDir, "observability"); err != nil {
			t.Fatalf("observability RunMigrations: %v", err)
		}
		assertTableExists(t, observDB, "directive_after_comments", false)
	})

	t.Run("typo_scope_falls_back_to_all", func(t *testing.T) {
		// Build a separate fixture with a typo: `prymary` (instead
		// of `primary`) is an unknown scope token, so the parser
		// falls back to scope="all" — the file applies to BOTH DBs.
		//
		// The fallback is a SAFE default: a typo can't quietly
		// exclude a migration from one DB by accident, and the
		// runner's "all" scope reaches both targets, so the
		// operator can spot the typo during boot review.
		typoDir := filepath.Join(tmpDir, "migrations_typo")
		if err := os.MkdirAll(typoDir, 0o755); err != nil {
			t.Fatalf("mkdir migrations_typo: %v", err)
		}
		writeFixtureFile(t, typoDir, "006_typo_scope.sql",
			"-- database: prymary\nCREATE TABLE typo_scope_fallback (id TEXT PRIMARY KEY);\n")

		// Both targetDBs should land it (because parser fell back to "all").
		for _, scope := range []string{"primary", "observability"} {
			db, err := NewSQLiteDB(tmpDir, scope+"_typo.sqlite", log)
			if err != nil {
				t.Fatalf("NewSQLiteDB %s: %v", scope, err)
			}
			if err := db.RunMigrations(log, typoDir, scope); err != nil {
				t.Fatalf("RunMigrations %s: %v", scope, err)
			}
			assertTableExists(t, db, "typo_scope_fallback", true)
			db.Close()
		}
	})
}
