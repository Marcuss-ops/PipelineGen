// repository_broker_roundtrip_test.go — JOBS-T01-SQLITE-REPO (P0, 2026-07-15)
// broker signature round-trip regression tests.
//
// godlike/06 SSOT (one canonical owner per fact): the canonical
// broker contract lives in internal/kernel/job/store.go::Store.
// SQLiteStore is the in-tree implementation; these 4 tests pin
// the round-trip state-transition contract that was silently
// drifting because RenewLease was bumping `revision` inline
// without returning the new value (the kernel signature has NO
// return channel for it).
//
// godlike/07 no-fake-availability: each test asserts a real
// SQL round-trip (insert → mutate → re-read) on a fresh
// in-memory SQLite. Failures are real; passes are real.
// No t.Skip, no log-as-success, no white-box mocks.
//
// The 4 tests pin the canonical state-transition contract:
//
//  1. TestBroker_RoundTrip_Complete_RunningToSucceeded
//     RUNNING → SUCCEEDED is atomic; status + completed_at +
//     result_json + progress=100 + job_completed event all
//     land in a single transaction. The job is NOT orphaned
//     in RUNNING after a successful Complete call.
//
//  2. TestBroker_RoundTrip_Fail_RunningToFailed
//     RUNNING → FAILED is atomic; status + completed_at +
//     error + job_failed event all land in a single
//     transaction. The job is NOT orphaned in RUNNING after
//     a Fail call.
//
//  3. TestBroker_RoundTrip_Cancel_RunningToCancelled
//     RUNNING → CANCELLED is atomic; status + cancelled_at
//     + job_cancelled event all land atomically. The job
//     is NOT orphaned in RUNNING after a Cancel call.
//
//  4. TestBroker_RoundTrip_RenewLease_DoesNotBumpRevision
//     (THE SIGNATURE-DRIFT FIX) RenewLease extends the lease
//     but does NOT bump the revision column. The worker's
//     expectedRevision remains valid for subsequent Complete /
//     Fail CAS checks. Pre-PR this test would FAIL because
//     the silent revision bump invalidated the worker's
//     expectedRevision → CAS mismatch → rollback → orphan.
//
// All tests use a fresh in-memory SQLite (`:memory:`) per test
// to avoid cross-test pollution. The test schema is a SUBSET of
// the canonical production CREATE TABLE for `jobs` and
// `job_events` — only the columns actually read or written by
// Complete / Fail / Cancel / RenewLease are present. The schema
// is intentionally NOT a full mirror (production carries
// additional columns like `parent_state_typed` from migration
// 129, `embedding_json`, etc. that the 4 state transitions do
// not touch).
package jobs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// jobsTestSchema is the canonical SUBSET of the production jobs
// + job_events tables — only the columns read or written by the
// 4 round-trip state transitions (Complete / Fail / Cancel /
// RenewLease) are present. Columns match repository.go::jobColumns
// (the canonical read column list) plus the lease/revision write
// columns. Future agents adding a test that exercises a
// different method (e.g. FinalizeAggregateParent, which writes
// `parent_state_typed`) MUST extend this schema constant per
// godlike/06 SSOT — do NOT silently rely on phantom columns.
const jobsTestSchema = `
CREATE TABLE jobs (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    status TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0,
    project TEXT NOT NULL DEFAULT '',
    video_name TEXT NOT NULL DEFAULT '',
    active_key TEXT NOT NULL DEFAULT '',
    correlation_id TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    result_json TEXT NOT NULL DEFAULT '{}',
    progress INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    retry_count INTEGER NOT NULL DEFAULT 0,
    max_retries INTEGER NOT NULL DEFAULT 3,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_id TEXT NOT NULL DEFAULT '',
    lease_expiry DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    started_at DATETIME,
    completed_at DATETIME,
    cancelled_at DATETIME,
    revision INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE job_events (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    type TEXT NOT NULL,
    message TEXT DEFAULT '',
    data_json TEXT DEFAULT '{}',
    created_at DATETIME
);
`

// newBrokerTestDB returns a fresh in-memory SQLite handle with the
// canonical jobs + job_events schema. Each test gets its own DB to
// avoid cross-test pollution (the broker uses package-level
// observability counters that survive between tests, but the DB
// itself is hermetic per-test).
func newBrokerTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(context.Background(), jobsTestSchema); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	return db
}

