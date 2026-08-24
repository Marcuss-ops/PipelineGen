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
//
// godlike/07 minimum-blast-radius: the read-path uses a single
// `readJob` helper that scans all 9 canonical fields in one
// query (was 6 separate round-trip helpers; see Defect 4 in
// the code-review for the prior refactor rationale). The
// `readLatestEventForJob` helper is retained because it queries
// a different table (job_events) and is a different concern.
// seedRunningJob uses the production timeutil.FormatRFC3339
// canonical format for consistency with the production code
// path (was SQL DATETIME literal format; see Defect 3).
package jobs

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// P0.F regression-surface synergy (July 2026): the previously
// imported `sqljobs "...internal/platform/sqlite/jobs"`
// alias is REMOVED. The alias created an import cycle at build time
// (this test file is already in `package jobs`, so importing the
// same package via its full path is a self-import). The pacote-local
// symbols (ErrLeaseLost, ErrTransitionConflict) are now referenced
// directly without the alias. This drops 3 lines from the test
// header and resolves the load-bearing build blocker that prevented
// the JOBS-T01-SQLITE-REPO round-trip regression suite from
// running.

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
    parent_state_typed TEXT NOT NULL DEFAULT '',
    parent_job_id TEXT NOT NULL DEFAULT '',
    root_job_id TEXT NOT NULL DEFAULT '',
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

CREATE TABLE outbox_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    aggregate_type TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    event_key TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX outbox_events_event_key_uniq ON outbox_events(event_key) WHERE event_key != '';
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

// jobSnapshot is the canonical row-level projection read by the 4
// round-trip tests. All fields the tests assert on are present
// in a single SELECT (see readJob) — this avoids the 6 separate
// round-trip helpers that the pre-refactor code carried
// (readJobStatus / readJobCompletedAt / readJobCancelledAt /
// readJobError / readJobProgress / readJobLeaseExpiry). The
// `*NullString` fields are nullable per the schema (lease_expiry
// is set on ClaimNext, completed_at is set on Complete/Fail,
// cancelled_at is set on Cancel) — `Valid` distinguishes
// "column is NULL" from "column is empty string".
type jobSnapshot struct {
	Status      string         // current status (RUNNING, SUCCEEDED, FAILED, CANCELLED, ...)
	Revision    int            // current revision counter (bumped on fenced state transitions)
	CompletedAt sql.NullString // set after Complete/Fail; NULL while pre-terminal
	CancelledAt sql.NullString // set after Cancel; NULL while pre-terminal
	Error       string         // error message (set after Fail)
	Progress    int            // progress percentage (100 after Complete)
	LeaseExpiry sql.NullString // set on ClaimNext; extended on RenewLease
	WorkerID    string         // owning worker (set on Claim, cleared on Complete/Fail)
	LeaseID     string         // lease identifier (set on Claim, cleared on Complete/Fail)
}

// readJob returns the canonical row-level projection for the
// given jobID in a single SELECT. Replaces the 6 pre-refactor
// read-helpers (one query per field) with a single query per
// row — 5 round-trips saved per test (4 tests × 5 saved
// round-trips = 20 round-trips saved across the suite).
//
// godlike/06 SSOT (one canonical owner per fact): the SELECT
// column list is the canonical SUBSET of the production
// `jobs` row that the 4 state transitions touch. The schema
// constant jobsTestSchema is the load-bearing invariant —
// adding a column to the SELECT WITHOUT extending the
// schema constant is a godlike/07 silent-fake-availability
// regression (the scan would fail at test-time).
func readJob(t *testing.T, db *sql.DB, jobID string) jobSnapshot {
	t.Helper()
	var s jobSnapshot
	err := db.QueryRowContext(context.Background(),
		`SELECT status, revision, completed_at, cancelled_at, error, progress, lease_expiry, worker_id, lease_id
		 FROM jobs WHERE id = ?`, jobID,
	).Scan(
		&s.Status, &s.Revision, &s.CompletedAt, &s.CancelledAt,
		&s.Error, &s.Progress, &s.LeaseExpiry, &s.WorkerID, &s.LeaseID,
	)
	if err != nil {
		t.Fatalf("readJob %q: %v", jobID, err)
	}
	return s
}

