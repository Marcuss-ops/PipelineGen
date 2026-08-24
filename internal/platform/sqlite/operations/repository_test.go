// Package operations_test pins the canonical operations.Repository
// contract via in-memory SQLite integration tests (FASE 2
// GenerationSubmissionService, July 2026).
//
// godlike/06 SSOT: tests open a fresh `:memory:` database via
// mattn/go-sqlite3, apply the migration 145 schema inline, then
// construct *SQLiteRepository and exercise every public method
// end-to-end. The hermetic port contract is independently pinned
// here (Behavior A) AND the application-layer service test (FASE 2
// push 2.2) covers Behavior B (idempotency hit / conflict /
// supersede flows).
//
// godlike/07 typed-error contract: every test asserts sentinel
// errors via errors.Is() so the assertion survives any future
// wrap that adds context (e.g., fmt.Errorf("...: %w", sentinel)).
package operations_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/operations"
	sqliteops "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/operations"
)

// migration145Schema is the inline mirror of
// migrations/sqlite/145_create_operations.sql so the `:memory:`
// test database can run without filesystem-coupled migration
// tooling. Keep in lockstep with the SQL — divergence is caught
// by the scan helper below (column order MUST match the Go-side
// operationColumns const).
const migration145Schema = `
CREATE TABLE IF NOT EXISTS operations (
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
CREATE INDEX IF NOT EXISTS idx_operations_idem_lookup
    ON operations(scope, idempotency_key, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_operations_state_created
    ON operations(state, created_at DESC);
-- NOTE: migration 146's partial UNIQUE INDEX
-- (ux_operations_active_scope_key WHERE state != 'SUPERSEDED')
-- is DELIBERATELY OMITTED from this test mirror. The
-- repository tests below intentionally exercise scenarios
-- forbidden by the production invariant (multiple QUEUED
-- operations in the same (scope, key) bucket, atomic-TX
-- supersede) — these verify the Repository contract at the
-- SQLite layer, not the Submit flow's invariant enforcement.
-- Production migration 146 enforces the constraint; tests
-- that go through Service.Submit honor it via the
-- application layer (see generation_submission_service_test.go
-- where the partial UNIQUE is mirrored and all tests pass).
`

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err, "open in-memory SQLite")
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(migration145Schema)
	require.NoError(t, err, "apply migration 145 schema")
	return db
}

func newTestRepo(t *testing.T) *sqliteops.SQLiteRepository {
	t.Helper()
	return sqliteops.NewSQLiteRepository(newTestDB(t))
}

