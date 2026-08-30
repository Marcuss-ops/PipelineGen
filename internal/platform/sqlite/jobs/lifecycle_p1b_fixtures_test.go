// Package jobs — lifecycle_p1b_fixtures_test.go (split surface: shared fixtures).
//
// Shared helpers + canonical P1.B scope for the SQLiteStore job-lifecycle
// test suite. Pure relocation from lifecycle_p1b_test.go; no behavior change.
// All 6 other split files (state / progress / payload / lease_retry /
// recovery / observation) reference the helpers defined here via
// package-level visibility (same `package jobs`).
//
// USER-SPEC INVARIANTS (verbatim, July 2026):
//
//   - All 8 user-spec states: PENDING, QUEUED, RUNNING, SUCCEEDED, FAILED,
//     CANCELLED, DEAD_LETTERED (and the 11 canonical kernel states).
//   - Progress monotonically increasing.
//   - Error available on failure.
//   - Result present ONLY at completion.
//   - No job stuck indefinitely (lease expiry reclaim).
//   - Retry limit respected.
//   - Model timeout handled.
//   - Worker crash recovered (RequeueExpiredLeases).
//   - Server restart during generation → resume or retry or fail
//     explicitly (NEVER RUNNING forever).
//   - Observation endpoint: status+progress+error+result.
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
//  1. SetProgress does NOT enforce monotonicity (lifecycle_progress.go:23
//     is a bare `UPDATE jobs SET progress = ?` with no monotonicity
//     guard). A worker calling SetProgress(75) then SetProgress(50)
//     silently regresses. The P1.B_ProgressMonotonic sub-test pins
//     this behavior and surfaces it as a TDD-reveals-bug. The
//     production fix is to enforce monotonicity via a guarded
//     UPDATE (`WHERE progress <= ?`) — an orthogonal follow-up PR.
//  2. PENDING is NOT a kernel state (kernel has 11 canonical states;
//     PENDING is a pre-QUEUED dispatcher concept owned by the
//     enqueue path, not the state machine). The P1.B_AllStates
//     sub-test pins the canonical state set and documents the gap.
//  3. DEAD_LETTERED is NOT a kernel status — it is a `dead_letter_jobs`
//     table presence. The P1.B_DeadLettered sub-test pins the
//     canonical mechanism (FinalizeAttempt with DLQPayload +
//     FailedPermanent inserts a dead_letter_jobs row in the same TX
//     as the status flip, atomically).
//  4. requeueSingle used to mask SQL exec errors as the generic
//     "rows affected 0" (mustRowsAffected(res) == 0 branch collapsed
//     with the err != nil branch). This commit FIXES it by splitting
//     the check (repository_claims.go): SQL errors are now surfaced
//     verbatim via fmt.Sprintf, and the rows-affected=0 case has a
//     descriptive CAS-fence suffix. The requeueSingle fix was the
//     direct result of the P1.B test surface area; the companion
//     rows-handle-during-BeginTx fix in RequeueExpiredLeases is
//     also part of this commit (buffer SELECT into a slice, close
//     rows BEFORE invoking requeueSingle — prevents SHARED→RESERVED
//     lock upgrade deadlock in non-WAL deployments).
//
// PRE-EXISTING SIBLING FAILURES (orthogonal, NOT caused by P1.B):
//   - TestPlaintextOutput_P0F — pre-existing failure
//   - TestFallbackPolicy_P0G — KNOWN GAP
//   - Pre-existing infra build errors in
//     internal/platform/sqlite/assets/text_track_repository.go +
//     internal/capabilities/jobs/queue/registry_texttracks.go +
//     internal/platform/sqlite/jobs/repository.go (orthogonal)
package jobs

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

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
			parent_state_typed TEXT NOT NULL DEFAULT '',
			parent_job_id TEXT NOT NULL DEFAULT '',
			root_job_id TEXT NOT NULL DEFAULT '',
			client_id TEXT NOT NULL DEFAULT '',
			idempotency_key TEXT NOT NULL DEFAULT ''
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
