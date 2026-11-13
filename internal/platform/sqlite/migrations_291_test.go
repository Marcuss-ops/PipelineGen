package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestMigrations_291_RunReportSnapshotsAreAppendOnly applies the real migration
// file to a minimal observability schema and exercises the triggers: the
// initial INSERT is captured, a changed report_json appends, an unchanged
// report_json does not duplicate, and the snapshot itself cannot be rewritten.
func TestMigrations_291_RunReportSnapshotsAreAppendOnly(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(schemaMigrationsTable); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE run_observability (
		run_id TEXT PRIMARY KEY,
		report_json TEXT NOT NULL DEFAULT '{}',
		updated_at TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create run_observability: %v", err)
	}

	const filename = "291_run_report_snapshots.sql"
	content, err := os.ReadFile(filepath.Join(migrationsDirFrom, filename))
	if err != nil {
		t.Fatalf("read %s: %v", filename, err)
	}
	if err := applyMigration(db, nil, 291, filename, "test-checksum", content); err != nil {
		t.Fatalf("apply migration 291: %v", err)
	}

	insert := `INSERT INTO run_observability (run_id, report_json, updated_at) VALUES (?, ?, ?)`
	if _, err := db.Exec(insert, "run-1", `{"status":"RUNNING"}`, "2026-11-12T10:00:00Z"); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	if got := snapshotCount(t, db); got != 1 {
		t.Fatalf("snapshots after insert = %d, want 1", got)
	}

	// SaveReport may run more than once with the same terminal body (retry /
	// idempotent finalization). It must not inflate the history.
	if _, err := db.Exec(`UPDATE run_observability SET report_json=?, updated_at=? WHERE run_id=?`,
		`{"status":"RUNNING"}`, "2026-11-12T10:00:01Z", "run-1"); err != nil {
		t.Fatalf("unchanged update: %v", err)
	}
	if got := snapshotCount(t, db); got != 1 {
		t.Fatalf("snapshots after unchanged update = %d, want 1", got)
	}

	if _, err := db.Exec(`UPDATE run_observability SET report_json=?, updated_at=? WHERE run_id=?`,
		`{"status":"SUCCEEDED"}`, "2026-11-12T10:00:02Z", "run-1"); err != nil {
		t.Fatalf("changed update: %v", err)
	}
	rows, err := db.Query(`SELECT report_json FROM run_report_snapshots WHERE run_id=? ORDER BY snapshot_id`, "run-1")
	if err != nil {
		t.Fatalf("query snapshots: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var bodies []string
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			t.Fatalf("scan snapshot: %v", err)
		}
		bodies = append(bodies, body)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate snapshots: %v", err)
	}
	if len(bodies) != 2 || bodies[0] != `{"status":"RUNNING"}` || bodies[1] != `{"status":"SUCCEEDED"}` {
		t.Fatalf("snapshot history = %v, want RUNNING then SUCCEEDED", bodies)
	}

	if _, err := db.Exec(`UPDATE run_report_snapshots SET report_json='{}' WHERE snapshot_id=1`); err == nil {
		t.Fatal("updating a snapshot succeeded, want append-only rejection")
	} else if !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("snapshot update error = %v, want append-only", err)
	}
}

func snapshotCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM run_report_snapshots`).Scan(&count); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	return count
}