// readLatestEventForJob returns the type + message of the most
// recently inserted event for the given jobID. Used to assert
// the canonical event types per state transition. RETAINED as
// a separate helper (not folded into readJob) because it
// queries the `job_events` table, not `jobs` — a different
// concern per godlike/06 SSOT one-canonical-owner-per-fact.
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

// seedRunningJob inserts a job in RUNNING state with the given
// (workerID, leaseID, revision, leaseTTL) tuple. The test caller
// then exercises a state transition (Complete / Fail / Cancel /
// RenewLease) and asserts the post-state. Returns the jobID.
//
// Timestamp format: timeutil.FormatRFC3339 (RFC3339 canonical
// format) for consistency with the production code path
// (godlike/06 SSOT — one canonical owner per fact). The SQL
// DATETIME column accepts both the SQL literal format
// ("YYYY-MM-DD HH:MM:SS") AND the RFC3339 format, so this is
// a refactor-only change with no behavioral drift.
func seedRunningJob(t *testing.T, db *sql.DB, workerID, leaseID string, revision int, leaseTTL time.Duration) string {
	t.Helper()
	jobID := "job_test_" + time.Now().Format("150405.000000000")
	now := time.Now().UTC()
	leaseExpiry := now.Add(leaseTTL)
	nowStr := timeutil.FormatRFC3339(now)
	leaseExpiryStr := timeutil.FormatRFC3339(leaseExpiry)
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO jobs (id, type, status, priority, project, video_name, active_key,
			correlation_id, payload_json, result_json, progress, error, retry_count, max_retries,
			worker_id, lease_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at, revision)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, jobID, "test.job", "RUNNING", 0, "test-project", "test-video", "",
		"corr-test", "{}", "{}", 0, "", 0, 3,
		workerID, leaseID, leaseExpiryStr,
		nowStr, nowStr, nowStr,
		nil, nil, revision)
	if err != nil {
		t.Fatalf("seed RUNNING job %q: %v", jobID, err)
	}
	return jobID
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
	pre := readJob(t, db, jobID)
	if pre.Status != "RUNNING" {
		t.Fatalf("pre-condition: expected status=RUNNING, got %q", pre.Status)
	}
	if pre.CompletedAt.Valid {
		t.Fatalf("pre-condition: expected completed_at NULL, got %q", pre.CompletedAt.String)
	}

	// Exercise: Complete with the matching (workerID, leaseID, expectedRevision).
	if err := store.Complete(ctx, jobID, workerID, leaseID, revision, []byte(`{"stats":{"images_generated":3}}`)); err != nil {
		t.Fatalf("Complete returned error: %v (signature drift: orphaning the RUNNING job)", err)
	}

	// Post-assert: status=SUCCEEDED, completed_at NOT NULL, progress=100.
	post := readJob(t, db, jobID)
	if post.Status != "SUCCEEDED" {
		t.Fatalf("post-Complete: expected status=SUCCEEDED, got %q (ORPHAN)", post.Status)
	}
	if post.Revision != revision+1 {
		t.Errorf("post-Complete: expected revision=%d (one bump from fenced state transition), got %d", revision+1, post.Revision)
	}
	if !post.CompletedAt.Valid {
		t.Errorf("post-Complete: expected completed_at NOT NULL, got NULL")
	}
	if post.Progress != 100 {
		t.Errorf("post-Complete: expected progress=100, got %d", post.Progress)
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
	pre := readJob(t, db, jobID)
	if pre.Status != "RUNNING" {
		t.Fatalf("pre-condition: expected status=RUNNING, got %q", pre.Status)
	}

	// Exercise: Fail with the matching (workerID, leaseID, expectedRevision).
	const failMsg = "test-fail: simulated downstream error"
	if err := store.Fail(ctx, jobID, workerID, leaseID, revision, failMsg); err != nil {
		t.Fatalf("Fail returned error: %v (signature drift: orphaning the RUNNING job)", err)
	}

	// Post-assert: status=FAILED, completed_at NOT NULL, error set.
	post := readJob(t, db, jobID)
	if post.Status != "FAILED" {
		t.Fatalf("post-Fail: expected status=FAILED, got %q (ORPHAN)", post.Status)
	}
	if post.Revision != revision+1 {
		t.Errorf("post-Fail: expected revision=%d (one bump from fenced state transition), got %d", revision+1, post.Revision)
	}
	if !post.CompletedAt.Valid {
		t.Errorf("post-Fail: expected completed_at NOT NULL, got NULL")
	}
	if post.Error != failMsg {
		t.Errorf("post-Fail: expected error=%q, got %q", failMsg, post.Error)
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
	pre := readJob(t, db, jobID)
	if pre.Status != "RUNNING" {
		t.Fatalf("pre-condition: expected status=RUNNING, got %q", pre.Status)
	}

	// Exercise: Cancel (Cancel does not take a worker/lease tuple
	// because it is an operator action with no lease requirement).
	if err := store.Cancel(ctx, jobID); err != nil {
		t.Fatalf("Cancel returned error: %v (signature drift: orphaning the RUNNING job)", err)
	}

	// Post-assert: status=CANCELLED, cancelled_at NOT NULL.
	post := readJob(t, db, jobID)
	if post.Status != "CANCELLED" {
		t.Fatalf("post-Cancel: expected status=CANCELLED, got %q (ORPHAN)", post.Status)
	}
	if post.Revision != revision+1 {
		t.Errorf("post-Cancel: expected revision=%d (one bump from fenced state transition), got %d", revision+1, post.Revision)
	}
	if !post.CancelledAt.Valid {
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
	pre := readJob(t, db, jobID)
	if pre.Status != "RUNNING" {
		t.Fatalf("pre-condition: expected status=RUNNING, got %q", pre.Status)
	}
	if pre.Revision != revision {
		t.Fatalf("pre-condition: expected revision=%d, got %d", revision, pre.Revision)
	}
	if !pre.LeaseExpiry.Valid {
		t.Fatalf("pre-condition: expected lease_expiry NOT NULL, got NULL")
	}

	// Exercise: RenewLease with the matching (id, workerID, newLeaseTTL).
	// FASE 4(b) (July 2026): the kernel signature is now
	// RenewLease(ctx, id, workerID, leaseTTL) (job.RenewLeaseResult, error)
	// — the typed result surfaces LeaseState (Continue | CancelRequested | LeaseLost)
	// atomically with the lease extension in a single SQL UPDATE. The implementation
	// must NOT bump the revision column.
	res, err := store.RenewLease(ctx, jobID, workerID, newLeaseTTL)
	if err != nil {
		t.Fatalf("RenewLease returned error: %v", err)
	}
	if res.State != job.LeaseStateContinue {
		t.Fatalf("RenewLease on a healthy RUNNING job: expected LeaseStateContinue, got %q (typed LeaseState drift regression)", res.State)
	}

	// Post-assert: revision is UNCHANGED (the canonical signature contract).
	// This is the load-bearing assertion: if RenewLease silently bumps
	// the revision, the worker's expectedRevision is invalidated and
	// subsequent Complete / Fail calls will hit CAS mismatch.
	post := readJob(t, db, jobID)
	if post.Status != "RUNNING" {
		t.Errorf("post-RenewLease: expected status=RUNNING, got %q", post.Status)
	}
	if post.Revision != pre.Revision {
		t.Errorf("post-RenewLease: revision DRIFT — expected unchanged revision=%d, got %d (signature-drift regression: RenewLease silently bumps revision, invalidating the worker's expectedRevision for subsequent Complete / Fail CAS checks -> ORPHAN)",
			pre.Revision, post.Revision)
	}

	// Post-assert: lease_expiry was extended (the positive effect).
	if !post.LeaseExpiry.Valid {
		t.Errorf("post-RenewLease: expected lease_expiry NOT NULL, got NULL")
	}
	if post.LeaseExpiry.String == pre.LeaseExpiry.String {
		t.Errorf("post-RenewLease: expected lease_expiry to be EXTENDED, got unchanged value %q", post.LeaseExpiry.String)
	}
}

// TestBroker_RoundTrip_RenewLease_CancelledAtSet_ReturnsCancelRequested
// pins the FASE 4(b) typed LeaseState contract for the cancel path. The
// entire point of Cut A is to surface the cancel flag atomically through
// the same SQL UPDATE that extends the lease — eliminating the pre-Fase-4
// 2-second IsCancelled-poll goroutine. This test seeds a RUNNING job with
// cancelled_at ALREADY SET (the operator-issued cancel land state) and
// asserts that RenewLease returns (result{State: LeaseStateCancelRequested},
// nil) — the worker must observe the cancel signal via the typed result
// and abort the in-flight job via jobCancel (ctx.Err()).
//
// godlike/07 no-fake-availability: real SQL round-trip. A buggy CASE
// expression (e.g. reading cancelled_at from the wrong table or
// returning the wrong literal) would silently regress to
// LeaseStateContinue and the worker would NEVER observe the cancel —
// the entire FASE 4(b) contract would be silently broken. This test
// is the load-bearing regression guard.
func TestBroker_RoundTrip_RenewLease_CancelledAtSet_ReturnsCancelRequested(t *testing.T) {
	db := newBrokerTestDB(t)
	ctx := context.Background()
	store := NewSQLiteStore(db, zap.NewNop())

	const (
		workerID = "worker-A"
		leaseID  = "lease-X"
		revision = 5
	)
	jobID := seedRunningJob(t, db, workerID, leaseID, revision, 30*time.Second)

	// Pre-assert: status=RUNNING, cancelled_at NULL.
	pre := readJob(t, db, jobID)
	if pre.Status != "RUNNING" {
		t.Fatalf("pre-condition: expected status=RUNNING, got %q", pre.Status)
	}
	if pre.CancelledAt.Valid {
		t.Fatalf("pre-condition: expected cancelled_at NULL, got %q", pre.CancelledAt.String)
	}

	// Operator-side: mark the job as cancelled (the canonical
	// pre-cancel state). RenewLease must observe this via the
	// RETURNING CASE expression and report LeaseStateCancelRequested.
	cancelTime := time.Now().UTC()
	if _, err := db.ExecContext(ctx,
		`UPDATE jobs SET cancelled_at = ?, updated_at = ? WHERE id = ?`,
		timeutil.FormatRFC3339(cancelTime), timeutil.FormatRFC3339(time.Now()), jobID,
	); err != nil {
		t.Fatalf("seed cancelled_at: %v", err)
	}

	// Exercise: RenewLease on a job with cancelled_at SET.
	res, err := store.RenewLease(ctx, jobID, workerID, 60*time.Second)
	if err != nil {
		t.Fatalf("RenewLease returned error: %v (typed LeaseState drift regression — should be nil error on the cancel path, only the typed result signals cancel)", err)
	}
	if res.State != job.LeaseStateCancelRequested {
		t.Fatalf("RenewLease on a job with cancelled_at SET: expected LeaseStateCancelRequested, got %q (CASE expression drift regression — the pre-Fase-4 polling goroutine was supposed to be replaced by this atomic SQL path; if the CASE silently returns Continue the cancel signal is LOST)", res.State)
	}

	// Post-assert: status is still RUNNING (Cancel is an
	// operator action with a separate state transition; RenewLease
	// does NOT mutate status — it only surfaces the cancel flag).
	post := readJob(t, db, jobID)
	if post.Status != "RUNNING" {
		t.Errorf("post-RenewLease(cancelled): expected status=RUNNING (RenewLease does not transition status), got %q", post.Status)
	}
}

// TestBroker_RoundTrip_RenewLease_NoMatchingRow_ReturnsLeaseLost pins
// the FASE 4(b) typed LeaseState contract for the lease-lost path.
// When the WHERE clause filters out the row (wrong id, wrong worker_id,
// non-RUNNING status, expired lease), the UPDATE matches 0 rows and
// RenewLease must return (result{State: LeaseStateLeaseLost},
// sqljobs.ErrLeaseLost) — the worker must observe the lease loss via
// BOTH the typed result AND the errors.Is-compatible error sentinel
// and treat the in-flight work as orphaned.
//
// godlike/07 no-fake-availability: real SQL round-trip on a
// non-existent jobID. A buggy WHERE clause (e.g. missing the
// worker_id filter) would match the wrong row and silently
// return LeaseStateContinue on a lease that was already
// reaped by another worker — the canonical double-claim
// scenario. This test is the load-bearing lease-stability
// regression guard.
func TestBroker_RoundTrip_RenewLease_NoMatchingRow_ReturnsLeaseLost(t *testing.T) {
	db := newBrokerTestDB(t)
	ctx := context.Background()
	store := NewSQLiteStore(db, zap.NewNop())

	// Exercise: RenewLease on a non-existent jobID. The WHERE
	// clause matches 0 rows, so the SQL UPDATE returns no rows
	// and Go surfaces sql.ErrNoRows on Scan.
	const (
		nonExistentJobID = "job-does-not-exist"
		workerID         = "worker-A"
	)
	res, err := store.RenewLease(ctx, nonExistentJobID, workerID, 60*time.Second)
	if err == nil {
		t.Fatalf("RenewLease on a non-existent jobID: expected error, got nil (typed LeaseState drift regression — the LeaseLost path MUST return the sqljobs.ErrLeaseLost sentinel)")
	}
	if !errors.Is(err, ErrLeaseLost) {
		t.Errorf("RenewLease on a non-existent jobID: expected errors.Is(err, ErrLeaseLost) for downstream typed-sentinel matching, got %v", err)
	}
	if res.State != job.LeaseStateLeaseLost {
		t.Errorf("RenewLease on a non-existent jobID: expected res.State=LeaseStateLeaseLost, got %q (typed LeaseState drift regression — the worker MUST inspect the typed result.State to surface lease loss, the error sentinel alone is insufficient)", res.State)
	}

	// Exercise: RenewLease on a RUNNING job with the WRONG workerID.
	// The WHERE clause (id=? AND status IN ('RUNNING','FINALIZING')
	// AND worker_id=?) must filter out the row because the workerID
	// does not match. This pins the worker_id filter — without it,
	// a worker could silently extend a lease that was already
	// reaped by another worker (the canonical double-claim scenario).
	jobID := seedRunningJob(t, db, "owner-worker", "lease-X", 5, 30*time.Second)
	res2, err2 := store.RenewLease(ctx, jobID, "different-worker", 60*time.Second)
	if err2 == nil {
		t.Fatalf("RenewLease on a RUNNING job with wrong workerID: expected error, got nil (worker_id filter regression — the canonical double-claim guard)")
	}
	if !errors.Is(err2, ErrLeaseLost) {
		t.Errorf("RenewLease on a RUNNING job with wrong workerID: expected errors.Is(err, ErrLeaseLost), got %v", err2)
	}
	if res2.State != job.LeaseStateLeaseLost {
		t.Errorf("RenewLease on a RUNNING job with wrong workerID: expected res.State=LeaseStateLeaseLost, got %q (worker_id filter regression — typed result must report LeaseLost so the worker can abort)", res2.State)
	}
}
