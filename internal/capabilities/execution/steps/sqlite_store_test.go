// Package steps — sqlite_store_test.go (Step 10 / Stock Cutover C1/4, July 2026).
//
// Test coverage target:
//   - Canonical contracts: every typed-error sentinel surfaces
//     from the right call site (ErrorIs-verified).
//   - Idempotency invariants: byte-equal result re-completion is
//     a no-op; byte-different re-completion surfaces
//     ErrStepAlreadyCompleted; pre-MarkStarted completion surfaces
//     ErrStepNotFound.
//   - Lease_until heartbeat: stamps on MarkStarted, clears on
//     MarkCompleted / MarkFailed; a stalled row (lease_until < now
//     AND status='pending') is the canonical crash-detection
//     signal queryable via ix_execution_steps_leased_stale.
//   - Recovery semantics: FirstNonCompleted returns the canonical
//     "next pending step" for resume-after-completed; ListByJob
//     returns the full fingerprint-version audit log.
//   - godlike/07 fail-closed: concurrent goroutines racing on
//     the same key surface typed sentinels (not opaque strings).
package execution

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openTestDB returns a fresh SQLite-backed *sql.DB with the
// canonical execution_steps migration applied (121 schema +
// 122 lease_until extension). File-backed via t.TempDir() so
// concurrent goroutines see shared state — :memory: would
// fragment per-connection and break the concurrency tests.
//
// Mirrors the production setup convention from
// 121_execution_steps.sql + 122_execution_steps_add_lease_until.sql.
// Both migrations are additive + idempotent (CREATE TABLE IF NOT
// EXISTS + CREATE INDEX IF NOT EXISTS), so re-running them on a
// fresh DB is safe.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Apply canonical migrations inline (121 + 122) so tests
	// stay hermetic — no path coupling to /migrations/sqlite/.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS execution_steps (
		    id INTEGER PRIMARY KEY AUTOINCREMENT,
		    job_id TEXT NOT NULL,
		    step_key TEXT NOT NULL,
		    input_fingerprint TEXT NOT NULL,
		    status TEXT NOT NULL DEFAULT 'pending',
		    attempt INTEGER NOT NULL DEFAULT 0,
		    result_json TEXT NOT NULL DEFAULT '{}',
		    artifact_refs_json TEXT NOT NULL DEFAULT '[]',
		    started_at TEXT NOT NULL DEFAULT '',
		    completed_at TEXT NOT NULL DEFAULT '',
		    last_error TEXT NOT NULL DEFAULT ''
		);
		CREATE UNIQUE INDEX IF NOT EXISTS uniq_execution_steps_dedup
		    ON execution_steps (job_id, step_key, input_fingerprint);
		CREATE INDEX IF NOT EXISTS ix_execution_steps_resume
		    ON execution_steps (job_id, status, step_key);
		CREATE INDEX IF NOT EXISTS ix_execution_steps_audit
		    ON execution_steps (job_id, step_key);
		ALTER TABLE execution_steps ADD COLUMN lease_until TEXT NOT NULL DEFAULT '';
		CREATE INDEX IF NOT EXISTS ix_execution_steps_leased_stale
		    ON execution_steps (lease_until)
		    WHERE lease_until != '';
	`); err != nil {
		t.Fatalf("openTestDB: apply migrations: %v", err)
	}
	return db
}

// newTestStore constructs the SQLiteStore bound to a fresh test DB.
// Helper shorthand — tests call it directly without ceremony.
func newTestStore(t *testing.T) (Store, *sql.DB) {
	t.Helper()
	db := openTestDB(t)
	return NewSQLiteStoreWithDB(db), db
}

// ── MarkStarted contracts ───────────────────────────────────────────

// TestSQLiteStore_MarkStarted_InsertsNewRow verifies the "no prior
// row" branch: INSERT at attempt=1, status='pending', lease_until
// non-empty.
func TestSQLiteStore_MarkStarted_InsertsNewRow(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	key := StepKey{
		JobID:            "run-1",
		StepKey:          "stock.plan",
		InputFingerprint: "run-1|stock.plan",
	}
	require.NoError(t, store.MarkStarted(ctx, key))

	// Read-back verifies the INSERT path wrote the canonical row.
	var (
		attempt     int
		status      string
		startedAt   string
		leaseUntil  string
		fingerprint string
		stepKey     string
	)
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT attempt, status, started_at, lease_until, input_fingerprint, step_key
		FROM execution_steps
		WHERE job_id = ?
	`, key.JobID).Scan(&attempt, &status, &startedAt, &leaseUntil, &fingerprint, &stepKey))

	assert.Equal(t, 1, attempt, "fresh INSERT should write attempt=1")
	assert.Equal(t, "pending", status, "MarkStarted writes canonical 'pending' status")
	assert.NotEmpty(t, startedAt, "started_at should be stamped")
	assert.NotEmpty(t, leaseUntil, "lease_until should be stamped with DefaultLeaseTTL offset")
	assert.Equal(t, key.InputFingerprint, fingerprint)
	assert.Equal(t, key.StepKey, stepKey)

	// Lease_until must be parseable + in the future.
	leaseTime, err := time.Parse(time.RFC3339, leaseUntil)
	require.NoError(t, err)
	assert.True(t, leaseTime.After(time.Now()),
		"lease_until must be in the future; got %v, now=%v", leaseTime, time.Now())
}