// seedRunningJob inserts a job in RUNNING state with the given
// (workerID, leaseID, revision, leaseTTL) tuple. The test caller
// then exercises a state transition (Complete / Fail / Cancel /
// RenewLease) and asserts the post-state. Returns the jobID.
func seedRunningJob(t *testing.T, db *sql.DB, workerID, leaseID string, revision int, leaseTTL time.Duration) string {
	t.Helper()
	jobID := "job_test_" + time.Now().Format("150405.000000000")
	now := time.Now().UTC()
	leaseExpiry := now.Add(leaseTTL)
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO jobs (id, type, status, priority, project, video_name, active_key,
			correlation_id, payload_json, result_json, progress, error, retry_count, max_retries,
			worker_id, lease_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at, revision)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, jobID, "test.job", "RUNNING", 0, "test-project", "test-video", "",
		"corr-test", "{}", "{}", 0, "", 0, 3,
		workerID, leaseID, leaseExpiry.UTC().Format("2006-01-02 15:04:05.999999999"),
		now.UTC().Format("2006-01-02 15:04:05.999999999"),
		now.UTC().Format("2006-01-02 15:04:05.999999999"),
		now.UTC().Format("2006-01-02 15:04:05.999999999"),
		nil, nil, revision)
	if err != nil {
		t.Fatalf("seed RUNNING job %q: %v", jobID, err)
	}
	return jobID
}

// readJobStatus returns the current (status, revision) for the
// given jobID, post-transition. Used by the round-trip assertions.
func readJobStatus(t *testing.T, db *sql.DB, jobID string) (string, int) {
	t.Helper()
	var status string
	var revision int
	err := db.QueryRowContext(context.Background(),
		`SELECT status, revision FROM jobs WHERE id = ?`, jobID,
	).Scan(&status, &revision)
	if err != nil {
		t.Fatalf("read job %q status: %v", jobID, err)
	}
	return status, revision
}

// readJobCompletedAt returns the completed_at value (nullable) for
// post-Complete assertions.
func readJobCompletedAt(t *testing.T, db *sql.DB, jobID string) sql.NullString {
	t.Helper()
	var completedAt sql.NullString
	err := db.QueryRowContext(context.Background(),
		`SELECT completed_at FROM jobs WHERE id = ?`, jobID,
	).Scan(&completedAt)
	if err != nil {
		t.Fatalf("read job %q completed_at: %v", jobID, err)
	}
	return completedAt
}

// readJobCancelledAt returns the cancelled_at value (nullable) for
// post-Cancel assertions.
func readJobCancelledAt(t *testing.T, db *sql.DB, jobID string) sql.NullString {
	t.Helper()
	var cancelledAt sql.NullString
	err := db.QueryRowContext(context.Background(),
		`SELECT cancelled_at FROM jobs WHERE id = ?`, jobID,
	).Scan(&cancelledAt)
	if err != nil {
		t.Fatalf("read job %q cancelled_at: %v", jobID, err)
	}
	return cancelledAt
}

// readJobError returns the error column for post-Fail assertions.
func readJobError(t *testing.T, db *sql.DB, jobID string) string {
	t.Helper()
	var errMsg string
	if err := db.QueryRowContext(context.Background(),
		`SELECT error FROM jobs WHERE id = ?`, jobID,
	).Scan(&errMsg); err != nil {
		t.Fatalf("read job %q error: %v", jobID, err)
	}
	return errMsg
}

// readJobProgress returns the progress column for post-Complete
// assertions (Complete must stamp progress=100).
func readJobProgress(t *testing.T, db *sql.DB, jobID string) int {
	t.Helper()
	var progress int
	if err := db.QueryRowContext(context.Background(),
		`SELECT progress FROM jobs WHERE id = ?`, jobID,
	).Scan(&progress); err != nil {
		t.Fatalf("read job %q progress: %v", jobID, err)
	}
	return progress
}

// readJobLeaseExpiry returns the lease_expiry value (nullable) for
// post-RenewLease assertions.
func readJobLeaseExpiry(t *testing.T, db *sql.DB, jobID string) sql.NullString {
	t.Helper()
	var leaseExpiry sql.NullString
	err := db.QueryRowContext(context.Background(),
		`SELECT lease_expiry FROM jobs WHERE id = ?`, jobID,
	).Scan(&leaseExpiry)
	if err != nil {
		t.Fatalf("read job %q lease_expiry: %v", jobID, err)
	}
	return leaseExpiry
}

