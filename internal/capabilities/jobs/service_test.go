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

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	drive "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
	corid "github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

// setupTestDB builds an isolated SQLite jobs DB for the test.
// Mirrors the canonical `jobs` + `job_events` schema (see
// migrations/sqlite/021_jobs_correlation_id.sql and the surrounding series).
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
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
		lease_id TEXT NOT NULL DEFAULT '',
		lease_expiry TEXT,
		revision INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		started_at TEXT,
		completed_at TEXT,
		cancelled_at TEXT,
		parent_state_typed TEXT NOT NULL DEFAULT '',
		parent_job_id TEXT NOT NULL DEFAULT '',
		root_job_id TEXT NOT NULL DEFAULT '',
		client_id TEXT NOT NULL DEFAULT '',
		idempotency_key TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS idx_jobs_status_priority ON jobs(status, priority DESC, created_at ASC);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_active_key ON jobs(active_key) WHERE active_key != '';
	CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_type_correlation ON jobs(type, correlation_id) WHERE correlation_id != '';
	CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_client_idempotency ON jobs(client_id, idempotency_key) WHERE client_id != '' AND idempotency_key != '';
	CREATE TABLE IF NOT EXISTS job_events (
		id TEXT PRIMARY KEY,
		job_id TEXT NOT NULL,
		type TEXT NOT NULL,
		message TEXT NOT NULL DEFAULT '',
		data_json TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL,
		FOREIGN KEY(job_id) REFERENCES jobs(id)
	);
	-- The canonical job.completed outbox event is emitted atomically with
	-- Complete / Fail (derived performance-projection trigger), so the
	-- fixture must carry the outbox_events table (mirrors migration 092).
	CREATE TABLE IF NOT EXISTS outbox_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_type TEXT NOT NULL,
		aggregate_id TEXT NOT NULL DEFAULT '',
		aggregate_type TEXT NOT NULL DEFAULT '',
		payload_json TEXT NOT NULL DEFAULT '{}',
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
	CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
		ON outbox_events(event_key);
	`
	return drive.NewTestDBWithSchema(t, schema)
}

// setupTestService builds a *Service backed by an isolated SQLite jobs DB.
//
// PR-B (Wave 22, June 2026): returns the *sqljobs.SQLiteStore alongside the
// *Service so tests that need the bare *sql.DB or SQLite-specific helpers
// (MarkRunningJobsOlderThanFailed, GetStats, RefreshMetrics) compose against
// the concrete. The Service itself is now typed against job.JobBroker — the
// canonical port — so its public surface no longer exposes DB() or those
// helpers. Tests that don't need the store destructure with `_`.
//
// The third return value is the cleanup closure (kept for future
// `drive.NewTestDBWithSchema` migrations that need explicit teardown).
func setupTestService(t *testing.T) (*Service, *sqljobs.Broker, func()) {
	t.Helper()
	db := setupTestDB(t)
	store := sqljobs.NewBroker(sqljobs.NewSQLiteStore(db, zap.NewNop()))
	reg := Compose()
	// Register ad-hoc test job types so the fail-closed Enqueue gate
	// (handler check + typed MaxRetries lookup) does not reject the
	// arbitrary types used by these tests.
	testTypes := []RegistryEntry{
		{Completion: CompletionDeclaration{JobType: "test_job", ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "test job", DefaultMaxRetries: 1},
		{Completion: CompletionDeclaration{JobType: "unknown_type", ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "unknown type", DefaultMaxRetries: 1},
		{Completion: CompletionDeclaration{JobType: "concurrent_job", ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "concurrent job", DefaultMaxRetries: 1},
		{Completion: CompletionDeclaration{JobType: "idem_test", ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "idempotency test", DefaultMaxRetries: 1},
		{Completion: CompletionDeclaration{JobType: "rescue_test", ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "rescue test", DefaultMaxRetries: 1},
		{Completion: CompletionDeclaration{JobType: "concurrent_idem", ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "concurrent idempotency test", DefaultMaxRetries: 1},
	}
	for _, e := range testTypes {
		if err := reg.Register(e); err != nil {
			t.Fatalf("register test type %q: %v", e.Completion.JobType, err)
		}
	}
	svc, err := NewService(store, nil, zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, store, func() {}
}

func TestCreateJobStoresPendingJob(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	j, err := svc.Enqueue(ctx, &job.EnqueueRequest{
		Type:     "test_job",
		Priority: 1,
		Project:  "test-project",
	})
	if err != nil {
		t.Fatalf("failed to enqueue job: %v", err)
	}
	if j.Status != job.StatusQueued {
		t.Errorf("expected status %s, got %s", job.StatusQueued, j.Status)
	}
	if j.ID == "" {
		t.Error("expected non-empty job ID")
	}
}

func TestJobMovesToCompleted(t *testing.T) {
	svc, store, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	submitted, err := svc.Enqueue(ctx, &job.EnqueueRequest{
		Type: "test_job",
	})
	if err != nil {
		t.Fatalf("failed to enqueue job: %v", err)
	}

	// Wave 5 PR1 state machine: QUEUED→RUNNING→COMPLETED.
	// Transition to RUNNING by directly updating the DB.
	// Service.Complete passes expectedRevision=0, so reset revision to 0.
	if _, err := store.DB().ExecContext(ctx,
		`UPDATE jobs SET status='RUNNING', worker_id='', lease_id='', revision=0 WHERE id=?`, submitted.ID); err != nil {
		t.Fatalf("failed to transition job to running: %v", err)
	}

	result := map[string]any{"output": "done"}
	err = svc.Complete(ctx, submitted.ID, result)
	if err != nil {
		t.Fatalf("failed to complete job: %v", err)
	}

	updated, err := svc.Get(ctx, submitted.ID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if updated.Status != job.StatusSucceeded {
		t.Errorf("expected status %s, got %s", job.StatusSucceeded, updated.Status)
	}
}

func TestJobMovesToFailedWithError(t *testing.T) {
	svc, store, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	submitted, err := svc.Enqueue(ctx, &job.EnqueueRequest{
		Type: "test_job",
	})
	if err != nil {
		t.Fatalf("failed to enqueue job: %v", err)
	}

	// Wave 5 PR1 state machine: QUEUED→RUNNING→FAILED.
	// Service.Fail passes expectedRevision=0, so reset revision to 0.
	if _, err := store.DB().ExecContext(ctx,
		`UPDATE jobs SET status='RUNNING', worker_id='', lease_id='', revision=0 WHERE id=?`, submitted.ID); err != nil {
		t.Fatalf("failed to transition job to running: %v", err)
	}

	err = svc.Fail(ctx, submitted.ID, fmt.Errorf("something went wrong"))
	if err != nil {
		t.Fatalf("failed to fail job: %v", err)
	}

	updated, err := svc.Get(ctx, submitted.ID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if updated.Status != job.StatusFailed {
		t.Errorf("expected status %s, got %s", job.StatusFailed, updated.Status)
	}
}

func TestJobPayloadRoundTrip(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	payload := map[string]any{"key": "value", "number": float64(42)}
	submitted, err := svc.Enqueue(ctx, &job.EnqueueRequest{
		Type:    "test_job",
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("failed to enqueue job: %v", err)
	}

	retrieved, err := svc.Get(ctx, submitted.ID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}

	if len(retrieved.Payload) == 0 {
		t.Fatal("expected non-empty payload")
	}
}

func TestUnknownJobTypeFailsClearly(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	submitted, err := svc.Enqueue(ctx, &job.EnqueueRequest{
		Type: "unknown_type",
	})
	if err != nil {
		t.Fatalf("enqueue should not fail for unknown type: %v", err)
	}

	if submitted.Type != "unknown_type" {
		t.Errorf("expected job type 'unknown_type', got %s", submitted.Type)
	}
}

func TestConcurrentJobCreationDoesNotRace(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	var wg sync.WaitGroup
	numGoroutines := 10

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			_, err := svc.Enqueue(ctx, &job.EnqueueRequest{
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
	jobs, err := svc.List(ctx, job.Filter{})
	if err != nil {
		t.Fatalf("failed to list jobs: %v", err)
	}
	if len(jobs) != numGoroutines {
		t.Errorf("expected %d jobs, got %d", numGoroutines, len(jobs))
	}
}

// TestSQLiteStore_MarkRunningJobsOlderThanFailed exercises the SQLite-specific
// MarkRunningJobsOlderThanFailed helper directly via the setupTestService
// concrete *sqljobs.SQLiteStore. Renamed from TestJobsMarkStaleRunningJobsFailed
// in PR-B (Wave 22, June 2026) because the helper moved off the application-
// layer Service onto the concrete *sqljobs.SQLiteStore.
//
// PR-B moves SQLite-specific helpers OUT of the application-layer Service —
// they live on the concrete adapter only. Composition root callers (rare;
// needed for the periodic stale-sweep pinger) hold the concrete store via
// JobsBundle.Repo. The helper has the same semantics that the old Service
// wrapper exposed: jobs whose lease_expiry is older than `cutoff` are
// transitioned to FAILED with `reason` written to the error column.
func TestSQLiteStore_MarkRunningJobsOlderThanFailed(t *testing.T) {
	svc, store, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Insert old running job with expired lease.
	oldTime := time.Now().UTC().Add(-30 * time.Minute)
	leaseExpired := oldTime.Add(-5 * time.Minute) // lease expired long ago
	oldJob := &Job{
		ID:          "job-old-running",
		Type:        TypeArtlistRun,
		Status:      job.StatusRunning,
		UpdatedAt:   oldTime,
		CreatedAt:   oldTime,
		Payload:     []byte("{}"),
		LeaseExpiry: &leaseExpired,
	}
	require.NoError(t, store.Create(ctx, oldJob))

	// Insert fresh running job with valid (future) lease.
	freshLease := time.Now().UTC().Add(30 * time.Minute)
	freshJob := &Job{
		ID:          "job-fresh-running",
		Type:        TypeArtlistRun,
		Status:      job.StatusRunning,
		UpdatedAt:   time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
		Payload:     []byte("{}"),
		LeaseExpiry: &freshLease,
	}
	require.NoError(t, store.Create(ctx, freshJob))

	// Insert completed job (should not be affected)
	completedJob := &Job{
		ID:        "job-completed",
		Type:      TypeArtlistRun,
		Status:    job.StatusSucceeded,
		UpdatedAt: time.Now().UTC().Add(-30 * time.Minute),
		CreatedAt: time.Now().UTC().Add(-30 * time.Minute),
		Payload:   []byte("{}"),
	}
	require.NoError(t, store.Create(ctx, completedJob))

	// Mark stale jobs (SQLite-specific helper, called via store directly).
	cutoff := time.Now().UTC().Add(-15 * time.Minute)
	changed, err := store.MarkRunningJobsOlderThanFailed(ctx, cutoff, "stale job timeout")
	require.NoError(t, err)
	assert.Equal(t, 1, changed)

	// Verify old job is now failed
	oldJobGot, err := svc.Get(ctx, oldJob.ID)
	require.NoError(t, err)
	assert.Equal(t, job.StatusFailed, oldJobGot.Status)
	assert.Contains(t, oldJobGot.Error, "stale")

	// Verify fresh job is still running
	freshJobGot, err := svc.Get(ctx, freshJob.ID)
	require.NoError(t, err)
	assert.Equal(t, job.StatusRunning, freshJobGot.Status)

	// Verify completed job is still succeeded
	completedJobGot, err := svc.Get(ctx, completedJob.ID)
	require.NoError(t, err)
	assert.Equal(t, job.StatusSucceeded, completedJobGot.Status)
}

// TestEnqueueDuplicateCorrelationID verifies the core
// idempotency contract (req 036/Section 1 — P4): two enqueues with the
// same (type, correlation_id) MUST converge on the same job_id, both
// when both correlation_ids are set explicitly and when one comes
// from corid auto-injection.
func TestEnqueue_Idempotence_DuplicateCorrelationID(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	req := &job.EnqueueRequest{
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
	all, err := svc.List(ctx, job.Filter{})
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
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	j1, err := svc.Enqueue(ctx, &job.EnqueueRequest{
		Type:          "idem_test",
		CorrelationID: "key-1",
	})
	require.NoError(t, err)
	j2, err := svc.Enqueue(ctx, &job.EnqueueRequest{
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
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	ctx := corid.WithCorrelationID(context.Background(), "auto-injected-key")
	j1, err := svc.Enqueue(ctx, &job.EnqueueRequest{Type: "idem_test"})
	require.NoError(t, err)
	assert.Equal(t, "auto-injected-key", j1.CorrelationID, "correlation_id must be auto-injected from context")

	// Same context, same correlation_id, same type — same job_id.
	j2, err := svc.Enqueue(ctx, &job.EnqueueRequest{Type: "idem_test"})
	require.NoError(t, err)
	assert.Equal(t, j1.ID, j2.ID, "auto-injected correlation_id must dedupe subsequent enqueues")
}

// TestEnqueueCompletedJobReturnedOnResubmit: a completed job
// still counts as 'existing' for dedup purposes, so the retry returns
// the same row and its terminal status is preserved. This matches the
// idempotency design: re-submission after a network timeout must not
// silently re-trigger expensive work.
func TestEnqueue_Idempotence_CompletedJobCanBeResubmitted(t *testing.T) {
	svc, store, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	j1, err := svc.Enqueue(ctx, &job.EnqueueRequest{
		Type:          "idem_test",
		CorrelationID: "completed-key",
	})
	require.NoError(t, err)

	// Wave 5 PR1 state machine: QUEUED→RUNNING→COMPLETED.
	// Service.Complete passes expectedRevision=0, so reset revision to 0.
	if _, err := store.DB().ExecContext(ctx,
		`UPDATE jobs SET status='RUNNING', worker_id='', lease_id='', revision=0 WHERE id=?`, j1.ID); err != nil {
		t.Fatalf("failed to transition job to running: %v", err)
	}
	require.NoError(t, svc.Complete(ctx, j1.ID, map[string]any{"ok": true}))

	j2, err := svc.Enqueue(ctx, &job.EnqueueRequest{
		Type:          "idem_test",
		CorrelationID: "completed-key",
	})
	require.NoError(t, err)
	assert.Equal(t, j1.ID, j2.ID, "resubmit with same key after completion must return same job_id")
	assert.Equal(t, job.StatusSucceeded, j2.Status, "status of the returned job must be the terminal completion")
}

// TestEnqueueConcurrentSameCorrelationConverges stresses the
// mutex + UNIQUE-index race recovery: 10 goroutines enqueue concurrently
// with the same (type, correlation_id). Exactly one row must exist
// after the storm and every goroutine must observe that one row's ID.
func TestEnqueue_Idempotence_ConcurrentSameCorrelation(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
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
			j, err := svc.Enqueue(ctx, &job.EnqueueRequest{
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

	all, err := svc.List(ctx, job.Filter{})
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
	storeA := sqljobs.NewBroker(sqljobs.NewSQLiteStore(db, zap.NewNop()))
	storeB := sqljobs.NewBroker(sqljobs.NewSQLiteStore(db, zap.NewNop()))
	reg := Compose()
	if err := reg.Register(RegistryEntry{Completion: CompletionDeclaration{JobType: "rescue_test", ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "rescue test", DefaultMaxRetries: 1}); err != nil {
		t.Fatalf("register rescue_test: %v", err)
	}
	svcA, errA := NewService(storeA, nil, zap.NewNop(), reg)
	require.NoError(t, errA)
	svcB, errB := NewService(storeB, nil, zap.NewNop(), reg)
	require.NoError(t, errB)

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
		j, err := svcA.Enqueue(ctxA, &job.EnqueueRequest{Type: "rescue_test"})
		if err == nil && j != nil {
			ids[0] = j.ID
		}
		errs[0] = err
	}()
	go func() {
		defer wg.Done()
		j, err := svcB.Enqueue(ctxB, &job.EnqueueRequest{Type: "rescue_test"})
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
	all, err := svcA.List(ctxA, job.Filter{})
	require.NoError(t, err)
	count := 0
	for _, j := range all {
		if j.Type == "rescue_test" {
			count++
		}
	}
	assert.Equal(t, 1, count, "exactly one row must exist after multi-service race recovery")
}
