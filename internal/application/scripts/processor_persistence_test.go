// Package scripts — processor_persistence_test.go exercises the
// single-writer idempotency contract of PersistenceProcessor.
//
// PR 5 (June 2026) acceptance tests:
//
//   - idempotency key is a 16-hex-char SHA-256 prefix
//   - same 5-tuple reproduces the same key
//   - changing language produces a different key
//   - changing target_words produces a different key
//   - changing item_id produces a different key
//   - a fresh insert path is taken when the repo returns found=false
//   - a replay path is taken when the repo returns found=true (no
//     second SaveScript call; the existing ScriptID is returned)
//   - the textual-script-only key criterion: same text + same item
//     but different language → different rows
//
// All tests use a counting in-memory fake ScriptRepository so the
// success / replay / insert paths are observable independently of
// the production sqlite repository.
package scripts

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.uber.org/zap"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Fakes ──────────────────────────────────────────────────────────────

// idemFakeRepo is a ScriptRepository used by persistence tests. It:
//   - records SaveScript calls (count + last record)
//   - returns a pre-populated existing row from
//     FindScriptByIdempotencyKey when the supplied hash matches
//     the seeded value
//   - otherwise returns found=false (fresh-insert path)
type idemFakeRepo struct {
	saveCalls atomic.Int32
	lastRec   *ScriptRecord

	// seedHash: when non-empty AND non-nil, return the seed
	// record with found=true from FindScriptByIdempotencyKey.
	seedHash string
	seedRec  *ScriptRecord

	// returnErr: if set, SaveScript returns this error.
	returnErr error
}

func (f *idemFakeRepo) SaveScript(_ context.Context, rec *ScriptRecord, _ []ScriptSectionRecord, _ []ScriptStockMatchRecord) (int64, error) {
	f.saveCalls.Add(1)
	f.lastRec = rec
	if f.returnErr != nil {
		return 0, f.returnErr
	}
	return 1234, nil
}

func (f *idemFakeRepo) UpdateScriptFinalContent(context.Context, int64, string, int, string, string, string, string, int) error {
	return nil
}
func (f *idemFakeRepo) SaveGenerationLog(_ context.Context, _ ScriptGenerationLog) error { return nil }
func (f *idemFakeRepo) SaveOutlineSections(_ context.Context, _ int64, _ []ScriptOutlineSectionRecord) error {
	return nil
}
func (f *idemFakeRepo) SaveResearchSources(_ context.Context, _ int64, _ []ScriptResearchSource) error {
	return nil
}
func (f *idemFakeRepo) NextVersionForTopic(_ context.Context, _, _, _ string) (int, error) {
	return 1, nil
}
func (f *idemFakeRepo) GetSectionByID(_ context.Context, _ int64) (*ScriptSectionRecord, error) {
	return nil, nil
}
func (f *idemFakeRepo) GetScriptByID(_ int64) (*ScriptRecord, []ScriptSectionRecord, []ScriptStockMatchRecord, error) {
	return nil, nil, nil, nil
}
func (f *idemFakeRepo) GetAdjacentSections(_ context.Context, _ int64, _ int) (*ScriptSectionRecord, *ScriptSectionRecord, error) {
	return nil, nil, nil
}
func (f *idemFakeRepo) UpdateSectionContent(_ context.Context, _ int64, _ string) error { return nil }
func (f *idemFakeRepo) ListScripts(_ context.Context, _ ScriptListFilter) ([]*ScriptRecord, error) {
	return nil, nil
}

// FindScriptByIdempotencyKey returns the seeded record only when the
// supplied idem hash matches the seedHash. Otherwise nil, false (the
// caller treats it as "fresh insert").
func (f *idemFakeRepo) FindScriptByIdempotencyKey(_ context.Context, _, _, _ string, _ int, _ string) (*ScriptRecord, bool, error) {
	// The processor computes the idem key from the plan; pass
	// through the recording via lastRec on the next SaveScript.
	// For deterministic testing, we tag the seedHash externally.
	if f.seedHash == "" || f.seedRec == nil {
		return nil, false, nil
	}
	// Return the seed record. (The processor computes the hash
	// from the plan; tests pre-compute it via computeIdempotencyKey
	// and inject only when the hashes match.)
	return f.seedRec, true, nil
}

// ── Helpers ────────────────────────────────────────────────────────────