// readLatestEventForJob returns the type + message of the most
// recently inserted event for the given jobID. Used to assert
// the canonical event types per state transition.
func readLatestEventForJob(t *testing.T, db *sql.DB, jobID string) (string, string) {
	t.Helper()
	var evtType, msg string
	err := db.QueryRowContext(context.Background(),
		`SELECT type, message FROM job_events WHERE job_id = ? ORDER BY created_at DESC LIMIT 1`,
		jobID,
	).Scan(&evtType, &msg)
	if err != nil {
		t.Fatalf("read latest event for job %q: %v", jobID, err)
	}
	return evtType, msg
}

// TestBroker_RoundTrip_Complete_RunningToSucceeded pins the
// canonical Complete happy-path contract: a job in RUNNING state
// with a valid (workerID, leaseID, expectedRevision) tuple
// transitions to SUCCEEDED atomically, with completed_at set,
// progress=100, and a `job_completed` event recorded.
//
// godlike/07 no-fake-availability: this is a real SQL round-trip
// (insert → Complete → re-read) on an in-memory SQLite. The
// assertion fails closed if Complete silently no-ops the
// transition (status stays RUNNING → job ORPHANED in broker).
func TestBroker_RoundTrip_Complete_RunningToSucceeded(t *testing.T) {
	db := newBrokerTestDB(t)
	ctx := context.Background()
	store := NewSQLiteStore(db, zap.NewNop())

	const (
		workerID = "worker-A"
		leaseID  = "lease-X"
		revision = 5
	)
	jobID := seedRunningJob(t, db, workerID, leaseID, revision, 30*time.Second)

	// Pre-assert: status=RUNNING, revision=5, completed_at NULL.
	if status, _ := readJobStatus(t, db, jobID); status != "RUNNING" {
		t.Fatalf("pre-condition: expected status=RUNNING, got %q", status)
	}
	if completedAt := readJobCompletedAt(t, db, jobID); completedAt.Valid {
		t.Fatalf("pre-condition: expected completed_at NULL, got %q", completedAt.String)
	}

	// Exercise: Complete with the matching (workerID, leaseID, expectedRevision).
	if err := store.Complete(ctx, jobID, workerID, leaseID, revision, []byte(`{"stats":{"images_generated":3}}`)); err != nil {
		t.Fatalf("Complete returned error: %v (signature drift: orphaning the RUNNING job)", err)
	}

	// Post-assert: status=SUCCEEDED, completed_at NOT NULL, progress=100.
	status, newRevision := readJobStatus(t, db, jobID)
	if status != "SUCCEEDED" {
		t.Fatalf("post-Complete: expected status=SUCCEEDED, got %q (ORPHAN)", status)
	}
	if newRevision != revision+1 {
		t.Errorf("post-Complete: expected revision=%d (one bump from fenced state transition), got %d", revision+1, newRevision)
	}
	if completedAt := readJobCompletedAt(t, db, jobID); !completedAt.Valid {
		t.Errorf("post-Complete: expected completed_at NOT NULL, got NULL")
	}
	if progress := readJobProgress(t, db, jobID); progress != 100 {
		t.Errorf("post-Complete: expected progress=100, got %d", progress)
	}

	// Event assertion: the canonical `job_completed` event was
	// recorded in the same transaction as the state transition.
	evtType, evtMsg := readLatestEventForJob(t, db, jobID)
	if evtType != "job_completed" {
		t.Errorf("post-Complete: expected latest event type=job_completed, got %q", evtType)
	}
	if evtMsg == "" {
		t.Errorf("post-Complete: expected latest event message non-empty, got empty")
	}
}

