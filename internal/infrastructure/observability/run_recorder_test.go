package observability

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	_ "github.com/mattn/go-sqlite3"
)

func testObservabilitySchema(t *testing.T, db *sql.DB) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE job_attempts (attempt_id TEXT PRIMARY KEY, job_id TEXT NOT NULL, run_id TEXT NOT NULL UNIQUE, attempt_number INTEGER NOT NULL, worker_id TEXT, lease_id TEXT, status TEXT NOT NULL, started_at TEXT, finished_at TEXT, lease_expires_at TEXT, error_code TEXT, error TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE run_observability (run_id TEXT PRIMARY KEY, job_id TEXT NOT NULL, job_type TEXT NOT NULL, attempt_id TEXT NOT NULL UNIQUE, parent_run_id TEXT, worker_id TEXT, lease_id TEXT, lease_expires_at TEXT, status TEXT NOT NULL, created_at TEXT NOT NULL, started_at TEXT NOT NULL, finished_at TEXT, queue_wait_ms INTEGER NOT NULL DEFAULT 0, wall_time_ms INTEGER NOT NULL DEFAULT 0, active_ms INTEGER NOT NULL DEFAULT 0, blocked_ms INTEGER NOT NULL DEFAULT 0, accumulated_operation_ms INTEGER NOT NULL DEFAULT 0, error_code TEXT, error TEXT, counters_json TEXT, children_json TEXT, report_json TEXT NOT NULL, observability_degraded INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL)`,
		`CREATE TABLE run_stage_observations (observation_id TEXT PRIMARY KEY, run_id TEXT NOT NULL, name TEXT NOT NULL, status TEXT NOT NULL, duration_ms INTEGER NOT NULL, attempts INTEGER NOT NULL, cache_status TEXT, error_code TEXT, items_input INTEGER NOT NULL, items_completed INTEGER NOT NULL, items_failed INTEGER NOT NULL, bytes_processed INTEGER NOT NULL, started_at TEXT, finished_at TEXT)`,
		`CREATE TABLE run_operation_observations (observation_id TEXT PRIMARY KEY, run_id TEXT NOT NULL, stage TEXT NOT NULL, component TEXT NOT NULL, operation TEXT NOT NULL, provider TEXT, status TEXT NOT NULL, duration_ms INTEGER NOT NULL, queue_wait_ms INTEGER NOT NULL DEFAULT 0, attempts INTEGER NOT NULL, items INTEGER NOT NULL, bytes INTEGER NOT NULL, cache_status TEXT, error_code TEXT, worker_id TEXT, queued_at TEXT, started_at TEXT, finished_at TEXT, source_sha256 TEXT NOT NULL DEFAULT '', source_duration_ms INTEGER NOT NULL DEFAULT 0, source_size_bytes INTEGER NOT NULL DEFAULT 0, width INTEGER NOT NULL DEFAULT 0, height INTEGER NOT NULL DEFAULT 0, fps REAL NOT NULL DEFAULT 0, input_codec TEXT NOT NULL DEFAULT '', output_codec TEXT NOT NULL DEFAULT '', cache_hit INTEGER NOT NULL DEFAULT 0, strategy TEXT NOT NULL DEFAULT '', metadata_json TEXT NOT NULL DEFAULT '{}', output_duration_ms INTEGER NOT NULL DEFAULT 0, output_size_bytes INTEGER NOT NULL DEFAULT 0, cpu_user_ms INTEGER NOT NULL DEFAULT 0, cpu_system_ms INTEGER NOT NULL DEFAULT 0, created_at TEXT)`,
		`CREATE TABLE run_artifact_observations (observation_id TEXT PRIMARY KEY, run_id TEXT NOT NULL, kind TEXT NOT NULL, ref TEXT, url TEXT, stage TEXT, bytes INTEGER NOT NULL, reused INTEGER NOT NULL)`,
		`CREATE TABLE run_child_observations (parent_run_id TEXT NOT NULL, child_job_id TEXT NOT NULL, child_run_id TEXT NOT NULL, status TEXT NOT NULL, wall_time_ms INTEGER NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(parent_run_id, child_job_id))`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
}

func TestSQLiteRecorder_StartReportRejectsIdentityReplay(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testObservabilitySchema(t, db)
	recorder := NewSQLiteRecorder(db)
	now := time.Now().UTC()
	first := &kernobs.RunReport{RunID: "run-a", JobID: "job-a", JobType: "script.generate", AttemptID: "attempt-a", Status: kernobs.StatusRunning, CreatedAt: now, StartedAt: now}
	if err := recorder.StartReport(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	conflicting := *first
	conflicting.JobID = "job-b"
	if err := recorder.StartReport(context.Background(), &conflicting); err == nil {
		t.Fatal("identity replay must be rejected")
	}
	var jobID string
	if err := db.QueryRow(`SELECT job_id FROM job_attempts WHERE attempt_id=?`, first.AttemptID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if jobID != "job-a" {
		t.Fatalf("replay changed immutable job identity to %q", jobID)
	}
}

func TestSQLiteRecorder_RecoverAbandonedPersistsWorkerLost(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testObservabilitySchema(t, db)
	recorder := NewSQLiteRecorder(db)
	started := time.Date(2026, time.August, 5, 10, 0, 0, 0, time.UTC)
	expires := started.Add(-time.Second)
	report := &kernobs.RunReport{RunID: "run-lost", JobID: "job-lost", JobType: "script.generate", AttemptID: "attempt-lost", LeaseID: "lease-lost", Status: kernobs.StatusRunning, CreatedAt: started, StartedAt: started, LeaseExpiresAt: expires}
	if err := recorder.StartReport(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	changed, err := recorder.RecoverAbandoned(context.Background(), started)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatalf("recovered rows = %d, want 1", changed)
	}
	var status, errorCode, reportJSON string
	if err := db.QueryRow(`SELECT status,error_code,report_json FROM run_observability WHERE run_id=?`, report.RunID).Scan(&status, &errorCode, &reportJSON); err != nil {
		t.Fatal(err)
	}
	if status != kernobs.StatusAbandoned || errorCode != "WORKER_LOST" {
		t.Fatalf("run recovery = %q/%q", status, errorCode)
	}
	var recovered kernobs.RunReport
	if err := json.Unmarshal([]byte(reportJSON), &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Status != kernobs.StatusAbandoned || recovered.ErrorCode != "WORKER_LOST" || recovered.FinishedAt.IsZero() {
		t.Fatalf("report recovery = %#v", recovered)
	}
}

func TestSQLiteRecorder_ChildLifecycleRefreshesParentIdempotently(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testObservabilitySchema(t, db)
	recorder := NewSQLiteRecorder(db)
	now := time.Now().UTC()
	parent := &kernobs.RunReport{RunID: "parent-run", JobID: "parent-job", JobType: "script.generate", AttemptID: "parent-attempt", Status: kernobs.StatusRunning, CreatedAt: now, StartedAt: now}
	if err := recorder.StartReport(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	child := &kernobs.RunReport{RunID: "child-run", JobID: "child-job", ParentRunID: parent.RunID, ParentJobID: parent.JobID, JobType: "voiceover.generate", AttemptID: "child-attempt", Status: kernobs.StatusRunning, CreatedAt: now, StartedAt: now}
	if err := recorder.RecordChild(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	child.Status = kernobs.StatusSucceeded
	child.WallTimeMs = 42
	if err := recorder.RecordChild(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	// A replay must update, not add another child.
	if err := recorder.RecordChild(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	var requested, completed, failed int
	var wall int64
	if err := db.QueryRow(`SELECT json_extract(children_json,'$.requested'), json_extract(children_json,'$.completed'), json_extract(children_json,'$.failed'), json_extract(children_json,'$.accumulated_child_ms') FROM run_observability WHERE run_id=?`, parent.RunID).Scan(&requested, &completed, &failed, &wall); err != nil {
		t.Fatal(err)
	}
	if requested != 1 || completed != 1 || failed != 0 || wall != 42 {
		t.Fatalf("parent summary = %d/%d/%d/%d", requested, completed, failed, wall)
	}
}
