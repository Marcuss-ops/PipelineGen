// Package storage hosts the canonical SQLite state store. This file
// holds SHARED TEST FIXTURES for the migration smoke + per-migration
// scenario tests in:
//   - migrations_smoke_test.go           — baseline apply/idempotency/integrity
//   - migrations_099_test.go             — Qdrant asset columns
//   - migrations_152_test.go             — canonical metadata columns
//   - migrations_153_test.go             — asset_artifacts table + indexes
//   - migrations_154_test.go             — script_localizations UNIQUE shape
//   - migrations_155_test.go             — translation fingerprint (partial UNIQUE idx)
//   - migrations_156_test.go             — asset_text_tracks source-track FK + segments text_hash
//   - migrations_157_test.go             — asset_state column + idx + round-trip
//   - migrations_158_test.go             — rights-extension columns
//   - migrations_183_test.go             — lifecycle shadow reconciliation
//
// Migration scenario tests use the same `applyFreshSmokeDB(t)` helper to
// spin up an isolated SQLite database in `t.TempDir()`, apply migrations
// with scope = "primary", and return an open handle plus a cleanup
// closure. `t.TempDir()` gives per-test isolation, so each scenario file
// is concurrency-safe and may opt into `t.Parallel()`.
package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap/zaptest"
)

// migrationsDirFrom is the repo-relative path from this test's working
// directory (always `internal/infrastructure/database` when running
// `go test ./internal/infrastructure/database/...`) to the canonical
// SQL migrations directory. Adjusting directory layout? Update this
// constant.
const migrationsDirFrom = "../../../migrations/sqlite"

// smokeDBConnString is the SQLite DSN used to open a fresh DB for
// pragma-based assertions. Mirrors the production DSN set in
// RunMigrationsOnDB (WAL + FK + 5s busy_timeout).
const smokeDBConnString = "_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000"

// essentialTables is the list of tables the smoke test asserts must be
// present after first apply. Each entry MUST be backed by a real
// migration in migrations/sqlite/ — adding a new entry here without a
// corresponding CREATE TABLE migration will fail the EssentialTablesPresent
// subtest on every CI run.
//
// At June 2026 HEAD: jobs (001), scripts (003), media_assets (059),
// outbox_events (092).
var essentialTables = []string{"jobs", "scripts", "media_assets", "outbox_events"}

// openSmokeDB opens a fresh sql.DB handle on path with the smoke DSN
// and pings it; failures fail the caller. Caller is responsible for
// close.
func openSmokeDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", path+"?"+smokeDBConnString)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatalf("ping %s: %v", path, err)
	}
	return db
}

// applyFreshSmokeDB opens a fresh SQLite DB in `t.TempDir()`, applies
// RunMigrationsOnDB with scope "primary", and returns the open
// `*sql.DB` plus a cleanup closure (which the test should defer).
//
// Failures here fail the caller via `t.Fatalf`. The returned handle
// is the post-apply connection — schema_migrations ledger is
// populated, WAL + FK + 5s busy_timeout are configured per DSN, and
// every additive-content scenario can build on the canonical schema.
//
// godlike/06 SSOT: this is the canonical harness for every additive
// migration scenario. New tests MUST use this helper instead of
// re-implementing their own apply path; doing so keeps CI isolation
// strict (one DB per test) AND keeps the apply path identical across
// every file (only the assertions vary).
func applyFreshSmokeDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatalf("resolve migrations dir from %s: %v", migrationsDirFrom, err)
	}
	st, err := os.Stat(targetDir)
	if err != nil {
		t.Fatalf("migrations dir %s not accessible (test must be invoked with cwd = internal/infrastructure/database): %v", targetDir, err)
	}
	if !st.IsDir() {
		t.Fatalf("migrations path %s is not a directory", targetDir)
	}
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "smoke.sqlite")
	log := zaptest.NewLogger(t)
	if err := RunMigrationsOnDB(dbPath, log, targetDir, "primary"); err != nil {
		t.Fatalf("RunMigrationsOnDB failed — wrap chain names the failing migration filename + statement index: %v", err)
	}
	db := openSmokeDB(t, dbPath)
	cleanup := func() { db.Close() }
	return db, cleanup
}

// contains is a tiny stdlib-free helper that mirrors slices.Contains
// behaviour; kept import-minimal so the smoke-baseline test file
// stays decoupled from the `slices` stdlib import surface.
func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

// scanColumnNames reads `PRAGMA table_info(<table>)` and returns the
// column names as a set. Used by every per-migration scenario test
// to assert "this column was added by migration N" without having to
// re-implement the table_info loop at every call site.
//
// The returned map is keyed by column name (value is empty struct)
// for O(1) membership lookup. The function fails the caller via
// `t.Fatalf` on any I/O error so a regression in the underlying
// SQLite connection cannot silently mask a missing-migration column.
func scanColumnNames(t *testing.T, db *sql.DB, table string) map[string]struct{} {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer rows.Close()
	out := make(map[string]struct{}, 64)
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan table_info(%s) row: %v", table, err)
		}
		out[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info(%s): %v", table, err)
	}
	return out
}

// itoa is a tiny stdlib-free formatter used to keep foreign_key_check
// violation messages compact and inspect-friendly.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
