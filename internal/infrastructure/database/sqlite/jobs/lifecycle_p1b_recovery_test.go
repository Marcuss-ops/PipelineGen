// Package jobs — lifecycle_p1b_recovery_test.go (split surface: crash + restart recovery).
//
// Three crash/restart recovery mechanisms: model timeout hard-fail,
// single-worker crash reclaim, and full server restart. Pure
// relocation from lifecycle_p1b_test.go; no behavior change. Shared
// helpers come from lifecycle_p1b_fixtures_test.go.
package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ─────────────────────────────────────────────────────────────────────────
// Test 7 — Model timeout handled
// ─────────────────────────────────────────────────────────────────────────

// TestJobLifecycle_P1B_ModelTimeoutHandled pins the user-spec
// invariant: a model that times out MUST NOT leave the job stuck.
// The SUT's recovery path is MarkRunningJobsOlderThanFailed: any
// RUNNING job whose lease_expiry is past the cutoff is hard-failed
// with the canonical reason in the error column.
//
// The per-job-timeout context (w.jobTimeoutFor) is at the worker
// layer; this sub-test pins the SUT-side recovery mechanism
// (MarkRunningJobsOlderThanFailed) that catches the job if the
// per-job-timeout context never fires (e.g., the worker goroutine
// itself is wedged).
func TestJobLifecycle_P1B_ModelTimeoutHandled(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	ctx := context.Background()
	const jobID = "p1b-model-timeout"

	// Seed a RUNNING job with a lease that expired 30 minutes ago
	// (simulates a model that never returned + worker that didn't
	// release the lease).
	expiredLease := time.Now().Add(-30 * time.Minute)
	p1bSeedRunningJob(t, db, jobID, "p1b.test", 3, "worker-A", "lease-timeout", expiredLease, 0)

	// Run the hard-fail recovery. Cutoff=now+1h catches every row
	// with lease_expiry < now+1h (i.e., all expired leases).
	const reason = "model timeout: lease expired before worker could renew"
	affected, err := store.MarkRunningJobsOlderThanFailed(ctx, time.Now().Add(1*time.Hour), reason)
	require.NoError(t, err, "MarkRunningJobsOlderThanFailed must succeed")
	assert.GreaterOrEqual(t, affected, 1,
		"MarkRunningJobsOlderThanFailed MUST affect at least our seeded job (affected=%d)", affected)

	// The row MUST be FAILED with the canonical reason.
	row := readLifecycleRow(t, db, jobID)
	assert.Equal(t, "FAILED", row.status,
		"MarkRunningJobsOlderThanFailed MUST transition RUNNING → FAILED (model timeout recovery)")
	assert.Equal(t, reason, row.errMessage,
		"error column MUST contain the operator-supplied reason for the hard-fail")
	assert.Empty(t, row.workerID, "hard-fail MUST clear worker_id (lease is dead)")
	assert.Empty(t, row.leaseID, "hard-fail MUST clear lease_id")
}

// ─────────────────────────────────────────────────────────────────────────
// Test 8 — Worker crash recovered
// ─────────────────────────────────────────────────────────────────────────

// TestJobLifecycle_P1B_WorkerCrashRecovered pins the user-spec
// invariant: when a worker crashes mid-process (no lease release),
// another worker MUST be able to reclaim the job. The SUT mechanism
// is RequeueExpiredLeases → another ClaimNext by a different worker.
func TestJobLifecycle_P1B_WorkerCrashRecovered(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	ctx := context.Background()
	const jobID = "p1b-worker-crash"

	// Seed a QUEUED job for worker-A to claim. The canonical recovery
	// flow starts with a QUEUED job that a worker claims, runs, then
	// crashes — we simulate the crash by backdating the lease so the
	// reclaim path picks it up.
	seedQueuedJob(t, db, jobID, "p1b.test", 3)

	// Worker A claims the job (status RUNNING, lease 5min, worker_id=worker-A).
	j, err := store.ClaimNext(ctx, "worker-A", 5*time.Minute, []string{"p1b.test"})
	require.NoError(t, err)
	require.NotNil(t, j, "ClaimNext MUST return the seeded job (id=%s)", jobID)
	require.Equal(t, jobID, j.ID)
	require.Equal(t, job.StatusRunning, j.Status)

	// Worker A crashes. The lease expires 1 hour later (backdated for
	// the test; the real-world mechanism is leaseTTL + clock).
	if _, err := db.ExecContext(ctx,
		`UPDATE jobs SET lease_expiry = ? WHERE id = ?`,
		timeutil.FormatRFC3339(time.Now().Add(-1*time.Hour).UTC()), jobID); err != nil {
		t.Fatalf("backdate lease: %v", err)
	}

	// Reclaimer runs.
	results, err := store.RequeueExpiredLeases(ctx, time.Now(), 100)
	require.NoError(t, err)
	var found bool
	for _, r := range results {
		if r.JobID == jobID {
			found = true
		}
	}
	require.True(t, found, "RequeueExpiredLeases MUST reclaim worker-A's lease (id=%s)", jobID)

	// The row MUST no longer belong to worker-A.
	row := readLifecycleRow(t, db, jobID)
	assert.NotEqual(t, "worker-A", row.workerID,
		"reclaim MUST clear worker_id (the crashed worker's lease is dead)")
	assert.NotEqual(t, "RUNNING", row.status,
		"reclaim MUST move the row out of RUNNING (RETRY_WAIT or FAILED or QUEUED)")

	// Canonical recovery flow: the reclaim moves the job to RETRY_WAIT;
	// the broker then drives RETRY_WAIT → QUEUED via store.Retry so
	// ClaimNext can pick it up. This two-step (reclaim → retry →
	// claim) mirrors the production sweepers.go loop and is the
	// load-bearing mechanism for "crash → another worker takes over".
	_, retryErr := store.Retry(ctx, jobID)
	require.NoError(t, retryErr, "Retry MUST succeed after reclaim (RETRY_WAIT → QUEUED)")

	// Worker B can claim the job. The canonical recovery invariant:
	// after worker-A's crash + reclaim + retry, worker-B MUST be able
	// to pick up the work.
	j2, err := store.ClaimNext(ctx, "worker-B", 5*time.Minute, []string{"p1b.test"})
	require.NoError(t, err)
	require.NotNil(t, j2, "worker-B MUST be able to claim the reclaimed job (id=%s)", jobID)
	assert.Equal(t, jobID, j2.ID, "worker-B MUST get the SAME job id (deterministic recovery)")
	assert.Equal(t, job.StatusRunning, j2.Status, "worker-B's claim MUST transition to RUNNING")
	assert.Equal(t, "worker-B", j2.WorkerID, "worker-B MUST be the new leaseholder")
	assert.NotEqual(t, "worker-A", j2.WorkerID, "worker-A's id MUST NOT persist (reclaim cleared it)")
}

