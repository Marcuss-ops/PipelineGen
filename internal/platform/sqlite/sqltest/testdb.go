// Package sqltest is the test-only SQLite fixture surface.
//
// It lives in its own package so the production sqlite package does not import
// "testing" and does not link the fixture constructors into the server binary.
// Tests in other packages may import this package; same-package tests keep a
// small in-package copy (testdb_test.go) because importing sqltest would create
// an import cycle through the migration runner.
package sqltest

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
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
//	db := sqltest.NewTestDB(t, nil)
//	defer db.Close()
func NewTestDB(t *testing.T, opts *TestDBOpts) *sql.DB {
	t.Helper()
	db, _ := newTestDB(t, opts)
	return db
}

// newTestDB also returns the on-disk path ("" for an in-memory database) so
// NewMigratedTestDB can run the migration chain on the same file.
func newTestDB(t *testing.T, opts *TestDBOpts) (*sql.DB, string) {
	t.Helper()

	if opts == nil {
		opts = &TestDBOpts{}
	}

	var db *sql.DB
	var dbPath string
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
				_ = db.Close()
			}
			_ = os.RemoveAll(tmpDir)
		})

		dbPath = filepath.Join(tmpDir, "test.db")
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
		_ = db.Close()
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

	return db, dbPath
}

// NewTestDBWithSchema creates a test database and runs the given schema SQL.
//
// DEPRECATED (PR-MIGRATIONS-SSOT, August 2026): prefer NewMigratedTestDB(t)
// which runs the full migration chain.
func NewTestDBWithSchema(t *testing.T, schema string) *sql.DB {
	t.Helper()

	db := NewTestDB(t, nil)
	if schema != "" {
		if _, err := db.Exec(schema); err != nil {
			_ = db.Close()
			t.Fatalf("failed to run test schema: %v\nSchema:\n%s", err, schema)
		}
	}
	return db
}

// NewMigratedTestDB creates a temp SQLite DB, runs the full migration chain
// from migrations/sqlite/, and returns the open *sql.DB. This is the
// canonical fixture helper for tests that need the production schema.
func NewMigratedTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dir := sqlite.MigrationsDir()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("NewMigratedTestDB: migrations dir not found at %s: %v (run from project root)", dir, err)
	}

	db, dbPath := newTestDB(t, nil)
	if dbPath == "" {
		t.Fatal("NewMigratedTestDB requires an on-disk database")
	}
	if err := sqlite.RunMigrationsOnDB(dbPath, zap.NewNop(), dir, "primary"); err != nil {
		_ = db.Close()
		t.Fatalf("NewMigratedTestDB: apply migrations: %v", err)
	}
	return db
}

// MustExec is a test helper that runs a SQL statement and fails the test on error.
func MustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("MustExec failed: %v\nQuery: %s\nArgs: %v", err, query, args)
	}
}
