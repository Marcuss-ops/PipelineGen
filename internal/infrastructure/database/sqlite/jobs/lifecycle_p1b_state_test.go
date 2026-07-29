// Package jobs — lifecycle_p1b_state_test.go (split surface: canonical state set).
//
// Kernel state-set round-trip + canonical status enum membership.
// Pure relocation from lifecycle_p1b_test.go; no behavior change.
// Shared helpers come from lifecycle_p1b_fixtures_test.go.
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
// Test 1 — All 11 canonical kernel states round-trip
// ─────────────────────────────────────────────────────────────────────────

// TestJobLifecycle_P1B_AllStates_RoundTrip pins the canonical state
// set: the kernel exposes 11 states (job.go:44-58), and the SQLite
// store + Get() must losslessly round-trip every one. The user spec
// lists 8 states (PENDING, QUEUED, RUNNING, SUCCEEDED, FAILED,
// CANCELLED, DEAD_LETTERED); the kernel superset includes LEASED,
// WAITING_CHILDREN, FINALIZING, RETRY_WAIT, PARTIALLY_SUCCEEDED,
// INDEX_PENDING. PENDING is NOT a kernel state (it's a pre-QUEUED
// dispatcher concept); DEAD_LETTERED is NOT a status (it's a
// dead_letter_jobs table presence — see TestJobLifecycle_P1B_DeadLettered).
//
// This sub-test pins the CANONICAL state set and the Get() round-trip.
// The SUT BUG (PENDING-not-in-kernel + DEAD_LETTERED-not-in-kernel)
// is documented in the commit body, not fixed here.
func TestJobLifecycle_P1B_AllStates_RoundTrip(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	now := timeutil.FormatRFC3339(time.Now().UTC())

	// All 11 canonical kernel states. The user-spec 8 states are a
	// subset; we test the FULL kernel surface so a future state
	// removal/rename is caught at the SQL layer.
	states := []job.Status{
		job.StatusQueued,
		job.StatusLeased,
		job.StatusRunning,
		job.StatusWaitingChildren,
		job.StatusFinalizing,
		job.StatusRetryWait,
		job.StatusSucceeded,
		job.StatusPartiallySucceeded,
		job.StatusIndexPending,
		job.StatusFailed,
		job.StatusCancelled,
	}

	for _, status := range states {
		status := status // pin loop variable
		t.Run(string(status), func(t *testing.T) {
			// Insert a job row directly with the target status. The
			// goal is to verify the SQL schema + Get() losslessly
			// round-trip every canonical state — we don't need to
			// drive the state machine for this sub-test (other
			// sub-tests cover the transition logic).
			jobID := "p1b-allstates-" + string(status)
			_, err := db.ExecContext(context.Background(),
				`INSERT INTO jobs (id, type, payload_json, status, worker_id, lease_id,
					created_at, updated_at, revision, max_retries, retry_count, progress, correlation_id)
				VALUES (?, 'p1b.test', '{}', ?, '', '', ?, ?, 0, 0, 0, 0, '')`,
				jobID, string(status), now, now)
			require.NoError(t, err)

			// Get() must return the canonical status string.
			j, err := store.Get(context.Background(), jobID)
			require.NoError(t, err)
			require.NotNil(t, j, "Get() must return the seeded job (status=%s)", status)
			assert.Equal(t, status, j.Status,
				"Get() must return the canonical kernel status verbatim (no aliasing, no case-folding)")

			// Status.IsTerminal() must agree with the canonical
			// terminal-set membership (SUCCEEDED, PARTIALLY_SUCCEEDED,
			// FAILED, CANCELLED).
			expectedTerminal := status == job.StatusSucceeded ||
				status == job.StatusPartiallySucceeded ||
				status == job.StatusFailed ||
				status == job.StatusCancelled
			assert.Equal(t, expectedTerminal, status.IsTerminal(),
				"Status.IsTerminal() must agree with the canonical terminal set for %s", status)
		})
	}

	// SUT BUG: the user spec lists PENDING + DEAD_LETTERED as states.
	// Neither is a kernel Status. Document the gap.
	t.Run("PENDING_not_in_kernel", func(t *testing.T) {
		// PENDING is a pre-QUEUED dispatcher concept. The kernel
		// (job.go:44-58) does NOT define StatusPending. The
		// Status.Valid() method is the canonical "is this a known
		// status?" check; PENDING is intentionally not in the enum.
		assert.False(t, job.Status("PENDING").Valid(),
			"PENDING is intentionally NOT a kernel status (pre-QUEUED dispatcher concept)")
	})

	t.Run("DEAD_LETTERED_not_a_status", func(t *testing.T) {
		// DEAD_LETTERED is a dead_letter_jobs table presence, NOT a
		// kernel status. The canonical failure mode that produces a
		// dead_letter_jobs row is FinalizeAttempt with
		// OutcomeFailedPermanent + DLQPayload (see TestJobLifecycle_P1B_DeadLettered).
		assert.False(t, job.Status("DEAD_LETTERED").Valid(),
			"DEAD_LETTERED is intentionally NOT a kernel status (it's a dead_letter_jobs table presence)")
	})
}