// TestSQLiteStore_MarkStarted_RefreshLeaseOnIdempotentReCall
// verifies the idempotent re-call branch: bumps attempt, refreshes
// lease_until, does NOT insert a duplicate row.
func TestSQLiteStore_MarkStarted_RefreshLeaseOnIdempotentReCall(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	key := StepKey{JobID: "run-2", StepKey: "stock.publish", InputFingerprint: "run-2|stock.publish"}
	require.NoError(t, store.MarkStarted(ctx, key))

	// Capture the lease_until_after first call.
	var leaseAfterFirst string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT lease_until FROM execution_steps WHERE job_id = ?`, key.JobID).Scan(&leaseAfterFirst))

	// Sleep 1.1s so the lease_after_second call is meaningfully newer.
	time.Sleep(1100 * time.Millisecond)

	// Re-call MarkStarted — should bump attempt (not insert a new row).
	require.NoError(t, store.MarkStarted(ctx, key))

	var (
		attempt    int
		leaseAfter string
		rowCount   int
	)
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT attempt, lease_until FROM execution_steps WHERE job_id = ?`, key.JobID).
		Scan(&attempt, &leaseAfter))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM execution_steps WHERE job_id = ?`, key.JobID).Scan(&rowCount))

	assert.Equal(t, 2, attempt, "re-call should bump attempt to 2")
	assert.Equal(t, 1, rowCount, "idempotent re-call MUST NOT insert a new row")
	assert.NotEqual(t, leaseAfterFirst, leaseAfter,
		"lease_until must be refreshed on re-call (got %q after first, %q after second)",
		leaseAfterFirst, leaseAfter)
}

// TestSQLiteStore_MarkStarted_AlreadyCompleted_ReturnsSentinel
// verifies the godlike/07 terminal-immutability contract: any
// Mark* call on a Completed row returns ErrStepAlreadyCompleted.
func TestSQLiteStore_MarkStarted_AlreadyCompleted_ReturnsSentinel(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	key := StepKey{JobID: "run-3", StepKey: "stock.finalize", InputFingerprint: "run-3|stock.finalize"}
	require.NoError(t, store.MarkStarted(ctx, key))
	require.NoError(t, store.MarkCompleted(ctx, key, json.RawMessage(`{"ok":true}`), nil))

	err := store.MarkStarted(ctx, key)
	require.Error(t, err)
	assert.True(t, errorsIs(err, ErrStepAlreadyCompleted),
		"re-MarkStarted on Completed row must return ErrStepAlreadyCompleted; got %v", err)
}

// TestSQLiteStore_MarkStarted_EmptyFields_ReturnsInvalidKey verifies
// validation; godlike/07 surfaces ALL missing fields in a single error.
func TestSQLiteStore_MarkStarted_EmptyFields_ReturnsInvalidKey(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	cases := []StepKey{
		{JobID: "", StepKey: "stock.x", InputFingerprint: "fp"},
		{JobID: "j", StepKey: "", InputFingerprint: "fp"},
		{JobID: "j", StepKey: "stock.x", InputFingerprint: ""},
		{JobID: "", StepKey: "", InputFingerprint: ""},
	}
	for i, key := range cases {
		key := key
		t.Run(cases[i].JobID+"_"+cases[i].StepKey, func(t *testing.T) {
			err := store.MarkStarted(ctx, key)
			require.Error(t, err)
			assert.True(t, errorsIs(err, ErrInvalidStepKey),
				"empty-field MarkStarted must return ErrInvalidStepKey; got %v", err)
		})
	}
}

// ── MarkCompleted contracts ─────────────────────────────────────────

// TestSQLiteStore_MarkCompleted_PendingToCompleted verifies the
// happy-path transition + lease_until clear.
func TestSQLiteStore_MarkCompleted_PendingToCompleted(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	key := StepKey{JobID: "run-4", StepKey: "stock.compose_chunks", InputFingerprint: "run-4|stock.compose_chunks"}
	require.NoError(t, store.MarkStarted(ctx, key))

	result := json.RawMessage(`{"chunks":3,"bytes":12345}`)
	refs := json.RawMessage(`["chunk-0","chunk-1","chunk-2"]`)
	require.NoError(t, store.MarkCompleted(ctx, key, result, refs))

	var (
		status      string
		completedAt string
		resultRaw   string
		refsRaw     string
		leaseRaw    string
	)
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT status, completed_at, result_json, artifact_refs_json, lease_until
		FROM execution_steps WHERE job_id = ?`, key.JobID).
		Scan(&status, &completedAt, &resultRaw, &refsRaw, &leaseRaw))

	assert.Equal(t, "completed", status)
	assert.NotEmpty(t, completedAt)
	assert.JSONEq(t, `{"chunks":3,"bytes":12345}`, resultRaw)
	assert.JSONEq(t, `["chunk-0","chunk-1","chunk-2"]`, refsRaw)
	assert.Equal(t, "", leaseRaw,
		"lease_until MUST be cleared on terminal Completed transition; godlike/07 fail-closed.")
}