// validOp returns a canonical valid Operation for hermetic tests.
// The caller can mutate the returned struct's fields.
//
// Push 2.1 (HIGH severity code-review fix): CreatedAt and UpdatedAt
// are stamped to a fixed test time here (instead of being left
// zero). The pre-fix Insert auto-filled zero timestamps with
// time.Now(); the post-fix Insert REQUIRES non-zero timestamps
// (godlike/07 fail-closed) so a caller that forgot to stamp
// them is rejected at the input boundary. Tests that exercise
// the "no auto-fill" contract stamp explicitly.
func validOp(scope operations.Scope, key, hash, jobID string) *operations.Operation {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	return &operations.Operation{
		OperationID:    "op_" + key,
		Scope:          scope,
		IdempotencyKey: key,
		RequestHash:    hash,
		JobID:          jobID,
		State:          operations.StateQueued,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

// makeHash computes a canonical 64-char lowercase hex SHA-256
// fingerprint from the input string. Tests use it to produce
// well-formed request_hash values without hardcoding 64-char
// literals throughout the suite.
func makeHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ── Insert contracts ────────────────────────────────────────────

func TestInsert_ValidOperation_Succeeds(t *testing.T) {
	r := newTestRepo(t)
	op := validOp(operations.ScopeScriptGenerate, "key-1", makeHash("body-1"), "job-1")
	require.NoError(t, r.Insert(context.Background(), op, nil))
	assert.False(t, op.CreatedAt.IsZero(), "CreatedAt must be non-zero on Insert (caller-stamped)")
	assert.False(t, op.UpdatedAt.IsZero(), "UpdatedAt must be non-zero on Insert (caller-stamped)")
}

// TestInsert_ZeroCreatedAt_ReturnsError pins the post-fix
// godlike/07 fail-closed contract: a caller that forgets to
// stamp CreatedAt is rejected at the input boundary, not
// silently auto-filled by the repository. This is the
// HIGH-severity code-review fix that closes the audit-
// invariant break (pre-fix: operation.created_at silently
// decoupled from job.created_at).
func TestInsert_ZeroCreatedAt_ReturnsError(t *testing.T) {
	r := newTestRepo(t)
	op := validOp(operations.ScopeScriptGenerate, "key-1", makeHash("body-1"), "job-1")
	op.CreatedAt = time.Time{} // explicitly zero
	err := r.Insert(context.Background(), op, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zero CreatedAt",
		"godlike/07 fail-closed: zero CreatedAt must be rejected, not auto-filled")
}

func TestInsert_ZeroUpdatedAt_ReturnsError(t *testing.T) {
	r := newTestRepo(t)
	op := validOp(operations.ScopeScriptGenerate, "key-1", makeHash("body-1"), "job-1")
	op.UpdatedAt = time.Time{} // explicitly zero
	err := r.Insert(context.Background(), op, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zero UpdatedAt",
		"godlike/07 fail-closed: zero UpdatedAt must be rejected, not auto-filled")
}

func TestInsert_NilOperation_ReturnsError(t *testing.T) {
	r := newTestRepo(t)
	err := r.Insert(context.Background(), nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil operation")
}

func TestInsert_EmptyOperationID_ReturnsError(t *testing.T) {
	r := newTestRepo(t)
	op := validOp(operations.ScopeScriptGenerate, "key-1", makeHash("body-1"), "job-1")
	op.OperationID = ""
	err := r.Insert(context.Background(), op, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty operation_id")
}

func TestInsert_InvalidScope_ReturnsErrInvalidOperationScope(t *testing.T) {
	r := newTestRepo(t)
	op := validOp(operations.Scope("unknown.scope"), "key-1", makeHash("body-1"), "job-1")
	err := r.Insert(context.Background(), op, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, operations.ErrInvalidOperationScope),
		"out-of-set scope must surface ErrInvalidOperationScope (godlike/07 fail-closed)")
}

func TestInsert_InvalidState_ReturnsErrInvalidOperationState(t *testing.T) {
	r := newTestRepo(t)
	op := validOp(operations.ScopeScriptGenerate, "key-1", makeHash("body-1"), "job-1")
	op.State = operations.State("PROVISIONAL") // not in canonical set
	err := r.Insert(context.Background(), op, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, operations.ErrInvalidOperationState),
		"out-of-set state must surface ErrInvalidOperationState (godlike/07 fail-closed)")
}

func TestInsert_EmptyIdempotencyKey_ReturnsErrIdempotencyKeyInvalid(t *testing.T) {
	r := newTestRepo(t)
	op := validOp(operations.ScopeScriptGenerate, "", makeHash("body-1"), "job-1")
	err := r.Insert(context.Background(), op, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, operations.ErrIdempotencyKeyInvalid),
		"empty idempotency key must surface ErrIdempotencyKeyInvalid (godlike/07 fail-closed)")
}

func TestInsert_TooLongIdempotencyKey_ReturnsErrIdempotencyKeyInvalid(t *testing.T) {
	r := newTestRepo(t)
	longKey := strings.Repeat("a", 256) // 1 char over the 255 cap
	op := validOp(operations.ScopeScriptGenerate, longKey, makeHash("body-1"), "job-1")
	err := r.Insert(context.Background(), op, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, operations.ErrIdempotencyKeyInvalid),
		"256-char idempotency key must surface ErrIdempotencyKeyInvalid (godlike/07 fail-closed)")
}

func TestInsert_NonPrintableASCIIIdempotencyKey_ReturnsErrIdempotencyKeyInvalid(t *testing.T) {
	r := newTestRepo(t)
	op := validOp(operations.ScopeScriptGenerate, "key\x00null", makeHash("body-1"), "job-1")
	err := r.Insert(context.Background(), op, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, operations.ErrIdempotencyKeyInvalid),
		"non-printable-ASCII idempotency key must surface ErrIdempotencyKeyInvalid (godlike/07 fail-closed)")
}

