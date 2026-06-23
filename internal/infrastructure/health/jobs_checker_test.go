package health

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestJobsChecker_OK(t *testing.T) {
	db := newSQLiteTestDB(t)
	if _, err := db.Exec("CREATE TABLE jobs (id TEXT PRIMARY KEY, status TEXT)"); err != nil {
		t.Fatalf("create jobs table: %v", err)
	}
	c := NewJobsChecker(db)
	res := c.CheckJobs(context.Background())
	ok, _ := res["ok"].(bool)
	if !ok {
		t.Fatalf("expected ok=true, got %v", res)
	}
}

func TestJobsChecker_TableMissing(t *testing.T) {
	db := newSQLiteTestDB(t)
	c := NewJobsChecker(db)
	res := c.CheckJobs(context.Background())
	ok, _ := res["ok"].(bool)
	if ok {
		t.Fatalf("expected ok=false when jobs table is missing, got %v", res)
	}
	msg, _ := res["error"].(string)
	if msg != "jobs table not found" {
		t.Fatalf("expected 'jobs table not found' error, got %v", res)
	}
}

func TestJobsChecker_PingFails(t *testing.T) {
	db := newSQLiteTestDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("close before check: %v", err)
	}
	c := NewJobsChecker(db)
	res := c.CheckJobs(context.Background())
	ok, _ := res["ok"].(bool)
	if ok {
		t.Fatalf("expected ok=false on closed db, got %v", res)
	}
}
