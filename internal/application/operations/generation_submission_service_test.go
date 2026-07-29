// Package operations_test — hermetic test for
// GenerationSubmissionService. Push 2.2a (HIGH severity
// code-review fix): the Service is the most complex piece
// of FASE 2 (12-step atomic-TX flow, 5 outcome branches,
// mutex serialisation, defer-rollback). This test pins the
// 3 most critical surfaces in isolation: validation
// failure (no DB write), happy path (atomic commit), and
// idempotency hit (no new DB write). The 4 canonical
// scenarios (new/hit/conflict/supersede) are exercised in
// Push 2.2b alongside the wire-up + handler refactor.
package operations_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/operations"
	domainops "github.com/Marcuss-ops/PipelineGen/internal/domain/operations"
	sqlitejobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	sqliteops "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/operations"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// schemasFASE2 is the inline mirror of migrations/sqlite/092
// (outbox_events), 145 (operations), and the canonical jobs
// table from `internal/infrastructure/database/sqlite/jobs`.
// Kept in lockstep with the production migrations. Drift
// between this schema and the production migrations would
// surface as SQL errors at INSERT time (NOT a silent mismatch).
//
// godlike/06 SSOT: the 3 schemas are the SOLE canonical
// shapes the Service touches in the atomic-TX path. The
// test exercises the same INSERT projections as the
// production adapters — adding a column to any of the 3
// production tables requires updating this string AND
// the corresponding production INSERT projection.
//
// `time` is reserved for the future hermetic test that
// exercises a deterministic `s.nowFunc()` injection (the
// Service has a `nowFunc` field for this purpose).
var _ = time.Now

const schemasFASE2 = `
CREATE TABLE operations (
    operation_id            TEXT PRIMARY KEY,
    scope                   TEXT NOT NULL,
    idempotency_key         TEXT NOT NULL,
    request_hash            TEXT NOT NULL,
    job_id                  TEXT NOT NULL,
    state                   TEXT NOT NULL,
    created_at              TEXT NOT NULL,
    updated_at              TEXT NOT NULL,
    supersedes_operation_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_operations_idem_lookup
    ON operations(scope, idempotency_key, created_at DESC);
CREATE UNIQUE INDEX ux_operations_active_scope_key
    ON operations(scope, idempotency_key)
    WHERE state != 'SUPERSEDED';

CREATE TABLE jobs (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'QUEUED',
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
    max_retries INTEGER NOT NULL DEFAULT 0,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_id TEXT NOT NULL DEFAULT '',
    lease_expiry TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT,
    cancelled_at TEXT,
    revision INTEGER NOT NULL DEFAULT 1,
    parent_state_typed TEXT NOT NULL DEFAULT ''
);

CREATE TABLE outbox_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL DEFAULT '',
    aggregate_type TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '',
    event_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10,
    last_error TEXT NOT NULL DEFAULT '',
    next_attempt_at TEXT,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_id TEXT NOT NULL DEFAULT '',
    lease_expiry TEXT,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX ux_outbox_events_event_key
    ON outbox_events(event_key);
`

// newFASE2DB opens an in-memory SQLite + applies the 3
// canonical FASE 2 schemas. Returns the *sql.DB so callers
// can wire concrete adapters.
func newFASE2DB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err, "open in-memory SQLite")
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(schemasFASE2)
	require.NoError(t, err, "apply FASE 2 schemas")
	return db
}

// jobsRepoAdapter wraps *sqlitejobs.SQLiteStore to satisfy
// the FASE 2 narrow JobEnqueuer port. Push 2.2a hermetic
// test-only — the production composition root in
// `internal/app` (Push 2.2b) constructs the canonical
// adapter; this test-local one avoids the cross-package
// wiring for the hermetic surface.
type jobsRepoAdapter struct {
	store *sqlitejobs.SQLiteStore
}

func newJobsRepoAdapter(store *sqlitejobs.SQLiteStore) *jobsRepoAdapter {
	return &jobsRepoAdapter{store: store}
}