func TestInsert_EmptyRequestHash_ReturnsErrRequestHashInvalid(t *testing.T) {
	r := newTestRepo(t)
	op := validOp(operations.ScopeScriptGenerate, "key-1", "", "job-1")
	err := r.Insert(context.Background(), op, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, operations.ErrRequestHashInvalid),
		"empty request_hash must surface ErrRequestHashInvalid (godlike/07 fail-closed)")
}

func TestInsert_UppercaseRequestHash_ReturnsErrRequestHashInvalid(t *testing.T) {
	r := newTestRepo(t)
	upperHash := strings.ToUpper(makeHash("body-1")) // uppercase hex
	op := validOp(operations.ScopeScriptGenerate, "key-1", upperHash, "job-1")
	err := r.Insert(context.Background(), op, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, operations.ErrRequestHashInvalid),
		"uppercase hex request_hash must surface ErrRequestHashInvalid (canonical 64-char lowercase hex)")
}

func TestInsert_ShortRequestHash_ReturnsErrRequestHashInvalid(t *testing.T) {
	r := newTestRepo(t)
	op := validOp(operations.ScopeScriptGenerate, "key-1", "deadbeef", "job-1") // 8 chars, not 64
	err := r.Insert(context.Background(), op, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, operations.ErrRequestHashInvalid),
		"non-64-char request_hash must surface ErrRequestHashInvalid (canonical SHA-256 length)")
}

func TestInsert_EmptyJobID_ReturnsError(t *testing.T) {
	r := newTestRepo(t)
	op := validOp(operations.ScopeScriptGenerate, "key-1", makeHash("body-1"), "")
	err := r.Insert(context.Background(), op, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty job_id",
		"empty job_id must be rejected (FASE 2 contract: every operation has a job)")
}

// TestInsert_SelfSupersedeReference_ReturnsErrSelfSupersedeReference
// pins the LOW-severity code-review fix: an operation that points
// supersedes_operation_id at itself is rejected at the input
// boundary. The service layer cannot introduce this by construction
// (it sets supersedes_operation_id AFTER reading a prior operation),
// but a buggy direct caller of the repository (e.g. an admin
// script) could. Bounded check, no perf cost.
func TestInsert_SelfSupersedeReference_ReturnsErrSelfSupersedeReference(t *testing.T) {
	r := newTestRepo(t)
	op := validOp(operations.ScopeScriptGenerate, "key-1", makeHash("body-1"), "job-1")
	op.SupersedesOperationID = op.OperationID // self-reference
	err := r.Insert(context.Background(), op, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, operations.ErrSelfSupersedeReference),
		"supersedes_operation_id == operation_id MUST surface ErrSelfSupersedeReference (godlike/07 fail-closed)")
}

func TestInsert_DuplicateOperationID_ReturnsError(t *testing.T) {
	r := newTestRepo(t)
	op := validOp(operations.ScopeScriptGenerate, "key-1", makeHash("body-1"), "job-1")
	require.NoError(t, r.Insert(context.Background(), op, nil))

	op2 := validOp(operations.ScopeScriptGenerate, "key-2", makeHash("body-2"), "job-2")
	op2.OperationID = op.OperationID // collide on PK
	err := r.Insert(context.Background(), op2, nil)
	require.Error(t, err, "duplicate operation_id must fail (PK constraint)")
}

// ── GetByID contracts ───────────────────────────────────────────

