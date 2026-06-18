package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestDBOpts controls behavior of NewTestDB.
type TestDBOpts struct {
	// InMemory uses an in-memory SQLite database. Faster but cannot be backed up.
	// Default: false (uses temp file).
	InMemory bool

	// SkipMigrations disables the default table creation for the test.
	// Default: false.
	SkipMigrations bool
}

// NewTestDB creates a properly configured SQLite database for testing.
//
// All test databases use WAL mode + busy_timeout=5000 + txlock=immediate
// by default, matching production. The database is created in a temp directory
// that is cleaned up automatically when the test finishes via t.Cleanup.
//
// Usage:
//
//	db := storage.NewTestDB(t, nil)
//	defer db.Close()
func NewTestDB(t *testing.T, opts *TestDBOpts) *sql.DB {
	t.Helper()

	if opts == nil {
		opts = &TestDBOpts{}
	}

	var db *sql.DB
	if opts.InMemory {
		dsn := "file::memory:?cache=shared&_journal_mode=MEMORY&_busy_timeout=5000&_txlock=immediate"
		var err error
		db, err = sql.Open("sqlite3", dsn)
		if err != nil {
			t.Fatalf("failed to open in-memory test db: %v", err)
		}
	} else {
		tmpDir, err := os.MkdirTemp("", "pipelinegen_test_*")
		if err != nil {
			t.Fatalf("failed to create temp dir for test db: %v", err)
		}

		t.Cleanup(func() {
			// Close the database before removing files (avoids "database is locked" on Windows)
			if db != nil {
				db.Close()
			}
			os.RemoveAll(tmpDir)
		})

		dbPath := filepath.Join(tmpDir, "test.db")
		dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate"

		db, err = sql.Open("sqlite3", dsn)
		if err != nil {
			t.Fatalf("failed to open test db at %s: %v", dbPath, err)
		}
	}

	// Connection pool: single connection for test determinism
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// Verify connection
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatalf("failed to ping test db: %v", err)
	}

	// Ensure WAL mode and busy_timeout are active (belt-and-suspenders)
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			t.Logf("warning: pragma %s failed: %v", p, err)
		}
	}

	return db
}

// NewTestDBWithSchema creates a test database and runs the given schema SQL.
// Use this when you need specific tables created before your test runs.
//
// Usage:
//
//	db := storage.NewTestDBWithSchema(t, testSchema)
//	defer db.Close()
func NewTestDBWithSchema(t *testing.T, schema string) *sql.DB {
	t.Helper()

	db := NewTestDB(t, nil)
	if schema != "" {
		if _, err := db.Exec(schema); err != nil {
			db.Close()
			t.Fatalf("failed to run test schema: %v\nSchema:\n%s", err, schema)
		}
	}
	return db
}

// MustExec is a test helper that runs a SQL statement and fails the test on error.
// Useful for quickly inserting test data.
//
// Example:
//
//	storage.MustExec(t, db, "INSERT INTO clips (id, name) VALUES (?, ?)", "cl_1", "Test clip")
func MustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("MustExec failed: %v\nQuery: %s\nArgs: %v", err, query, args)
	}
}

// MustQueryRow is a test helper that runs a query expecting exactly one row,
// returning the *sql.Row for scanning.
func MustQueryRow(t *testing.T, db *sql.DB, query string, args ...any) *sql.Row {
	t.Helper()
	return db.QueryRow(query, args...)
}

// CountRows returns the number of rows matching a query. Fails the test on error.
func CountRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("CountRows failed: %v\nQuery: %s", err, query)
	}
	return count
}

// AssertNoRows fails the test if the query returns any rows.
func AssertNoRows(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatalf("AssertNoRows query failed: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatalf("expected no rows but found at least one\nQuery: %s", query)
	}
}

// TestSchema is a convenience wrapper that builds a CREATE TABLE statement
// and returns it for use with NewTestDBWithSchema.
//
// Usage:
//
//	schema := storage.TestSchema("clips", "id TEXT PRIMARY KEY, name TEXT, tags TEXT")
//	db := storage.NewTestDBWithSchema(t, schema)
func TestSchema(tableName string, columns ...string) string {
	schema := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n", tableName)
	for i, col := range columns {
		schema += "  " + col
		if i < len(columns)-1 {
			schema += ","
		}
		schema += "\n"
	}
	schema += ")"
	return schema
}
