// Package jobs — worker_recover_abandoned_test.go (August 2026).
//
// Closes the end-to-end observability loop for the worker crash story:
//
//   - runJob surfaces the lease fence on every claim via
//     StartRunForClaim (LeaseID / WorkerID / LeaseExpiresAt), so
//     SQLiteRecorder.StartReport persists a non-NULL lease_expires_at.
//   - RecoverAbandoned then reclaims a RUNNING run whose lease expired
//     by flipping both run_observability and job_attempts to ABANDONED
//     with error_code WORKER_LOST.
//
// The isolated halves are already pinned elsewhere
// (TestSQLiteRecorder_RecoverAbandonedPersistsWorkerLost covers the
// reconciler; TestWorker_RunJob_ProducesRunReport covers the claim
// lease fields via an in-memory recorder). This test proves the two
// halves compose: a run created through the REAL runJob path (a real
// SQLite recorder) is recoverable.
package jobs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	obsmetrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// recoverTestObservabilitySchema installs the minimum observability tables
// that runJob's StartReport/SaveReport and RecoverAbandoned touch. Mirrors
// the canonical definitions in the observability package's own
// testObservabilitySchema helper (which is unexported).
func recoverTestObservabilitySchema(t *testing.T, db *sql.DB) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE job_attempts (attempt_id TEXT PRIMARY KEY, job_id TEXT NOT NULL, run_id TEXT NOT NULL UNIQUE, attempt_number INTEGER NOT NULL, worker_id TEXT, lease_id TEXT, status TEXT NOT NULL, started_at TEXT, finished_at TEXT, lease_expires_at TEXT, error_code TEXT, error TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE run_observability (run_id TEXT PRIMARY KEY, job_id TEXT NOT NULL, job_type TEXT NOT NULL, attempt_id TEXT NOT NULL UNIQUE, parent_run_id TEXT, worker_id TEXT, lease_id TEXT, lease_expires_at TEXT, status TEXT NOT NULL, created_at TEXT NOT NULL, started_at TEXT NOT NULL, finished_at TEXT, queue_wait_ms INTEGER NOT NULL DEFAULT 0, wall_time_ms INTEGER NOT NULL DEFAULT 0, active_ms INTEGER NOT NULL DEFAULT 0, blocked_ms INTEGER NOT NULL DEFAULT 0, accumulated_operation_ms INTEGER NOT NULL DEFAULT 0, error_code TEXT, error TEXT, counters_json TEXT, children_json TEXT, report_json TEXT NOT NULL, observability_degraded INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
}

// TestWorker_RecoverAbandonedMarksRunJobRunAbandoned proves the crash
// recovery contract end to end: a run created through runJob's claim
// instrumentation (real SQLite recorder) with an already-expired lease is
// marked ABANDONED by RecoverAbandoned, in both run_observability and
// job_attempts.
func TestWorker_RecoverAbandonedMarksRunJobRunAbandoned(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recoverTestObservabilitySchema(t, db)
	recorder := obsmetrics.NewSQLiteRecorder(db)

	// The handler blocks so runJob stays mid-execution: StartReport has
	// already persisted the RUNNING run, but the final Finish() (which
	// would finalize it) is withheld until the assertion completes.
	handlerStarted := make(chan struct{})
	release := make(chan struct{})
	handler := HandlerFunc(func(_ context.Context, _ *job.Job, _ *JobTools) (map[string]any, error) {
		close(handlerStarted)
		<-release
		return map[string]any{"ok": true}, nil
	})

	broker := newMockCancelBroker()
	broker.jobStatus = job.StatusRunning
	broker.revision = 1
	dispatcher := NewDispatcher()
	if err := dispatcher.Register(TypeScriptGenerate, handler); err != nil {
		t.Fatalf("register handler: %v", err)
	}
	observer := kernobs.NewRunObserver(recorder)
	worker := NewWorker(WorkerDeps{
		ID:         "recover-abandoned-worker",
		Repo:       broker,
		Dispatcher: dispatcher,
		Log:        zap.NewNop(),
		LeaseTTL:   5 * time.Minute, // renew tick = 100s — never fires during the test
		PollEvery:  2 * time.Second,
		Backoff:    BackoffConfig{},
		Types:      []string{TypeScriptGenerate},
	}).WithObserver(observer)

	// Lease already expired: RecoverAbandoned(now) with now >= lease_expiry
	// must reclaim the run.
	now := time.Now()
	leaseExpiry := now.Add(-time.Minute)
	createdAt := now.Add(-2 * time.Minute)
	startedAt := now.Add(-90 * time.Second)

	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.runJob(context.Background(), &job.Job{
			ID:          "obs-recover-1",
			Type:        TypeScriptGenerate,
			Status:      job.StatusRunning,
			LeaseID:     "lease-recover-1",
			LeaseExpiry: &leaseExpiry,
			RetryCount:  0,
			MaxRetries:  3,
			CreatedAt:   createdAt,
			StartedAt:   &startedAt,
		})
	}()

	// handlerStarted closes only after StartReport committed the run, so the
	// RUNNING row (with lease_expires_at) is guaranteed present here.
	select {
	case <-handlerStarted:
	case <-time.After(8 * time.Second):
		t.Fatal("handler did not start within 8s")
	}

	var runID string
	var leaseExp sql.NullString
	if err := db.QueryRow(`SELECT run_id, lease_expires_at FROM run_observability WHERE job_id = ? AND status = 'RUNNING'`, "obs-recover-1").Scan(&runID, &leaseExp); err != nil {
		t.Fatalf("runJob must persist a RUNNING run: %v", err)
	}
	if !leaseExp.Valid || leaseExp.String == "" {
		t.Fatal("lease_expires_at must be non-NULL (RecoverAbandoned depends on it)")
	}

	changed, err := recorder.RecoverAbandoned(context.Background(), now)
	if err != nil {
		t.Fatalf("RecoverAbandoned: %v", err)
	}
	if changed != 1 {
		t.Fatalf("recovered rows = %d, want 1", changed)
	}

	var status, errorCode string
	if err := db.QueryRow(`SELECT status, error_code FROM run_observability WHERE run_id = ?`, runID).Scan(&status, &errorCode); err != nil {
		t.Fatal(err)
	}
	if status != kernobs.StatusAbandoned || errorCode != "WORKER_LOST" {
		t.Fatalf("run recovery = %q/%q, want ABANDONED/WORKER_LOST", status, errorCode)
	}

	var attemptStatus string
	if err := db.QueryRow(`SELECT status FROM job_attempts WHERE run_id = ?`, runID).Scan(&attemptStatus); err != nil {
		t.Fatal(err)
	}
	if attemptStatus != kernobs.StatusAbandoned {
		t.Fatalf("job_attempts status = %q, want ABANDONED", attemptStatus)
	}

	// Release the handler so runJob can finalize and its deferred cleanup
	// (lease loop, contexts) unwinds. The final Finish() overwrites the
	// recovered row with SUCCEEDED — expected, and asserted after the fact
	// is out of scope for this test.
	close(release)
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("runJob did not complete after release")
	}
}
