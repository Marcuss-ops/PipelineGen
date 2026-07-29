// Package jobs — lifecycle_p1b_lease_retry_test.go (split surface: lease + retry invariants).
//
// Lease-expiry reclaim ("no job stuck indefinitely") and retry-count
// CAS-fence invariants. Pure relocation from lifecycle_p1b_test.go;
// no behavior change. Shared helpers come from lifecycle_p1b_fixtures_test.go.
package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ─────────────────────────────────────────────────────────────────────────
// Test 5 — No job stuck indefinitely (lease expiry reclaim)
// ─────────────────────────────────────────────────────────────────────────

// TestJobLifecycle_P1B_NoJobStuckIndefinitely is the load-bearing test
// for the "MAI RUNNING forever" invariant. It proves the SUT's
// recovery mechanism: when a worker's lease expires (without renewal
// or release), RequeueExpiredLeases reclaims the row so it can be
// re-processed or fail-terminally — never stuck RUNNING.
func TestJobLifecycle_P1B_NoJobStuckIndefinitely(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	ctx := context.Background()
	const jobID = "p1b-no-stuck"

	// Seed a RUNNING job with a lease that expired 1 hour ago
	// (simulates a worker that died mid-process, never releasing the
	// lease). Under the lease this is "active" (worker_id=worker-A,
	// lease_id=lease-stale); past the expiry it's a reclaimable
	// orphan.
	expiredLease := time.Now().Add(-1 * time.Hour)
	p1bSeedRunningJob(t, db, jobID, "p1b.test", 3, "worker-A", "lease-stale", expiredLease, 0)

	// Sanity: the row is RUNNING with stale lease.
	row := readLifecycleRow(t, db, jobID)
	require.Equal(t, "RUNNING", row.status, "precondition: row seeded as RUNNING with stale lease")
	require.Equal(t, "worker-A", row.workerID)

	// Run the reclaimer. The reclaim path:
	//   LEASED → QUEUED (re-queued for another worker)
	//   RUNNING/FINALIZING → RETRY_WAIT (back to retry queue)
	//   At retry_count >= max_retries → FAILED
	results, err := store.RequeueExpiredLeases(ctx, time.Now(), 100)
	require.NoError(t, err, "RequeueExpiredLeases must succeed against a stale lease")

	// Find our job in the reclaim results.
	var found bool
	for _, r := range results {
		if r.JobID == jobID {
			found = true
			assert.NotEmpty(t, string(r.NewStatus),
				"reclaim MUST produce a non-empty NewStatus (RETRY_WAIT or FAILED)")
		}
	}
	require.True(t, found, "RequeueExpiredLeases MUST surface our job (id=%s) in the reclaim results", jobID)

	// The row MUST no longer be RUNNING. The user-spec invariant is
	// "MAI RUNNING forever" — this is the assertion that proves it.
	row = readLifecycleRow(t, db, jobID)
	assert.NotEqual(t, "RUNNING", row.status,
		"reclaim MUST move the row out of RUNNING (RETRY_WAIT or FAILED or QUEUED) — NEVER stuck RUNNING")
	assert.Empty(t, row.workerID, "reclaim MUST clear the worker_id (lease is dead)")
	assert.Empty(t, row.leaseID, "reclaim MUST clear the lease_id")
}

// ─────────────────────────────────────────────────────────────────────────
// Test 6 — Retry limit respected
// ─────────────────────────────────────────────────────────────────────────

