// Package executionsteps_test pins the canonical Repository contract
// via in-memory SQLite integration tests (PipelineGen Stock Cutover
// §12-3, July 2026).
//
// godlike/06 SSOT: tests open a fresh `:memory:` database via
// mattn/go-sqlite3, apply migration 121 (table + UNIQUE index +
// ix_resume + ix_audit) inline, then construct *Repository and exercise
// every Store method end-to-end. The hermetic port contract is
// independently pinned at internal/application/execution/steps/store_test.go
// (FakeStore, pure-Go) so the SQL surface here validates Behavior A
// only, not behavioral drift between port and concrete.
//
// godlike/07 typed-error contract: every test asserts sentinel errors
// via errors.Is() so the assertion survives any future wrap that adds
// context (e.g., fmt.Errorf("...: %w", sentinel)).
package executionsteps_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3" // SQLite driver registration.
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/executionsteps"
)

// migration121Schema is the inline mirror of
// migrations/sqlite/121_execution_steps.sql so the `:memory:` test
// database can run without filesystem-coupled migration tooling. Keep
// in lockstep with the SQL — divergence is caught by `Compile-time`
// assertion below.
const migration121Schema = `
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
`

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err, "open in-memory SQLite")
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(migration121Schema)
	require.NoError(t, err, "apply migration 121 schema")
	return db
}

func newTestRepo(t *testing.T) *executionsteps.Repository {
	t.Helper()
	r, err := executionsteps.New(newTestDB(t))
	require.NoError(t, err)
	return r
}

func TestNew_NilDB_FailsLoudly(t *testing.T) {
	r, err := executionsteps.New(nil)
	require.Error(t, err)
	assert.Nil(t, r)
	assert.True(t, errors.Is(err, steps.ErrStoreNotWired),
		"nil db must surface ErrStoreNotWired (godlike/05 fail-closed)")
}

// ── MarkStarted contracts ───────────────────────────────────────

func TestRepo_MarkStarted_HappyPath(t *testing.T) {
	r := newTestRepo(t)
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	require.NoError(t, r.MarkStarted(context.Background(), key))

	rows, err := r.ListByJob(context.Background(), "j-1")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, steps.StatusPending, rows[0].Status)
	assert.Equal(t, 1, rows[0].Attempt)
	assert.Equal(t, "fp-A", rows[0].Fingerprint)
}

func TestRepo_MarkStarted_ReEntryBumpsAttempt(t *testing.T) {
	r := newTestRepo(t)
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	require.NoError(t, r.MarkStarted(context.Background(), key))
	require.NoError(t, r.MarkStarted(context.Background(), key))

	rows, err := r.ListByJob(context.Background(), "j-1")
	require.NoError(t, err)
	require.Len(t, rows, 1, "ON CONFLICT DO UPDATE bumps attempt, NOT inserts a new row")
	assert.Equal(t, 2, rows[0].Attempt)
}

func TestRepo_MarkStarted_NewFingerprintInserts(t *testing.T) {
	r := newTestRepo(t)
	keyA := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	keyB := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-B"}
	require.NoError(t, r.MarkStarted(context.Background(), keyA))
	require.NoError(t, r.MarkStarted(context.Background(), keyB))

	rows, err := r.ListByJob(context.Background(), "j-1")
	require.NoError(t, err)
	require.Len(t, rows, 2, "different fingerprint SHOULD INSERT a separate audit-trail row")
	assert.Equal(t, "fp-A", rows[0].Fingerprint)
	assert.Equal(t, "fp-B", rows[1].Fingerprint)
}