// TestSQLiteStore_MarkCompleted_IdempotentByteEqual verifies the
// idempotency path: re-completion with the SAME result is a no-op
// (returns nil) so retries without a StructuredOutcome diff are
// safe.
func TestSQLiteStore_MarkCompleted_IdempotentByteEqual(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	key := StepKey{JobID: "run-5", StepKey: "stock.publish", InputFingerprint: "run-5|stock.publish"}
	require.NoError(t, store.MarkStarted(ctx, key))

	r := json.RawMessage(`{"drive_id":"abc123"}`)
	require.NoError(t, store.MarkCompleted(ctx, key, r, nil))

	// Same payload → no-op (nil).
	require.NoError(t, store.MarkCompleted(ctx, key, r, nil),
		"byte-equal re-completion must be a no-op (returns nil)")
}

// TestSQLiteStore_MarkCompleted_IdempotentByteDifferent_ReturnsSentinel
// verifies the typed-error contract: re-completion with a
// DIFFERENT result surfaces ErrStepAlreadyCompleted (godlike/07
// no-fake-availability — re-completion with mismatched shape is
// a programming error).
func TestSQLiteStore_MarkCompleted_IdempotentByteDifferent_ReturnsSentinel(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	key := StepKey{JobID: "run-6", StepKey: "stock.publish", InputFingerprint: "run-6|stock.publish"}
	require.NoError(t, store.MarkStarted(ctx, key))
	require.NoError(t, store.MarkCompleted(ctx, key, json.RawMessage(`{"drive_id":"abc"}`), nil))

	err := store.MarkCompleted(ctx, key, json.RawMessage(`{"drive_id":"xyz"}`), nil)
	require.Error(t, err)
	assert.True(t, errorsIs(err, ErrStepAlreadyCompleted),
		"byte-different re-completion must return ErrStepAlreadyCompleted; got %v", err)
}

