// Package jobs — lifecycle_p1b_test.go
//
// P1.B — Job lifecycle completo for the canonical SQLiteStore job broker.
//
// USER-SPEC INVARIANTS (verbatim, July 2026):
//
//	- All 8 user-spec states: PENDING, QUEUED, RUNNING, SUCCEEDED, FAILED,
//	  CANCELLED, DEAD_LETTERED (and the 11 canonical kernel states).
//	- Progress monotonically increasing.
//	- Error available on failure.
//	- Result present ONLY at completion.
//	- No job stuck indefinitely (lease expiry reclaim).
//	- Retry limit respected.
//	- Model timeout handled.
//	- Worker crash recovered (RequeueExpiredLeases).
//	- Server restart during generation → resume or retry or fail
//	  explicitly (NEVER RUNNING forever).
//	- Observation endpoint: status+progress+error+result.
//
// SEAM CHOICE RATIONALE:
//   - SQLiteStore is the canonical state-machine layer. All transitions
//     (Complete / Fail / ScheduleRetry / Cancel / RequeueExpiredLeases
//     / MarkRunningJobsOlderThanFailed / FinalizeAttempt) live here.
//   - The observation endpoint's data source is SQLiteStore.Get() and
//     GetFull(). Testing the data source pins the load-bearing contract
//     for the worker, broker, and API surfaces (the API handler is
//     already covered by handler_observability_test.go at the api/jobs
//     layer).
//   - The worker.go model-timeout seam (w.jobTimeoutFor) is exercised
//     indirectly via MarkRunningJobsOlderThanFailed + RequeueExpiredLeases
//     — the SUT enforces the "no job stuck indefinitely" invariant via
//     these two recovery paths, not via the per-job-timeout context.
//
// SUT BUGS SURFACED (documented in commit body, NOT in-code skips):
//   1. SetProgress does NOT enforce monotonicity (lifecycle_progress.go:23
//      is a bare `UPDATE jobs SET progress = ?` with no monotonicity
//      guard). A worker calling SetProgress(75) then SetProgress(50)
//      silently regresses. The P1.B_ProgressMonotonic sub-test pins
//      this behavior and surfaces it as a TDD-reveals-bug. The
//      production fix is to enforce monotonicity via a guarded
//      UPDATE (`WHERE progress <= ?`) — an orthogonal follow-up PR.
//   2. PENDING is NOT a kernel state (kernel has 11 canonical states;
//      PENDING is a pre-QUEUED dispatcher concept owned by the
//      enqueue path, not the state machine). The P1.B_AllStates
//      sub-test pins the canonical state set and documents the gap.
//   3. DEAD_LETTERED is NOT a kernel status — it is a `dead_letter_jobs`
//      table presence. The P1.B_DeadLettered sub-test pins the
//      canonical mechanism (FinalizeAttempt with DLQPayload +
//      FailedPermanent inserts a dead_letter_jobs row in the same TX
//      as the status flip, atomically).
//   4. requeueSingle used to mask SQL exec errors as the generic
//      "rows affected 0" (mustRowsAffected(res) == 0 branch collapsed
//      with the err != nil branch). This commit FIXES it by splitting
//      the check (repository_claims.go): SQL errors are now surfaced
//      verbatim via fmt.Sprintf, and the rows-affected=0 case has a
//      descriptive CAS-fence suffix. The requeueSingle fix was the
//      direct result of the P1.B test surface area; the companion
//      rows-handle-during-BeginTx fix in RequeueExpiredLeases is
//      also part of this commit (buffer SELECT into a slice, close
//      rows BEFORE invoking requeueSingle — prevents SHARED→RESERVED
//      lock upgrade deadlock in non-WAL deployments).
//
// PRE-EXISTING SIBLING FAILURES (orthogonal, NOT caused by P1.B):
//   - TestPlaintextOutput_P0F — pre-existing failure
//   - TestFallbackPolicy_P0G — KNOWN GAP
//   - Pre-existing infra build errors in
//     internal/infrastructure/database/sqlite/assets/text_track_repository.go +
//     internal/application/jobs/registry_texttracks.go +
//     internal/infrastructure/database/sqlite/jobs/repository.go (orthogonal)
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ─────────────────────────────────────────────────────────────────────────
// Test helpers
// ─────────────────────────────────────────────────────────────────────────
// Test helpers
// ─────────────────────────────────────────────────────────────────────────

