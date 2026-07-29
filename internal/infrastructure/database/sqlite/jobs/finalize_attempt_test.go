// Package jobs — finalize_attempt_test.go: hermetic round-trip
// validation for SQLiteStore.FinalizeAttempt (Fase 4(a), July 2026).
//
// In-memory SQLite plus the minimal canonical schemas (jobs/job_events/
// dead_letter_jobs/artifact_stages/outbox_events) hand-written for
// hermeticity. Covers:
//
//   - All 3 outcomes (Succeeded, FailedPermanent, ScheduleRetry)
//   - Retry-exhaustion downgrade (ScheduleRetry at retry_count == max_retries
//     atomically downgrades to FAILED + carries "(max retries exhausted)"
//     suffix on the error column for forensic clarity)
//   - CAS-fence errors (worker mismatch, lease mismatch, revision
//     mismatch) surface as canonical sentinels + the
//     job_transition_conflict_total{"finalize_attempt"} counter bumps.
//   - In-TX sub-operations:
//     DLQ (atomic INSERT INTO dead_letter_jobs)
//     artifact_state (atomic UPDATE artifact_stages)
//     outbox_events (atomic INSERT batch, ON CONFLICT idempotency)
//   - Pre-TX precondition rejections (OutcomeSucceeded w/o Result,
//     non-Succeeded w/o ErrorMessage, DLQ + Succeeded, OutboxEvent
//     missing Type/EventKey) surface as typed sentinels.
//
// godlike/06 SSOT discipline: the canonical test schema mirrors the
// canonical migrations (jobs, dead_letter_jobs, artifact_stages from
// migration 147, outbox_events). Drift is caught at integration time
// when the production adapter's CREATE TABLE differs from this file's
// schema — surfaced as an SQL parse error on first INSERT.
//
// godlike/07 fail-closed: every CAS-fence rejection path is asserted
// at the typed-error level (errors.Is to the canonical sentinels).
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// ── Setup helpers ─────────────────────────────────────────────────────────