// TestJobLifecycle_P1B_RetryLimitRespected pins the user-spec
// invariant: a job with MaxRetries=N MUST NOT be re-queued past N
// retries. ScheduleRetry at retry_count >= max_retries atomically
// downgrades to FAILED with the canonical "max retries exhausted"
// error message.
func TestJobLifecycle_P1B_RetryLimitRespected(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	ctx := context.Background()
	const jobID = "p1b-retry-limit"
	const maxRetries = 2
	seedQueuedJob(t, db, jobID, "p1b.test", maxRetries)

	// claimAndReclaim claims the QUEUED job, asserts it transitioned
	// to RUNNING, and returns the post-claim (revision, leaseID).
	// The CAS fence in ScheduleRetry requires BOTH the CURRENT
	// revision (revision is bumped by Start, ScheduleRetry, and Retry
	// on every transition) AND the CURRENT lease_id (ClaimNext
	// generates a fresh lease_id internally, so the test must use
	// the actual lease_id, NOT a hardcoded placeholder). Tracking
	// both via store.Get is the load-bearing mechanism that keeps
	// the test from spuriously tripping ErrTransitionConflict.
	type claimedJob struct {
		revision int
		leaseID  string
	}
	claimAndReclaim := func(workerID string) claimedJob {
		t.Helper()
		// ClaimNext selects the oldest QUEUED job and transitions it
		// to RUNNING via Start (which bumps revision by 1) and
		// generates a fresh lease_id (stored back into the returned
		// *job.Job).
		j, err := store.ClaimNext(ctx, workerID, 5*time.Minute, []string{"p1b.test"})
		require.NoError(t, err)
		require.NotNil(t, j, "ClaimNext must return our seeded job (id=%s)", jobID)
		require.Equal(t, jobID, j.ID)
		require.Equal(t, job.StatusRunning, j.Status)
		require.NotEmpty(t, j.LeaseID, "ClaimNext MUST populate LeaseID (CAS-fence dependency)")
		return claimedJob{revision: j.Revision, leaseID: j.LeaseID}
	}

	// Re-enqueue (RETRY_WAIT → QUEUED) and assert the transition. The
	// 2-value return of Retry requires capturing both (*job.Job, error);
	// the post-Retry row is irrelevant to subsequent assertions.
	reEnqueue := func() {
		t.Helper()
		_, err := store.Retry(ctx, jobID)
		require.NoError(t, err)
		row := readLifecycleRow(t, db, jobID)
		require.Equal(t, "QUEUED", row.status, "Retry MUST re-enqueue RETRY_WAIT → QUEUED")
	}

	cA := claimAndReclaim("worker-A")
	row := readLifecycleRow(t, db, jobID)
	require.Equal(t, 0, row.retryCount, "precondition: retry_count=0")

	// Attempt 1 → ScheduleRetry under limit (retry_count 0→1, status RETRY_WAIT).
	// The CAS fence expects (worker_id, lease_id, revision) = (worker-A, cA.leaseID, cA.revision).
	require.NoError(t, store.ScheduleRetry(ctx, jobID, "worker-A", cA.leaseID, cA.revision,
		"transient_1", 30*time.Second))
	row = readLifecycleRow(t, db, jobID)
	assert.Equal(t, "RETRY_WAIT", row.status, "1st ScheduleRetry under limit MUST → RETRY_WAIT")
	assert.Equal(t, 1, row.retryCount, "retry_count MUST increment to 1")

	// Re-enqueue + re-claim (status back to RUNNING, revision bumped again).
	reEnqueue()
	cB := claimAndReclaim("worker-B")

	// Attempt 2 → ScheduleRetry at limit-1 (retry_count 1→2, status RETRY_WAIT).
	require.NoError(t, store.ScheduleRetry(ctx, jobID, "worker-B", cB.leaseID, cB.revision,
		"transient_2", 30*time.Second))
	row = readLifecycleRow(t, db, jobID)
	assert.Equal(t, "RETRY_WAIT", row.status, "2nd ScheduleRetry under limit (retry_count=1, max=2) MUST → RETRY_WAIT")
	assert.Equal(t, 2, row.retryCount, "retry_count MUST increment to 2 (now at max)")

	// Cycle 2 exhausted the retry budget (retry_count 2 == max_retries 2).
	// The canonical retry-limit invariant: Retry MUST refuse to re-enqueue
	// a row whose retry_count has already reached max_retries. Asserting
	// this here (rather than driving a phantom cycle 3) is the load-bearing
	// assertion for the "retry limit respected" user-spec invariant.
	_, retryErr := store.Retry(ctx, jobID)
	require.Error(t, retryErr,
		"Retry MUST refuse to re-enqueue when retry_count == max_retries (canonical retry-limit invariant)")
	assert.Contains(t, retryErr.Error(), "exhausted",
		"Retry error MUST surface the 'exhausted' reason for operator visibility")

	// The row MUST stay in RETRY_WAIT (Retry refused, so no transition).
	row = readLifecycleRow(t, db, jobID)
	assert.Equal(t, "RETRY_WAIT", row.status,
		"Retry refusal MUST leave the row in RETRY_WAIT (no silent QUEUED transition)")
	assert.Equal(t, 2, row.retryCount,
		"retry_count MUST stay at max_retries (Retry refusal does not mutate state)")
}
