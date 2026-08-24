// Package app — performance_projection_integration_test.go
//
// End-to-end integration test for the derived performance projection:
//
//	SQLiteStore.Complete/Fail (terminal flip + job.completed outbox event)
//	    → outbox_events row (durable, pending)
//	    → Repository.ClaimNext (lease-fenced claim)
//	    → jobCompletedPerformanceAdapter.Handle (extracts job id)
//	    → perfstore.Projection → capabilities/performance.Projector
//	    → performance_runs / performance_steps
//
// This drives every production component against a real SQLite database:
// the canonical SQLiteStore, the real outbox repository (ClaimNext +
// MarkCompleted, exactly the claim→handle→mark-completed dispatch that
// outboxevents.Pool.processEvent performs), the real composition-root
// jobCompletedPerformanceAdapter, and the real platform projection +
// capability projector. The Pool's panic-recovery / backoff / terminal
// classification wrappers are intentionally not re-exercised here — they are
// covered by internal/infrastructure/database/sqlite/outboxevents/
// pool_resiliency_test.go.
//
// godlike/07 no-fake-availability: every stage is a real SQL round-trip; no
// mocks, no t.Skip, no white-box stubs. A broken hop fails the test instead
// of silently degrading.
package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	sqlitejobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	perfstore "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/performance"
)

// performanceProjectionSchema is the canonical SQLite schema subset needed by
// the integration flow: the jobs + job_events tables (written by Complete /
// Fail), the outbox_events table (production DDL from migration 092 + 186),
// the job_steps table (read by the report source), and the performance_runs /
// performance_steps registry tables (written by the projection). Each test
// gets a fresh in-memory database pair.
const performanceProjectionSchema = `
CREATE TABLE jobs (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    status TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0,
    project TEXT NOT NULL DEFAULT '',
    video_name TEXT NOT NULL DEFAULT '',
    active_key TEXT NOT NULL DEFAULT '',
    correlation_id TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    result_json TEXT NOT NULL DEFAULT '{}',
    progress INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    retry_count INTEGER NOT NULL DEFAULT 0,
    max_retries INTEGER NOT NULL DEFAULT 3,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_id TEXT NOT NULL DEFAULT '',
    lease_expiry DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    started_at DATETIME,
    completed_at DATETIME,
    cancelled_at DATETIME,
    parent_state_typed TEXT NOT NULL DEFAULT '',
    parent_job_id TEXT NOT NULL DEFAULT '',
    root_job_id TEXT NOT NULL DEFAULT '',
    video_id TEXT NOT NULL DEFAULT '',
    git_sha TEXT NOT NULL DEFAULT '',
    host TEXT NOT NULL DEFAULT '',
    revision INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE job_events (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    type TEXT NOT NULL,
    message TEXT DEFAULT '',
    data_json TEXT DEFAULT '{}',
    created_at DATETIME
);

CREATE TABLE outbox_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL DEFAULT '',
    aggregate_type TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '',
    event_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10,
    last_error TEXT NOT NULL DEFAULT '',
    next_attempt_at TEXT,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_id TEXT NOT NULL DEFAULT '',
    lease_expiry TEXT,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT '',
    priority INTEGER NOT NULL DEFAULT 5
);
CREATE UNIQUE INDEX ux_outbox_events_event_key ON outbox_events(event_key);
CREATE INDEX idx_outbox_events_status_next_attempt ON outbox_events(status, next_attempt_at, id);
CREATE INDEX idx_outbox_events_status_priority_claim ON outbox_events(status, priority, next_attempt_at, id);

CREATE TABLE job_steps (
    step_id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    step_name TEXT NOT NULL,
    step_type TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT,
    duration_ms INTEGER NOT NULL,
    input_count INTEGER NOT NULL,
    output_count INTEGER NOT NULL,
    input_bytes INTEGER NOT NULL,
    output_bytes INTEGER NOT NULL,
    metrics_json TEXT NOT NULL,
    error_code TEXT NOT NULL,
    error_message TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE performance_runs (
    run_id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL DEFAULT '',
    root_job_id TEXT NOT NULL DEFAULT '',
    video_id TEXT NOT NULL DEFAULT '',
    workload_id TEXT NOT NULL DEFAULT '',
    workload_version TEXT NOT NULL DEFAULT '',
    git_sha TEXT NOT NULL DEFAULT '',
    worker_id TEXT NOT NULL DEFAULT '',
    host_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    wall_ms INTEGER NOT NULL DEFAULT 0,
    cpu_user_ms INTEGER NOT NULL DEFAULT 0,
    cpu_system_ms INTEGER NOT NULL DEFAULT 0,
    peak_rss_bytes INTEGER NOT NULL DEFAULT 0,
    disk_read_bytes INTEGER NOT NULL DEFAULT 0,
    disk_write_bytes INTEGER NOT NULL DEFAULT 0,
    network_rx_bytes INTEGER NOT NULL DEFAULT 0,
    network_tx_bytes INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    started_at TEXT NOT NULL,
    completed_at TEXT
);

CREATE TABLE performance_steps (
    step_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    job_id TEXT NOT NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    input_count INTEGER NOT NULL DEFAULT 0,
    output_count INTEGER NOT NULL DEFAULT 0,
    input_bytes INTEGER NOT NULL DEFAULT 0,
    output_bytes INTEGER NOT NULL DEFAULT 0,
    cache_hits INTEGER NOT NULL DEFAULT 0,
    cache_misses INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    started_at TEXT NOT NULL,
    completed_at TEXT
);
`