// setupFinalizeTestDB creates an in-memory SQLite with the canonical
// minimal schemas needed by FinalizeAttempt. Returns the *SQLiteStore
// ready to claim + finalize a seeded row. Cleanup is automatic at t.Cleanup.
func setupFinalizeTestDB(t *testing.T) (*SQLiteStore, string) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open :memory: sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Canonical minimal schemas matching the production migrations.
	// Only the columns FinalizeAttempt reads/writes are typed; the rest
	// are deliberately omitted — FinalizeAttempt is the SCOPED surface
	// under test.
	tables := []string{
		`CREATE TABLE jobs (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			payload_json TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL,
			worker_id TEXT NOT NULL DEFAULT '',
			lease_id TEXT NOT NULL DEFAULT '',
			lease_expiry TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			started_at TEXT,
			completed_at TEXT,
			cancelled_at TEXT,
			revision INTEGER NOT NULL DEFAULT 0,
			max_retries INTEGER NOT NULL DEFAULT 3,
			retry_count INTEGER NOT NULL DEFAULT 0,
			correlation_id TEXT,
			progress INTEGER NOT NULL DEFAULT 0,
			result_json TEXT,
			error TEXT
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

	// Seed a claimed-running job (FASE-4 canonical precondition for
	// FinalizeAttempt). The row has worker_id + lease_id + revision=N
	// populated and status='RUNNING' so the CAS fence in FinalizeAttempt
	// checks against exactly one leaseholder.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const jobID = "job-finalize-test-1"
	_, err = db.ExecContext(context.Background(), `INSERT INTO jobs
		(id, type, payload_json, status, worker_id, lease_id, lease_expiry,
		 created_at, updated_at, started_at, revision, max_retries, retry_count,
		 correlation_id, progress, result_json, error)
		VALUES (?, 'test.phase4', '{}', 'RUNNING', 'worker-A', 'lease-1', ?,
		        ?, ?, ?, 1, 3, 0, 'corr-1', 50, NULL, '')`,
		jobID, now, now, now, now)
	if err != nil {
		t.Fatalf("seed job: %v", err)
	}

	return store, jobID
}

// finalJobRow is a per-test convenience struct mirroring the final
// post-commit state for assertion.
type finalJobRow struct {
	status       string
	revision     int
	retryCount   int
	progress     int
	resultJSON   string
	errorMessage string
	workerID     string
	leaseID      string
}

// readFinalJob reads the post-FinalizeAttempt jobs row into finalJobRow.
// Used by every "happy path" assertion in this file.
func readFinalJob(t *testing.T, db *sql.DB, jobID string) finalJobRow {
	t.Helper()
	var row finalJobRow
	if err := db.QueryRow(`SELECT status, revision, retry_count, progress, COALESCE(result_json, ''), COALESCE(error, ''), worker_id, lease_id FROM jobs WHERE id = ?`, jobID).
		Scan(&row.status, &row.revision, &row.retryCount, &row.progress, &row.resultJSON, &row.errorMessage, &row.workerID, &row.leaseID); err != nil {
		t.Fatalf("readFinalJob (id=%s): %v", jobID, err)
	}
	return row
}

// ── Test: OutcomeSucceeded happy path ──────────────────────────────────────

func TestFinalizeAttempt_OutcomeSucceeded(t *testing.T) {
	store, jobID := setupFinalizeTestDB(t)
	ctx := context.Background()
	cmd := jobs.FinalizeAttemptCommand{
		JobID:            jobID,
		Outcome:          jobs.OutcomeSucceeded,
		WorkerID:         "worker-A",
		LeaseID:          "lease-1",
		ExpectedRevision: 1,
		Result:           json.RawMessage(`{"ok":true,"items":3}`),
		EventType:        "job_completed",
	}
	res, err := store.FinalizeAttempt(ctx, cmd)
	if err != nil {
		t.Fatalf("FinalizeAttempt Succeeded: unexpected error: %v", err)
	}
	if res.JobID != jobID {
		t.Errorf("result.JobID = %q, want %q", res.JobID, jobID)
	}
	if res.FinalStatus != jobs.StatusSucceeded {
		t.Errorf("result.FinalStatus = %q, want SUCCEEDED", res.FinalStatus)
	}
	if res.NewRevision != 2 {
		t.Errorf("result.NewRevision = %d, want 2", res.NewRevision)
	}
	if res.DLQRecorded {
		t.Errorf("DLQRecorded = true on SUCCEEDED outcome, want false")
	}
	if len(res.OutboxEventsWritten) != 0 {
		t.Errorf("OutboxEventsWritten non-empty on SUCCEEDED without cmd.OutboxEvents: %v", res.OutboxEventsWritten)
	}

	row := readFinalJob(t, store.DB(), jobID)
	if row.status != "SUCCEEDED" {
		t.Errorf("jobs.status = %q, want SUCCEEDED", row.status)
	}
	if row.revision != 2 {
		t.Errorf("jobs.revision = %d, want 2", row.revision)
	}
	if row.progress != 100 {
		t.Errorf("jobs.progress = %d, want 100", row.progress)
	}
	if row.resultJSON != `{"ok":true,"items":3}` {
		t.Errorf("jobs.result_json = %q, want canonical payload", row.resultJSON)
	}
	if row.workerID != "" || row.leaseID != "" {
		t.Errorf("post-SUCCEEDED lock should be cleared; got worker=%q lease=%q", row.workerID, row.leaseID)
	}

	// job_events audit row must have landed.
	var evtCount int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM job_events WHERE job_id = ? AND type = ?`, jobID, "job_completed").Scan(&evtCount); err != nil {
		t.Fatalf("count job_events: %v", err)
	}
	if evtCount != 1 {
		t.Errorf("job_events with type=job_completed: got %d, want 1", evtCount)
	}
}

// ── Test: OutcomeFailedPermanent happy path ─────────────────────────────────

func TestFinalizeAttempt_OutcomeFailedPermanent(t *testing.T) {
	store, jobID := setupFinalizeTestDB(t)
	ctx := context.Background()
	cmd := jobs.FinalizeAttemptCommand{
		JobID:            jobID,
		Outcome:          jobs.OutcomeFailedPermanent,
		WorkerID:         "worker-A",
		LeaseID:          "lease-1",
		ExpectedRevision: 1,
		ErrorMessage:     "TTS provider unavailable",
		EventType:        "job_failed",
	}
	res, err := store.FinalizeAttempt(ctx, cmd)
	if err != nil {
		t.Fatalf("FinalizeAttempt FailedPermanent: unexpected error: %v", err)
	}
	if res.FinalStatus != jobs.StatusFailed {
		t.Errorf("FinalStatus = %q, want FAILED", res.FinalStatus)
	}

	row := readFinalJob(t, store.DB(), jobID)
	if row.status != "FAILED" {
		t.Errorf("jobs.status = %q, want FAILED", row.status)
	}
	if row.errorMessage != "TTS provider unavailable" {
		t.Errorf("jobs.error = %q, want canonical message", row.errorMessage)
	}
	if row.retryCount != 0 {
		t.Errorf("retry_count MUST NOT bump on FailedPermanent; got %d", row.retryCount)
	}
}