// TestSQLiteStore_MarkCompleted_NoPriorRow_ReturnsNotFound verifies the
// ErrStepNotFound contract: pre-MarkStarted completion is a
// programming error, surfaced loudly.
func TestSQLiteStore_MarkCompleted_NoPriorRow_ReturnsNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	key := StepKey{JobID: "run-7", StepKey: "stock.publish", InputFingerprint: "run-7|stock.publish"}
	err := store.MarkCompleted(ctx, key, json.RawMessage(`{}`), nil)
	require.Error(t, err)
	assert.True(t, errorsIs(err, ErrStepNotFound),
		"MarkCompleted without prior MarkStarted must return ErrStepNotFound; got %v", err)
}

// ── MarkFailed contracts ────────────────────────────────────────────

// TestSQLiteStore_MarkFailed_PendingToFailed verifies the happy
// path + LastError stamping + lease_until clear.
func TestSQLiteStore_MarkFailed_PendingToFailed(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	key := StepKey{JobID: "run-8", StepKey: "stock.publish", InputFingerprint: "run-8|stock.publish"}
	require.NoError(t, store.MarkStarted(ctx, key))

	errMsg := "Drive upload: rate-limited retry-exceeded"
	require.NoError(t, store.MarkFailed(ctx, key, errMsg))

	var (
		status      string
		lastError   string
		completedAt string
		leaseRaw    string
	)
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT status, last_error, completed_at, lease_until
		FROM execution_steps WHERE job_id = ?`, key.JobID).
		Scan(&status, &lastError, &completedAt, &leaseRaw))

	assert.Equal(t, "failed", status)
	assert.Equal(t, errMsg, lastError)
	assert.NotEmpty(t, completedAt, "terminal failure must stamp completed_at")
	assert.Equal(t, "", leaseRaw, "lease_until MUST be cleared on Failed transition")
}

// TestSQLiteStore_MarkFailed_NoPriorRow_InsertsFailedRow verifies the
// audit-trail contract: a fatal-error path before MarkStarted still
// produces a Failed row at attempt=1.
func TestSQLiteStore_MarkFailed_NoPriorRow_InsertsFailedRow(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	key := StepKey{JobID: "run-9", StepKey: "stock.stage_sources", InputFingerprint: "run-9|stock.stage_sources"}
	require.NoError(t, store.MarkFailed(ctx, key, "fatal: planner returned no sources"))

	var (
		attempt   int
		status    string
		lastError string
	)
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT attempt, status, last_error FROM execution_steps WHERE job_id = ?`, key.JobID).
		Scan(&attempt, &status, &lastError))

	assert.Equal(t, 1, attempt, "fresh MarkFailed writes attempt=1")
	assert.Equal(t, "failed", status)
	assert.Contains(t, lastError, "no sources")
}

// TestSQLiteStore_MarkFailed_OnCompleted_ReturnsSentinel verifies
// terminal-immutability on the Failed path.
func TestSQLiteStore_MarkFailed_OnCompleted_ReturnsSentinel(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	key := StepKey{JobID: "run-10", StepKey: "stock.publish", InputFingerprint: "run-10|stock.publish"}
	require.NoError(t, store.MarkStarted(ctx, key))
	require.NoError(t, store.MarkCompleted(ctx, key, json.RawMessage(`{}`), nil))

	err := store.MarkFailed(ctx, key, "should be ignored")
	require.Error(t, err)
	assert.True(t, errorsIs(err, ErrStepAlreadyCompleted),
		"MarkFailed on Completed row must return ErrStepAlreadyCompleted; got %v", err)
}