// performanceProjectionObsSchema is the run_observability schema read by the
// report source (loadRun) — the finalized RunReport + audio workflow
// checkpoint live here.
const performanceProjectionObsSchema = `
CREATE TABLE run_observability (
    run_id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    job_type TEXT NOT NULL,
    attempt_id TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TEXT NOT NULL,
    started_at TEXT NOT NULL,
    finished_at TEXT,
    report_json TEXT NOT NULL,
    workflow_payload_json TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
`

// setupPerformanceProjectionDBs opens a fresh primary (jobs + outbox +
// performance registry) and observability (run_observability) in-memory
// database pair.
func setupPerformanceProjectionDBs(t *testing.T) (primary, obs *sql.DB) {
	t.Helper()
	var err error
	primary, err = sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = primary.Close() })
	obs, err = sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = obs.Close() })

	if _, err := primary.Exec(performanceProjectionSchema); err != nil {
		t.Fatalf("create primary schema: %v", err)
	}
	if _, err := obs.Exec(performanceProjectionObsSchema); err != nil {
		t.Fatalf("create observability schema: %v", err)
	}
	return primary, obs
}

// seedRunningJob inserts a RUNNING job claimed by (workerID, leaseID) at the
// given revision, plus its finalized run report and two runner execution
// steps (DOCUMENT + VELOX_ENQUEUE). The report status + wall time drive the
// projected run row; the ollama generate operation and audio mix_ms drive two
// measured phases so the projection writes steps.
func seedRunningJobWithFinalizedRun(t *testing.T, primary, obs *sql.DB, jobID, runID, runStatus string, wallMS int64) {
	t.Helper()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)
	started := now.Add(-time.Duration(wallMS) * time.Millisecond)

	_, err := primary.Exec(`
		INSERT INTO jobs (id, type, status, worker_id, lease_id, lease_expiry, created_at, updated_at, started_at, revision, root_job_id, video_id, git_sha, host)
		VALUES (?, 'script.generate', 'RUNNING', 'worker-A', 'lease-X', NULL, ?, ?, ?, 5, 'root-job', 'video-1', 'abc123', 'host-1')
	`, jobID, nowStr, nowStr, started.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("seed RUNNING job %q: %v", jobID, err)
	}

	report := kernobs.RunReport{
		RunID:      runID,
		JobID:      jobID,
		JobType:    "script.generate",
		Status:     runStatus,
		WorkerID:   "worker-A",
		StartedAt:  started,
		FinishedAt: now,
		WallTimeMs: wallMS,
		Operations: []kernobs.OperationReport{
			{Component: string(kernobs.ComponentOllama), Operation: string(kernobs.OperationGenerate), DurationMs: 18340, Items: 1},
			{Stage: "audio", Component: "rust", Operation: "mix", DurationMs: 4120},
		},
	}
	reportJSON, err := report.JSON()
	if err != nil {
		t.Fatal(err)
	}

	checkpoint := struct {
		Result *scriptgeneration.GenerateResult `json:"result,omitempty"`
	}{
		Result: &scriptgeneration.GenerateResult{},
	}
	payloadJSON, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := obs.Exec(`
		INSERT INTO run_observability (run_id, job_id, job_type, attempt_id, status, created_at, started_at, finished_at, report_json, workflow_payload_json, updated_at)
		VALUES (?, ?, 'script.generate', 'attempt-e2e', ?, ?, ?, ?, ?, ?, ?)
	`, runID, jobID, runStatus, nowStr, started.Format(time.RFC3339Nano), nowStr, string(reportJSON), string(payloadJSON), nowStr); err != nil {
		t.Fatalf("seed run_observability %q: %v", jobID, err)
	}

	for _, st := range []struct {
		id, name string
		dur      int64
	}{
		{"step-doc", "DOCUMENT", 1241},
		{"step-enq", "VELOX_ENQUEUE", 115},
	} {
		if _, err := primary.Exec(`
			INSERT INTO job_steps (step_id, job_id, step_name, step_type, status, started_at, completed_at, duration_ms, input_count, output_count, input_bytes, output_bytes, metrics_json, error_code, error_message, created_at)
			VALUES (?, ?, ?, 'phase', 'COMPLETED', ?, ?, ?, 0, 0, 0, 0, '{}', '', '', ?)
		`, st.id, jobID, st.name, nowStr, nowStr, st.dur, nowStr); err != nil {
			t.Fatalf("seed job_step %q: %v", st.id, err)
		}
	}
}