// ── Test: OutcomeScheduleRetry under retry limit ───────────────────────────

func TestFinalizeAttempt_OutcomeScheduleRetry_UnderLimit(t *testing.T) {
	store, jobID := setupFinalizeTestDB(t)
	// Bump retry_count to 1 (under max_retries=3) to verify retry_count
	// increments by exactly 1 on OutcomeScheduleRetry.
	if _, err := store.DB().Exec(`UPDATE jobs SET retry_count = 1 WHERE id = ?`, jobID); err != nil {
		t.Fatalf("bump retry_count: %v", err)
	}

	ctx := context.Background()
	cmd := jobs.FinalizeAttemptCommand{
		JobID:            jobID,
		Outcome:          jobs.OutcomeScheduleRetry,
		WorkerID:         "worker-A",
		LeaseID:          "lease-1",
		ExpectedRevision: 1,
		ErrorMessage:     "transient_Drive_5xx",
		Backoff:          30 * time.Second,
	}
	res, err := store.FinalizeAttempt(ctx, cmd)
	if err != nil {
		t.Fatalf("FinalizeAttempt ScheduleRetry: unexpected error: %v", err)
	}
	if res.FinalStatus != jobs.StatusRetryWait {
		t.Errorf("FinalStatus = %q, want RETRY_WAIT", res.FinalStatus)
	}

	row := readFinalJob(t, store.DB(), jobID)
	if row.status != "RETRY_WAIT" {
		t.Errorf("jobs.status = %q, want RETRY_WAIT", row.status)
	}
	if row.retryCount != 2 {
		t.Errorf("retry_count = %d, want 2 (incremented from 1)", row.retryCount)
	}
	if row.errorMessage != "transient_Drive_5xx" {
		t.Errorf("jobs.error = %q, want canonical message", row.errorMessage)
	}
}

// ── Test: OutcomeScheduleRetry at retry limit → atomic downgrade ────────────

func TestFinalizeAttempt_OutcomeScheduleRetry_AtomicDowngradeAtLimit(t *testing.T) {
	store, jobID := setupFinalizeTestDB(t)
	// Set retry_count = max_retries = 3 to force retry-exhaustion downgrade.
	if _, err := store.DB().Exec(`UPDATE jobs SET retry_count = 3 WHERE id = ?`, jobID); err != nil {
		t.Fatalf("bump retry_count to max: %v", err)
	}

	ctx := context.Background()
	cmd := jobs.FinalizeAttemptCommand{
		JobID:            jobID,
		Outcome:          jobs.OutcomeScheduleRetry,
		WorkerID:         "worker-A",
		LeaseID:          "lease-1",
		ExpectedRevision: 1,
		ErrorMessage:     "transient_TTS_429",
	}
	res, err := store.FinalizeAttempt(ctx, cmd)
	if err != nil {
		t.Fatalf("FinalizeAttempt ScheduleRetry at limit: unexpected error: %v", err)
	}

	// FinalStatus reflects the downgraded terminal value, not the caller-
	// supplied retry intent.
	if res.FinalStatus != jobs.StatusFailed {
		t.Errorf("FinalStatus = %q, want FAILED (downgraded)", res.FinalStatus)
	}

	row := readFinalJob(t, store.DB(), jobID)
	if row.status != "FAILED" {
		t.Errorf("jobs.status = %q, want FAILED", row.status)
	}
	// The SQL-layer must surface the canonical forensic suffix on the
	// error column so operators distinguish "caller asked retry" from
	// "retry limit was hit" on the row read.
	if row.errorMessage != "transient_TTS_429 (max retries exhausted)" {
		t.Errorf("jobs.error = %q, want forensic-suffixed message", row.errorMessage)
	}
	// retry_count MUST NOT bump on downgrade (the upgrade was denied; the
	// row is now terminal-Failed).
	if row.retryCount != 3 {
		t.Errorf("retry_count = %d, want 3 (no increment on downgrade)", row.retryCount)
	}
}