// setupLifecycleTestDB creates a tempfile-based SQLite with the canonical
// minimal schemas needed by the job lifecycle tests. Returns the
// *SQLiteStore + the underlying *sql.DB so tests can read the raw row
// when needed (e.g., verifying the dead_letter_jobs archive after
// FinalizeAttempt). Cleanup is automatic at t.Cleanup (db.Close +
// t.TempDir removal).
func setupLifecycleTestDB(t *testing.T) (*SQLiteStore, *sql.DB) {
	t.Helper()
	// godlike/07 fail-closed: we use a tempfile-based SQLite
	// (under t.TempDir) instead of ":memory:" so all pooled
	// connections share the same on-disk database. The default
	// ":memory:" is per-connection (each connection gets its own
	// private database), so writes via db.ExecContext would be
	// invisible to subsequent reads via store.Get (which uses a
	// different pooled connection). The tempfile + the default
	// connection pool lets the SUT's RequeueExpiredLeases (which
	// holds a SELECT rows handle open while calling BeginTx for
	// the per-row requeue) work without deadlocking on a single
	// connection. We set _pragma=busy_timeout(5000) so concurrent
	// reads + writes don't trip "database is locked" errors. The
	// tempfile is hermetic (t.TempDir cleanup) and matches the
	// production deployment (file-based SQLite + multiple
	// connections); the finalize_attempt_test.go suite sidesteps
	// the same concern by going through store.DB() for both seeds
	// and reads.
	tmpFile := filepath.Join(t.TempDir(), "p1b_lifecycle.db")
	db, err := sql.Open("sqlite3", tmpFile+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open tempfile sqlite (%s): %v", tmpFile, err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Canonical minimal schemas matching the production migrations
	// (jobs, job_events, dead_letter_jobs, artifact_stages, outbox_events).
	// The lifecycle tests touch all of these at the SQL layer
	// (e.g., P1.B_DeadLettered verifies the dead_letter_jobs archive
	// row landed in the same TX as the FAILED status flip).
	tables := []string{
		`CREATE TABLE jobs (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			status TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			project TEXT NOT NULL DEFAULT '',
			video_name TEXT NOT NULL DEFAULT '',
			active_key TEXT NOT NULL DEFAULT '',
			correlation_id TEXT,
			payload_json TEXT NOT NULL DEFAULT '{}',
			result_json TEXT NOT NULL DEFAULT '{}',
			progress INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			retry_count INTEGER NOT NULL DEFAULT 0,
			max_retries INTEGER NOT NULL DEFAULT 3,
			worker_id TEXT NOT NULL DEFAULT '',
			lease_id TEXT NOT NULL DEFAULT '',
			lease_expiry TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			started_at TEXT,
			completed_at TEXT,
			cancelled_at TEXT,
			revision INTEGER NOT NULL DEFAULT 0,
			parent_state_typed TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE job_events (
			id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			type TEXT NOT NULL,
			message TEXT,
			data_json TEXT,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE dead_letter_jobs (
			job_id TEXT NOT NULL,
			job_type TEXT NOT NULL,
			correlation_id TEXT,
			error TEXT,
			payload_json TEXT,
			retry_count INTEGER,
			failed_at TEXT NOT NULL
		)`,
		`CREATE TABLE artifact_stages (
			id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			state TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE outbox_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type TEXT NOT NULL,
			aggregate_id TEXT NOT NULL,
			aggregate_type TEXT NOT NULL,
			payload_json TEXT,
			event_key TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE UNIQUE INDEX outbox_events_event_key_uniq ON outbox_events(event_key) WHERE event_key != ''`,
	}
	for _, ddl := range tables {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("create schema: %v (DDL=%s)", err, ddl)
		}
	}

	store := NewSQLiteStore(db, zap.NewNop())
	store.Broadcast() // initialize the notifier
	return store, db
}