func TestGetByID_Found_ReturnsOperation(t *testing.T) {
	r := newTestRepo(t)
	op := validOp(operations.ScopeScriptGenerate, "key-1", makeHash("body-1"), "job-1")
	require.NoError(t, r.Insert(context.Background(), op, nil))

	got, err := r.GetByID(context.Background(), op.OperationID, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, op.OperationID, got.OperationID)
	assert.Equal(t, op.Scope, got.Scope)
	assert.Equal(t, op.IdempotencyKey, got.IdempotencyKey)
	assert.Equal(t, op.RequestHash, got.RequestHash)
	assert.Equal(t, op.JobID, got.JobID)
	assert.Equal(t, op.State, got.State)
	assert.Empty(t, got.SupersedesOperationID)
	assert.WithinDuration(t, op.CreatedAt, got.CreatedAt, 0, "CreatedAt must round-trip")
	assert.WithinDuration(t, op.UpdatedAt, got.UpdatedAt, 0, "UpdatedAt must round-trip")
}

func TestGetByID_NotFound_ReturnsErrOperationNotFound(t *testing.T) {
	r := newTestRepo(t)
	got, err := r.GetByID(context.Background(), "op-ghost", nil)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.True(t, errors.Is(err, operations.ErrOperationNotFound),
		"missing operation_id must surface ErrOperationNotFound")
}

// TestGetByID_CorruptedTimestamp_ReturnsErrCorruptedOperationRow pins
// the MEDIUM-severity code-review fix: a stored created_at that
// cannot be parsed back to a valid RFC3339 time.Time surfaces
// as the typed ErrCorruptedOperationRow sentinel, NOT a silent
// time.Time{} that would be indistinguishable from a legitimate
// value. The pre-fix version masked DB corruption; the post-fix
// version makes it loud and operator-actionable.
func TestGetByID_CorruptedTimestamp_ReturnsErrCorruptedOperationRow(t *testing.T) {
	db := newTestDB(t)
	r := sqliteops.NewSQLiteRepository(db)

	// Bypass the repository Insert to write a row with a
	// malformed created_at (the Insert path validates + formats
	// timestamps, so it would never produce a malformed value).
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO operations (operation_id, scope, idempotency_key, request_hash, job_id, state, created_at, updated_at, supersedes_operation_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"op-corrupt", "script.generate", "key-corrupt", makeHash("body"), "job-1", "QUEUED",
		"NOT-A-VALID-RFC3339", "NOT-A-VALID-RFC3339", "")
	require.NoError(t, err)

	got, err := r.GetByID(context.Background(), "op-corrupt", nil)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.True(t, errors.Is(err, operations.ErrCorruptedOperationRow),
		"malformed created_at MUST surface ErrCorruptedOperationRow (godlike/07 fail-closed)")
}

func TestGetByID_EmptyOperationID_ReturnsError(t *testing.T) {
	r := newTestRepo(t)
	got, err := r.GetByID(context.Background(), "", nil)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "empty operation_id")
}

// ── GetLatestForKey contracts ───────────────────────────────────

func TestGetLatestForKey_Found_ReturnsMostRecent(t *testing.T) {
	r := newTestRepo(t)
	scope := operations.ScopeScriptGenerate
	key := "key-1"
	hashA := makeHash("body-A")
	hashB := makeHash("body-B")

	// Insert two operations on the same (scope, key) — the 2nd must win.
	op1 := validOp(scope, key, hashA, "job-1")
	op1.OperationID = "op-first"
	require.NoError(t, r.Insert(context.Background(), op1, nil))

	op2 := validOp(scope, key, hashB, "job-2")
	op2.OperationID = "op-second"
	op2.CreatedAt = op1.CreatedAt.Add(1) // ensure strict ordering
	require.NoError(t, r.Insert(context.Background(), op2, nil))

	got, err := r.GetLatestForKey(context.Background(), scope, key, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "op-second", got.OperationID,
		"GetLatestForKey must return the most-recent row in (scope, key)")
	assert.Equal(t, hashB, got.RequestHash)
}