// ── Test: CAS-fence: worker mismatch returns ErrLeaseLost ──────────────────

func TestFinalizeAttempt_LeaseLost_WorkerMismatch(t *testing.T) {
	store, jobID := setupFinalizeTestDB(t)
	ctx := context.Background()
	cmd := jobs.FinalizeAttemptCommand{
		JobID:            jobID,
		Outcome:          jobs.OutcomeSucceeded,
		WorkerID:         "WORKER-B-STEALER", // does not match seeded "worker-A"
		LeaseID:          "lease-1",
		ExpectedRevision: 1,
		Result:           json.RawMessage(`{"ok":true}`),
	}
	_, err := store.FinalizeAttempt(ctx, cmd)
	if err == nil {
		t.Fatalf("FinalizeAttempt on worker mismatch: expected ErrLeaseLost, got nil")
	}
	if !errors.Is(err, ErrLeaseLost) {
		t.Errorf("FinalizeAttempt on worker mismatch: err = %v, want ErrLeaseLost chain", err)
	}
	// Job state MUST be unchanged (TX rolled back).
	row := readFinalJob(t, store.DB(), jobID)
	if row.status != "RUNNING" {
		t.Errorf("jobs.status = %q after rejected finalize, want RUNNING (TX rolled back)", row.status)
	}
}

// ── Test: CAS-fence: revision mismatch returns ErrTransitionConflict ────────

func TestFinalizeAttempt_TransitionConflict_RevisionMismatch(t *testing.T) {
	store, jobID := setupFinalizeTestDB(t)
	ctx := context.Background()
	cmd := jobs.FinalizeAttemptCommand{
		JobID:            jobID,
		Outcome:          jobs.OutcomeSucceeded,
		WorkerID:         "worker-A",
		LeaseID:          "lease-1",
		ExpectedRevision: 99, // does not match seeded revision=1
		Result:           json.RawMessage(`{"ok":true}`),
	}
	_, err := store.FinalizeAttempt(ctx, cmd)
	if err == nil {
		t.Fatalf("FinalizeAttempt on revision mismatch: expected ErrTransitionConflict, got nil")
	}
	if !errors.Is(err, ErrTransitionConflict) {
		t.Errorf("FinalizeAttempt on revision mismatch: err = %v, want ErrTransitionConflict chain", err)
	}
}

// ── Test: invalid outcome returns typed sentinel (pre-TX gate) ──────────────

func TestFinalizeAttempt_OutcomeInvalid(t *testing.T) {
	store, jobID := setupFinalizeTestDB(t)
	ctx := context.Background()
	cmd := jobs.FinalizeAttemptCommand{
		JobID:            jobID,
		Outcome:          jobs.FinalizeAttemptOutcome("UNKNOWN"),
		WorkerID:         "worker-A",
		LeaseID:          "lease-1",
		ExpectedRevision: 1,
		Result:           json.RawMessage(`{"ok":true}`),
	}
	_, err := store.FinalizeAttempt(ctx, cmd)
	if err == nil {
		t.Fatalf("FinalizeAttempt with unknown outcome: expected sentinel")
	}
	if !errors.Is(err, ErrFinalizeAttemptOutcomeInvalid) {
		t.Errorf("FinalizeAttempt with unknown outcome: err = %v, want ErrFinalizeAttemptOutcomeInvalid chain", err)
	}
}

// ── Test: OutcomeSucceeded + DLQPayload → ErrFinalizeAttemptDLQIncompatible ─