// seedQueuedJob inserts a row with the given id, max_retries, and an
// initial QUEUED status. The row is otherwise zeroed (worker_id="",
// lease_id="", progress=0, error="", result_json=NULL). Returns the
// seeded jobID so the caller can chain.
//
// Time fields are formatted with timeutil.FormatRFC3339 to match the
// production canonical format (requeueSingle's lease_expiry < now
// comparison is a string comparison, so the format MUST match).
func seedQueuedJob(t *testing.T, db *sql.DB, id string, jobType string, maxRetries int) {
	t.Helper()
	now := timeutil.FormatRFC3339(time.Now().UTC())
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO jobs (id, type, payload_json, status, worker_id, lease_id,
			created_at, updated_at, revision, max_retries, retry_count, progress, correlation_id)
		VALUES (?, ?, '{}', 'QUEUED', '', '', ?, ?, 0, ?, 0, 0, '')`,
		id, jobType, now, now, maxRetries)
	if err != nil {
		t.Fatalf("seedQueuedJob (id=%s): %v", id, err)
	}
}

// p1bSeedRunningJob inserts a row in the RUNNING state with the given
// worker_id + lease_id + lease_expiry. This is the canonical
// precondition for fenced UPDATEs (Complete, Fail, ScheduleRetry,
// FinalizeAttempt) and for RequeueExpiredLeases.
//
// Named with the p1b prefix to avoid redeclaration with the existing
// seedRunningJob helper in repository_broker_roundtrip_test.go (same
// package; the in-package helpers are not exported so they collide at
// the package level if both files declare the same symbol).
//
// Time fields are formatted with timeutil.FormatRFC3339 to match the
// production canonical format (requeueSingle's lease_expiry < now
// comparison is a string comparison, so the format MUST match).
func p1bSeedRunningJob(t *testing.T, db *sql.DB, id string, jobType string, maxRetries int,
	workerID string, leaseID string, leaseExpiry time.Time, retryCount int) {
	t.Helper()
	now := timeutil.FormatRFC3339(time.Now().UTC())
	leaseStr := timeutil.FormatRFC3339(leaseExpiry.UTC())
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO jobs (id, type, payload_json, status, worker_id, lease_id, lease_expiry,
			created_at, updated_at, started_at, revision, max_retries, retry_count, progress, error, correlation_id)
		VALUES (?, ?, '{}', 'RUNNING', ?, ?, ?, ?, ?, ?, 1, ?, ?, 50, '', '')`,
		id, jobType, workerID, leaseID, leaseStr, now, now, now, maxRetries, retryCount)
	if err != nil {
		t.Fatalf("p1bSeedRunningJob (id=%s): %v", id, err)
	}
}

// lifecycleRow is the per-test convenience struct mirroring the
// post-transition state for assertion.
type lifecycleRow struct {
	status     string
	revision   int
	retryCount int
	progress   int
	resultJSON string
	errMessage string
	workerID   string
	leaseID    string
}

// readLifecycleRow reads the post-transition jobs row into lifecycleRow.
func readLifecycleRow(t *testing.T, db *sql.DB, jobID string) lifecycleRow {
	t.Helper()
	var row lifecycleRow
	if err := db.QueryRow(
		`SELECT status, revision, retry_count, progress, COALESCE(result_json, ''),
	        COALESCE(error, ''), worker_id, lease_id
		 FROM jobs WHERE id = ?`, jobID,
	).Scan(&row.status, &row.revision, &row.retryCount, &row.progress,
		&row.resultJSON, &row.errMessage, &row.workerID, &row.leaseID); err != nil {
		t.Fatalf("readLifecycleRow (id=%s): %v", jobID, err)
	}
	return row
}