func (a *jobsRepoAdapter) CreateInTx(ctx context.Context, tx *sql.Tx, j *job.Job) error {
	return a.store.CreateInTx(ctx, tx, j)
}

// dbTxManager wraps *sql.DB to satisfy the TxManager port.
type dbTxManager struct {
	db *sql.DB
}

func newDBTxManager(db *sql.DB) *dbTxManager {
	return &dbTxManager{db: db}
}

func (m *dbTxManager) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return m.db.BeginTx(ctx, nil)
}

// newFASE2Service constructs a Service wired with the
// canonical concrete adapters + the FASE 2 schemas applied
// to the in-memory DB. Returns the service + a teardown
// handle for inspecting the DB state in the test body.
type fase2TestEnv struct {
	Service *operations.Service
	DB      *sql.DB
	Ops     *sqliteops.SQLiteRepository
	Outbox  *outboxevents.Repository
	Jobs    *sqlitejobs.SQLiteStore
	TxMgr   *dbTxManager
}

func newFASE2Service(t *testing.T) *fase2TestEnv {
	t.Helper()
	db := newFASE2DB(t)
	ops := sqliteops.NewSQLiteRepository(db)
	// The jobs.SQLiteStore constructor is heavy (it wires a
	// queueChanged broadcast channel + log). For the hermetic
	// test we just need the *sql.DB — the CreateInTx method is
	// the only one the test exercises. We construct the
	// SQLiteStore via the canonical NewSQLiteStore constructor
	// (the *zap.Logger is unused by CreateInTx, so a no-op
	// logger is sufficient).
	jobsStore := sqlitejobs.NewSQLiteStore(db, zap.NewNop())
	outboxRepo := outboxevents.NewRepository(db)
	txMgr := newDBTxManager(db)
	// FASE 2 close-out: jobsStore satisfies the JobGetter port
	// natively (its Get(ctx, id) method matches the port shape).
	// Wired twice — as JobEnqueuer (CreateInTx use) and as
	// JobGetter (canonical-state-on-replay read).
	svc := operations.NewService(ops, newJobsRepoAdapter(jobsStore), jobsStore, outboxRepo, txMgr, nil)
	return &fase2TestEnv{
		Service: svc,
		DB:      db,
		Ops:     ops,
		Outbox:  outboxRepo,
		Jobs:    jobsStore,
		TxMgr:   txMgr,
	}
}