// claimHandleAndComplete drives the outbox consumption exactly the way
// outboxevents.Pool.processEvent does (claim → handle → mark completed),
// using the real repository and the real composition-root adapter.
func claimHandleAndComplete(t *testing.T, primary *sql.DB, adapter outboxevents.Handler) {
	t.Helper()
	ctx := context.Background()
	repo := outboxevents.NewRepository(primary)

	claim, err := repo.ClaimNext(ctx, "test-worker", time.Minute)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claim == nil {
		t.Fatal("ClaimNext returned no event (job.completed must be pending)")
	}
	if claim.Event.EventType != outboxevents.EventJobCompleted {
		t.Fatalf("claimed event type = %q, want %q", claim.Event.EventType, outboxevents.EventJobCompleted)
	}

	if err := adapter.Handle(ctx, claim.Event); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if err := repo.MarkCompleted(ctx, claim.Event.ID, claim.LeaseID); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
}

func assertProjectedRun(t *testing.T, primary *sql.DB, jobID, runID, wantStatus string, wantWallMS int64) {
	t.Helper()
	var gotStatus string
	var gotRootJobID, gotVideoID, gotGitSHA, gotHost string
	var gotWallMS int64
	if err := primary.QueryRow(`
		SELECT status, root_job_id, video_id, git_sha, host_id, wall_ms
		FROM performance_runs WHERE job_id = ? AND run_id = ?
	`, jobID, runID).Scan(&gotStatus, &gotRootJobID, &gotVideoID, &gotGitSHA, &gotHost, &gotWallMS); err != nil {
		t.Fatalf("read projected run %q: %v", jobID, err)
	}
	if gotStatus != wantStatus {
		t.Errorf("projected status = %q, want %q", gotStatus, wantStatus)
	}
	if gotRootJobID != "root-job" || gotVideoID != "video-1" || gotGitSHA != "abc123" || gotHost != "host-1" {
		t.Errorf("correlation columns = root=%q video=%q sha=%q host=%q, want root-job/video-1/abc123/host-1",
			gotRootJobID, gotVideoID, gotGitSHA, gotHost)
	}
	if gotWallMS != wantWallMS {
		t.Errorf("wall_ms = %d, want %d", gotWallMS, wantWallMS)
	}
}