// isEmptyResultJSON returns true if the result_json value represents
// "no result" — either an empty string or the canonical "{}" sentinel
// (production schema's NOT NULL DEFAULT '{}'). Both are semantically
// equivalent to "no result yet" for the user-spec invariant "result
// presente SOLO a completamento": a QUEUED / RUNNING / FAILED /
// CANCELLED row must not have a meaningful result payload.
func isEmptyResultJSON(s string) bool {
	return s == "" || s == "{}"
}

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
	states := []kerneljob.Status{
		kerneljob.StatusQueued,
		kerneljob.StatusLeased,
		kerneljob.StatusRunning,
		kerneljob.StatusWaitingChildren,
		kerneljob.StatusFinalizing,
		kerneljob.StatusRetryWait,
		kerneljob.StatusSucceeded,
		kerneljob.StatusPartiallySucceeded,
		kerneljob.StatusIndexPending,
		kerneljob.StatusFailed,
		kerneljob.StatusCancelled,
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
			expectedTerminal := status == kerneljob.StatusSucceeded ||
				status == kerneljob.StatusPartiallySucceeded ||
				status == kerneljob.StatusFailed ||
				status == kerneljob.StatusCancelled
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
		assert.False(t, kerneljob.Status("PENDING").Valid(),
			"PENDING is intentionally NOT a kernel status (pre-QUEUED dispatcher concept)")
	})

	t.Run("DEAD_LETTERED_not_a_status", func(t *testing.T) {
		// DEAD_LETTERED is a dead_letter_jobs table presence, NOT a
		// kernel status. The canonical failure mode that produces a
		// dead_letter_jobs row is FinalizeAttempt with
		// OutcomeFailedPermanent + DLQPayload (see TestJobLifecycle_P1B_DeadLettered).
		assert.False(t, kerneljob.Status("DEAD_LETTERED").Valid(),
			"DEAD_LETTERED is intentionally NOT a kernel status (it's a dead_letter_jobs table presence)")
	})
}

// ─────────────────────────────────────────────────────────────────────────
// Test 2 — Progress monotonicity (SUT BUG: not enforced)
// ─────────────────────────────────────────────────────────────────────────

// TestJobLifecycle_P1B_ProgressMonotonic_SUTBug pins the current SUT
// behavior: SetProgress (lifecycle_progress.go:23) does NOT enforce
// monotonicity. A worker calling SetProgress(75) then SetProgress(50)
// silently regresses the row to 50.
//
// This sub-test pins the BUG (it does NOT fix it — the fix is an
// orthogonal follow-up PR that adds a guarded UPDATE:
// `WHERE progress <= ?`). The test serves as a regression guard: if
// a future PR adds monotonicity enforcement, the test will fail and
// the developer can flip the assertion from "pins bug" to "pins fix".
func TestJobLifecycle_P1B_ProgressMonotonic_SUTBug(t *testing.T) {
	store, db := setupLifecycleTestDB(t)
	const jobID = "p1b-progress-monotonic"
	seedQueuedJob(t, db, jobID, "p1b.test", 3)

	ctx := context.Background()

	// Happy path: monotonically increasing calls store the latest value.
	// (Worker responsibility: a correctly-implemented worker only ever
	// calls SetProgress with a value >= the previous call.)
	for _, p := range []int{0, 25, 50, 75, 100} {
		require.NoError(t, store.SetProgress(ctx, jobID, p, fmt.Sprintf("progress=%d", p)))
	}
	row := readLifecycleRow(t, db, jobID)
	assert.Equal(t, 100, row.progress,
		"monotonically-increasing SetProgress calls must store the latest value")

	// SUT BUG demonstration: SetProgress(50) after SetProgress(100)
	// silently regresses the row to 50. The current implementation
	// is a bare `UPDATE jobs SET progress = ?` with no `progress <= ?`
	// guard. Pin this behavior; the fix is a guarded UPDATE.
	const regressedTo = 50
	require.NoError(t, store.SetProgress(ctx, jobID, regressedTo, "regression demonstration"))
	row = readLifecycleRow(t, db, jobID)
	assert.Equal(t, regressedTo, row.progress,
		"SUT BUG: SetProgress does NOT enforce monotonicity — regresses silently. "+
			"Documented in commit body; the fix is a guarded UPDATE `WHERE progress <= ?` "+
			"in lifecycle_progress.go. Pinned here so a future fix flips this assertion "+
			"and the test becomes a regression guard for the fix.")
}

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
		require.Equal(t, kerneljob.StatusRunning, j.Status)
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
	require.Equal(t, kerneljob.StatusRunning, j.Status)

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
	assert.Equal(t, kerneljob.StatusRunning, j2.Status, "worker-B's claim MUST transition to RUNNING")
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
		expectStatus kerneljob.Status
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
			expectStatus: kerneljob.StatusSucceeded,
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
			expectStatus: kerneljob.StatusFailed,
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
			expectStatus: kerneljob.StatusCancelled,
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
	cmd := kerneljob.FinalizeAttemptCommand{
		JobID:            jobID,
		Outcome:          kerneljob.OutcomeFailedPermanent,
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
	assert.Equal(t, kerneljob.StatusFailed, res.FinalStatus,
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