// makeHash returns a canonical 64-char lowercase hex SHA-256
// for the given input. Mirrors the helper in the
// operations-package repository test.
func makeHashFASE2(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// canonicalSubmitRequest builds a valid SubmitRequest with
// the given scope+key+hash, and a fixed payload. Used by
// happy-path + idempotency-hit + force-refresh tests.
func canonicalSubmitRequest(scope domainops.Scope, key, hash string) operations.SubmitRequest {
	return operations.SubmitRequest{
		Scope:          scope,
		IdempotencyKey: key,
		RequestHash:    hash,
		ForceRefresh:   false,
		JobType:        "script.generate",
		JobPayload:     json.RawMessage(`{"version":2,"items":[]}`),
		JobPriority:    5,
		JobMaxRetries:  2,
	}
}

// ── Test 1: validation failure (HIGH severity code-review fix) ──

// TestSubmit_InvalidScope_ReturnsErrInvalidOperationScope pins
// the godlike/07 fail-closed contract: a caller-supplied
// out-of-set scope is rejected at the input boundary, BEFORE
// the mutex is acquired (no DB write, no allocation). This
// is the most fundamental guard — without it, a misconfigured
// caller could silently write rows to the operations table
// with bogus scope values.
func TestSubmit_InvalidScope_ReturnsErrInvalidOperationScope(t *testing.T) {
	env := newFASE2Service(t)
	req := canonicalSubmitRequest(domainops.Scope("bogus.scope"), "key-1", makeHashFASE2("body-1"))

	res, err := env.Service.Submit(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, res)
	assert.True(t, errors.Is(err, domainops.ErrInvalidOperationScope),
		"out-of-set scope must surface ErrInvalidOperationScope (godlike/07 fail-closed)")

	// Confirm no DB write happened.
	var rowCount int
	require.NoError(t, env.DB.QueryRow(`SELECT COUNT(*) FROM operations`).Scan(&rowCount))
	assert.Equal(t, 0, rowCount, "no operations row should be written for invalid scope")
}

// TestSubmit_EmptyIdempotencyKey_ReturnsErrIdempotencyKeyInvalid
// pins the same fail-closed contract for the idempotency_key.
func TestSubmit_EmptyIdempotencyKey_ReturnsErrIdempotencyKeyInvalid(t *testing.T) {
	env := newFASE2Service(t)
	req := canonicalSubmitRequest(domainops.ScopeScriptGenerate, "", makeHashFASE2("body-1"))

	res, err := env.Service.Submit(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, res)
	assert.True(t, errors.Is(err, domainops.ErrIdempotencyKeyInvalid),
		"empty idempotency_key must surface ErrIdempotencyKeyInvalid (godlike/07 fail-closed)")
}

// TestSubmit_BadRequestHash_ReturnsErrRequestHashInvalid pins
// the same fail-closed contract for the request_hash. The
// 64-char lowercase hex validation rejects non-canonical
// shapes (uppercase, truncated, non-hex) at the input boundary.
func TestSubmit_BadRequestHash_ReturnsErrRequestHashInvalid(t *testing.T) {
	env := newFASE2Service(t)
	// Uppercase hex — canonical validator requires lowercase.
	upperHash := strings.ToUpper(makeHashFASE2("body-1"))
	req := canonicalSubmitRequest(domainops.ScopeScriptGenerate, "key-1", upperHash)

	res, err := env.Service.Submit(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, res)
	assert.True(t, errors.Is(err, domainops.ErrRequestHashInvalid),
		"uppercase hex request_hash must surface ErrRequestHashInvalid (godlike/07 fail-closed)")
}

// TestSubmit_EmptyJobType_ReturnsError pins the same
// fail-closed contract for the JobType field.
func TestSubmit_EmptyJobType_ReturnsError(t *testing.T) {
	env := newFASE2Service(t)
	req := canonicalSubmitRequest(domainops.ScopeScriptGenerate, "key-1", makeHashFASE2("body-1"))
	req.JobType = ""

	res, err := env.Service.Submit(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "empty JobType",
		"empty JobType must be rejected at the input boundary (godlike/07 fail-closed)")
}

// ── Test 2: happy path (HIGH severity code-review fix) ──

// TestSubmit_HappyPath_CommitsOperationJobAndOutboxAtomically
// pins the canonical FASE 2 atomic-TX shape: a fresh
// submission commits 3 rows (operations + jobs +
// outbox_events) in a single transaction. This is the
// load-bearing test of the 12-step flow; if any step's
// side effect is not atomic, the post-Commit state would
// show a partial write.
//
// Push 2.2a (LOW code-review fix): the outbox payload
// (`payload_json` column on `outbox_events`) carries
// `{"operation_id": ..., "job_id": ...}` per Submit Step 10.
// Asserting the payload is the canonical proof that the
// outbox INSERT happened with the correct values; a future
// regression that drops the payload (or sets it to an empty
// string) would be caught here.
//
// Push 2.2a (MEDIUM code-review fix deferred): the rollback
// test (outbox INSERT fails → operations + jobs rolled back
// atomically) is NOT covered in this test file — it requires
// a mock OutboxEmitter that returns an error, which the
// current hermetic setup with real outboxevents.Repository
// cannot inject without a port interface seam. Deferred to
// Push 2.2b alongside the wire-up + handler refactor where
// the mock-port pattern is established.
func TestSubmit_HappyPath_CommitsOperationJobAndOutboxAtomically(t *testing.T) {
	env := newFASE2Service(t)
	req := canonicalSubmitRequest(
		domainops.ScopeScriptGenerate,
		"key-happy-1",
		makeHashFASE2("body-happy-1"),
	)
	// Pre-generate IDs for deterministic test inspection.
	req.OperationID = "op-happy-1"
	req.JobID = "job-happy-1"

	res, err := env.Service.Submit(context.Background(), req)
	require.NoError(t, err, "Submit happy path must succeed")
	require.NotNil(t, res)
	require.NotNil(t, res.Operation)

	// 1. SubmitResult correctness.
	assert.Equal(t, "op-happy-1", res.Operation.OperationID)
	assert.False(t, res.IsIdempotencyHit, "first-time submission must NOT be an idempotency hit")
	assert.False(t, res.IsSupersede, "first-time submission must NOT be a supersede")
	assert.Equal(t, domainops.StateQueued, res.Operation.State)
	assert.Equal(t, "job-happy-1", res.Operation.JobID)
	assert.Empty(t, res.Operation.SupersedesOperationID, "first-time submission has no supersede link")
	assert.Equal(t, req.IdempotencyKey, res.Operation.IdempotencyKey)
	assert.Equal(t, req.RequestHash, res.Operation.RequestHash)
	assert.Equal(t, req.Scope, res.Operation.Scope)

	// 2. operations row was written.
	var opCount int
	require.NoError(t, env.DB.QueryRow(
		`SELECT COUNT(*) FROM operations WHERE operation_id = ? AND state = 'QUEUED'`,
		"op-happy-1",
	).Scan(&opCount))
	assert.Equal(t, 1, opCount, "exactly 1 operations row should be committed with state=QUEUED")

	// 3. jobs row was written.
	var jobCount int
	require.NoError(t, env.DB.QueryRow(
		`SELECT COUNT(*) FROM jobs WHERE id = ? AND type = ? AND status = 'QUEUED'`,
		"job-happy-1", "script.generate",
	).Scan(&jobCount))
	assert.Equal(t, 1, jobCount, "exactly 1 jobs row should be committed with status=QUEUED")

	// 4. outbox_events row was written (event_type =
	//    script.generate.queued, event_key = operation_id).
	var outboxCount int
	var eventType, eventKey, payloadJSON string
	require.NoError(t, env.DB.QueryRow(
		`SELECT COUNT(*), MAX(event_type), MAX(event_key), MAX(payload_json) FROM outbox_events WHERE event_key = ?`,
		"op-happy-1",
	).Scan(&outboxCount, &eventType, &eventKey, &payloadJSON))
	assert.Equal(t, 1, outboxCount, "exactly 1 outbox_events row should be committed")
	assert.Equal(t, "script.generate.queued", eventType)
	assert.Equal(t, "op-happy-1", eventKey)
	// 5. outbox payload assertion (LOW code-review fix): the
	//    payload is the FASE 2 minimal envelope
	//    `{"operation_id": "...", "job_id": "..."}`.
	var payload map[string]string
	require.NoError(t, json.Unmarshal([]byte(payloadJSON), &payload))
	assert.Equal(t, "op-happy-1", payload["operation_id"],
		"outbox payload must contain the operation_id (FASE 2 cross-trace)")
	assert.Equal(t, "job-happy-1", payload["job_id"],
		"outbox payload must contain the job_id (FASE 2 cross-trace)")
}

// ── Test 3: idempotency hit (HIGH severity code-review fix) ──

// TestSubmit_IdempotencyHit_ReturnsSameOperation_NoNewWrites
// pins the (d) requirement of the FASE 2 spec: same key + same
// hash → same operation. A second Submit with the same
// (scope, key, hash) returns the existing operation without
// committing a new row. Critical for retry-safe semantics.
func TestSubmit_IdempotencyHit_ReturnsSameOperation_NoNewWrites(t *testing.T) {
	env := newFASE2Service(t)
	req := canonicalSubmitRequest(
		domainops.ScopeScriptGenerate,
		"key-hit-1",
		makeHashFASE2("body-hit-1"),
	)
	req.OperationID = "op-hit-first"
	req.JobID = "job-hit-first"

	// First Submit creates the operation.
	first, err := env.Service.Submit(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.NotNil(t, first.Operation)
	require.False(t, first.IsIdempotencyHit)

	// Second Submit with the same (scope, key, hash) — even
	// with a different pre-supplied OperationID/JobID — must
	// return the FIRST operation and NOT create a new row.
	req2 := req
	req2.OperationID = "op-hit-second-DIFFERENT"
	req2.JobID = "job-hit-second-DIFFERENT"

	second, err := env.Service.Submit(context.Background(), req2)
	require.NoError(t, err, "idempotency hit must NOT return an error")
	require.NotNil(t, second)
	require.NotNil(t, second.Operation)

	// (d) same operation_id returned.
	assert.Equal(t, "op-hit-first", second.Operation.OperationID,
		"idempotency hit must return the FIRST operation_id, ignoring the caller-supplied pre-supplied ID")
	assert.True(t, second.IsIdempotencyHit,
		"idempotency hit must surface IsIdempotencyHit=true")
	assert.False(t, second.IsSupersede,
		"idempotency hit must NOT be a supersede")

	// No new row was written — the count of operations rows
	// matching the (scope, key) pair is still 1.
	var opCount int
	require.NoError(t, env.DB.QueryRow(
		`SELECT COUNT(*) FROM operations WHERE scope = ? AND idempotency_key = ?`,
		string(req.Scope), req.IdempotencyKey,
	).Scan(&opCount))
	assert.Equal(t, 1, opCount, "idempotency hit must NOT commit a new operations row")
}

// ── Test 4: canonical job-state on replay (FASE 2 close-out) ──

// TestSubmit_IdempotencyHit_ReadsCanonicalJobState pins the FASE 2
// close-out contract: on idempotency hit, Submit returns the canonical
// live Job state (post-worker UPDATEd, NOT the stale QUEUED deposited
// at Submit commit). This is the user-spec requirement: "leggendo lo
// stato del job canonico, non più una copia HTTP 202".
func TestSubmit_IdempotencyHit_ReadsCanonicalJobState(t *testing.T) {
	env := newFASE2Service(t)
	req := canonicalSubmitRequest(
		domainops.ScopeScriptGenerate,
		"key-canonical-1",
		makeHashFASE2("body-canonical-1"),
	)
	req.OperationID = "op-canonical-1"
	req.JobID = "job-canonical-1"

	// First Submit creates the operation + job (status=QUEUED).
	first, err := env.Service.Submit(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.NotNil(t, first.Job, "fresh submit MUST return the canonical Job")
	require.Equal(t, job.StatusQueued, first.Job.Status,
		"fresh submit returns the freshly-INSERTed Job in QUEUED state")
	require.Equal(t, "job-canonical-1", first.Job.ID)

	// Simulate worker progress: directly UPDATE the jobs row's
	// status to SUCCEEDED (the canonical live state).
	_, dbErr := env.DB.Exec(
		`UPDATE jobs SET status = ?, completed_at = ?, updated_at = ? WHERE id = ?`,
		string(job.StatusSucceeded),
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
		"job-canonical-1",
	)
	require.NoError(t, dbErr, "direct job-status UPDATE for canonical-state setup")

	// Second Submit with the same (scope, key, hash) — replay.
	// Empty req.OperationID + req.JobID so the service generates
	// fresh IDs and the self-reference guard (req.OperationID ==
	// prior.OperationID && req.OperationID != "") does not fire.
	// A real idempotent retry never re-supplies the IDs that the
	// first call already committed (those belong to the canonical
	// prior operation).
	req2 := req
	req2.OperationID = ""
	req2.JobID = ""
	second, err := env.Service.Submit(context.Background(), req2)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.True(t, second.IsIdempotencyHit)
	require.NotNil(t, second.Job,
		"replay MUST return the canonical live Job state (FASE 2 close-out contract)")
	// The canonical state MUST be observed: SUCCEEDED,
	// NOT the stale QUEUED.
	require.Equal(t, job.StatusSucceeded, second.Job.Status,
		"replay MUST surface the canonical live Job.Status, not the stale QUEUED")
	require.False(t, second.IsSupersede)
}