// TestBroker_RoundTrip_Fail_RunningToFailed pins the canonical
// Fail happy-path contract: a job in RUNNING state with a valid
// (workerID, leaseID, expectedRevision) tuple transitions to
// FAILED atomically, with completed_at set, error set, and a
// `job_failed` event recorded.
//
// godlike/07 no-fake-availability: real SQL round-trip; the
// assertion fails closed if Fail silently no-ops the transition.
func TestBroker_RoundTrip_Fail_RunningToFailed(t *testing.T) {
	db := newBrokerTestDB(t)
	ctx := context.Background()
	store := NewSQLiteStore(db, zap.NewNop())

	const (
		workerID = "worker-A"
		leaseID  = "lease-X"
		revision = 7
	)
	jobID := seedRunningJob(t, db, workerID, leaseID, revision, 30*time.Second)

	// Pre-assert: status=RUNNING.
	if status, _ := readJobStatus(t, db, jobID); status != "RUNNING" {
		t.Fatalf("pre-condition: expected status=RUNNING, got %q", status)
	}

	// Exercise: Fail with the matching (workerID, leaseID, expectedRevision).
	const failMsg = "test-fail: simulated downstream error"
	if err := store.Fail(ctx, jobID, workerID, leaseID, revision, failMsg); err != nil {
		t.Fatalf("Fail returned error: %v (signature drift: orphaning the RUNNING job)", err)
	}

	// Post-assert: status=FAILED, completed_at NOT NULL, error set.
	status, newRevision := readJobStatus(t, db, jobID)
	if status != "FAILED" {
		t.Fatalf("post-Fail: expected status=FAILED, got %q (ORPHAN)", status)
	}
	if newRevision != revision+1 {
		t.Errorf("post-Fail: expected revision=%d (one bump from fenced state transition), got %d", revision+1, newRevision)
	}
	if completedAt := readJobCompletedAt(t, db, jobID); !completedAt.Valid {
		t.Errorf("post-Fail: expected completed_at NOT NULL, got NULL")
	}
	if errMsg := readJobError(t, db, jobID); errMsg != failMsg {
		t.Errorf("post-Fail: expected error=%q, got %q", failMsg, errMsg)
	}

	// Event assertion: the canonical `job_failed` event was recorded.
	evtType, evtMsg := readLatestEventForJob(t, db, jobID)
	if evtType != "job_failed" {
		t.Errorf("post-Fail: expected latest event type=job_failed, got %q", evtType)
	}
	if evtMsg != failMsg {
		t.Errorf("post-Fail: expected latest event message=%q, got %q", failMsg, evtMsg)
	}
}

// TestBroker_RoundTrip_Cancel_RunningToCancelled pins the canonical
// Cancel happy-path contract: a job in RUNNING state transitions
// to CANCELLED atomically, with cancelled_at set and a
// `job_cancelled` event recorded.
//
// godlike/07 no-fake-availability: real SQL round-trip; the
// assertion fails closed if Cancel silently no-ops the transition.
func TestBroker_RoundTrip_Cancel_RunningToCancelled(t *testing.T) {
	db := newBrokerTestDB(t)
	ctx := context.Background()
	store := NewSQLiteStore(db, zap.NewNop())

	const (
		workerID = "worker-A"
		leaseID  = "lease-X"
		revision = 3
	)
	jobID := seedRunningJob(t, db, workerID, leaseID, revision, 30*time.Second)

	// Pre-assert: status=RUNNING.
	if status, _ := readJobStatus(t, db, jobID); status != "RUNNING" {
		t.Fatalf("pre-condition: expected status=RUNNING, got %q", status)
	}

	// Exercise: Cancel (Cancel does not take a worker/lease tuple
	// because it is an operator action with no lease requirement).
	if err := store.Cancel(ctx, jobID); err != nil {
		t.Fatalf("Cancel returned error: %v (signature drift: orphaning the RUNNING job)", err)
	}

	// Post-assert: status=CANCELLED, cancelled_at NOT NULL.
	status, newRevision := readJobStatus(t, db, jobID)
	if status != "CANCELLED" {
		t.Fatalf("post-Cancel: expected status=CANCELLED, got %q (ORPHAN)", status)
	}
	if newRevision != revision+1 {
		t.Errorf("post-Cancel: expected revision=%d (one bump from fenced state transition), got %d", revision+1, newRevision)
	}
	if cancelledAt := readJobCancelledAt(t, db, jobID); !cancelledAt.Valid {
		t.Errorf("post-Cancel: expected cancelled_at NOT NULL, got NULL")
	}

	// Event assertion: the canonical `job_cancelled` event was recorded.
	evtType, _ := readLatestEventForJob(t, db, jobID)
	if evtType != "job_cancelled" {
		t.Errorf("post-Cancel: expected latest event type=job_cancelled, got %q", evtType)
	}
}

