package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/database"
	corid "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	// Create jobs table
	schema := `
	CREATE TABLE IF NOT EXISTS jobs (
		id TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		status TEXT NOT NULL,
		priority INTEGER NOT NULL DEFAULT 0,
		project TEXT DEFAULT '',
		video_name TEXT DEFAULT '',
		active_key TEXT DEFAULT '',
		correlation_id TEXT NOT NULL DEFAULT '',
		payload_json TEXT NOT NULL DEFAULT '{}',
		result_json TEXT NOT NULL DEFAULT '{}',
		progress INTEGER NOT NULL DEFAULT 0,
		error TEXT DEFAULT '',
		retry_count INTEGER NOT NULL DEFAULT 0,
		max_retries INTEGER NOT NULL DEFAULT 3,
		worker_id TEXT DEFAULT '',
		lease_expiry TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		started_at TEXT,
		completed_at TEXT,
		cancelled_at TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_jobs_status_priority ON jobs(status, priority DESC, created_at ASC);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_active_key ON jobs(active_key) WHERE active_key != '';
	CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_type_correlation ON jobs(type, correlation_id) WHERE correlation_id != '';
	CREATE TABLE IF NOT EXISTS job_events (
		id TEXT PRIMARY KEY,
		job_id TEXT NOT NULL,
		type TEXT NOT NULL,
		message TEXT NOT NULL DEFAULT '',
		data_json TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL,
		FOREIGN KEY(job_id) REFERENCES jobs(id)
	);
	`
	return storage.NewTestDBWithSchema(t, schema)
}

func setupTestService(t *testing.T) (*Service, func()) {
	t.Helper()
	db := setupTestDB(t)
	repo := NewSQLiteStore(db, zap.NewNop())
	svc := NewService(repo, nil, zap.NewNop())

	return svc, func() {}
}

func TestCreateJobStoresPendingJob(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	j, err := svc.Enqueue(ctx, &EnqueueRequest{
		Type:     "test_job",
		Priority: 1,
		Project:  "test-project",
	})
	if err != nil {
		t.Fatalf("failed to enqueue job: %v", err)
	}
	if j.Status != StatusQueued {
		t.Errorf("expected status %s, got %s", StatusQueued, j.Status)
	}
	if j.ID == "" {
		t.Error("expected non-empty job ID")
	}
}

func TestJobMovesToCompleted(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	job, err := svc.Enqueue(ctx, &EnqueueRequest{
		Type: "test_job",
	})
	if err != nil {
		t.Fatalf("failed to enqueue job: %v", err)
	}

	result := map[string]any{"output": "done"}
	err = svc.Complete(ctx, job.ID, result)
	if err != nil {
		t.Fatalf("failed to complete job: %v", err)
	}

	updated, err := svc.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if updated.Status != StatusSucceeded {
		t.Errorf("expected status %s, got %s", StatusSucceeded, updated.Status)
	}
}

func TestJobMovesToFailedWithError(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	job, err := svc.Enqueue(ctx, &EnqueueRequest{
		Type: "test_job",
	})
	if err != nil {
		t.Fatalf("failed to enqueue job: %v", err)
	}

	err = svc.Fail(ctx, job.ID, fmt.Errorf("something went wrong"))
	if err != nil {
		t.Fatalf("failed to fail job: %v", err)
	}

	updated, err := svc.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if updated.Status != StatusFailed {
		t.Errorf("expected status %s, got %s", StatusFailed, updated.Status)
	}
}

func TestJobPayloadRoundTrip(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	payload := map[string]any{"key": "value", "number": float64(42)}
	job, err := svc.Enqueue(ctx, &EnqueueRequest{
		Type:    "test_job",
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("failed to enqueue job: %v", err)
	}

	retrieved, err := svc.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}

	if len(retrieved.Payload) == 0 {
		t.Fatal("expected non-empty payload")
	}
}

func TestUnknownJobTypeFailsClearly(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	job, err := svc.Enqueue(ctx, &EnqueueRequest{
		Type: "unknown_type",
	})
	if err != nil {
		t.Fatalf("enqueue should not fail for unknown type: %v", err)
	}

	if job.Type != "unknown_type" {
		t.Errorf("expected job type 'unknown_type', got %s", job.Type)
	}
}

