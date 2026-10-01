// Package jobs — lifecycle_retry_cancelled_test.go
//
// The 2026-09-30 Milton incident: three service restarts in one afternoon
// (deploy + two operator-side investigations) cancelled two RUNNING
// script.generate jobs mid-flight. Retry rejected CANCELLED ("invalid
// status"), so the only way forward was a brand-new submission — a new
// idempotency key and a full TTS/overlay replay of work whose durable
// checkpoints were still intact. These pins hold the fix: CANCELLED is a
// retryable source (the retry is always a deliberate operator call, never an
// automatic sweep), while genuinely terminal and active states stay
// rejected.
package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// TestRetry_RequeuesCancelledJobAfterServiceRestart pins the incident fix: a
// CANCELLED job (restart casualty, retry budget intact) re-enters the queue
// as QUEUED with cleared lease fields and without spending a retry.
func TestRetry_RequeuesCancelledJobAfterServiceRestart(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	ctx := context.Background()
	const jobID = "retry-cancelled-restart"

	now := time.Now().UTC()
	_, err := db.ExecContext(ctx,
		`INSERT INTO jobs (id, type, payload_json, status, worker_id, lease_id, lease_expiry,
			created_at, updated_at, started_at, revision, max_retries, retry_count, progress, error, correlation_id)
		VALUES (?, 'p1b.test', '{}', 'CANCELLED', '', '', NULL, ?, ?, ?, 1, 3, 0, 50, '', 'corr-retry-cancelled')`,
		jobID, now.Format(time.RFC3339), now.Format(time.RFC3339), now.Format(time.RFC3339))
	require.NoError(t, err, "seed CANCELLED job")

	retried, err := store.Retry(ctx, jobID)
	require.NoError(t, err, "a restart-cancelled job MUST be retryable")
	require.NotNil(t, retried)
	require.Equal(t, job.StatusQueued, retried.Status, "retry must requeue the job")
	require.Equal(t, 0, retried.RetryCount, "requeue after cancel must NOT spend the retry budget")
	require.Empty(t, retried.WorkerID)
	require.Empty(t, retried.LeaseID)
}

// TestRetry_RequeuesFailedJobStillWorks pins the pre-existing contract the fix
// must not regress: FAILED jobs remain retryable.
func TestRetry_RequeuesFailedJobStillWorks(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	ctx := context.Background()
	const jobID = "retry-failed-still-works"

	now := time.Now().UTC()
	_, err := db.ExecContext(ctx,
		`INSERT INTO jobs (id, type, payload_json, status, worker_id, lease_id, lease_expiry,
			created_at, updated_at, started_at, revision, max_retries, retry_count, progress, error, correlation_id)
		VALUES (?, 'p1b.test', '{}', 'FAILED', '', '', NULL, ?, ?, ?, 2, 3, 1, 50, 'boom', 'corr-retry-failed')`,
		jobID, now.Format(time.RFC3339), now.Format(time.RFC3339), now.Format(time.RFC3339))
	require.NoError(t, err, "seed FAILED job")

	retried, err := store.Retry(ctx, jobID)
	require.NoError(t, err, "a FAILED job MUST stay retryable")
	require.Equal(t, job.StatusQueued, retried.Status)
}

// TestRetry_RejectsActiveAndSucceededStates pins the guard rails: QUEUED,
// RUNNING and SUCCEEDED rows must never pass the retry gate — a retry is a
// deliberate operator call on a dead job, never a duplicate dispatch.
func TestRetry_RejectsActiveAndSucceededStates(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, tc := range []struct{ id, status string }{
		{"retry-reject-queued", "QUEUED"},
		{"retry-reject-running", "RUNNING"},
		{"retry-reject-succeeded", "SUCCEEDED"},
	} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO jobs (id, type, payload_json, status, worker_id, lease_id, lease_expiry,
				created_at, updated_at, started_at, revision, max_retries, retry_count, progress, error, correlation_id)
			VALUES (?, 'p1b.test', '{}', ?, '', '', NULL, ?, ?, ?, 1, 3, 0, 50, '', ?)`,
			tc.id, tc.status, now, now, now, tc.id)
		require.NoError(t, err, "seed %s job", tc.status)

		_, retryErr := store.Retry(ctx, tc.id)
		require.Error(t, retryErr, "%s must stay retry-rejected", tc.status)
		require.Contains(t, retryErr.Error(), "invalid status")
	}
}