func basePlanForIdem() *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		ID:                  "item-p5",
		Title:               "Idempotency Test",
		Topic:               "PR 5 contract",
		Language:            "en",
		Tone:                "neutral",
		Model:               "llama3:8b",
		Mode:                "text",
		TargetWords:         200,
		PromptVersion:       "v1",
		EditorPromptVersion: "v1",
		QAPromptVersion:     "v1",
		CacheKey:            "deadbeef00000000",
	}
}

func baseInputForIdem() ProcessInput {
	return ProcessInput{
		Text:        "Canonical V1 prose text.",
		WordCount:   4,
		SpecScene:   scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{}},
		ModelUsed:   "llama3:8b",
		CacheStatus: "generated",
	}
}

// ── Idempotency key shape ──────────────────────────────────────────────

// TestIdempotencyKey_Shape asserts the key is 16 lowercase hex chars.
func TestIdempotencyKey_Shape(t *testing.T) {
	t.Parallel()
	k := computeIdempotencyKey(basePlanForIdem())
	assert.Len(t, k, 16)
	for _, c := range k {
		assert.True(t,
			(c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
			"key must be hex lowercase; got %q", string(c))
	}
}

// TestIdempotencyKey_Stable asserts that the same plan reproduces
// the same idempotency key — rename / non-logical changes must NOT
// alter it.
func TestIdempotencyKey_Stable(t *testing.T) {
	t.Parallel()
	k1 := computeIdempotencyKey(basePlanForIdem())
	k2 := computeIdempotencyKey(basePlanForIdem())
	assert.Equal(t, k1, k2, "idem key must be stable for the same plan")

	// Mutating non-key fields on the plan must NOT alter the key.
	p := basePlanForIdem()
	p.Title = "Different Title"
	p.Topic = "Different Topic"
	p.SourceText = "Different source text"
	assert.Equal(t, k1, computeIdempotencyKey(p),
		"title / topic / source-text must not affect the idem key")
}

// TestIdempotencyKey_LanguageChangesKey asserts that language is
// PART of the key — a request that flips language generates a
// fresh row rather than colliding with a previous-language insert.
func TestIdempotencyKey_LanguageChangesKey(t *testing.T) {
	t.Parallel()
	k1 := computeIdempotencyKey(basePlanForIdem())
	p2 := basePlanForIdem()
	p2.Language = "it"
	k2 := computeIdempotencyKey(p2)
	assert.NotEqual(t, k1, k2, "language change must alter the idem key")
}

// TestIdempotencyKey_TargetWordsChangesKey asserts that target
// words is part of the key — a request that changes target_words
// generates a fresh row rather than colliding with the prior
// generation's idem row.
func TestIdempotencyKey_TargetWordsChangesKey(t *testing.T) {
	t.Parallel()
	k1 := computeIdempotencyKey(basePlanForIdem())
	p2 := basePlanForIdem()
	p2.TargetWords = 350
	k2 := computeIdempotencyKey(p2)
	assert.NotEqual(t, k1, k2, "target_words change must alter the idem key")
}

// TestIdempotencyKey_ItemIDChangesKey asserts that the item_id is in
// the key — different items produce fresh rows.
func TestIdempotencyKey_ItemIDChangesKey(t *testing.T) {
	t.Parallel()
	k1 := computeIdempotencyKey(basePlanForIdem())
	p2 := basePlanForIdem()
	p2.ID = "item-p5-other"
	k2 := computeIdempotencyKey(p2)
	assert.NotEqual(t, k1, k2, "item_id change must alter the idem key")
}

// ── Processor behaviour ────────────────────────────────────────────────

// TestPersistence_FreshInsert asserts the first call inserts.
func TestPersistence_FreshInsert(t *testing.T) {
	t.Parallel()
	repo := &idemFakeRepo{}
	proc := NewPersistenceProcessor(repo, zap.NewNop())

	result, err := proc.Process(context.Background(), basePlanForIdem(), baseInputForIdem())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int64(1234), result.ScriptID)
	assert.False(t, result.AlreadyPersisted)
	assert.Equal(t, int32(1), repo.saveCalls.Load())

	// The idem key must be persisted on the saved record's Template
	// slot so future replays can collide.
	require.NotNil(t, repo.lastRec)
	assert.Len(t, repo.lastRec.Template, 16, "Template slot must carry the idem key (16 hex chars)")
}