func TestRepo_MarkStarted_CompletedIsTerminal(t *testing.T) {
	r := newTestRepo(t)
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	require.NoError(t, r.MarkStarted(context.Background(), key))
	require.NoError(t, r.MarkCompleted(context.Background(), key, []byte(`{"v":1}`), []byte(`[]`)))

	err := r.MarkStarted(context.Background(), key)
	require.Error(t, err)
	assert.True(t, errors.Is(err, steps.ErrStepAlreadyCompleted))

	// NEW fingerprint on the same step_key: fresh row, prior completed row stays.
	newKey := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-B"}
	require.NoError(t, r.MarkStarted(context.Background(), newKey))

	rows, err := r.ListByJob(context.Background(), "j-1")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, steps.StatusCompleted, rows[0].Status)
	assert.Equal(t, steps.StatusPending, rows[1].Status)
}

func TestRepo_MarkStarted_EmptyKeyFailsLoudly(t *testing.T) {
	r := newTestRepo(t)
	err := r.MarkStarted(context.Background(), steps.StepKey{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, steps.ErrInvalidStepKey))
}

// ── MarkCompleted contracts ────────────────────────────────────

func TestRepo_MarkCompleted_HappyPath(t *testing.T) {
	r := newTestRepo(t)
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	require.NoError(t, r.MarkStarted(context.Background(), key))
	require.NoError(t, r.MarkCompleted(context.Background(), key,
		[]byte(`{"drive_link":"x"}`), []byte(`[{"kind":"chunk"}]`)))

	rows, err := r.ListByJob(context.Background(), "j-1")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, steps.StatusCompleted, rows[0].Status)
	assert.JSONEq(t, `{"drive_link":"x"}`, string(rows[0].Result))
	assert.JSONEq(t, `[{"kind":"chunk"}]`, string(rows[0].ArtifactRefs))
	assert.False(t, rows[0].CompletedAt.IsZero(), "CompletedAt must be stamped")
}

func TestRepo_MarkCompleted_PreStartReturnsNotFound(t *testing.T) {
	r := newTestRepo(t)
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	err := r.MarkCompleted(context.Background(), key, []byte(`{}`), []byte(`[]`))
	require.Error(t, err)
	assert.True(t, errors.Is(err, steps.ErrStepNotFound))
}

func TestRepo_MarkCompleted_IdempotentOnSamePayload(t *testing.T) {
	r := newTestRepo(t)
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	require.NoError(t, r.MarkStarted(context.Background(), key))
	require.NoError(t, r.MarkCompleted(context.Background(), key, []byte(`{"x":1}`), []byte(`[]`)))

	rows, err := r.ListByJob(context.Background(), "j-1")
	require.NoError(t, err)
	originalCompletedAt := rows[0].CompletedAt

	require.NoError(t, r.MarkCompleted(context.Background(), key, []byte(`{"x":1}`), []byte(`[]`)))

	rows, err = r.ListByJob(context.Background(), "j-1")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, originalCompletedAt, rows[0].CompletedAt,
		"idempotent re-completion must NOT bump CompletedAt")
}

func TestRepo_MarkCompleted_DifferentPayloadReturnsAlreadyCompleted(t *testing.T) {
	r := newTestRepo(t)
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	require.NoError(t, r.MarkStarted(context.Background(), key))
	require.NoError(t, r.MarkCompleted(context.Background(), key, []byte(`{"x":1}`), []byte(`[]`)))

	err := r.MarkCompleted(context.Background(), key, []byte(`{"x":2}`), []byte(`[]`))
	require.Error(t, err)
	assert.True(t, errors.Is(err, steps.ErrStepAlreadyCompleted),
		"re-completion with different payload must surface ErrStepAlreadyCompleted")
}

// ── MarkFailed contracts ────────────────────────────────────────

func TestRepo_MarkFailed_HappyPath(t *testing.T) {
	r := newTestRepo(t)
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	require.NoError(t, r.MarkStarted(context.Background(), key))
	require.NoError(t, r.MarkFailed(context.Background(), key, "transient network"))

	rows, err := r.ListByJob(context.Background(), "j-1")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, steps.StatusFailed, rows[0].Status)
	assert.Equal(t, "transient network", rows[0].LastError)
	assert.False(t, rows[0].CompletedAt.IsZero(), "CompletedAt fails alongside status flip")
}