// TestGetLatestForKey_ThreeOperationsInBucket_ReturnsMostRecent pins
// that the index idx_operations_idem_lookup (with DESC suffix) actually
// walks the bucket in DESC order and returns the LATEST row, not the
// middle one. The pre-MEDIUM-fix 2-row test could not distinguish
// "any one of two" from "the latest of two". With 3 rows in strict
// chronological order, a future ORDER BY created_at ASC regression
// would be caught here.
func TestGetLatestForKey_ThreeOperationsInBucket_ReturnsMostRecent(t *testing.T) {
	r := newTestRepo(t)
	scope := operations.ScopeScriptGenerate
	key := "key-3"
	hash1 := makeHash("body-1")
	hash2 := makeHash("body-2")
	hash3 := makeHash("body-3")

	base := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	for i, hash := range []string{hash1, hash2, hash3} {
		op := validOp(scope, key, hash, "job-"+string(rune('1'+i)))
		op.OperationID = "op-third-test-" + string(rune('A'+i))
		op.CreatedAt = base.Add(time.Duration(i) * time.Second)
		op.UpdatedAt = op.CreatedAt
		require.NoError(t, r.Insert(context.Background(), op, nil))
	}

	got, err := r.GetLatestForKey(context.Background(), scope, key, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "op-third-test-C", got.OperationID,
		"GetLatestForKey must return the LATEST row (3rd inserted), not the middle one")
	assert.Equal(t, hash3, got.RequestHash)
}

// TestGetLatestForKey_SameCreatedAtPicksHigherOperationID pins the
// tie-breaker behaviour: when 2+ operations in the same (scope, key)
// bucket have IDENTICAL created_at (a race-real scenario — the
// service layer may stamp `time.Now()` twice in <1µs, or two requests
// arrive in the same clock tick), the ORDER BY tie-breaker
// `operation_id DESC` MUST pick the higher operation_id
// deterministically. Without the tie-breaker, the query would
// return a non-deterministic row, breaking idempotency-hit
// semantics.
func TestGetLatestForKey_SameCreatedAtPicksHigherOperationID(t *testing.T) {
	r := newTestRepo(t)
	scope := operations.ScopeScriptGenerate
	key := "key-tie"
	sameTime := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	// Insert in lexically ASCENDING operation_id order so the
	// tie-breaker has a clear winner.
	low := validOp(scope, key, makeHash("body-A"), "job-A")
	low.OperationID = "op-tie-A"
	low.CreatedAt = sameTime
	low.UpdatedAt = sameTime
	require.NoError(t, r.Insert(context.Background(), low, nil))

	mid := validOp(scope, key, makeHash("body-B"), "job-B")
	mid.OperationID = "op-tie-M"
	mid.CreatedAt = sameTime
	mid.UpdatedAt = sameTime
	require.NoError(t, r.Insert(context.Background(), mid, nil))

	high := validOp(scope, key, makeHash("body-Z"), "job-Z")
	high.OperationID = "op-tie-Z"
	high.CreatedAt = sameTime
	high.UpdatedAt = sameTime
	require.NoError(t, r.Insert(context.Background(), high, nil))

	got, err := r.GetLatestForKey(context.Background(), scope, key, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "op-tie-Z", got.OperationID,
		"GetLatestForKey tie-breaker: with identical created_at, the HIGHER operation_id (op-tie-Z > op-tie-M > op-tie-A) MUST win deterministically")
	assert.Equal(t, makeHash("body-Z"), got.RequestHash)
}

func TestGetLatestForKey_NotFound_ReturnsNilNil(t *testing.T) {
	r := newTestRepo(t)
	got, err := r.GetLatestForKey(context.Background(), operations.ScopeScriptGenerate, "ghost-key", nil)
	require.NoError(t, err)
	assert.Nil(t, got,
		"unknown (scope, key) must return (nil, nil) — not a sentinel error (idempotency-miss is not an error)")
}

func TestGetLatestForKey_InvalidScope_ReturnsError(t *testing.T) {
	r := newTestRepo(t)
	got, err := r.GetLatestForKey(context.Background(), operations.Scope("bogus"), "key-1", nil)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.True(t, errors.Is(err, operations.ErrInvalidOperationScope),
		"out-of-set scope must surface ErrInvalidOperationScope (godlike/07 fail-closed)")
}

func TestGetLatestForKey_InvalidKey_ReturnsErrIdempotencyKeyInvalid(t *testing.T) {
	r := newTestRepo(t)
	got, err := r.GetLatestForKey(context.Background(), operations.ScopeScriptGenerate, "", nil)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.True(t, errors.Is(err, operations.ErrIdempotencyKeyInvalid))
}

