// Package steps_test pins the canonical Store contract via hermetic tests
// (PipelineGen Stock Cutover §12-3, July 2026).
//
// godlike/06 SSOT: tests use FakeStore (an in-memory implementation that
// satisfies Store via Go's duck-typing). No `database/sql` is touched in
// this file; the SQLite concrete has its own integration tests in
// internal/platform/sqlite/executionsteps/repository_test.go.
//
// godlike/07 typed-error contract: every test asserts sentinel errors
// via errors.Is() so a future port evolution that wraps the error in
// additional context (e.g. fmt.Errorf("...: %w", ErrStepAlreadyCompleted))
// does not break the audit-pin surface.
package execution

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
)

// ── FakeStore ────────────────────────────────────────────────────
//
// In-memory implementation of Store for hermetic port contract testing.
// Pure-Go (no SQLite), safe for concurrent use via sync.Mutex.
// The fake operates in the SAME conceptual model as the SQLite concrete:
// Design A (per-row canonical, multiple rows per (jobID, stepKey) on
// fingerprint change). All transitions (Mark* methods) follow the same
// rules as the SQLite port (see store.go doc-comment + repository.go).
// ─────────────────────────────────────────────────────────────────

type fakeStep struct {
	JobID        string
	StepKey      string
	Fingerprint  string
	Status       steps.StepStatus
	Attempt      int
	Result       json.RawMessage
	ArtifactRefs json.RawMessage
	StartedAt    time.Time
	CompletedAt  time.Time
	LastError    string
	id           int64 // monotonic insertion order
}

type FakeStore struct {
	mu     sync.Mutex
	rows   []*fakeStep
	nextID int64
}

// Compile-time assertion: FakeStore satisfies the canonical Store port.
var _ steps.Store = (*FakeStore)(nil)

func (f *FakeStore) snapshot() []fakeStep {
	out := make([]fakeStep, len(f.rows))
	for i, r := range f.rows {
		out[i] = *r
	}
	return out
}

func (f *FakeStore) findLatestByTriple(key steps.StepKey) *fakeStep {
	for i := len(f.rows) - 1; i >= 0; i-- {
		r := f.rows[i]
		if r.JobID == key.JobID && r.StepKey == key.StepKey && r.Fingerprint == key.InputFingerprint {
			return r
		}
	}
	return nil
}

