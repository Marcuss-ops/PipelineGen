package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestDBOpts controls behavior of NewTestDB.
type TestDBOpts struct {
	// InMemory uses an in-memory SQLite database. Faster but cannot be backed up.
	InMemory bool
	// SkipMigrations disables the default table creation for the test.
	SkipMigrations bool
}

// NewTestDB is the in-package fixture used by this package's own tests. Other
// packages use internal/platform/sqlite/sqltest, which cannot be imported here
// without an import cycle through migrateAll.
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
			if db != nil {
				_ = db.Close()
			}
			_ = os.RemoveAll(tmpDir)
		})

		dbPath := filepath.Join(tmpDir, "test.db")
		dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate"
		db, err = sql.Open("sqlite3", dsn)
		if err != nil {
			t.Fatalf("failed to open test db at %s: %v", dbPath, err)
		}
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatalf("failed to ping test db: %v", err)
	}
	for _, p := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(p); err != nil {
			t.Logf("warning: pragma %s failed: %v", p, err)
		}
	}
	return db
}
