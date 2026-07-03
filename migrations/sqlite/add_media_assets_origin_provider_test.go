// Package migration_test verifies the FASE 1B image-territories
// migration 115 (original, immutable). The duplicate at slot 126
// was removed in Task 9 (July 2026); the corrective migration 128
// applies the index adjustments that 126's design intended.
//
// Precedent: internal/infrastructure/database/migrations_test.go
// (smoke-test on a fresh DB in t.TempDir(); never references production
// DBs under data/).
//
// The migration does `ALTER TABLE media_assets ADD COLUMN origin
// TEXT NOT NULL DEFAULT 'retrieved'` + equivalent for provider +
// a backfill (`provider = 'unknown'`) + two full indexes. SQLite
// ALTER TABLE ADD COLUMN does NOT support IF NOT EXISTS, so the
// migration runner's `isDuplicateColumnError` soft-skip handles
// reapply; this test verifies the test is solvable on a clean DB only.
//
// Subtests:
//   (a) ApplyFirstTime              — clean DB, apply migration, no error.
//   (b) OriginProviderColumnsPresent — PRAGMA table_info returns the
//                                    two new columns with DEFAULT 'retrieved'
//                                    (origin) and '' (provider).
//   (c) OriginProviderDefaultValues  — SELECT origin/provider on a row
//                                    inserted WITHOUT setting them
//                                    returns 'retrieved' for origin, '' for provider.
//   (d) OriginProviderRoundTrip       — INSERT with origin='generated',
//                                    provider='flux'; SELECT back, assert
//                                    bit-identical round-trip.
//   (e) IndexesPresent                — both idx_media_assets_origin and
//                                    idx_media_assets_provider registered
//                                    in sqlite_master after apply.
//   (f) IdempotenceFailureOnSecondApply — second apply produces
//                                    "duplicate column name" error
//                                    (proves migration is NOT silently
//                                    idempotent at the SQL level; the
//                                    runner MUST intercept).
//   (g) AntiModification_ChecksumMatches — SHA-256 of migration 115
//                                    matches the frozen expected value
//                                    (proves the file was not modified
//                                    after being applied — migration
//                                    integrity gate).
//
// Run via `go test ./migrations/sqlite/...` from the repo root.
package migration_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// migrationFile is read relative to the test package's cwd, which is
// `migrations/sqlite/` when invoked via `go test ./migrations/sqlite/...`.
const migrationFile = "115_add_image_origin_provider.sql"

// frozenChecksum115 is the SHA-256 of migration 115 as committed.
// Task 9 (July 2026): this value is frozen — any modification to the
// migration file will break this test and must be accompanied by a
// corrective migration at a higher slot number, not a modification
// of the already-applied file.
//
// To recompute: sha256sum migrations/sqlite/115_add_image_origin_provider.sql
const frozenChecksum115 = "af71fcc9bb57dd71a442407f6812cf452a999023cfb49b36785925c91017401b"

// openTestDB opens a fresh SQLite DB in t.TempDir() and ensures a
// MINIMAL media_assets table exists so the migration's ALTER TABLE
// statement has a target.
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
			name TEXT NOT NULL DEFAULT '',
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
func readMigration(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(migrationFile)
	if err != nil {
		t.Fatalf("read %s: %v (cwd must be migrations/sqlite/ — run via `go test ./migrations/sqlite/...` from repo root)", migrationFile, err)
	}
	return string(data)
}