func TestRepo_MarkFailed_OnCompletedReturnsAlreadyCompleted(t *testing.T) {
	r := newTestRepo(t)
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	require.NoError(t, r.MarkStarted(context.Background(), key))
	require.NoError(t, r.MarkCompleted(context.Background(), key, []byte(`{}`), []byte(`[]`)))

	err := r.MarkFailed(context.Background(), key, "should not be allowed")
	require.Error(t, err)
	assert.True(t, errors.Is(err, steps.ErrStepAlreadyCompleted))
}

func TestRepo_MarkFailed_PreStartReturnsNotFound(t *testing.T) {
	r := newTestRepo(t)
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	err := r.MarkFailed(context.Background(), key, "should not be allowed")
	require.Error(t, err)
	assert.True(t, errors.Is(err, steps.ErrStepNotFound))
}

func TestRepo_MarkFailed_FailedThenCompleted_OK(t *testing.T) {
	r := newTestRepo(t)
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	require.NoError(t, r.MarkStarted(context.Background(), key))
	require.NoError(t, r.MarkFailed(context.Background(), key, "transient"))
	// Retry after the failure: marker has been re-touched by MarkStarted
	// before MarkCompleted (the canonical pipeline-restart flow).
	require.NoError(t, r.MarkStarted(context.Background(), key))
	require.NoError(t, r.MarkCompleted(context.Background(), key, []byte(`{"v":"retry"}`), []byte(`[]`)))

	rows, err := r.ListByJob(context.Background(), "j-1")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, steps.StatusCompleted, rows[0].Status)
}

// ── FirstNonCompleted ────────────────────────────────────────────