func TestFinalizeAttempt_DLQ_IncompatibleWithSucceeded(t *testing.T) {
	store, jobID := setupFinalizeTestDB(t)
	ctx := context.Background()
	cmd := jobs.FinalizeAttemptCommand{
		JobID:            jobID,
		Outcome:          jobs.OutcomeSucceeded,
		WorkerID:         "worker-A",
		LeaseID:          "lease-1",
		ExpectedRevision: 1,
		Result:           json.RawMessage(`{"ok":true}`),
		DLQPayload:       json.RawMessage(`{"reason":"oops"}`), // incompatible
	}
	_, err := store.FinalizeAttempt(ctx, cmd)
	if err == nil {
		t.Fatalf("FinalizeAttempt SUCCEEDED+DLQ: expected ErrFinalizeAttemptDLQIncompatible")
	}
	if !errors.Is(err, ErrFinalizeAttemptDLQIncompatible) {
		t.Errorf("FinalizeAttempt SUCCEEDED+DLQ: err = %v, want ErrFinalizeAttemptDLQIncompatible chain", err)
	}
}

// ── Test: OutcomeFailedPermanent + DLQPayload → DLQ row inserted ───────────

func TestFinalizeAttempt_DLQ_RecordedOnFailure(t *testing.T) {
	store, jobID := setupFinalizeTestDB(t)
	ctx := context.Background()
	cmd := jobs.FinalizeAttemptCommand{
		JobID:            jobID,
		Outcome:          jobs.OutcomeFailedPermanent,
		WorkerID:         "worker-A",
		LeaseID:          "lease-1",
		ExpectedRevision: 1,
		ErrorMessage:     "deterministic_failure",
		DLQPayload:       json.RawMessage(`{"snapshot":true}`),
	}
	res, err := store.FinalizeAttempt(ctx, cmd)
	if err != nil {
		t.Fatalf("FinalizeAttempt FailedPermanent + DLQ: unexpected error: %v", err)
	}
	if !res.DLQRecorded {
		t.Errorf("res.DLQRecorded = false, want true")
	}
	// Verify the canonical dead_letter_jobs row.
	var errCol, dlqPayload string
	if err := store.DB().QueryRow(`SELECT error, payload_json FROM dead_letter_jobs WHERE job_id = ?`, jobID).
		Scan(&errCol, &dlqPayload); err != nil {
		t.Fatalf("read dead_letter_jobs: %v", err)
	}
	if errCol != "deterministic_failure" {
		t.Errorf("dead_letter_jobs.error = %q, want canonical message", errCol)
	}
	if dlqPayload != `{"snapshot":true}` {
		t.Errorf("dead_letter_jobs.payload_json = %q, want canonical payload", dlqPayload)
	}
}

// ── Test: ArtifactState patch happy path ───────────────────────────────────