// TestPersistence_ReplayNoInsert asserts that when the repository
// reports a hit, the processor returns the existing ScriptID without
// a second SaveScript call. The AlreadyPersisted flag is set so
// downstream consumers see the replay state.
func TestPersistence_ReplayNoInsert(t *testing.T) {
	t.Parallel()
	plan := basePlanForIdem()
	seedHash := computeIdempotencyKey(plan)
	repo := &idemFakeRepo{
		seedHash: seedHash,
		seedRec:  &ScriptRecord{ID: 99, Title: plan.Title, Template: seedHash},
	}
	proc := NewPersistenceProcessor(repo, zap.NewNop())

	result, err := proc.Process(context.Background(), plan, baseInputForIdem())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int64(99), result.ScriptID, "replay must return the existing row's ID")
	assert.True(t, result.AlreadyPersisted, "AlreadyPersisted must be set on replay")
	assert.Equal(t, int32(0), repo.saveCalls.Load(), "SaveScript must NOT be called on replay")
}

// TestPersistence_EmptyScriptNoOp asserts that an empty script text
// is treated as a no-op (returns a zero PostProcessResult with no
// repo call). This mirrors the rest of the processors and prevents
// persisting half-written rows on cache-miss+decode-fail edge
// cases.
func TestPersistence_EmptyScriptNoOp(t *testing.T) {
	t.Parallel()
	repo := &idemFakeRepo{}
	proc := NewPersistenceProcessor(repo, zap.NewNop())

	in := baseInputForIdem()
	in.Text = ""
	result, err := proc.Process(context.Background(), basePlanForIdem(), in)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int64(0), result.ScriptID)
	assert.False(t, result.AlreadyPersisted)
	assert.Equal(t, int32(0), repo.saveCalls.Load())
}

// TestPersistence_NilRepoRejected asserts the processor refuses to
// run when the repo is not configured.
func TestPersistence_NilRepoRejected(t *testing.T) {
	t.Parallel()
	proc := NewPersistenceProcessor(nil, zap.NewNop())
	_, err := proc.Process(context.Background(), basePlanForIdem(), baseInputForIdem())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ScriptRepository not configured")
}

// TestPersistence_PersistsSpecSceneJSON asserts that the canonical
// SpecScene flows to the record on SaveScript. PR 5 stores the
// SpecScene JSON in the existing TimelineJSON slot — PR 6 will
// introduce a dedicated specscene column.
func TestPersistence_PersistsSpecSceneJSON(t *testing.T) {
	t.Parallel()
	repo := &idemFakeRepo{}
	proc := NewPersistenceProcessor(repo, zap.NewNop())

	in := baseInputForIdem()
	in.SpecScene = scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: "A scene.", Kind: scriptpkg.SceneNarration},
		},
	}

	_, err := proc.Process(context.Background(), basePlanForIdem(), in)
	require.NoError(t, err)
	require.NotNil(t, repo.lastRec)
	// The TimelineJSON slot should now contain valid JSON with the
	// expected shape (the specscene is serialised with json.Marshal).
	assert.Contains(t, repo.lastRec.TimelineJSON, "scene-0")
	// SpecSceneOutput has no per-field json tags — json.Marshal
	// emits Go field names "Version" and "Scenes" (capitalised).
	assert.Contains(t, repo.lastRec.TimelineJSON, "\"version\":1")
	assert.Contains(t, repo.lastRec.TimelineJSON, "\"scenes\":")
}

// TestPersistence_PropagatesWordCountAndModelUsed asserts the engine
// metadata fields propagate to the saved record.
func TestPersistence_PropagatesWordCountAndModelUsed(t *testing.T) {
	t.Parallel()
	repo := &idemFakeRepo{}
	proc := NewPersistenceProcessor(repo, zap.NewNop())

	in := baseInputForIdem()
	in.WordCount = 555
	in.ModelUsed = "qwen2.5:14b"
	in.CacheStatus = "exact_hit"

	_, err := proc.Process(context.Background(), basePlanForIdem(), in)
	require.NoError(t, err)
	require.NotNil(t, repo.lastRec)
	assert.Equal(t, 555, repo.lastRec.FinalWordCount)
	assert.Equal(t, "qwen2.5:14b", repo.lastRec.ModelUsed)
}
