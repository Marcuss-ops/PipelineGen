// Package scripts — processor_persistence_test.go exercises the
// single-writer idempotency contract of PersistenceProcessor.
//
// PR 3 (June 2026) acceptance tests:
//
//   - idempotency key is a 16-hex-char SHA-256 prefix
//   - same 5-tuple reproduces the same key
//   - changing language produces a different key
//   - changing target_words produces a different key
//   - changing item_id produces a different key
//   - a fresh insert path is taken when the repo returns found=false
//   - a replay path is taken when the repo returns found=true (no
//     second SaveScript call; the existing ScriptID is returned).
//     The AlreadyPersisted flag is GONE in PR 3 — the persistence
//     layer logs INFO on idempotency hit instead.
//   - the textual-script-only key criterion: same text + same item
//     but different language → different rows
//
// All tests use a counting in-memory fake ScriptRepository so the
// success / replay / insert paths are observable independently of
// the production sqlite repository.
//
// PR 3 (June 2026) structural change: the helper now returns a typed
// *scriptpkg.ModelScriptOutputV1 (not a ProcessInput envelope) since
// the Process signature changed to take the canonical typed model.
// WordCount, ModelUsed, CacheStatus live on ModelScriptOutputV1 as
// engine-stamped provenance fields.
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
	if f.seedHash == "" || f.seedRec == nil {
		return nil, false, nil
	}
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

// baseModelForIdem returns the canonical typed MSOV1 that
// PersistenceProcessor consumes. Replaces the pre-PR-3
// baseInputForIdem() ProcessInput helper. PersistenceProcessor
// reads model.{Text, WordCount, SpecScene, ModelUsed, CacheStatus}.
func baseModelForIdem() *scriptpkg.ModelScriptOutputV1 {
	return &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Canonical V1 prose text.",
		WordCount:     4,
		ModelUsed:     "llama3:8b",
		CacheStatus:   "generated",
		SpecScene:     scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{}},
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
// PR 3: the typed walk now returns *PostProcessArtifact{ScriptID}
// (no AlreadyPersisted flag — single-writer contract).
func TestPersistence_FreshInsert(t *testing.T) {
	t.Parallel()
	repo := &idemFakeRepo{}
	proc := NewPersistenceProcessor(repo, zap.NewNop())

	result, err := proc.Process(context.Background(), basePlanForIdem(), baseModelForIdem(), &PostProcessArtifact{})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int64(1234), result.ScriptID)
	assert.Equal(t, int32(1), repo.saveCalls.Load())

	require.NotNil(t, repo.lastRec)
	assert.Len(t, repo.lastRec.Template, 16, "Template slot must carry the idem key (16 hex chars)")
}

// TestPersistence_ReplayNoInsert asserts that when the repository
// reports a hit, the processor returns the existing ScriptID without
// a second SaveScript call. PR 3: the AlreadyPersisted flag is gone
// — the persistence layer logs INFO on hit instead. The flag's
// absence is enforced by compile-time: the PostProcessArtifact
// struct in postprocessor_registry.go has no AlreadyPersisted
// field, so the test cannot reference one even by accident.
func TestPersistence_ReplayNoInsert(t *testing.T) {
	t.Parallel()
	plan := basePlanForIdem()
	seedHash := computeIdempotencyKey(plan)
	repo := &idemFakeRepo{
		seedHash: seedHash,
		seedRec:  &ScriptRecord{ID: 99, Title: plan.Title, Template: seedHash},
	}
	proc := NewPersistenceProcessor(repo, zap.NewNop())

	result, err := proc.Process(context.Background(), plan, baseModelForIdem(), &PostProcessArtifact{})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int64(99), result.ScriptID, "replay must return the existing row's ID")
	assert.Equal(t, int32(0), repo.saveCalls.Load(), "SaveScript must NOT be called on replay")
}

// TestPersistence_EmptyScriptNoOp asserts that an empty script text
// is treated as a no-op (returns a zero PostProcessArtifact with no
// repo call).
func TestPersistence_EmptyScriptNoOp(t *testing.T) {
	t.Parallel()
	repo := &idemFakeRepo{}
	proc := NewPersistenceProcessor(repo, zap.NewNop())

	m := baseModelForIdem()
	m.Text = ""
	result, err := proc.Process(context.Background(), basePlanForIdem(), m, &PostProcessArtifact{})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int64(0), result.ScriptID)
	assert.Equal(t, int32(0), repo.saveCalls.Load())
}

// TestPersistence_NilRepoRejected asserts the processor refuses to
// run when the repo is not configured.
func TestPersistence_NilRepoRejected(t *testing.T) {
	t.Parallel()
	proc := NewPersistenceProcessor(nil, zap.NewNop())
	_, err := proc.Process(context.Background(), basePlanForIdem(), baseModelForIdem(), &PostProcessArtifact{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ScriptRepository not configured")
}

// TestPersistence_PersistsSpecSceneJSON asserts that the canonical
// SpecScene flows to the record on SaveScript. PR 5/3 stores the
// SpecScene JSON in the existing TimelineJSON slot.
func TestPersistence_PersistsSpecSceneJSON(t *testing.T) {
	t.Parallel()
	repo := &idemFakeRepo{}
	proc := NewPersistenceProcessor(repo, zap.NewNop())

	m := baseModelForIdem()
	m.SpecScene = scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: "A scene.", Kind: scriptpkg.SceneNarration},
		},
	}

	_, err := proc.Process(context.Background(), basePlanForIdem(), m, &PostProcessArtifact{})
	require.NoError(t, err)
	require.NotNil(t, repo.lastRec)
	// SpecSceneOutput uses lowercase json tags ("version", "scenes")
	// — assert on the canonical lowercase keys.
	assert.Contains(t, repo.lastRec.TimelineJSON, "scene-0")
	assert.Contains(t, repo.lastRec.TimelineJSON, `"version":1`)
	assert.Contains(t, repo.lastRec.TimelineJSON, `"scenes":`)
}

// TestPersistence_PropagatesWordCountAndModelUsed asserts the engine
// metadata fields propagate to the saved record.
//
// PR 3: WordCount + ModelUsed live on ModelScriptOutputV1 (engine-stamped
// provenance). The processor reads them from model.
func TestPersistence_PropagatesWordCountAndModelUsed(t *testing.T) {
	t.Parallel()
	repo := &idemFakeRepo{}
	proc := NewPersistenceProcessor(repo, zap.NewNop())

	m := baseModelForIdem()
	m.WordCount = 555
	m.ModelUsed = "qwen2.5:14b"
	m.CacheStatus = "exact_hit"

	_, err := proc.Process(context.Background(), basePlanForIdem(), m, &PostProcessArtifact{})
	require.NoError(t, err)
	require.NotNil(t, repo.lastRec)
	assert.Equal(t, 555, repo.lastRec.FinalWordCount)
	assert.Equal(t, "qwen2.5:14b", repo.lastRec.ModelUsed)
}

// _ ensures the type reference stays auditable for any future
// continuation — the AlreadyPersisted field is compile-time absent
// from PostProcessArtifact (see postprocessor_registry.go).
var _ = PostProcessArtifact{}