func TestPerformanceProjection_EndToEnd_CompleteConsumesAndProjects(t *testing.T) {
	primary, obs := setupPerformanceProjectionDBs(t)
	store := sqlitejobs.NewSQLiteStore(primary, zap.NewNop())
	ctx := context.Background()

	const (
		jobID = "job-e2e-complete"
		runID = "run-e2e-complete"
	)
	seedRunningJobWithFinalizedRun(t, primary, obs, jobID, runID, kernobs.StatusSucceeded, 87431)

	// Stage 1 — Complete: terminal flip + job.completed outbox event.
	if err := store.Complete(ctx, jobID, "worker-A", "lease-X", 5, []byte(`{}`)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var pendingCount int
	if err := primary.QueryRow(`
		SELECT COUNT(*) FROM outbox_events
		WHERE event_type = ? AND aggregate_id = ? AND status = 'pending'
	`, outboxevents.EventJobCompleted, jobID).Scan(&pendingCount); err != nil {
		t.Fatal(err)
	}
	if pendingCount != 1 {
		t.Fatalf("pending job.completed events = %d, want 1", pendingCount)
	}

	// Stage 2+3+4 — claim → handler → projection, then mark completed.
	proj, err := perfstore.NewProjection(primary, obs)
	if err != nil {
		t.Fatalf("NewProjection: %v", err)
	}
	adapter := jobCompletedPerformanceAdapter{projection: proj, log: zap.NewNop()}
	claimHandleAndComplete(t, primary, adapter)

	// The event must have reached the terminal outbox state.
	var eventStatus string
	if err := primary.QueryRow(`SELECT status FROM outbox_events WHERE event_type = ? AND aggregate_id = ?`,
		outboxevents.EventJobCompleted, jobID).Scan(&eventStatus); err != nil {
		t.Fatal(err)
	}
	if eventStatus != "completed" {
		t.Errorf("outbox event status = %q, want %q", eventStatus, "completed")
	}

	assertProjectedRun(t, primary, jobID, runID, "SUCCEEDED", 87431)

	// Measured phases become steps: ollama generate + rust_mix + DOCUMENT +
	// VELOX_ENQUEUE. Every step must be correlated to the run.
	var stepCount int
	if err := primary.QueryRow(`SELECT COUNT(*) FROM performance_steps WHERE run_id = ?`, runID).Scan(&stepCount); err != nil {
		t.Fatal(err)
	}
	if stepCount != 4 {
		t.Fatalf("performance_steps = %d, want 4 (script_gemma, rust_mix, google_doc, render_enqueue)", stepCount)
	}
	wantSteps := map[string]int64{
		"script_gemma":   18340,
		"rust_mix":       4120,
		"google_doc":     1241,
		"render_enqueue": 115,
	}
	rows, err := primary.Query(`SELECT name, duration_ms FROM performance_steps WHERE run_id = ?`, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name string
		var dur int64
		if err := rows.Scan(&name, &dur); err != nil {
			t.Fatal(err)
		}
		want, ok := wantSteps[name]
		if !ok {
			t.Errorf("unexpected step %q", name)
			continue
		}
		if dur != want {
			t.Errorf("step %q duration_ms = %d, want %d", name, dur, want)
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != 4 {
		t.Errorf("matched steps = %d, want 4", seen)
	}
}

func TestPerformanceProjection_EndToEnd_FailConsumesAndProjects(t *testing.T) {
	primary, obs := setupPerformanceProjectionDBs(t)
	store := sqlitejobs.NewSQLiteStore(primary, zap.NewNop())
	ctx := context.Background()

	const (
		jobID = "job-e2e-fail"
		runID = "run-e2e-fail"
	)
	seedRunningJobWithFinalizedRun(t, primary, obs, jobID, runID, kernobs.StatusFailed, 99932)

	// Stage 1 — Fail: terminal flip + job.completed outbox event.
	if err := store.Fail(ctx, jobID, "worker-A", "lease-X", 5, "boom"); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	proj, err := perfstore.NewProjection(primary, obs)
	if err != nil {
		t.Fatalf("NewProjection: %v", err)
	}
	adapter := jobCompletedPerformanceAdapter{projection: proj, log: zap.NewNop()}
	claimHandleAndComplete(t, primary, adapter)

	// A failed run must still project (FAILED), never a silent no-op.
	assertProjectedRun(t, primary, jobID, runID, "FAILED", 99932)
}