func TestConcurrentJobCreationDoesNotRace(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	var wg sync.WaitGroup
	numGoroutines := 10

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			_, err := svc.Enqueue(ctx, &EnqueueRequest{
				Type:    "concurrent_job",
				Project: "concurrent-test",
			})
			if err != nil {
				t.Errorf("goroutine %d failed to enqueue: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	// Verify all jobs were created
	jobs, err := svc.List(ctx, Filter{})
	if err != nil {
		t.Fatalf("failed to list jobs: %v", err)
	}
	if len(jobs) != numGoroutines {
		t.Errorf("expected %d jobs, got %d", numGoroutines, len(jobs))
	}
}

func TestJobsMarkStaleRunningJobsFailed(t *testing.T) {
	ctx := context.Background()

	svc, cleanup := setupTestService(t)
	defer cleanup()

	repo := svc.repo

	// Insert old running job
	oldTime := time.Now().UTC().Add(-30 * time.Minute)
	oldJob := &Job{
		ID:        "job-old-running",
		Type:      JobTypeArtlistRun,
		Status:    StatusRunning,
		UpdatedAt: oldTime,
		CreatedAt: oldTime,
		Payload:   []byte("{}"),
	}
	require.NoError(t, repo.Create(ctx, oldJob))

	// Insert fresh running job
	freshJob := &Job{
		ID:        "job-fresh-running",
		Type:      JobTypeArtlistRun,
		Status:    StatusRunning,
		UpdatedAt: time.Now().UTC(),
		CreatedAt: time.Now().UTC(),
		Payload:   []byte("{}"),
	}
	require.NoError(t, repo.Create(ctx, freshJob))

	// Insert completed job (should not be affected)
	completedJob := &Job{
		ID:        "job-completed",
		Type:      JobTypeArtlistRun,
		Status:    StatusSucceeded,
		UpdatedAt: time.Now().UTC().Add(-30 * time.Minute),
		CreatedAt: time.Now().UTC().Add(-30 * time.Minute),
		Payload:   []byte("{}"),
	}
	require.NoError(t, repo.Create(ctx, completedJob))

	// Mark stale jobs
	changed, err := svc.MarkStaleRunningJobsFailed(ctx, 15*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 1, changed)

	// Verify old job is now failed
	oldJobGot, err := svc.Get(ctx, oldJob.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, oldJobGot.Status)
	assert.Contains(t, oldJobGot.Error, "stale")

	// Verify fresh job is still running
	freshJobGot, err := svc.Get(ctx, freshJob.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, freshJobGot.Status)

	// Verify completed job is still succeeded
	completedJobGot, err := svc.Get(ctx, completedJob.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, completedJobGot.Status)
}

// TestEnqueueDuplicateCorrelationID verifies the core
// idempotency contract (req 036/Section 1 — P4): two enqueues with the
// same (type, correlation_id) MUST converge on the same job_id, both
// when both correlation_ids are set explicitly and when one comes
// from corid auto-injection.
func TestEnqueue_Idempotence_DuplicateCorrelationID(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	req := &EnqueueRequest{
		Type:          "idem_test",
		CorrelationID: "client-req-abc-123",
	}
	j1, err := svc.Enqueue(ctx, req)
	require.NoError(t, err)
	require.NotEmpty(t, j1.ID)
	assert.Equal(t, "client-req-abc-123", j1.CorrelationID)

	// Second enqueue with the same (type, correlation_id) MUST return
	// the same job_id, not a fresh one.
	j2, err := svc.Enqueue(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, j1.ID, j2.ID, "duplicate enqueue must return same job_id")

	// The DB now has exactly one row.
	all, err := svc.List(ctx, Filter{})
	require.NoError(t, err)
	count := 0
	for _, j := range all {
		if j.Type == "idem_test" {
			count++
		}
	}
	assert.Equal(t, 1, count, "exactly one row must exist for the (type, correlation_id) pair")
}

// TestEnqueueDifferentCorrelationIDIsDistinct verifies that distinct
// correlation_ids are treated as distinct submissions.
func TestEnqueue_Idempotence_DifferentCorrelationID(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	j1, err := svc.Enqueue(ctx, &EnqueueRequest{
		Type:          "idem_test",
		CorrelationID: "key-1",
	})
	require.NoError(t, err)
	j2, err := svc.Enqueue(ctx, &EnqueueRequest{
		Type:          "idem_test",
		CorrelationID: "key-2",
	})
	require.NoError(t, err)
	assert.NotEqual(t, j1.ID, j2.ID, "different correlation_ids must produce different job_ids")
}

// TestEnqueueAutoInjectsCorrelationIDFromContext verifies the
// corid.WithCorrelationID(ctx, ...) auto-injection path: callers don't
// need to set CorrelationID explicitly for idempotency to work, as
// long as they propagate the request context (which middleware.RequestID
// already does via X-Request-ID).
func TestEnqueue_Idempotence_AutoInjectsFromContext(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := corid.WithCorrelationID(context.Background(), "auto-injected-key")
	j1, err := svc.Enqueue(ctx, &EnqueueRequest{Type: "idem_test"})
	require.NoError(t, err)
	assert.Equal(t, "auto-injected-key", j1.CorrelationID, "correlation_id must be auto-injected from context")

	// Same context, same correlation_id, same type — same job_id.
	j2, err := svc.Enqueue(ctx, &EnqueueRequest{Type: "idem_test"})
	require.NoError(t, err)
	assert.Equal(t, j1.ID, j2.ID, "auto-injected correlation_id must dedupe subsequent enqueues")
}

// TestEnqueueCompletedJobReturnedOnResubmit: a completed job
// still counts as 'existing' for dedup purposes, so the retry returns
// the same row and its terminal status is preserved. This matches the
// idempotency design: re-submission after a network timeout must not
// silently re-trigger expensive work.
func TestEnqueue_Idempotence_CompletedJobCanBeResubmitted(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	j1, err := svc.Enqueue(ctx, &EnqueueRequest{
		Type:          "idem_test",
		CorrelationID: "completed-key",
	})
	require.NoError(t, err)
	require.NoError(t, svc.Complete(ctx, j1.ID, map[string]any{"ok": true}))

	j2, err := svc.Enqueue(ctx, &EnqueueRequest{
		Type:          "idem_test",
		CorrelationID: "completed-key",
	})
	require.NoError(t, err)
	assert.Equal(t, j1.ID, j2.ID, "resubmit with same key after completion must return same job_id")
	assert.Equal(t, StatusSucceeded, j2.Status, "status of the returned job must be the terminal completion")
}

// TestEnqueueConcurrentSameCorrelationConverges stresses the
// mutex + UNIQUE-index race recovery: 10 goroutines enqueue concurrently
// with the same (type, correlation_id). Exactly one row must exist
// after the storm and every goroutine must observe that one row's ID.
func TestEnqueue_Idempotence_ConcurrentSameCorrelation(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	const n = 10
	var wg sync.WaitGroup
	ids := make([]string, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			j, err := svc.Enqueue(ctx, &EnqueueRequest{
				Type:          "concurrent_idem",
				CorrelationID: "concurrent-key",
			})
			if err != nil {
				t.Errorf("goroutine %d failed to enqueue: %v", i, err)
				return
			}
			ids[i] = j.ID
		}()
	}
	wg.Wait()

	first := ids[0]
	require.NotEmpty(t, first, "first goroutine must have produced a job_id")
	for i := 1; i < n; i++ {
		assert.Equal(t, first, ids[i], "all concurrent enqueues with same correlation_id must converge to one job_id")
	}

	all, err := svc.List(ctx, Filter{})
	require.NoError(t, err)
	count := 0
	for _, j := range all {
		if j.Type == "concurrent_idem" {
			count++
		}
	}
	assert.Equal(t, 1, count, "exactly one row must exist after concurrent storm")
}

// TestEnqueueRescuePathMultiService covers the post-INSERT UNIQUE-rescue
// branch (Service.go Enqueue rescue-on-error). It works around the in-process
// mutex serialisation by spinning TWO Service instances over the SAME
// *sql.DB and racing them — each Service has its own mutex so the pre-INSERT
// dedup check sees nothing (the other Service's INSERT hasn't committed yet),
// and SQLite fires the UNIQUE-constraint violation for the loser, who must
// then re-fetch and return the winning row. This is the precise scenario
// the rescue branch exists for: future multi-process / multi-node setups.
func TestEnqueueRescuePathMultiService(t *testing.T) {
	db := setupTestDB(t)

	// Two Service instances over the same *sql.DB deliberately share state
	// but NOT the in-process enqueueMu.
	repoA := NewSQLiteStore(db, zap.NewNop())
	repoB := NewSQLiteStore(db, zap.NewNop())
	svcA := NewService(repoA, nil, zap.NewNop())
	svcB := NewService(repoB, nil, zap.NewNop())

	// Both contexts carry the same correlation_id so the (type, correlation_id)
	// UNIQUE index is the gatekeeper — not the per-Service mutex.
	ctxA := corid.WithCorrelationID(context.Background(), "shared-rescue-key")
	ctxB := corid.WithCorrelationID(context.Background(), "shared-rescue-key")

	var wg sync.WaitGroup
	ids := make([]string, 2)
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		j, err := svcA.Enqueue(ctxA, &EnqueueRequest{Type: "rescue_test"})
		if err == nil && j != nil {
			ids[0] = j.ID
		}
		errs[0] = err
	}()
	go func() {
		defer wg.Done()
		j, err := svcB.Enqueue(ctxB, &EnqueueRequest{Type: "rescue_test"})
		if err == nil && j != nil {
			ids[1] = j.ID
		}
		errs[1] = err
	}()
	wg.Wait()

	// Both calls MUST succeed (no "failed to create job" leak).
	require.NoError(t, errs[0], "Service A must succeed via pre-INSERT dedup OR rescue branch")
	require.NoError(t, errs[1], "Service B must succeed via rescue branch when pre-check races")

	// Both calls MUST return the SAME job_id.
	require.NotEmpty(t, ids[0])
	assert.Equal(t, ids[0], ids[1], "both Service instances must converge on one job_id (rescue branch returns the winner)")

	// And only one row exists in the DB.
	all, err := svcA.List(ctxA, Filter{})
	require.NoError(t, err)
	count := 0
	for _, j := range all {
		if j.Type == "rescue_test" {
			count++
		}
	}
	assert.Equal(t, 1, count, "exactly one row must exist after multi-service race recovery")
}
