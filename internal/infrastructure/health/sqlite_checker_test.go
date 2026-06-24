package health

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// newSQLiteTestDB returns the *sql.DB handle so existing tests can
// still use db.Exec / db.Close for fixture setup. Checker construction
// wraps the raw handle in *storage.SQLiteDB (PG-011 typed-canonical
// migration: composition root must not import database/sql).
func newSQLiteTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// wrapDB is declared in jobs_checker_test.go (single canonical helper
// shared by both checker tests in this package — same semantics).

func TestSQLiteChecker_OK(t *testing.T) {
	db := newSQLiteTestDB(t)
	if _, err := db.Exec("CREATE TABLE media_assets (id TEXT PRIMARY KEY)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	c := NewSQLiteChecker(wrapDB(db))
	res := c.CheckDB(context.Background())
	ok, _ := res["ok"].(bool)
	if !ok {
		t.Fatalf("expected ok=true, got %v", res)
	}
	if dur, ok := res["duration_ms"].(int64); !ok || dur < 0 {
		t.Fatalf("expected non-negative duration_ms, got %v", res["duration_ms"])
	}
}

func TestSQLiteChecker_TableMissing(t *testing.T) {
	db := newSQLiteTestDB(t) // empty db → no media_assets
	if _, err := db.Exec("CREATE TABLE unrelated (id INTEGER)"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	c := NewSQLiteChecker(wrapDB(db))
	res := c.CheckDB(context.Background())
	ok, _ := res["ok"].(bool)
	if ok {
		t.Fatalf("expected ok=false when media_assets is missing, got %v", res)
	}
	msg, _ := res["error"].(string)
	if msg != "migrations may not be applied" {
		t.Fatalf("expected 'migrations' error, got %v", res)
	}
}

func TestSQLiteChecker_PingFails(t *testing.T) {
	db := newSQLiteTestDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("close before check: %v", err)
	}
	c := NewSQLiteChecker(wrapDB(db))
	res := c.CheckDB(context.Background())
	ok, _ := res["ok"].(bool)
	if ok {
		t.Fatalf("expected ok=false on closed db, got %v", res)
	}
}
