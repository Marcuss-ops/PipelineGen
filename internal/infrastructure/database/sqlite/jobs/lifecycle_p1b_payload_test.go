// Package jobs — lifecycle_p1b_payload_test.go (split surface: error + result payloads).
//
// Terminal-state data coherence on failure (error column populated) and
// completion-only result_json constraints. Pure relocation from
// lifecycle_p1b_test.go; no behavior change. Shared helpers come from
// lifecycle_p1b_fixtures_test.go.
package jobs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────
// Test 3 — Error available on failure
// ─────────────────────────────────────────────────────────────────────────

// TestJobLifecycle_P1B_ErrorOnFailure pins the user-spec invariant:
// on FAILED + CANCELLED, the `error` column MUST be populated with a
// non-empty string identifying the failure cause. This is the
// observation-endpoint's primary debug surface for failed jobs.
func TestJobLifecycle_P1B_ErrorOnFailure(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	ctx := context.Background()

	t.Run("FailedViaFail", func(t *testing.T) {
		const jobID = "p1b-error-failed"
		p1bSeedRunningJob(t, db, jobID, "p1b.test", 3, "worker-A", "lease-1",
			time.Now().Add(5*time.Minute), 0)

		const errMsg = "model_timeout: ollama response exceeded 30s"
		require.NoError(t, store.Fail(ctx, jobID, "worker-A", "lease-1", 1, errMsg))

		row := readLifecycleRow(t, db, jobID)
		assert.Equal(t, "FAILED", row.status, "Fail must transition RUNNING → FAILED")
		assert.Equal(t, errMsg, row.errMessage,
			"error column MUST be populated on FAILED (observation-endpoint debug surface)")
		assert.True(t, isEmptyResultJSON(row.resultJSON),
			"result_json MUST be empty on FAILED (Fail does not write result_json), got %q", row.resultJSON)
	})

	t.Run("FailedViaScheduleRetry_AtLimit", func(t *testing.T) {
		const jobID = "p1b-error-retry-exhausted"
		p1bSeedRunningJob(t, db, jobID, "p1b.test", 2, "worker-B", "lease-2",
			time.Now().Add(5*time.Minute), 2) // retry_count=2 == max_retries=2

		// ScheduleRetry at retry_limit must downgrade to FAILED with
		// the canonical "max retries exhausted" error message.
		require.NoError(t, store.ScheduleRetry(ctx, jobID, "worker-B", "lease-2", 1,
			"transient_TTS_429", 30*time.Second))

		row := readLifecycleRow(t, db, jobID)
		assert.Equal(t, "FAILED", row.status,
			"ScheduleRetry at retry_limit MUST downgrade to FAILED")
		assert.Contains(t, row.errMessage, "max retries exhausted",
			"error column MUST contain the canonical 'max retries exhausted' suffix for forensic clarity")
	})

	t.Run("CancelledViaCancel", func(t *testing.T) {
		const jobID = "p1b-error-cancelled"
		seedQueuedJob(t, db, jobID, "p1b.test", 3)

		require.NoError(t, store.Cancel(ctx, jobID))

		row := readLifecycleRow(t, db, jobID)
		assert.Equal(t, "CANCELLED", row.status, "Cancel must transition QUEUED → CANCELLED")
		// Cancel does NOT populate `error` (the user explicitly
		// cancelled; no error message to record). Pin this.
		assert.Empty(t, row.errMessage,
			"Cancel does not populate `error` (explicit user action, no error message)")
	})
}

// ─────────────────────────────────────────────────────────────────────────
// Test 4 — Result present ONLY at completion
// ─────────────────────────────────────────────────────────────────────────

// TestJobLifecycle_P1B_ResultOnlyAtCompletion pins the user-spec
// invariant: result_json is NULL/empty before Complete, populated
// after Complete with OutcomeSucceeded, and STAYS NULL after Fail
// (failed jobs do not have a "result" — they have an error).
func TestJobLifecycle_P1B_ResultOnlyAtCompletion(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	ctx := context.Background()

	t.Run("ResultNullBeforeComplete", func(t *testing.T) {
		const jobID = "p1b-result-null-before"
		seedQueuedJob(t, db, jobID, "p1b.test", 3)
		row := readLifecycleRow(t, db, jobID)
		assert.True(t, isEmptyResultJSON(row.resultJSON),
			"result_json MUST be empty for a QUEUED job (no completion yet), got %q", row.resultJSON)
	})

	t.Run("ResultPopulatedAfterComplete", func(t *testing.T) {
		const jobID = "p1b-result-after-complete"
		p1bSeedRunningJob(t, db, jobID, "p1b.test", 3, "worker-A", "lease-1",
			time.Now().Add(5*time.Minute), 0)
		result := json.RawMessage(`{"script":"hello world","items":3}`)
		require.NoError(t, store.Complete(ctx, jobID, "worker-A", "lease-1", 1, result))
		row := readLifecycleRow(t, db, jobID)
		assert.Equal(t, "SUCCEEDED", row.status)
		assert.Equal(t, string(result), row.resultJSON,
			"result_json MUST be populated after Complete")
		assert.Equal(t, 100, row.progress,
			"Complete MUST set progress=100 (canonical 'fully done' marker)")
	})

	t.Run("ResultNullAfterFail", func(t *testing.T) {
		const jobID = "p1b-result-null-after-fail"
		p1bSeedRunningJob(t, db, jobID, "p1b.test", 3, "worker-A", "lease-1",
			time.Now().Add(5*time.Minute), 0)
		require.NoError(t, store.Fail(ctx, jobID, "worker-A", "lease-1", 1, "some error"))
		row := readLifecycleRow(t, db, jobID)
		assert.Equal(t, "FAILED", row.status)
		assert.True(t, isEmptyResultJSON(row.resultJSON),
			"result_json MUST stay empty after Fail (failures have error, not result), got %q", row.resultJSON)
	})
}