// ── UpdateState contracts ───────────────────────────────────────

func TestUpdateState_ValidTransition_Succeeds(t *testing.T) {
	r := newTestRepo(t)
	op := validOp(operations.ScopeScriptGenerate, "key-1", makeHash("body-1"), "job-1")
	require.NoError(t, r.Insert(context.Background(), op, nil))

	originalUpdatedAt := op.UpdatedAt

	require.NoError(t, r.UpdateState(context.Background(), op.OperationID, operations.StateSuperseded, nil))

	got, err := r.GetByID(context.Background(), op.OperationID, nil)
	require.NoError(t, err)
	assert.Equal(t, operations.StateSuperseded, got.State,
		"UpdateState must flip State to the requested value")
	assert.True(t, got.UpdatedAt.After(originalUpdatedAt) || got.UpdatedAt.Equal(originalUpdatedAt),
		"UpdatedAt must be bumped (or at least not regressed) on UpdateState")
}

func TestUpdateState_InvalidState_ReturnsErrInvalidOperationState(t *testing.T) {
	r := newTestRepo(t)
	op := validOp(operations.ScopeScriptGenerate, "key-1", makeHash("body-1"), "job-1")
	require.NoError(t, r.Insert(context.Background(), op, nil))

	err := r.UpdateState(context.Background(), op.OperationID, operations.State("WAT"), nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, operations.ErrInvalidOperationState),
		"out-of-set state must surface ErrInvalidOperationState (godlike/07 fail-closed)")
}

func TestUpdateState_NotFound_ReturnsErrOperationNotFound(t *testing.T) {
	r := newTestRepo(t)
	err := r.UpdateState(context.Background(), "op-ghost", operations.StateSuperseded, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, operations.ErrOperationNotFound),
		"missing operation_id must surface ErrOperationNotFound")
}

func TestUpdateState_EmptyOperationID_ReturnsError(t *testing.T) {
	r := newTestRepo(t)
	err := r.UpdateState(context.Background(), "", operations.StateSuperseded, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty operation_id")
}

// ── *sql.Tx acceptance + atomic-TX scenario (FASE 2 critical) ──

func TestInsert_AcceptsSQLTx(t *testing.T) {
	db := newTestDB(t)
	r := sqliteops.NewSQLiteRepository(db)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	op := validOp(operations.ScopeScriptGenerate, "key-tx", makeHash("body-tx"), "job-tx")
	require.NoError(t, r.Insert(context.Background(), op, tx),
		"Insert with non-nil *sql.Tx must succeed (FASE 2 atomic-TX shape)")
}

func TestGetByID_AcceptsSQLTx(t *testing.T) {
	db := newTestDB(t)
	r := sqliteops.NewSQLiteRepository(db)
	op := validOp(operations.ScopeScriptGenerate, "key-tx", makeHash("body-tx"), "job-tx")
	require.NoError(t, r.Insert(context.Background(), op, nil))

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	got, err := r.GetByID(context.Background(), op.OperationID, tx)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, op.OperationID, got.OperationID)
}

func TestGetLatestForKey_AcceptsSQLTx(t *testing.T) {
	db := newTestDB(t)
	r := sqliteops.NewSQLiteRepository(db)
	op := validOp(operations.ScopeScriptGenerate, "key-tx", makeHash("body-tx"), "job-tx")
	require.NoError(t, r.Insert(context.Background(), op, nil))

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	got, err := r.GetLatestForKey(context.Background(), operations.ScopeScriptGenerate, "key-tx", tx)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, op.OperationID, got.OperationID)
}

func TestUpdateState_AcceptsSQLTx(t *testing.T) {
	db := newTestDB(t)
	r := sqliteops.NewSQLiteRepository(db)
	op := validOp(operations.ScopeScriptGenerate, "key-tx", makeHash("body-tx"), "job-tx")
	require.NoError(t, r.Insert(context.Background(), op, nil))

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	require.NoError(t, r.UpdateState(context.Background(), op.OperationID, operations.StateSuperseded, tx))
}