func TestRepo_FirstNonCompleted_AllCompleteReturnsNil(t *testing.T) {
	r := newTestRepo(t)
	for _, key := range []steps.StepKey{
		{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"},
		{JobID: "j-1", StepKey: "02_render", InputFingerprint: "fp-A"},
		{JobID: "j-1", StepKey: "03_upload", InputFingerprint: "fp-A"},
	} {
		require.NoError(t, r.MarkStarted(context.Background(), key))
		require.NoError(t, r.MarkCompleted(context.Background(), key, []byte(`{}`), []byte(`[]`)))
	}

	got, err := r.FirstNonCompleted(context.Background(), "j-1")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestRepo_FirstNonCompleted_PicksLexicalSmallestPending(t *testing.T) {
	r := newTestRepo(t)
	// Insert in REVERSE lexical order.
	keys := []steps.StepKey{
		{JobID: "j-1", StepKey: "03_upload", InputFingerprint: "fp-A"},
		{JobID: "j-1", StepKey: "02_render", InputFingerprint: "fp-A"},
		{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"},
	}
	for _, k := range keys {
		require.NoError(t, r.MarkStarted(context.Background(), k))
	}
	require.NoError(t, r.MarkCompleted(context.Background(), keys[0], []byte(`{}`), []byte(`[]`)))
	require.NoError(t, r.MarkCompleted(context.Background(), keys[1], []byte(`{}`), []byte(`[]`)))
	// keys[2] = 01_stage stays PENDING.

	got, err := r.FirstNonCompleted(context.Background(), "j-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "01_stage", got.StepKey, "lexically smallest pending step is 01_stage")
	assert.Equal(t, steps.StatusPending, got.Status)
}

func TestRepo_FirstNonCompleted_PicksLatestFingerprintWhenSuperSuperseded(t *testing.T) {
	r := newTestRepo(t)
	keyA := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	keyB := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-B"}
	require.NoError(t, r.MarkStarted(context.Background(), keyA))
	require.NoError(t, r.MarkCompleted(context.Background(), keyA, []byte(`{"v":"A"}`), []byte(`[]`)))
	require.NoError(t, r.MarkStarted(context.Background(), keyB))
	require.NoError(t, r.MarkFailed(context.Background(), keyB, "transient"))

	got, err := r.FirstNonCompleted(context.Background(), "j-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "01_stage", got.StepKey)
	assert.Equal(t, steps.StatusFailed, got.Status,
		"LATEST fingerprint wins (fp-B failed) over older completed fp-A")
	assert.Equal(t, "fp-B", got.Fingerprint)
}

func TestRepo_FirstNonCompleted_UnknownJobReturnsNil(t *testing.T) {
	r := newTestRepo(t)
	got, err := r.FirstNonCompleted(context.Background(), "unknown-job")
	require.NoError(t, err)
	assert.Nil(t, got)
}

// ── ListByJob ────────────────────────────────────────────────────

func TestRepo_ListByJob_OrdersByStepKeyThenID(t *testing.T) {
	r := newTestRepo(t)
	// Insertion IDs: 1=02_render/fp-A, 2=01_stage/fp-B, 3=02_render/fp-C,
	// 4=01_stage/fp-A, 5=j-other/01_stage/fp-A (different job → filtered).
	for _, k := range []steps.StepKey{
		{JobID: "j-1", StepKey: "02_render", InputFingerprint: "fp-A"},
		{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-B"},
		{JobID: "j-1", StepKey: "02_render", InputFingerprint: "fp-C"},
		{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"},
		{JobID: "j-other", StepKey: "01_stage", InputFingerprint: "fp-A"}, // different job
	} {
		require.NoError(t, r.MarkStarted(context.Background(), k))
	}

	rows, err := r.ListByJob(context.Background(), "j-1")
	require.NoError(t, err)
	require.Len(t, rows, 4)
	// Canonical ORDER BY step_key ASC, id ASC: (01_stage id=2 fp-B) →
	// (01_stage id=4 fp-A) → (02_render id=1 fp-A) → (02_render id=3 fp-C).
	// Fingerprint is NOT a sort key (only the canonical fingerprint
	// within an `(id, step_key)` tuple matters; fingerprint invariant
	// is preserved via the UNIQUE INDEX).
	for i, w := range []struct{ stepKey, fp string }{
		{"01_stage", "fp-B"},
		{"01_stage", "fp-A"},
		{"02_render", "fp-A"},
		{"02_render", "fp-C"},
	} {
		assert.Equal(t, w.stepKey, rows[i].StepKey, "row %d step_key", i)
		assert.Equal(t, w.fp, rows[i].Fingerprint, "row %d fingerprint", i)
	}
}

func TestRepo_ListByJob_UnknownJobReturnsEmpty(t *testing.T) {
	r := newTestRepo(t)
	rows, err := r.ListByJob(context.Background(), "unknown-job")
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestRepo_ListByJob_Roundtrip_ResultAndArtifactRefs(t *testing.T) {
	r := newTestRepo(t)
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	require.NoError(t, r.MarkStarted(context.Background(), key))
	resultPayload := json.RawMessage(`{"drive_link":"https://drive.google.com/x","size_bytes":1024}`)
	artifactRefs := json.RawMessage(`[{"kind":"chunk","id":"ch-1"}]`)
	require.NoError(t, r.MarkCompleted(context.Background(), key, resultPayload, artifactRefs))

	rows, err := r.ListByJob(context.Background(), "j-1")
	require.NoError(t, err)
	require.Len(t, rows, 1)

	var got map[string]any
	require.NoError(t, json.Unmarshal(rows[0].Result, &got))
	assert.Equal(t, "https://drive.google.com/x", got["drive_link"])
	assert.EqualValues(t, 1024, got["size_bytes"])

	var arts []map[string]any
	require.NoError(t, json.Unmarshal(rows[0].ArtifactRefs, &arts))
	require.Len(t, arts, 1)
	assert.Equal(t, "chunk", arts[0]["kind"])
	assert.Equal(t, "ch-1", arts[0]["id"])
}