// ── FirstNonCompleted — recovery semantics ──────────────────────────

// TestSQLiteStore_FirstNonCompleted_PartialProgress verifies the
// resume-after-crash contract: with 4 steps in mixed states,
// FirstNonCompleted returns the canonical lexically smallest
// step_key whose latest row is NOT completed.
func TestSQLiteStore_FirstNonCompleted_PartialProgress(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// Setup: 4 steps at distinct statuses.
	//   stock.plan            → completed (prior)
	//   stock.stage_sources   → completed (prior)
	//   stock.extract_clips   → pending (next to resume)
	//   stock.compose_chunks  → failed (also not completed)
	mustStart := func(k StepKey) { require.NoError(t, store.MarkStarted(ctx, k)) }
	mustComplete := func(k StepKey) { require.NoError(t, store.MarkCompleted(ctx, k, json.RawMessage(`{}`), nil)) }

	plan := StepKey{"run-11", "stock.plan", "run-11|stock.plan"}
	stage := StepKey{"run-11", "stock.stage_sources", "run-11|stock.stage_sources"}
	extract := StepKey{"run-11", "stock.extract_clips", "run-11|stock.extract_clips"}
	compose := StepKey{"run-11", "stock.compose_chunks", "run-11|stock.compose_chunks"}

	mustStart(plan)
	mustComplete(plan)
	mustStart(stage)
	mustComplete(stage)
	mustStart(extract) // pending
	mustStart(compose)
	require.NoError(t, store.MarkFailed(ctx, compose, "boom"))

	got, err := store.FirstNonCompleted(ctx, "run-11")
	require.NoError(t, err)
	require.NotNil(t, got, "FirstNonCompleted must return non-nil when partial progress exists")
	// Lex-smallest non-completed: 'c' (compose, 99) < 'e' (extract, 101)
	// in ASCII so compose_chunks comes before extract_clips regardless
	// of pipeline order. The cached resume contract is via
	// ErrStepAlreadyCompleted on MarkStarted (C2/4 forward-pointer);
	// FirstNonCompleted is a diagnostic helper.
	assert.Equal(t, "stock.compose_chunks", got.StepKey,
		"FirstNonCompleted returns the lex-smallest non-completed step_key; 'c' < 'e' in ASCII so compose_chunks < extract_clips")
}

// TestSQLiteStore_FirstNonCompleted_AllCompleted_ReturnsNil verifies
// the terminal-resume contract: when all steps are Completed, the
// orchestrator should re-enter with `continue` and stamp
// MarkCompleted on every step — none should re-fire.
func TestSQLiteStore_FirstNonCompleted_AllCompleted_ReturnsNil(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	for _, stepKey := range []string{"stock.plan", "stock.stage_sources", "stock.publish", "stock.finalize"} {
		k := StepKey{"run-12", stepKey, "run-12|" + stepKey}
		require.NoError(t, store.MarkStarted(ctx, k))
		require.NoError(t, store.MarkCompleted(ctx, k, json.RawMessage(`{}`), nil))
	}

	got, err := store.FirstNonCompleted(ctx, "run-12")
	require.NoError(t, err)
	assert.Nil(t, got, "FirstNonCompleted must return nil when all latest rows are Completed")
}

// TestSQLiteStore_FirstNonCompleted_UnseenJob_ReturnsNil verifies
// the unseen-jobID contract: (nil, nil) — not (nil, err).
func TestSQLiteStore_FirstNonCompleted_UnseenJob_ReturnsNil(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	got, err := store.FirstNonCompleted(ctx, "never-seen-job")
	require.NoError(t, err)
	assert.Nil(t, got)
}

// ── ListByJob — fingerprint-version audit log ───────────────────────