// TestAtomic_NewOperationInsert_AndPriorSupersede_RollsBackTogether
// is the FASE 2 critical-path scenario: the new operation's INSERT
// + the prior operation's state flip to SUPERSEDED must commit
// TOGETHER or roll back TOGETHER. The test asserts the rollback
// path: a forced error in the second statement must leave the
// database unchanged (no new operation, no SUPERSEDED flip).
func TestAtomic_NewOperationInsert_AndPriorSupersede_RollsBackTogether(t *testing.T) {
	db := newTestDB(t)
	r := sqliteops.NewSQLiteRepository(db)

	// Seed a prior operation.
	prior := validOp(operations.ScopeScriptGenerate, "key-1", makeHash("body-1"), "job-1")
	prior.OperationID = "op-prior"
	require.NoError(t, r.Insert(context.Background(), prior, nil))

	// Open a TX, insert a new operation that supersedes the prior,
	// flip the prior to SUPERSEDED, then force a rollback.
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)

	newOp := validOp(operations.ScopeScriptGenerate, "key-1", makeHash("body-1"), "job-2")
	newOp.OperationID = "op-new"
	newOp.SupersedesOperationID = prior.OperationID
	require.NoError(t, r.Insert(context.Background(), newOp, tx))
	require.NoError(t, r.UpdateState(context.Background(), prior.OperationID, operations.StateSuperseded, tx))

	// Force a rollback — simulates "the next statement in the
	// atomic TX (e.g. outbox INSERT) failed, so we must not
	// have committed the operation OR the supersede flip".
	require.NoError(t, tx.Rollback())

	// After rollback: prior must still be QUEUED, newOp must NOT
	// exist in the table at all.
	gotPrior, err := r.GetByID(context.Background(), prior.OperationID, nil)
	require.NoError(t, err)
	require.NotNil(t, gotPrior)
	assert.Equal(t, operations.StateQueued, gotPrior.State,
		"after TX rollback, prior operation's state MUST be unchanged (still QUEUED)")

	gotNew, err := r.GetByID(context.Background(), newOp.OperationID, nil)
	require.Error(t, err, "after TX rollback, the new operation row MUST NOT exist")
	assert.True(t, errors.Is(err, operations.ErrOperationNotFound))
	assert.Nil(t, gotNew)
}

// TestAtomic_NewOperationInsert_AndPriorSupersede_CommitsTogether
// is the FASE 2 critical-path COMMIT scenario: the new operation
// and the prior's SUPERSEDED flip must BOTH be visible after COMMIT.
func TestAtomic_NewOperationInsert_AndPriorSupersede_CommitsTogether(t *testing.T) {
	db := newTestDB(t)
	r := sqliteops.NewSQLiteRepository(db)

	prior := validOp(operations.ScopeScriptGenerate, "key-1", makeHash("body-1"), "job-1")
	prior.OperationID = "op-prior"
	require.NoError(t, r.Insert(context.Background(), prior, nil))

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)

	newOp := validOp(operations.ScopeScriptGenerate, "key-1", makeHash("body-1"), "job-2")
	newOp.OperationID = "op-new"
	newOp.SupersedesOperationID = prior.OperationID
	require.NoError(t, r.Insert(context.Background(), newOp, tx))
	require.NoError(t, r.UpdateState(context.Background(), prior.OperationID, operations.StateSuperseded, tx))
	require.NoError(t, tx.Commit())

	gotPrior, err := r.GetByID(context.Background(), prior.OperationID, nil)
	require.NoError(t, err)
	require.NotNil(t, gotPrior)
	assert.Equal(t, operations.StateSuperseded, gotPrior.State,
		"after COMMIT, prior operation's state MUST be SUPERSEDED")

	gotNew, err := r.GetByID(context.Background(), newOp.OperationID, nil)
	require.NoError(t, err)
	require.NotNil(t, gotNew)
	assert.Equal(t, prior.OperationID, gotNew.SupersedesOperationID,
		"after COMMIT, the new operation MUST point at the prior via supersedes_operation_id")
	assert.Equal(t, operations.StateQueued, gotNew.State,
		"new operation starts in QUEUED (not COMPLETED — finalizer sets that later)")
}
