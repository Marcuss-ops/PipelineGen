package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestLoadActiveResumeRefsReadsJobPayloads pins the schema contract of the
// live jobs SQLite database: job bodies live in job_payloads.payload, not in a
// jobs.payload_json column. A regression here makes cliprender-gc fail before
// it can retain any resume document.
func TestLoadActiveResumeRefsReadsJobPayloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db.sqlite")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	for _, ddl := range []string{
		`CREATE TABLE jobs (id TEXT PRIMARY KEY, status TEXT NOT NULL)`,
		`CREATE TABLE job_payloads (job_id TEXT PRIMARY KEY, payload TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}

	activeRef := strings.Repeat("a", 64)
	terminalRef := strings.Repeat("b", 64)
	for _, row := range []struct{ id, status, ref string }{
		{"job-active", "RUNNING", activeRef},
		{"job-done", "SUCCEEDED", terminalRef},
	} {
		if _, err := db.Exec(`INSERT INTO jobs (id, status) VALUES (?, ?)`, row.id, row.status); err != nil {
			t.Fatalf("insert job %s: %v", row.id, err)
		}
		if _, err := db.Exec(`INSERT INTO job_payloads (job_id, payload) VALUES (?, ?)`,
			row.id, `{"resume":"`+row.ref+`"}`); err != nil {
			t.Fatalf("insert payload %s: %v", row.id, err)
		}
	}

	refs, err := loadActiveResumeRefs(context.Background(), path)
	if err != nil {
		t.Fatalf("load refs: %v", err)
	}
	if _, ok := refs[activeRef]; !ok {
		t.Fatalf("active job resume ref %s missing from %v", activeRef, refs)
	}
	if _, ok := refs[terminalRef]; ok {
		t.Fatalf("terminal job resume ref %s must not be retained", terminalRef)
	}
}