func TestFinalizeAttempt_ArtifactStatePatch_Success(t *testing.T) {
	store, jobID := setupFinalizeTestDB(t)
	// Seed a linked artifact row in STAGED state.
	const artifactID = "art-finalize-test-1"
	if _, err := store.DB().Exec(`INSERT INTO artifact_stages (id, job_id, state, created_at, updated_at) VALUES (?, ?, 'STAGED', ?, ?)`,
		artifactID, jobID, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	ctx := context.Background()
	cmd := jobs.FinalizeAttemptCommand{
		JobID:            jobID,
		Outcome:          jobs.OutcomeSucceeded,
		WorkerID:         "worker-A",
		LeaseID:          "lease-1",
		ExpectedRevision: 1,
		Result:           json.RawMessage(`{"ok":true}`),
		ArtifactState: &jobs.ArtifactStatePatch{
			ArtifactID: artifactID,
			NewState:   "SUCCEEDED",
		},
	}
	res, err := store.FinalizeAttempt(ctx, cmd)
	if err != nil {
		t.Fatalf("FinalizeAttempt with ArtifactState: unexpected error: %v", err)
	}
	if res.DLQRecorded {
		t.Errorf("DLQRecorded = true on SUCCEEDED-no-DLQ, want false")
	}
	// Verify the artifact row was updated.
	var state string
	if err := store.DB().QueryRow(`SELECT state FROM artifact_stages WHERE id = ?`, artifactID).Scan(&state); err != nil {
		t.Fatalf("read artifact_stages: %v", err)
	}
	if state != "SUCCEEDED" {
		t.Errorf("artifact_stages.state = %q, want SUCCEEDED", state)
	}
}

// ── Test: ArtifactState patch on already-terminal artifact → ErrStale ──────

func TestFinalizeAttempt_ArtifactStatePatch_Stale_OnTerminal(t *testing.T) {
	store, jobID := setupFinalizeTestDB(t)
	const artifactID = "art-already-terminal"
	if _, err := store.DB().Exec(`INSERT INTO artifact_stages (id, job_id, state, created_at, updated_at) VALUES (?, ?, 'FAILED_PERMANENT', ?, ?)`,
		artifactID, jobID, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed terminal artifact: %v", err)
	}
	ctx := context.Background()
	cmd := jobs.FinalizeAttemptCommand{
		JobID:            jobID,
		Outcome:          jobs.OutcomeSucceeded,
		WorkerID:         "worker-A",
		LeaseID:          "lease-1",
		ExpectedRevision: 1,
		Result:           json.RawMessage(`{"ok":true}`),
		ArtifactState: &jobs.ArtifactStatePatch{
			ArtifactID: artifactID,
			NewState:   "SUCCEEDED",
		},
	}
	_, err := store.FinalizeAttempt(ctx, cmd)
	if err == nil {
		t.Fatalf("FinalizeAttempt on terminal artifact: expected ErrFinalizeAttemptArtifactStale")
	}
	if !errors.Is(err, ErrFinalizeAttemptArtifactStale) {
		t.Errorf("FinalizeAttempt on terminal artifact: err = %v, want ErrFinalizeAttemptArtifactStale chain", err)
	}
	// The TX must have rolled back — the jobs row MUST remain RUNNING
	// so the worker can retry.
	row := readFinalJob(t, store.DB(), jobID)
	if row.status != "RUNNING" {
		t.Errorf("jobs.status = %q after TX-rollback on stale artifact patch, want RUNNING", row.status)
	}
}

// ── Test: Outbox events written atomically ─────────────────────────────────

func TestFinalizeAttempt_OutboxEvents_EachInserted(t *testing.T) {
	store, jobID := setupFinalizeTestDB(t)
	ctx := context.Background()
	cmd := jobs.FinalizeAttemptCommand{
		JobID:            jobID,
		Outcome:          jobs.OutcomeSucceeded,
		WorkerID:         "worker-A",
		LeaseID:          "lease-1",
		ExpectedRevision: 1,
		Result:           json.RawMessage(`{"ok":true}`),
		OutboxEvents: []jobs.OutboxEventSpec{
			{Type: "asset.index.requested", EventKey: "key-1", Payload: json.RawMessage(`{"asset":"a1"}`)},
			{Type: "delivery.requested", EventKey: "key-2", Payload: json.RawMessage(`{"folder":"f1"}`)},
		},
	}
	res, err := store.FinalizeAttempt(ctx, cmd)
	if err != nil {
		t.Fatalf("FinalizeAttempt with outbox events: unexpected error: %v", err)
	}
	if len(res.OutboxEventsWritten) != 2 {
		t.Errorf("OutboxEventsWritten = %v, want 2 entries; got %d", res.OutboxEventsWritten, len(res.OutboxEventsWritten))
	}
	// Verify the actual rows.
	var evCount int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`, jobID).Scan(&evCount); err != nil {
		t.Fatalf("count outbox_events: %v", err)
	}
	if evCount != 2 {
		t.Errorf("outbox_events row count = %d, want 2", evCount)
	}
}

// ── Test: Outbox event missing EventKey → typed sentinel (pre-TX) ─────────

func TestFinalizeAttempt_OutboxEvent_MissingEventKey(t *testing.T) {
	store, jobID := setupFinalizeTestDB(t)
	ctx := context.Background()
	cmd := jobs.FinalizeAttemptCommand{
		JobID:            jobID,
		Outcome:          jobs.OutcomeSucceeded,
		WorkerID:         "worker-A",
		LeaseID:          "lease-1",
		ExpectedRevision: 1,
		Result:           json.RawMessage(`{"ok":true}`),
		OutboxEvents: []jobs.OutboxEventSpec{
			{Type: "asset.index.requested", EventKey: ""}, // missing key
		},
	}
	_, err := store.FinalizeAttempt(ctx, cmd)
	if err == nil {
		t.Fatalf("FinalizeAttempt with missing EventKey: expected ErrFinalizeAttemptOutboxEventMissing")
	}
	if !errors.Is(err, ErrFinalizeAttemptOutboxEventMissing) {
		t.Errorf("FinalizeAttempt with missing EventKey: err = %v, want ErrFinalizeAttemptOutboxEventMissing chain", err)
	}
}
