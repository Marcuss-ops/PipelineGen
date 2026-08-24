// Package jobs — lifecycle_p1b_observation_test.go (split surface: observation endpoint + dead letter audit).
//
// Post-transition observation surfaces: SQLiteStore.Get() projection
// for terminal states + the dead_letter_jobs audit-row mechanism.
// Pure relocation from lifecycle_p1b_test.go; no behavior change.
// Shared helpers come from lifecycle_p1b_fixtures_test.go.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ─────────────────────────────────────────────────────────────────────────
// Test 10 — Observation endpoint (data source: Get / GetFull)
// ─────────────────────────────────────────────────────────────────────────

// TestJobLifecycle_P1B_ObservationEndpoint pins the data-source
// contract for GET /api/jobs/:id (and /api/jobs/:id/full). For
// each terminal state, the SQLiteStore.Get() projection must
// return status + progress + error + result as a coherent tuple —
// the operator-facing observation surface.
//
// The API handler (internal/api/jobs/handler_observability_test.go)
// already tests the HTTP surface; this sub-test pins the underlying
// data source so the API's contract is grounded.
func TestJobLifecycle_P1B_ObservationEndpoint(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	ctx := context.Background()

	// Pre-populate a row and then drive it through the canonical
	// terminal transitions. For each terminal state, verify the
	// Get() projection returns the canonical tuple.
	terminalCases := []struct {
		name         string
		jobType      string
		driver       func(t *testing.T, store *SQLiteStore, db *sql.DB, jobID string)
		expectStatus job.Status
		expectResult string
		expectError  string
		expectProg   int
	}{
		{
			name:    "Succeeded",
			jobType: "p1b.test",
			driver: func(t *testing.T, store *SQLiteStore, db *sql.DB, jobID string) {
				p1bSeedRunningJob(t, db, jobID, "p1b.test", 3, "worker-A", "lease-A",
					time.Now().Add(5*time.Minute), 0)
				require.NoError(t, store.Complete(ctx, jobID, "worker-A", "lease-A", 1,
					json.RawMessage(`{"ok":true,"items":7}`)))
			},
			expectStatus: job.StatusSucceeded,
			expectResult: `{"ok":true,"items":7}`,
			expectProg:   100,
		},
		{
			name:    "Failed",
			jobType: "p1b.test",
			driver: func(t *testing.T, store *SQLiteStore, db *sql.DB, jobID string) {
				p1bSeedRunningJob(t, db, jobID, "p1b.test", 3, "worker-B", "lease-B",
					time.Now().Add(5*time.Minute), 0)
				require.NoError(t, store.Fail(ctx, jobID, "worker-B", "lease-B", 1,
					"deterministic_failure"))
			},
			expectStatus: job.StatusFailed,
			expectError:  "deterministic_failure",
			expectResult: "{}", // Fail does not touch result_json; production's NOT NULL DEFAULT '{}' remains
			expectProg:   50,   // unchanged from seed
		},
		{
			name:    "Cancelled",
			jobType: "p1b.test",
			driver: func(t *testing.T, store *SQLiteStore, db *sql.DB, jobID string) {
				seedQueuedJob(t, db, jobID, "p1b.test", 3)
				require.NoError(t, store.Cancel(ctx, jobID))
			},
			expectStatus: job.StatusCancelled,
			expectResult: "{}", // production's NOT NULL DEFAULT '{}' (scanJobColumns returns the raw value)
		},
	}

	for _, tc := range terminalCases {
		tc := tc // pin loop variable
		t.Run(tc.name, func(t *testing.T) {
			jobID := "p1b-obs-" + tc.name
			tc.driver(t, store, db, jobID)

			// The observation surface: Get() must return a coherent
			// (status, progress, error, result) tuple.
			j, err := store.Get(ctx, jobID)
			require.NoError(t, err)
			require.NotNil(t, j, "Get() must return the job (id=%s)", jobID)
			assert.Equal(t, tc.expectStatus, j.Status,
				"observation surface: status MUST match the canonical terminal state")
			assert.Equal(t, tc.expectProg, j.Progress,
				"observation surface: progress MUST match the canonical value for %s", tc.name)
			assert.Equal(t, tc.expectError, j.Error,
				"observation surface: error MUST match the canonical error for %s", tc.name)
			assert.Equal(t, tc.expectResult, string(j.Result),
				"observation surface: result MUST match the canonical result for %s", tc.name)
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Test 11 — DEAD_LETTERED via FinalizeAttempt + DLQPayload
// ─────────────────────────────────────────────────────────────────────────

// TestJobLifecycle_P1B_DeadLettered pins the user-spec "DEAD_LETTERED"
// state. Per the kernel design (finalize_attempt.go:248), DEAD_LETTERED
// is NOT a job status — it is the presence of a row in the
// `dead_letter_jobs` archive table. The canonical mechanism that
// produces a dead_letter_jobs row is FinalizeAttempt with
// OutcomeFailedPermanent + DLQPayload (in the same TX as the FAILED
// status flip, atomically).
//
// This test pins: a failed job with a DLQPayload MUST have a
// corresponding dead_letter_jobs row. The DLQ row carries the
// failure's payload for forensic review by operators.
func TestJobLifecycle_P1B_DeadLettered(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	ctx := context.Background()
	const jobID = "p1b-dead-letter"
	p1bSeedRunningJob(t, db, jobID, "p1b.test", 3, "worker-A", "lease-A",
		time.Now().Add(5*time.Minute), 0)

	// FinalizeAttempt with OutcomeFailedPermanent + DLQPayload MUST
	// (a) flip the job to FAILED, (b) insert a dead_letter_jobs row
	// in the same TX.
	const errMsg = "deterministic_failure_dlq"
	dlqPayload := json.RawMessage(`{"snapshot":true,"reason":"operator_review"}`)
	cmd := job.FinalizeAttemptCommand{
		JobID:            jobID,
		Outcome:          job.OutcomeFailedPermanent,
		WorkerID:         "worker-A",
		LeaseID:          "lease-A",
		ExpectedRevision: 1,
		ErrorMessage:     errMsg,
		DLQPayload:       dlqPayload,
		// EventType intentionally omitted: the load-bearing surface
		// for DEAD_LETTERED is the dead_letter_jobs archive row, not
		// the job_events audit row. (job_failed event is also inserted
		// by Fail separately, so dropping it here avoids a duplicate.)
	}
	res, err := store.FinalizeAttempt(ctx, cmd)
	require.NoError(t, err)
	assert.Equal(t, job.StatusFailed, res.FinalStatus,
		"FinalizeAttempt(FailedPermanent) MUST return StatusFailed")
	assert.True(t, res.DLQRecorded,
		"FinalizeAttempt(DLQPayload) MUST set res.DLQRecorded=true")

	// The job row MUST be FAILED with the canonical error.
	row := readLifecycleRow(t, db, jobID)
	assert.Equal(t, "FAILED", row.status)
	assert.Equal(t, errMsg, row.errMessage)

	// The dead_letter_jobs row MUST exist with the canonical payload
	// (forensic archive surface for operators).
	var dlqErrCol, dlqPayloadCol string
	if err := db.QueryRow(
		`SELECT error, payload_json FROM dead_letter_jobs WHERE job_id = ?`, jobID,
	).Scan(&dlqErrCol, &dlqPayloadCol); err != nil {
		t.Fatalf("read dead_letter_jobs (id=%s): %v", jobID, err)
	}
	assert.Equal(t, errMsg, dlqErrCol, "dead_letter_jobs.error MUST mirror the FAILED error")
	assert.Equal(t, string(dlqPayload), dlqPayloadCol,
		"dead_letter_jobs.payload_json MUST carry the canonical DLQ payload")
}
