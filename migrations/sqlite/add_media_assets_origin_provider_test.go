// Package migration_test verifies the FASE 1B image-territories
// migration 126 (renumbered twice in July 2026 to break two
// cascading prefix collisions: first 115 → 123, then yield 123 to
// a parallel agent's 10d1d528 commit (123_add_media_assets_enrich_state_column.sql)
// and move to slot 126). Precedent: internal/infrastructure/database/migrations_test.go
// (smoke-test on a fresh DB in t.TempDir(); never references production
// DBs under data/).
//
// The migration does `ALTER TABLE media_assets ADD COLUMN origin
// TEXT NOT NULL DEFAULT ''` + equivalent for provider + a partial
// index `idx_media_assets_origin WHERE origin != ''`. SQLite ALTER
// TABLE ADD COLUMN does NOT support IF NOT EXISTS, so the migration
// runner's `isDuplicateColumnError` soft-skip handles reapply; this
// test verifies the test is solvable on a clean DB only.
//
// Subtests:
//   (a) ApplyFirstTime              — clean DB, apply migration, no error.
//   (b) OriginProviderColumnsPresent — PRAGMA table_info returns the
//                                    two new columns with DEFAULT ''.
//   (c) OriginProviderDefaultEmpty   — SELECT origin/provider on a row
//                                    inserted WITHOUT setting them
//                                    returns '' for both (default).
//   (d) OriginProviderRoundTrip       — INSERT with origin='generated',
//                                    provider='flux'; SELECT back, assert
//                                    bit-identical round-trip.
//   (e) IndexPresent                  — idx_media_assets_origin partial
//                                    index is registered in
//                                    sqlite_master after apply.
//   (f) IdempotenceFailureOnSecondApply — second apply produces
//                                    "duplicate column name" error
//                                    (proves migration is NOT silently
//                                    idempotent at the SQL level; the
//                                    runner MUST intercept).
//
// Run via `go test ./migrations/sqlite/...` from the repo root.
package migration_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// migrationFile is read relative to the test package's cwd, which is
// `migrations/sqlite/` when invoked via `go test ./migrations/sqlite/...`.
// The structure is robust against the working-directory misconfiguration:
// readMigration logs the path the test expected.
const migrationFile = "126_add_media_assets_origin_provider.sql"

// openTestDB opens a fresh SQLite DB in t.TempDir() and ensures a
// MINIMAL media_assets table exists so the migration's ALTER TABLE
// statement has a target. We use the minimal schema (only the columns
// strictly required by FASE 1B ADD COLUMNs) because the migration is
// self-contained — it doesn't depend on prior migration history.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test115.sqlite")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open %s: %v", dbPath, err)
	}
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}'
		)
	`)
	if err != nil {
		t.Fatalf("create minimal media_assets: %v", err)
	}
	return db
}

// readMigration reads the migration SQL file from the package cwd.
// Fails the test if cwd is not the package directory; this guards
// against misconfigured go test invocations.
func readMigration(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(migrationFile)
	if err != nil {
		t.Fatalf("read %s: %v (cwd must be migrations/sqlite/ — run via `go test ./migrations/sqlite/...` from repo root)", migrationFile, err)
	}
	return string(data)
}

// TestMigration115 covers the FASE 1B contract end-to-end.
func TestMigration126(t *testing.T) {
	db := openTestDB(t)
	migration := readMigration(t)

	t.Run("ApplyFirstTime", func(t *testing.T) {
		if _, err := db.Exec(migration); err != nil {
			t.Fatalf("apply 115: %v", err)
		}
	})

	t.Run("OriginProviderColumnsPresent", func(t *testing.T) {
		rows, err := db.Query(`PRAGMA table_info(media_assets)`)
		if err != nil {
			t.Fatalf("PRAGMA table_info: %v", err)
		}
		defer rows.Close()
		// Map column name -> default value (NULL -> "").
		seen := map[string]string{}
		for rows.Next() {
			var cid, notnull, pk int
			var name, ctype string
			var dfltValue sql.NullString
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
				t.Fatalf("scan table_info: %v", err)
			}
			seen[name] = dfltValue.String
		}
		for _, col := range []string{"origin", "provider"} {
			if _, ok := seen[col]; !ok {
				t.Errorf("media_assets missing column %q (added by migration 115)", col)
			}
			if seen[col] != "" {
				t.Errorf("column %q default = %q, want \"\"", col, seen[col])
			}
		}
	})

	t.Run("OriginProviderDefaultEmpty", func(t *testing.T) {
		// Insert WITHOUT specifying origin/provider — DEFAULT ''
		// must kick in.
		_, err := db.Exec(`
			INSERT INTO media_assets (id, source, name)
			VALUES (?, ?, ?)
		`, "default-test", "image", "default-row")
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		var gotOrigin, gotProvider string
		err = db.QueryRow(`
			SELECT origin, provider FROM media_assets WHERE id = ?
		`, "default-test").Scan(&gotOrigin, &gotProvider)
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		if gotOrigin != "" {
			t.Errorf("default origin = %q, want \"\" (DEFAULT '' kick-in expected)", gotOrigin)
		}
		if gotProvider != "" {
			t.Errorf("default provider = %q, want \"\" (DEFAULT '' kick-in expected)", gotProvider)
		}
	})

	t.Run("OriginProviderRoundTrip", func(t *testing.T) {
		_, err := db.Exec(`
			INSERT INTO media_assets (id, source, name, origin, provider)
			VALUES (?, ?, ?, ?, ?)
		`, "round-trip-test", "image", "round-trip-row", "generated", "flux")
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		var gotOrigin, gotProvider string
		err = db.QueryRow(`
			SELECT origin, provider FROM media_assets WHERE id = ?
		`, "round-trip-test").Scan(&gotOrigin, &gotProvider)
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		if gotOrigin != "generated" {
			t.Errorf("origin round-trip = %q, want \"generated\"", gotOrigin)
		}
		if gotProvider != "flux" {
			t.Errorf("provider round-trip = %q, want \"flux\"", gotProvider)
		}
	})

	t.Run("IndexPresent", func(t *testing.T) {
		var name string
		err := db.QueryRow(`
			SELECT name FROM sqlite_master
			WHERE type = 'index' AND name = 'idx_media_assets_origin'
		`).Scan(&name)
		if err == sql.ErrNoRows {
			t.Errorf("idx_media_assets_origin partial index not registered in sqlite_master")
			return
		}
		if err != nil {
			t.Fatalf("query sqlite_master: %v", err)
		}
		if name != "idx_media_assets_origin" {
			t.Errorf("index name = %q, want idx_media_assets_origin", name)
		}
	})

	t.Run("IdempotenceFailureOnSecondApply", func(t *testing.T) {
		// SQLite has no IF NOT EXISTS for ADD COLUMN. The migration
		// runner's isDuplicateColumnError soft-skip handles this.
		// Asserting the second apply produces a duplicate-column
		// error proves the migration is NOT silently idempotent at
		// the SQL level — the runner MUST intercept to make re-runs
		// safe (otherwise the runner surfaces the error to operators).
		_, err := db.Exec(migration)
		if err == nil {
			t.Fatal("second apply did not error; SQLite ADD COLUMN idempotence claim unverified (this should never be silent)")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			t.Errorf("second apply error = %v, want error containing \"duplicate column name\" (kind=column duplication)", err)
		}
	})
}