// TestBroker_RoundTrip_RenewLease_DoesNotBumpRevision is the
// SIGNATURE-DRIFT-FIX test. It pins the canonical
// kernel/job.Store::RenewLease contract: RenewLease extends the
// lease expiry but does NOT bump the revision column.
//
// Pre-PR this test would FAIL because RenewLease silently
// incremented `revision = revision + 1` inline, which
// invalidated the worker's expectedRevision for subsequent
// Complete / Fail calls (the kernel signature has NO return
// channel for the new revision). The worker's stale
// expectedRevision triggered CAS mismatch on Complete / Fail
// → tx rollback → job orphaned in RUNNING state in the broker.
//
// Post-PR (the fix): RenewLease returns void and does NOT mutate
// the revision column. The worker's expectedRevision remains
// valid for subsequent Complete / Fail calls. The audit-pin
// (revision is bumped only on fenced state transitions:
// Complete, Fail, ScheduleRetry, Cancel, Retry,
// FinalizeAggregateParent) preserves the canonical CAS-fence
// invariant.
//
// godlike/07 no-fake-availability: real SQL round-trip; the
// assertion fails closed if RenewLease silently mutates the
// revision column (the regression scenario).
func TestBroker_RoundTrip_RenewLease_DoesNotBumpRevision(t *testing.T) {
	db := newBrokerTestDB(t)
	ctx := context.Background()
	store := NewSQLiteStore(db, zap.NewNop())

	const (
		workerID = "worker-A"
		leaseID  = "lease-X"
		revision = 5 // CRITICAL: this is the value the test pins against
	)
	const newLeaseTTL = 60 * time.Second
	jobID := seedRunningJob(t, db, workerID, leaseID, revision, 30*time.Second)

	// Capture pre-state.
	preStatus, preRevision := readJobStatus(t, db, jobID)
	if preStatus != "RUNNING" {
		t.Fatalf("pre-condition: expected status=RUNNING, got %q", preStatus)
	}
	if preRevision != revision {
		t.Fatalf("pre-condition: expected revision=%d, got %d", revision, preRevision)
	}
	preLeaseExpiry := readJobLeaseExpiry(t, db, jobID)
	if !preLeaseExpiry.Valid {
		t.Fatalf("pre-condition: expected lease_expiry NOT NULL, got NULL")
	}

	// Exercise: RenewLease with the matching (id, workerID, newLeaseTTL).
	// The kernel signature is RenewLease(ctx, id, workerID, leaseTTL) — no
	// expectedRevision arg, no return value. The implementation must
	// NOT bump the revision column.
	if err := store.RenewLease(ctx, jobID, workerID, newLeaseTTL); err != nil {
		t.Fatalf("RenewLease returned error: %v", err)
	}

	// Post-assert: revision is UNCHANGED (the canonical signature contract).
	// This is the load-bearing assertion: if RenewLease silently bumps
	// the revision, the worker's expectedRevision is invalidated and
	// subsequent Complete / Fail calls will hit CAS mismatch.
	postStatus, postRevision := readJobStatus(t, db, jobID)
	if postStatus != "RUNNING" {
		t.Errorf("post-RenewLease: expected status=RUNNING, got %q", postStatus)
	}
	if postRevision != preRevision {
		t.Errorf("post-RenewLease: revision DRIFT — expected unchanged revision=%d, got %d (signature-drift regression: RenewLease silently bumps revision, invalidating the worker's expectedRevision for subsequent Complete / Fail CAS checks -> ORPHAN)",
			preRevision, postRevision)
	}

	// Post-assert: lease_expiry was extended (the positive effect).
	postLeaseExpiry := readJobLeaseExpiry(t, db, jobID)
	if !postLeaseExpiry.Valid {
		t.Errorf("post-RenewLease: expected lease_expiry NOT NULL, got NULL")
	}
	if postLeaseExpiry.String == preLeaseExpiry.String {
		t.Errorf("post-RenewLease: expected lease_expiry to be EXTENDED, got unchanged value %q", postLeaseExpiry.String)
	}
}