// sha256Hex returns the hex-encoded SHA-256 of data.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// TestMigration115 covers the FASE 1B contract end-to-end.
func TestMigration115(t *testing.T) {
	db := openTestDB(t)
	migration := readMigration(t)

	// Seed a pre-existing row BEFORE running the migration so the
	// backfill subtest can verify the UPDATE on legacy rows.
	_, err := db.Exec(`
		INSERT INTO media_assets (id, source, name)
		VALUES (?, ?, ?)
	`, "pre-existing", "image", "pre-existing-row")
	if err != nil {
		t.Fatalf("seed pre-existing row: %v", err)
	}

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
		}
		// origin DEFAULT is 'retrieved' in the original migration 115.
		// PRAGMA table_info.dflt_value returns the SQL literal text:
		// DEFAULT '' → "''", DEFAULT 'retrieved' → "'retrieved'".
		if seen["origin"] != "'retrieved'" {
			t.Errorf("column origin default = %q, want 'retrieved' (original migration 115 default)", seen["origin"])
		}
		if seen["provider"] != "''" {
			t.Errorf("column provider default = %q, want '' (original migration 115 default)", seen["provider"])
		}
	})

	t.Run("OriginProviderDefaultValues", func(t *testing.T) {
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
		if gotOrigin != "retrieved" {
			t.Errorf("default origin = %q, want \"retrieved\" (migration 115 DEFAULT)", gotOrigin)
		}
		if gotProvider != "" {
			t.Errorf("default provider = %q, want \"\" (migration 115 DEFAULT)", gotProvider)
		}
	})

	t.Run("BackfillProviderUnknown", func(t *testing.T) {
		// Migration 115 backfills provider='unknown' for pre-existing
		// image rows that had empty provider after the column was
		// added. The row seeded BEFORE ApplyFirstTime should now have
		// provider='unknown' after the migration's UPDATE ran.
		var gotProvider string
		err := db.QueryRow(`
			SELECT provider FROM media_assets WHERE id = ?
		`, "pre-existing").Scan(&gotProvider)
		if err != nil {
			t.Fatalf("select backfilled provider: %v", err)
		}
		if gotProvider != "unknown" {
			t.Errorf("backfilled provider = %q, want \"unknown\" (migration 115 backfill)", gotProvider)
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

	t.Run("IndexesPresent", func(t *testing.T) {
		wantIndexes := []string{
			"idx_media_assets_origin",
			"idx_media_assets_provider",
		}
		for _, want := range wantIndexes {
			var name string
			err := db.QueryRow(`
				SELECT name FROM sqlite_master
				WHERE type = 'index' AND name = ?
			`, want).Scan(&name)
			if err == sql.ErrNoRows {
				t.Errorf("index %q not registered in sqlite_master (migration 115 creates it)", want)
				continue
			}
			if err != nil {
				t.Fatalf("query sqlite_master for %q: %v", want, err)
			}
			if name != want {
				t.Errorf("index name = %q, want %q", name, want)
			}
		}
	})

	t.Run("IdempotenceFailureOnSecondApply", func(t *testing.T) {
		_, err := db.Exec(migration)
		if err == nil {
			t.Fatal("second apply did not error; SQLite ADD COLUMN idempotence claim unverified")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			t.Errorf("second apply error = %v, want error containing \"duplicate column name\"", err)
		}
	})

	t.Run("AntiModification_ChecksumMatches", func(t *testing.T) {
		// Task 9: anti-modification gate. Migration files must
		// never be modified after being applied. This test
		// verifies the SHA-256 of migration 115 matches the
		// frozen expected value. If this test fails because the
		// expected checksum is stale (the file was intentionally
		// updated), update frozenChecksum115 AND add a corrective
		// migration at a higher slot — never modify the applied
		// file content alone.
		data, err := os.ReadFile(migrationFile)
		if err != nil {
			t.Fatalf("read %s for checksum: %v", migrationFile, err)
		}
		actual := sha256Hex(data)
		// First run: compute and print the actual checksum so the
		// operator can freeze it. Subsequent runs assert equality.
		if frozenChecksum115 == "frozen-sha256-of-original-migration-115" {
			t.Logf("FIRST RUN — freeze this checksum:\nconst frozenChecksum115 = %q", actual)
			t.Skip("set frozenChecksum115 to the value above and re-run")
		}
		if actual != frozenChecksum115 {
			t.Errorf("ANTI-MODIFICATION GATE: migration 115 checksum changed.\n  expected: %s\n  actual:   %s\n\nMigration files must never be modified after being applied.\nIf this change is intentional, update frozenChecksum115 AND create\na corrective migration at a higher slot number.", frozenChecksum115, actual)
		}
	})
}
