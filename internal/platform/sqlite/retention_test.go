package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func openRetentionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "retention.db.sqlite")
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestApplyRetentionPlanDeletesOldRowsAndVacuum(t *testing.T) {
	db := openRetentionTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`CREATE TABLE telemetry (id INTEGER PRIMARY KEY, created_at TEXT NOT NULL, payload TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		ts := now.AddDate(0, 0, -10).Format(time.RFC3339Nano)
		if i >= 10 {
			ts = now.AddDate(0, 0, -1).Format(time.RFC3339Nano)
		}
		if _, err := db.Exec(`INSERT INTO telemetry (created_at, payload) VALUES (?, ?)`, ts, "row"); err != nil {
			t.Fatalf("insert row %d: %v", i, err)
		}
	}

	plan := RetentionPlan{
		Name: "test",
		Path: filepath.Join(t.TempDir(), "retention.db.sqlite"),
		Ops: []RetentionOp{{
			Table:      "telemetry",
			TimeColumn: "created_at",
			Cutoff:     now.AddDate(0, 0, -7),
		}},
	}
	res, err := ApplyRetentionPlan(ctx, db, plan, false, true)
	if err != nil {
		t.Fatalf("apply retention: %v", err)
	}
	if len(res.Tables) != 1 || res.Tables[0].DeletedRows != 10 {
		t.Fatalf("deleted rows = %+v, want 10", res.Tables)
	}
	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM telemetry`).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 10 {
		t.Fatalf("remaining rows = %d, want 10", remaining)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("integrity_check = %q", integrity)
	}
	if !res.Vacuumed {
		t.Fatal("expected vacuum to run")
	}
	if res.PostSizeBytes > res.PreSizeBytes {
		t.Fatalf("post size %d > pre size %d", res.PostSizeBytes, res.PreSizeBytes)
	}
}

func TestApplyRetentionPlanDryRunDeletesNothing(t *testing.T) {
	db := openRetentionTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`CREATE TABLE telemetry (id INTEGER PRIMARY KEY, created_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := db.Exec(`INSERT INTO telemetry (created_at) VALUES ('2026-01-01T00:00:00Z')`); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	plan := RetentionPlan{
		Name: "test",
		Path: filepath.Join(t.TempDir(), "retention.db.sqlite"),
		Ops: []RetentionOp{{
			Table:      "telemetry",
			TimeColumn: "created_at",
			Cutoff:     time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC),
		}},
	}
	res, err := ApplyRetentionPlan(ctx, db, plan, true, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Tables[0].DeletedRows != 5 {
		t.Fatalf("dry-run matched rows = %d, want 5", res.Tables[0].DeletedRows)
	}
	if res.Vacuumed {
		t.Fatal("dry run must not vacuum")
	}
	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM telemetry`).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 5 {
		t.Fatalf("dry run deleted rows: remaining = %d, want 5", remaining)
	}
}

func TestApplyRetentionPlanCascadesChildren(t *testing.T) {
	db := openRetentionTestDB(t)
	ctx := context.Background()
	ddl := []string{
		`CREATE TABLE run_observability (run_id TEXT PRIMARY KEY, created_at TEXT NOT NULL)`,
		`CREATE TABLE run_operation_observations (
			observation_id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			created_at TEXT,
			FOREIGN KEY(run_id) REFERENCES run_observability(run_id) ON DELETE CASCADE
		)`,
	}
	for _, stmt := range ddl {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}
	for i := 0; i < 6; i++ {
		runID := fmt.Sprintf("run-%d", i)
		created := "2026-01-01T00:00:00Z"
		if i >= 3 {
			created = "2026-10-20T00:00:00Z"
		}
		if _, err := db.Exec(`INSERT INTO run_observability (run_id, created_at) VALUES (?, ?)`, runID, created); err != nil {
			t.Fatalf("insert run: %v", err)
		}
		for j := 0; j < 4; j++ {
			if _, err := db.Exec(`INSERT INTO run_operation_observations (observation_id, run_id, created_at) VALUES (?, ?, ?)`,
				fmt.Sprintf("%s-%d", runID, j), runID, created); err != nil {
				t.Fatalf("insert child: %v", err)
			}
		}
	}

	plan := RetentionPlan{
		Name: "observability",
		Path: filepath.Join(t.TempDir(), "retention.db.sqlite"),
		Ops: []RetentionOp{{
			Table:      "run_observability",
			TimeColumn: "created_at",
			Cutoff:     time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		}},
	}
	if _, err := ApplyRetentionPlan(ctx, db, plan, false, false); err != nil {
		t.Fatalf("apply retention: %v", err)
	}
	var runs, children int
	if err := db.QueryRow(`SELECT COUNT(*) FROM run_observability`).Scan(&runs); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM run_operation_observations`).Scan(&children); err != nil {
		t.Fatalf("count children: %v", err)
	}
	if runs != 3 || children != 12 {
		t.Fatalf("after retention runs=%d children=%d, want 3/12 (cascade)", runs, children)
	}
}

func TestApplyRetentionPlanRejectsInvalidIdentifier(t *testing.T) {
	db := openRetentionTestDB(t)
	plan := RetentionPlan{
		Name: "test",
		Path: filepath.Join(t.TempDir(), "retention.db.sqlite"),
		Ops: []RetentionOp{{
			Table:      `telemetry; DROP TABLE jobs`,
			TimeColumn: "created_at",
			Cutoff:     time.Now().UTC(),
		}},
	}
	if _, err := ApplyRetentionPlan(context.Background(), db, plan, false, false); err == nil {
		t.Fatal("expected invalid identifier error")
	}
}