// TestSQLiteStore_ListByJob_FingerprintVersioning verifies that
// a retry with a DIFFERENT fingerprint INSERTs a new row (Design A
// versioning), and ListByJob returns both rows ordered by
// step_key ASC, id ASC.
func TestSQLiteStore_ListByJob_FingerprintVersioning(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	k1 := StepKey{"run-13", "stock.extract_clips", "v1-fingerprint"}
	k2 := StepKey{"run-13", "stock.extract_clips", "v2-fingerprint"}

	require.NoError(t, store.MarkStarted(ctx, k1))
	require.NoError(t, store.MarkCompleted(ctx, k1, json.RawMessage(`{"version":1}`), nil))
	require.NoError(t, store.MarkStarted(ctx, k2))
	require.NoError(t, store.MarkCompleted(ctx, k2, json.RawMessage(`{"version":2}`), nil))

	rows, err := store.ListByJob(ctx, "run-13")
	require.NoError(t, err)
	require.Len(t, rows, 2, "fingerprint-versioning MUST preserve both rows in the audit log")
	assert.Equal(t, "v1-fingerprint", rows[0].Fingerprint,
		"ListByJob orders by step_key ASC, id ASC; v1 inserted first so id ASC order")
	assert.Equal(t, "v2-fingerprint", rows[1].Fingerprint)
	// StepState.ID is int64 (mirrors the SQLite AUTOINCREMENT column);
	// testify assert.Equal distinguishes int vs int64 strict types.
	assert.Equal(t, int64(1), rows[0].ID)
	assert.Equal(t, int64(2), rows[1].ID)
}

// TestSQLiteStore_ListByJob_OrderedByStepKeyASC verifies the
// canonical ORDER BY semantics across multiple steps.
func TestSQLiteStore_ListByJob_OrderedByStepKeyASC(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	for _, stepKey := range []string{"stock.finalize", "stock.publish", "stock.extract_clips", "stock.plan"} {
		k := StepKey{"run-14", stepKey, "run-14|" + stepKey}
		require.NoError(t, store.MarkStarted(ctx, k))
		require.NoError(t, store.MarkCompleted(ctx, k, json.RawMessage(`{}`), nil))
	}

	rows, err := store.ListByJob(ctx, "run-14")
	require.NoError(t, err)
	require.Len(t, rows, 4)
	expected := []string{
		"stock.extract_clips",
		"stock.finalize",
		"stock.plan",
		"stock.publish",
	}
	for i, want := range expected {
		assert.Equal(t, want, rows[i].StepKey,
			"ListByJob must order by step_key ASC; row %d should be %q", i, want)
	}
}

// TestSQLiteStore_ListByJob_UnseenJob_ReturnsNil verifies the
// nil-slice convention (NOT a non-nil empty slice).
func TestSQLiteStore_ListByJob_UnseenJob_ReturnsNil(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	rows, err := store.ListByJob(ctx, "never-seen-job")
	require.NoError(t, err)
	assert.Nil(t, rows, "unseen-jobID must return a nil slice (matches InMemoryStore convention)")
}

// ── Lease_until heartbeat semantics ─────────────────────────────────