// ─────────────────────────────────────────────────────────────────────────
// Test 9 — Server restart during generation
// ─────────────────────────────────────────────────────────────────────────

// TestJobLifecycle_P1B_ServerRestartDuringGeneration is the
// user-spec invariant framed as a SERVER restart (vs. a single
// worker crash). At the broker layer, the two are mechanically
// identical: when the server restarts, all RUNNING jobs are
// orphaned, and the lease_expiry reclaim path picks them up. The
// load-bearing assertion is the same: NEVER RUNNING forever.
//
// The SUT does NOT have a "resume on restart" primitive — the
// recovery is exclusively lease-expiry-based. The test pins this
// behavior explicitly.
func TestJobLifecycle_P1B_ServerRestartDuringGeneration(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	ctx := context.Background()
	const jobID = "p1b-server-restart"

	// Pre-restart: worker-A claimed the job, started generation.
	// The server then crashes (operator-side restart: SIGKILL,
	// OOM, deploy). The lease is now orphaned with no holder.
	expiredLease := time.Now().Add(-1 * time.Hour)
	p1bSeedRunningJob(t, db, jobID, "p1b.test", 3, "worker-A-pre-restart", "lease-pre-restart", expiredLease, 0)
	row := readLifecycleRow(t, db, jobID)
	require.Equal(t, "RUNNING", row.status)

	// Post-restart: the new server starts up. The lease-expiry
	// reclaim runs (sweepers.go) and reclaims the orphaned job.
	results, err := store.RequeueExpiredLeases(ctx, time.Now(), 100)
	require.NoError(t, err)
	var found bool
	for _, r := range results {
		if r.JobID == jobID {
			found = true
		}
	}
	require.True(t, found, "post-restart reclaimer MUST reclaim the orphaned job (id=%s)", jobID)

	// Load-bearing assertion: NEVER RUNNING forever. The row MUST
	// have moved out of RUNNING.
	row = readLifecycleRow(t, db, jobID)
	assert.NotEqual(t, "RUNNING", row.status,
		"post-restart recovery MUST move the row out of RUNNING (user-spec 'MAI RUNNING forever')")

	// Canonical recovery flow: the post-restart reclaim moves the
	// job to RETRY_WAIT; the new server's broker then drives
	// RETRY_WAIT → QUEUED via store.Retry so ClaimNext can pick it
	// up. This is the load-bearing mechanism for "server restart →
	// resume or retry or fail explicitly (NEVER RUNNING forever)".
	_, retryErr := store.Retry(ctx, jobID)
	require.NoError(t, retryErr, "Retry MUST succeed after post-restart reclaim")

	// The new server's workers MUST be able to claim the job.
	j, err := store.ClaimNext(ctx, "worker-post-restart", 5*time.Minute, []string{"p1b.test"})
	require.NoError(t, err)
	require.NotNil(t, j, "post-restart worker MUST be able to claim the reclaimed job")
	assert.Equal(t, jobID, j.ID, "post-restart worker MUST get the SAME job id (deterministic recovery)")
	assert.Equal(t, "worker-post-restart", j.WorkerID,
		"post-restart worker MUST be the new leaseholder (not worker-A-pre-restart)")
}
