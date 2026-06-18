// Package storage — Test helpers for unit tests.
//
// NewTestDB returns a *sql.DB suitable for tests. The caller is responsible
// for the returned handle's lifetime — production call sites use
// `defer db.Close()` and that is the contract this helper preserves
// (NewTestDB does NOT register a t.Cleanup, to avoid a double Close).
//
// NewTestDBWithSchema opens an in-memory database and applies the given DDL
// statements in order, returning the *sql.DB. Useful for tests that need a
// minimal schema without spinning up the migration runner.
//
// MustExec executes a single SQL statement or fails the test via t.Fatalf.
// Pairs naturally with NewTestDB / NewTestDBWithSchema for readable fixtures.
package storage

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3" // sqlite3 driver (FTS5 disabled in upstream build, not needed here)
	"go.uber.org/zap"
)

// TestDBOpts controls NewTestDB behavior.
//
//   - InMemory (default true for test ergonomics): opens a shared in-memory
//     SQLite database (file::memory:?cache=shared), useful when many tests
//     in one binary run side by side.
//   - DataDir / DBName: only used when InMemory=false; delegates to
//     NewSQLiteDB and registers t.Cleanup so the test does not need to
//     remember to Close().
type TestDBOpts struct {
	InMemory bool
	DataDir  string
	DBName   string
}

// NewTestDB returns a *sql.DB for tests.
//
// Signature: `NewTestDB(t, opts) -> *sql.DB`. Caller owns the lifetime of
// the handle (production call sites do `defer db.Close()`). For InMemory,
// the returned handle is a plain *sql.DB without auto-Cleanup so the
// explicit defer remains the single owner.
func NewTestDB(t *testing.T, opts *TestDBOpts) *sql.DB {
	t.Helper()
	if opts == nil {
		opts = &TestDBOpts{InMemory: true}
	}

	if opts.InMemory {
		// Use a unique in-memory database per test to prevent test
		// cross-contamination. The "cache=shared" mode is NOT used here
		// because it would cause schema_migrations and other tables from
		// one test to leak into another.
		// For tests that need shared-memory access across connections,
		// use InMemory=false with a temp file.
		db, err := sql.Open("sqlite3", "file::memory:?_foreign_keys=on&_busy_timeout=5000")
		if err != nil {
			t.Fatalf("storage.NewTestDB: open in-memory: %v", err)
		}
		if err := db.Ping(); err != nil {
			t.Fatalf("storage.NewTestDB: ping: %v", err)
		}
		return db
	}

	dbName := opts.DBName
	if dbName == "" {
		dbName = ":memory:"
	}
	sdb, err := NewSQLiteDB(opts.DataDir, dbName, zap.NewNop())
	if err != nil {
		t.Fatalf("storage.NewTestDB: open sqlite (dir=%s, name=%s): %v", opts.DataDir, dbName, err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	return sdb.DB
}

// NewTestDBWithSchema opens an in-memory database and applies the given DDL
// statements in order. Each schema string may contain multiple statements
// (sqlite3 driver executes a multi-statement Exec in one round trip).
// Returns the *sql.DB; caller owns its lifetime.
//
// Variadic so callers can pass either a single multi-statement string
// (`NewTestDBWithSchema(t, singleDdl)`) or a spread list
// (`NewTestDBWithSchema(t, schemas...)`).
func NewTestDBWithSchema(t *testing.T, schemas ...string) *sql.DB {
	t.Helper()
	db := NewTestDB(t, &TestDBOpts{InMemory: true})
	for i, schema := range schemas {
		if _, err := db.Exec(schema); err != nil {
			t.Fatalf("storage.NewTestDBWithSchema: schema[%d] failed: %v\nSQL: %s", i, err, schema)
		}
	}
	return db
}

// MustExec executes a single SQL statement or fails the test. Use for
// fixture setup, NOT for assertions (assertions should use require.*).
func MustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("storage.MustExec: %v\nSQL: %s", err, query)
	}
}