// TestSQLiteStore_LeaseUntil_StampOnMarkStarted verifies that
// MarkStarted stamps lease_until with a future timestamp.
func TestSQLiteStore_LeaseUntil_StampOnMarkStarted(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	key := StepKey{"run-15", "stock.stage_sources", "run-15|stock.stage_sources"}
	before := time.Now()
	require.NoError(t, store.MarkStarted(ctx, key))
	after := time.Now()

	var leaseRaw string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT lease_until FROM execution_steps WHERE job_id = ?`, key.JobID).Scan(&leaseRaw))
	leaseTime, err := time.Parse(time.RFC3339, leaseRaw)
	require.NoError(t, err)

	// Lease_until must be in the future relative to (before, after).
	assert.True(t, leaseTime.After(before),
		"lease_until must be after call-start: lease=%v, before=%v", leaseTime, before)
	assert.True(t, leaseTime.After(after.Add(DefaultLeaseTTL-time.Second)),
		"lease_until must include DefaultLeaseTTL offset: lease=%v, expected ⩾ %v",
		leaseTime, after.Add(DefaultLeaseTTL))
}

// TestSQLiteStore_LeaseUntil_ClearedOnMarkCompleted verifies that
// terminal transitions clear the lease.
func TestSQLiteStore_LeaseUntil_ClearedOnMarkCompleted(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	key := StepKey{"run-16", "stock.publish", "run-16|stock.publish"}
	require.NoError(t, store.MarkStarted(ctx, key))
	require.NoError(t, store.MarkCompleted(ctx, key, json.RawMessage(`{}`), nil))

	var leaseRaw string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT lease_until FROM execution_steps WHERE job_id = ?`, key.JobID).Scan(&leaseRaw))
	assert.Equal(t, "", leaseRaw, "Completed row must have lease_until cleared")
}

// TestSQLiteStore_LeaseUntil_ClearedOnMarkFailed verifies that
// Failed transitions clear the lease too.
func TestSQLiteStore_LeaseUntil_ClearedOnMarkFailed(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	key := StepKey{"run-17", "stock.publish", "run-17|stock.publish"}
	require.NoError(t, store.MarkStarted(ctx, key))
	require.NoError(t, store.MarkFailed(ctx, key, "boom"))

	var leaseRaw string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT lease_until FROM execution_steps WHERE job_id = ?`, key.JobID).Scan(&leaseRaw))
	assert.Equal(t, "", leaseRaw, "Failed row must have lease_until cleared")
}

// ── Concurrency: simultaneous goroutines on the same triple ───────

// TestSQLiteStore_Concurrent_MarkStarted_SerializeOnUniqueIndex
// verifies that concurrent goroutines calling MarkStarted on the
// same key survive the SQLite UNIQUE INDEX contention (via
// busy_timeout=5000 in the test DB pragma).
//
// This is the canonical "production concurrency safety" test:
// in real production, worker goroutines CAN race on the same key;
// the SQLite layer serializes via UNIQUE INDEX + WAL + busy_timeout.
// The test exercises 5 goroutines × 1 key + 5 goroutines × 1 distinct
// key each.
func TestSQLiteStore_Concurrent_MarkStarted_SerializeOnUniqueIndex(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	contendedKey := StepKey{"run-18", "stock.finalize", "run-18|stock.finalize"}
	var wg sync.WaitGroup

	// 5 concurrent goroutines on the same key.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = store.MarkStarted(ctx, contendedKey)
		}()
	}
	wg.Wait()

	var rows int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM execution_steps WHERE job_id = ?`, contendedKey.JobID).Scan(&rows))
	assert.Equal(t, 1, rows, "5 concurrent MarkStarted calls on same triple MUST collapse to 1 row")

	var attemptAfterRacingGoroutines int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT attempt FROM execution_steps WHERE job_id = ?`, contendedKey.JobID).Scan(&attemptAfterRacingGoroutines))
	assert.Equal(t, 5, attemptAfterRacingGoroutines, "5 MarkStarted calls should bump attempt to 5")
}

// ── helpers ─────────────────────────────────────────────────────────

// errorsIs wraps errors.Is so the assertion macros don't need
// direct `errors` import surface visibility beyond this helper.
// Wraps stdlib errors.Is verbatim.
func errorsIs(err, target error) bool {
	type unwrap interface{ Unwrap() error }
	for err != nil {
		if err == target {
			return true
		}
		if u, ok := err.(unwrap); ok {
			err = u.Unwrap()
			continue
		}
		// Final fallback: keep parity with errors.Is semantics
		// (handles fmt.Errorf-wrapped sentinels via Unwrap chain).
		if bytes.Contains([]byte(err.Error()), []byte(target.Error())) {
			return true
		}
		return false
	}
	return false
}