func (f *FakeStore) MarkStarted(ctx context.Context, key steps.StepKey) error {
	if err := key.Validated(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	// Check for an existing completed row (per triple). The terminal
	// sink at Design A is COMPLETED — it's immutable even if the
	// caller uses the same fingerprint. The port's contract is to
	// return ErrStepAlreadyCompleted in that case.
	if existing := f.findLatestByTriple(key); existing != nil {
		if existing.Status == steps.StatusCompleted {
			return steps.ErrStepAlreadyCompleted
		}
		// Otherwise: bump attempt + reset status (non-completed idempotent reentry).
		existing.Attempt++
		existing.Status = steps.StatusPending
		existing.StartedAt = time.Now().UTC()
		existing.LastError = ""
		return nil
	}

	// No existing row: INSERT a new one.
	f.nextID++
	f.rows = append(f.rows, &fakeStep{
		id:           f.nextID,
		JobID:        key.JobID,
		StepKey:      key.StepKey,
		Fingerprint:  key.InputFingerprint,
		Status:       steps.StatusPending,
		Attempt:      1,
		StartedAt:    time.Now().UTC(),
		Result:       json.RawMessage("{}"),
		ArtifactRefs: json.RawMessage("[]"),
	})
	return nil
}

func (f *FakeStore) MarkCompleted(ctx context.Context, key steps.StepKey, result, artifactRefs json.RawMessage) error {
	if err := key.Validated(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	existing := f.findLatestByTriple(key)
	if existing == nil {
		return steps.ErrStepNotFound
	}
	if existing.Status == steps.StatusCompleted {
		// Idempotent re-completion ONLY if the result + artifact_refs
		// match byte-for-byte; otherwise terminal-immutability error.
		if string(existing.Result) == string(result) && string(existing.ArtifactRefs) == string(artifactRefs) {
			return nil
		}
		return steps.ErrStepAlreadyCompleted
	}
	existing.Status = steps.StatusCompleted
	existing.Result = result
	existing.ArtifactRefs = artifactRefs
	existing.CompletedAt = time.Now().UTC()
	existing.LastError = ""
	return nil
}

func (f *FakeStore) MarkFailed(ctx context.Context, key steps.StepKey, errMessage string) error {
	if err := key.Validated(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	existing := f.findLatestByTriple(key)
	if existing == nil {
		return steps.ErrStepNotFound
	}
	if existing.Status == steps.StatusCompleted {
		return steps.ErrStepAlreadyCompleted
	}
	existing.Status = steps.StatusFailed
	existing.LastError = errMessage
	existing.CompletedAt = time.Now().UTC()
	return nil
}

func (f *FakeStore) FirstNonCompleted(ctx context.Context, jobID string) (*steps.StepState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Find the most-recent row per (jobID, stepKey), then return
	// the first (lexically smallest stepKey) whose status != Completed.
	type latestBy struct {
		StepKey string
		Idx     int64
	}
	mapper := make(map[string]latestBy)
	for i, r := range f.rows {
		if r.JobID != jobID {
			continue
		}
		cur, ok := mapper[r.StepKey]
		if !ok || r.id > cur.Idx {
			mapper[r.StepKey] = latestBy{StepKey: r.StepKey, Idx: int64(i)}
		}
	}
	// Iterate by lexical order over the mapper keys (stdlib sort.Strings).
	keys := make([]string, 0, len(mapper))
	for k := range mapper {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		i := mapper[k].Idx
		r := f.rows[i]
		if r.Status != steps.StatusCompleted {
			return r.toStepState(), nil
		}
	}
	return nil, nil
}

func (f *FakeStore) ListByJob(ctx context.Context, jobID string) ([]steps.StepState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []steps.StepState
	for _, r := range f.rows {
		if r.JobID == jobID {
			out = append(out, *r.toStepState())
		}
	}
	// Mirror the SQLite concrete's `ORDER BY step_key ASC, id ASC` so
	// hermetic and integration tests agree on canonical order.
	sortStepsByKeyThenID(out)
	return out, nil
}

func (r *fakeStep) toStepState() *steps.StepState {
	return &steps.StepState{
		ID:           r.id,
		JobID:        r.JobID,
		StepKey:      r.StepKey,
		Fingerprint:  r.Fingerprint,
		Status:       r.Status,
		Attempt:      r.Attempt,
		Result:       append(json.RawMessage(nil), r.Result...),
		ArtifactRefs: append(json.RawMessage(nil), r.ArtifactRefs...),
		StartedAt:    r.StartedAt,
		CompletedAt:  r.CompletedAt,
		LastError:    r.LastError,
	}
}

// sortStepsByKeyThenID sorts in place by (StepKey ASC, ID ASC). Stable
// insertion keeps tie-breaks by id (since rows are appended in id-asc),
// matching the SQLite concrete's ORDER BY step_key ASC, id ASC clause.
func sortStepsByKeyThenID(steps []steps.StepState) {
	for i := 1; i < len(steps); i++ {
		for j := i; j > 0; j-- {
			prev, cur := steps[j-1], steps[j]
			if prev.StepKey > cur.StepKey || (prev.StepKey == cur.StepKey && prev.ID > cur.ID) {
				steps[j-1], steps[j] = cur, prev
				continue
			}
			break
		}
	}
}

// ── Test cases ───────────────────────────────────────────────────

func TestStepKey_Validated(t *testing.T) {
	t.Run("all fields set → nil", func(t *testing.T) {
		require.NoError(t, steps.StepKey{
			JobID: "j-1", StepKey: "01_stage", InputFingerprint: "abc",
		}.Validated())
	})
	t.Run("missing JobID names ALL missing fields in one diagnostic", func(t *testing.T) {
		err := steps.StepKey{StepKey: "01_stage", InputFingerprint: "abc"}.Validated()
		require.Error(t, err)
		assert.True(t, errors.Is(err, steps.ErrInvalidStepKey))
		assert.Contains(t, err.Error(), "JobID")
	})
	t.Run("all 3 missing lists all 3", func(t *testing.T) {
		err := steps.StepKey{}.Validated()
		require.Error(t, err)
		assert.True(t, errors.Is(err, steps.ErrInvalidStepKey))
		for _, want := range []string{"JobID", "StepKey", "InputFingerprint"} {
			assert.Contains(t, err.Error(), want)
		}
	})
}

func TestCanonicalStepStatusValues(t *testing.T) {
	values := steps.CanonicalStepStatusValues()
	require.Equal(t, []steps.StepStatus{
		steps.StatusPending, steps.StatusRunning,
		steps.StatusCompleted, steps.StatusFailed,
	}, values)
	for _, v := range values {
		assert.True(t, v.IsValid(), "closed set value %q must be IsValid", v)
	}
	// non-closed values must fail.
	for _, bogus := range []steps.StepStatus{"", "PENDING", "DONE", "aborted"} {
		assert.False(t, bogus.IsValid(), "non-closed value %q must fail IsValid", bogus)
	}
}

func TestFakeStore_MarkStarted_HappyPath(t *testing.T) {
	s := &FakeStore{}
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	require.NoError(t, s.MarkStarted(context.Background(), key))

	rows := s.snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, steps.StatusPending, rows[0].Status)
	assert.Equal(t, 1, rows[0].Attempt)
}

func TestFakeStore_MarkStarted_ReEntryBumpsAttempt(t *testing.T) {
	s := &FakeStore{}
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	require.NoError(t, s.MarkStarted(context.Background(), key))
	require.NoError(t, s.MarkStarted(context.Background(), key))

	rows := s.snapshot()
	require.Len(t, rows, 1, "same fingerprint should NOT INSERT a second row")
	assert.Equal(t, 2, rows[0].Attempt)
}

func TestFakeStore_MarkStarted_NewFingerprintInserts(t *testing.T) {
	s := &FakeStore{}
	keyA := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	keyB := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-B"}
	require.NoError(t, s.MarkStarted(context.Background(), keyA))
	require.NoError(t, s.MarkStarted(context.Background(), keyB))

	rows := s.snapshot()
	require.Len(t, rows, 2, "different fingerprint SHOULD INSERT a new row (audit trail)")
	// Rows in insertion order; latest carries fp-B.
	assert.Equal(t, "fp-A", rows[0].Fingerprint)
	assert.Equal(t, "fp-B", rows[1].Fingerprint)
}

func TestFakeStore_MarkStarted_CompletedIsTerminal(t *testing.T) {
	s := &FakeStore{}
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	require.NoError(t, s.MarkStarted(context.Background(), key))
	require.NoError(t, s.MarkCompleted(context.Background(), key, []byte(`{"drive_link":"x"}`), []byte(`[]`)))

	// Try re-MarkStarted on the COMPLETED triple → ErrStepAlreadyCompleted.
	err := s.MarkStarted(context.Background(), key)
	require.Error(t, err)
	assert.True(t, errors.Is(err, steps.ErrStepAlreadyCompleted))

	// But MarkStarted with NEW fingerprint on the same step_key should
	// still INSERT a new row (the prior completed row stays as audit).
	keyNew := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-B"}
	require.NoError(t, s.MarkStarted(context.Background(), keyNew))

	rows := s.snapshot()
	require.Len(t, rows, 2, "new fingerprint inserts a fresh row alongside the completed audit row")
	assert.Equal(t, steps.StatusCompleted, rows[0].Status)
	assert.Equal(t, steps.StatusPending, rows[1].Status)
}

func TestFakeStore_MarkCompleted_PreStartReturnsNotFound(t *testing.T) {
	s := &FakeStore{}
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	err := s.MarkCompleted(context.Background(), key, []byte(`{}`), []byte(`[]`))
	require.Error(t, err)
	assert.True(t, errors.Is(err, steps.ErrStepNotFound))
}

func TestFakeStore_MarkCompleted_IdempotentOnSamePayload(t *testing.T) {
	s := &FakeStore{}
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	require.NoError(t, s.MarkStarted(context.Background(), key))
	before := time.Now().UTC()
	require.NoError(t, s.MarkCompleted(context.Background(), key, []byte(`{"x":1}`), []byte(`[]`)))

	rows := s.snapshot()
	require.Len(t, rows, 1)
	originalCompletedAt := rows[0].CompletedAt
	originalAttempt := rows[0].Attempt

	// Ensure wall-clock has moved past `before` — assert idempotent
	// re-completion lands STRICTLY at-or-after our prior stamp but
	// the existing CompletedAt unchanged (no fresh stamp).
	after := time.Now().UTC()
	require.True(t, after.After(before), "monotonic-clock guard for idempotent-recom assert")

	// Same payload → idempotent re-completion; timestamp NOT bumped.
	require.NoError(t, s.MarkCompleted(context.Background(), key, []byte(`{"x":1}`), []byte(`[]`)))

	rows = s.snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, originalCompletedAt, rows[0].CompletedAt, "completed_at must NOT change on idempotent re-completion")
	assert.Equal(t, originalAttempt, rows[0].Attempt)
}

func TestFakeStore_MarkCompleted_DifferentPayloadReturnsAlreadyCompleted(t *testing.T) {
	s := &FakeStore{}
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	require.NoError(t, s.MarkStarted(context.Background(), key))
	require.NoError(t, s.MarkCompleted(context.Background(), key, []byte(`{"x":1}`), []byte(`[]`)))

	err := s.MarkCompleted(context.Background(), key, []byte(`{"x":2}`), []byte(`[]`))
	require.Error(t, err)
	assert.True(t, errors.Is(err, steps.ErrStepAlreadyCompleted))
}

func TestFakeStore_MarkFailed_OnCompletedReturnsAlreadyCompleted(t *testing.T) {
	s := &FakeStore{}
	key := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	require.NoError(t, s.MarkStarted(context.Background(), key))
	require.NoError(t, s.MarkCompleted(context.Background(), key, []byte(`{}`), []byte(`[]`)))

	err := s.MarkFailed(context.Background(), key, "should not be allowed")
	require.Error(t, err)
	assert.True(t, errors.Is(err, steps.ErrStepAlreadyCompleted))
}

func TestFakeStore_FirstNonCompleted_AllCompleteReturnsNil(t *testing.T) {
	s := &FakeStore{}
	for _, key := range []steps.StepKey{
		{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"},
		{JobID: "j-1", StepKey: "02_render", InputFingerprint: "fp-A"},
		{JobID: "j-1", StepKey: "03_upload", InputFingerprint: "fp-A"},
	} {
		require.NoError(t, s.MarkStarted(context.Background(), key))
		require.NoError(t, s.MarkCompleted(context.Background(), key, []byte(`{}`), []byte(`[]`)))
	}

	got, err := s.FirstNonCompleted(context.Background(), "j-1")
	require.NoError(t, err)
	assert.Nil(t, got, "all completed → FirstNonCompleted returns nil")
}

func TestFakeStore_FirstNonCompleted_PicksLexicallySmallest(t *testing.T) {
	s := &FakeStore{}
	// Insert in REVERSE lexical order to verify sort, not insertion-order.
	stages := []steps.StepKey{
		{JobID: "j-1", StepKey: "03_upload", InputFingerprint: "fp-A"},
		{JobID: "j-1", StepKey: "02_render", InputFingerprint: "fp-A"},
		{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"},
	}
	for _, k := range stages {
		require.NoError(t, s.MarkStarted(context.Background(), k))
	}
	// Only complete the LAST 2.
	require.NoError(t, s.MarkCompleted(context.Background(), stages[0], []byte(`{}`), []byte(`[]`)))
	require.NoError(t, s.MarkCompleted(context.Background(), stages[1], []byte(`{}`), []byte(`[]`)))
	// stages[2] = 01_stage stays PENDING.

	got, err := s.FirstNonCompleted(context.Background(), "j-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "01_stage", got.StepKey, "lexically smallest pending step is 01_stage")
	assert.Equal(t, steps.StatusPending, got.Status)
}

func TestFakeStore_FirstNonCompleted_PicksLatestFingerprint(t *testing.T) {
	s := &FakeStore{}
	// 01_stage with fp-A was COMPLETED earlier; fp-B is now FAILED.
	keyA := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"}
	keyB := steps.StepKey{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-B"}
	require.NoError(t, s.MarkStarted(context.Background(), keyA))
	require.NoError(t, s.MarkCompleted(context.Background(), keyA, []byte(`{"v":"A"}`), []byte(`[]`)))
	require.NoError(t, s.MarkStarted(context.Background(), keyB))
	require.NoError(t, s.MarkFailed(context.Background(), keyB, "transient"))

	got, err := s.FirstNonCompleted(context.Background(), "j-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "01_stage", got.StepKey)
	assert.Equal(t, steps.StatusFailed, got.Status, "LATEST fingerprint (fp-B = failed) wins over older completed fp-A")
	assert.Equal(t, "fp-B", got.Fingerprint)
}

func TestFakeStore_ListByJob_OrdersByStepKeyThenID(t *testing.T) {
	s := &FakeStore{}
	// Insert deliberately out of lexical order with multiple fingerprints.
	// IDs (in insertion order): 1=02_render/fp-A, 2=01_stage/fp-B, 3=02_render/fp-C,
	// 4=01_stage/fp-A, 5=j-other/01_stage/fp-A (different job → filtered).
	for _, k := range []steps.StepKey{
		{JobID: "j-1", StepKey: "02_render", InputFingerprint: "fp-A"},    // id 1
		{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-B"},     // id 2
		{JobID: "j-1", StepKey: "02_render", InputFingerprint: "fp-C"},    // id 3
		{JobID: "j-1", StepKey: "01_stage", InputFingerprint: "fp-A"},     // id 4
		{JobID: "j-other", StepKey: "01_stage", InputFingerprint: "fp-A"}, // id 5, filtered
	} {
		require.NoError(t, s.MarkStarted(context.Background(), k))
	}

	rows, err := s.ListByJob(context.Background(), "j-1")
	require.NoError(t, err)
	require.Len(t, rows, 4)
	// Canonical order: (01_stage, id=2 fp-B) → (01_stage, id=4 fp-A) → (02_render, id=1 fp-A) → (02_render, id=3 fp-C).
	// Sort key is (step_key ASC, id ASC); fingerprint is NOT a sort key.
	want := []struct {
		StepKey string
		FP      string
		ID      int64
	}{
		{"01_stage", "fp-B", 2},
		{"01_stage", "fp-A", 4},
		{"02_render", "fp-A", 1},
		{"02_render", "fp-C", 3},
	}
	for i, w := range want {
		assert.Equal(t, w.StepKey, rows[i].StepKey, "row %d step_key", i)
		assert.Equal(t, w.FP, rows[i].Fingerprint, "row %d fingerprint", i)
		assert.Equal(t, w.ID, rows[i].ID, "row %d id", i)
	}
}

func TestFakeStore_EmptyState_FirstNonCompleted(t *testing.T) {
	s := &FakeStore{}
	got, err := s.FirstNonCompleted(context.Background(), "unknown-job")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestFakeStore_EmptyState_ListByJob(t *testing.T) {
	s := &FakeStore{}
	got, err := s.ListByJob(context.Background(), "unknown-job")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestFakeStore_ValidatedErrorPropagation(t *testing.T) {
	s := &FakeStore{}
	err := s.MarkStarted(context.Background(), steps.StepKey{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, steps.ErrInvalidStepKey))

	err = s.MarkCompleted(context.Background(), steps.StepKey{}, []byte(`{}`), []byte(`[]`))
	require.Error(t, err)
	assert.True(t, errors.Is(err, steps.ErrInvalidStepKey))

	err = s.MarkFailed(context.Background(), steps.StepKey{}, "x")
	require.Error(t, err)
	assert.True(t, errors.Is(err, steps.ErrInvalidStepKey))
}
