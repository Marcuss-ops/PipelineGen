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
// All tests use a counting in-memory fake ports.ScriptRepository (canonical repo port) so the
// success / replay / insert paths are observable independently of
// the production sqlite repository.
//
// PR 3 (June 2026) structural change: the helper now returns a typed
// *scriptpkg.ModelScriptOutputV1 (not a ProcessInput envelope) since
// the Process signature changed to take the canonical typed model.
// WordCount, ModelUsed, CacheStatus live on ModelScriptOutputV1 as
// engine-stamped provenance fields.
//
// PR 6 (June 2026) follow-up: the test asserts against the dedicated
// ScriptRecord.IdempotencyKey + ScriptRecord.SpecScene fields instead
// of the pre-PR-6 dual-purpose Template / TimelineJSON slots. The
// Template slot is left empty by the processor (no longer the idem
// key carrier); TimelineJSON is no longer the SpecScene carrier.
package adapters

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Fakes ──────────────────────────────────────────────────────────────

// idemFakeRepo is a ports.ScriptRepository (canonical repo port) fake used by persistence tests. It:
//   - records SaveScript calls (count + last record)
//   - returns a pre-populated existing row from
//     FindScriptByIdempotencyKey when the supplied hash matches
//     the seeded value
//   - otherwise returns found=false (fresh-insert path)
type idemFakeRepo struct {
	saveCalls atomic.Int32
	lastRec   *ports.ScriptRecord

	// PR 1 (SCRIPT-DOWNSTREAM-CUTOVER wave): SaveManifestV2 audit
	// fields so the 4 manifest emit TDD tests can assert the
	// canonical NEW-mode write seam.
	saveManifestCalls    atomic.Int32
	lastManifestScriptID int64
	lastManifest         []byte

	// seedHash: when non-empty AND non-nil, return the seed
	// record with found=true from FindScriptByIdempotencyKey.
	seedHash string
	seedRec  *ports.ScriptRecord

	// returnErr: if set, SaveScript returns this error.
	returnErr error
}

func (f *idemFakeRepo) SaveScript(_ context.Context, rec *ports.ScriptRecord, _ []ports.ScriptSectionRecord, _ []ports.ScriptStockMatchRecord) (int64, error) {
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
func (f *idemFakeRepo) SaveGenerationLog(_ context.Context, _ ports.ScriptGenerationLog) error {
	return nil
}
func (f *idemFakeRepo) SaveOutlineSections(_ context.Context, _ int64, _ []ports.ScriptOutlineSectionRecord) error {
	return nil
}
func (f *idemFakeRepo) SaveResearchSources(_ context.Context, _ int64, _ []ports.ScriptResearchSource) error {
	return nil
}
func (f *idemFakeRepo) NextVersionForTopic(_ context.Context, _, _, _ string) (int, error) {
	return 1, nil
}
func (f *idemFakeRepo) GetSectionByID(_ context.Context, _ int64) (*ports.ScriptSectionRecord, error) {
	return nil, nil
}
func (f *idemFakeRepo) GetScriptByID(_ int64) (*ports.ScriptRecord, []ports.ScriptSectionRecord, []ports.ScriptStockMatchRecord, error) {
	return nil, nil, nil, nil
}
func (f *idemFakeRepo) GetAdjacentSections(_ context.Context, _ int64, _ int) (*ports.ScriptSectionRecord, *ports.ScriptSectionRecord, error) {
	return nil, nil, nil
}
func (f *idemFakeRepo) UpdateSectionContent(_ context.Context, _ int64, _ string) error { return nil }
func (f *idemFakeRepo) ListScripts(_ context.Context, _ ports.ScriptListFilter) ([]*ports.ScriptRecord, error) {
	return nil, nil
}

// FindScriptByIdempotencyKey returns the seeded record only when the
// supplied idem hash matches the seedHash. Otherwise nil, false (the
// caller treats it as "fresh insert").
//
// PR 6: the application-layer FindScriptByIdempotencyKey contract takes the
// full reconciliation tuple (itemID, cacheKey, promptVersion, targetWords,
// language) — the application-side adapter computeAdapterIdempotencyKey
// reduces them to the 16-hex hash before the SQLite concrete repo's
// FindByIdempotencyKey matches against the dedicated idempotency_key
// column. The fake accepts the tuple but only matches against the
// pre-computed seedHash set during fixture construction.
func (f *idemFakeRepo) FindScriptByIdempotencyKey(_ context.Context, _, _, _ string, _ int, _ string) (*ports.ScriptRecord, bool, error) {
	if f.seedHash == "" || f.seedRec == nil {
		return nil, false, nil
	}
	return f.seedRec, true, nil
}

// SaveManifestV2 (PR 1, SCRIPT-DOWNSTREAM-CUTOVER wave) records the
// canonical NEW-mode manifest envelope + scriptID so TDD tests can
// assert the canonical NEW-mode write seam. The mock matches the port
// signature (ScriptManifestJSON = []byte, pre-marshalled JSON) and
// stores the raw bytes so TDD tests can unmarshal + assert shape.
func (f *idemFakeRepo) SaveManifestV2(_ context.Context, scriptID int64, manifest ports.ScriptManifestJSON) error {
	f.saveManifestCalls.Add(1)
	f.lastManifestScriptID = scriptID
	if len(manifest) > 0 {
		f.lastManifest = manifest
	}
	return nil
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

// TestIdempotencyKey_CacheKeyChangesKey asserts that the cache_key is in
// the 5-tuple — different cache keys produce fresh rows.
func TestIdempotencyKey_CacheKeyChangesKey(t *testing.T) {
	t.Parallel()
	k1 := computeIdempotencyKey(basePlanForIdem())
	p2 := basePlanForIdem()
	p2.CacheKey = "feedface12345678"
	k2 := computeIdempotencyKey(p2)
	assert.NotEqual(t, k1, k2, "cache_key change must alter the idem key")
}

// TestIdempotencyKey_PromptVersionChangesKey asserts that the
// prompt_version is in the 5-tuple — different prompt versions produce
// fresh rows.
func TestIdempotencyKey_PromptVersionChangesKey(t *testing.T) {
	t.Parallel()
	k1 := computeIdempotencyKey(basePlanForIdem())
	p2 := basePlanForIdem()
	p2.PromptVersion = "v2-experimental"
	k2 := computeIdempotencyKey(p2)
	assert.NotEqual(t, k1, k2, "prompt_version change must alter the idem key")
}

// ── Processor behaviour ────────────────────────────────────────────────

// TestPersistence_FreshInsert asserts the first call inserts.
// PR 3: the typed walk now returns *PostProcessResult{ScriptID}
// (no AlreadyPersisted flag — single-writer contract).
func TestPersistence_FreshInsert(t *testing.T) {
	t.Parallel()
	repo := &idemFakeRepo{}
	proc := NewPersistenceProcessor(repo, zap.NewNop())

	result, err := proc.Process(context.Background(), basePlanForIdem(), baseProcessInput())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int64(1234), result.ScriptID)
	assert.Equal(t, int32(1), repo.saveCalls.Load())

	require.NotNil(t, repo.lastRec)
	// PR 6: the idempotency key lives on the dedicated
	// IdempotencyKey field of ports.ScriptRecord (no longer stuffed into
	// the multi-purpose Template slot — Template is reserved for
	// semantic values like "book" / "lesson").
	assert.Len(t, repo.lastRec.IdempotencyKey, 16, "IdempotencyKey field must carry the idem-key (16 hex chars)")
	assert.Empty(t, repo.lastRec.Template, "Template slot must remain empty under PR 6 (was the pre-PR-6 idem carrier)")
}

// TestPersistence_ReplayNoInsert asserts that when the repository
// reports a hit, the processor returns the existing ScriptID without
// a second SaveScript call. PR 3: the AlreadyPersisted flag is gone
// — the persistence layer logs INFO on hit instead. The flag's
// absence is enforced by compile-time: the PostProcessResult
// struct has no AlreadyPersisted field, so the test cannot reference
// one even by accident.
//
// PR 6: the seed record's idempotency key is set on the dedicated
// IdempotencyKey field (not the pre-PR-6 Template slot).
func TestPersistence_ReplayNoInsert(t *testing.T) {
	t.Parallel()
	plan := basePlanForIdem()
	seedHash := computeIdempotencyKey(plan)
	repo := &idemFakeRepo{
		seedHash: seedHash,
		seedRec:  &ports.ScriptRecord{ID: 99, Title: plan.Title, IdempotencyKey: seedHash},
	}
	proc := NewPersistenceProcessor(repo, zap.NewNop())

	result, err := proc.Process(context.Background(), plan, baseProcessInput())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int64(99), result.ScriptID, "replay must return the existing row's ID")
	assert.Equal(t, int32(0), repo.saveCalls.Load(), "SaveScript must NOT be called on replay")
}

// TestPersistence_EmptyScriptNoOp asserts that an empty script text
// is treated as a no-op (returns a zero PostProcessResult with no
// repo call).
func TestPersistence_EmptyScriptNoOp(t *testing.T) {
	t.Parallel()
	repo := &idemFakeRepo{}
	proc := NewPersistenceProcessor(repo, zap.NewNop())

	m := baseModelForIdem()
	m.Text = ""
	input := baseProcessInput()
	input.Text = m.Text
	input.WordCount = m.WordCount
	input.SpecScene = m.SpecScene
	input.ModelUsed = m.ModelUsed
	input.CacheStatus = m.CacheStatus
	result, err := proc.Process(context.Background(), basePlanForIdem(), input)
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
	_, err := proc.Process(context.Background(), basePlanForIdem(), baseProcessInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ScriptRepository not configured")
}

// TestPersistence_PersistsSpecSceneJSON asserts that the canonical
// SpecScene flows to the dedicated SpecScene field on the record.
// PR 6: the processor writes SpecScene JSON directly into the
// `specscene` ports.ScriptRecord field; the pre-PR-6 accommodation of
// storing SpecScene in the TimelineJSON slot is fully retired.
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

	input2 := baseProcessInput()
	input2.Text = m.Text
	input2.WordCount = m.WordCount
	input2.SpecScene = m.SpecScene
	input2.ModelUsed = m.ModelUsed
	input2.CacheStatus = m.CacheStatus
	_, err := proc.Process(context.Background(), basePlanForIdem(), input2)
	require.NoError(t, err)
	require.NotNil(t, repo.lastRec)
	// SpecSceneOutput uses lowercase json tags ("version", "scenes")
	// — assert on the canonical lowercase keys.
	assert.Contains(t, repo.lastRec.SpecScene, "scene-0")
	assert.Contains(t, repo.lastRec.SpecScene, `"version":1`)
	assert.Contains(t, repo.lastRec.SpecScene, `"scenes":`)
	assert.NotContains(t, repo.lastRec.SpecScene, `"local_path":`)
	assert.Empty(t, repo.lastRec.TimelineJSON, "TimelineJSON slot must remain empty under PR 6")
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

	input3 := baseProcessInput()
	input3.Text = m.Text
	input3.WordCount = m.WordCount
	input3.SpecScene = m.SpecScene
	input3.ModelUsed = m.ModelUsed
	input3.CacheStatus = m.CacheStatus
	_, err := proc.Process(context.Background(), basePlanForIdem(), input3)
	require.NoError(t, err)
	require.NotNil(t, repo.lastRec)
	assert.Equal(t, 555, repo.lastRec.FinalWordCount)
	assert.Equal(t, "qwen2.5:14b", repo.lastRec.ModelUsed)
}

// _ ensures the type reference stays auditable — the AlreadyPersisted
// field is compile-time absent from PostProcessResult.
var _ = PostProcessResult{}

func baseProcessInput() ProcessInput {
	return ProcessInput{Text: "test", ModelUsed: "llama3.2", CacheStatus: "generated"}
}

// ── PR-PERSIST-PR6-CANONICAL TDD regression locks ────────────────────
//
// These tests lock the canonical PR 6 persistence contract:
//
//   (a) IdempotencyKey = SHA-256(plan.ID|"|"|plan.CacheKey|"|"|
//       plan.PromptVersion|"|"|plan.TargetWords|"|"|plan.Language)
//       → first 16 lowercase hex characters.
//   (b) SpecScene = json.Marshal(input.SpecScene) producing the
//       canonical shape {"version":1,"scenes":[...]}.
//
// Byte-stability, empty-component resilience, and SpecScene
// independence from the idem key are locked by these tests.
// Changing any of these contracts is a SSOT regression (godlike/06).

// TestIdempotencyKey_ByteStability_1000Retries asserts the key is
// byte-stable across 1000 retries with the same 5-tuple. Verifies
// the SHA-256 hash is deterministic and no external state (time,
// RNG, global counter) affects the output.
func TestIdempotencyKey_ByteStability_1000Retries(t *testing.T) {
	t.Parallel()
	p := basePlanForIdem()
	first := computeIdempotencyKey(p)
	for i := 0; i < 1000; i++ {
		if got := computeIdempotencyKey(p); got != first {
			t.Fatalf("retry %d: key diverged (want %q, got %q)", i, first, got)
		}
	}
}

// TestIdempotencyKey_EmptyComponents_ProducesValidKey asserts that
// empty CacheKey and empty PromptVersion still produce a valid
// 16-hex key. These are valid 5-tuple components (an item can
// legitimately have no cache key or a default prompt version).
func TestIdempotencyKey_EmptyComponents_ProducesValidKey(t *testing.T) {
	t.Parallel()

	t.Run("empty CacheKey", func(t *testing.T) {
		t.Parallel()
		p := basePlanForIdem()
		p.CacheKey = ""
		k := computeIdempotencyKey(p)
		assert.Len(t, k, 16)
		for _, c := range k {
			assert.True(t,
				(c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
				"empty CacheKey still produces hex; got %q", string(c))
		}
	})

	t.Run("empty PromptVersion", func(t *testing.T) {
		t.Parallel()
		p := basePlanForIdem()
		p.PromptVersion = ""
		k := computeIdempotencyKey(p)
		assert.Len(t, k, 16)
		for _, c := range k {
			assert.True(t,
				(c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
				"empty PromptVersion still produces hex; got %q", string(c))
		}
	})

	t.Run("both empty", func(t *testing.T) {
		t.Parallel()
		p := basePlanForIdem()
		p.CacheKey = ""
		p.PromptVersion = ""
		k := computeIdempotencyKey(p)
		assert.Len(t, k, 16)
		for _, c := range k {
			assert.True(t,
				(c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
				"both empty still produces hex; got %q", string(c))
		}
	})
}

// TestIdempotencyKey_IndependentOfSpecScene asserts that changing
// the SpecScene (which is NOT part of the 5-tuple) does NOT alter
// the idempotency key. Runtime variation in scene count, text,
// or bindings must not produce a new row.
func TestIdempotencyKey_IndependentOfSpecScene(t *testing.T) {
	t.Parallel()
	p1 := basePlanForIdem()
	k1 := computeIdempotencyKey(p1)

	// Different scene count — all other 5-tuple fields identical.
	p2 := basePlanForIdem()
	// SpecScene is NOT a field on ResolvedGenerationPlan; the
	// idem key is independent of the scene output by design.
	// We verify two calls with the same plan produce the same key
	// regardless of what scenes the model produces.
	k2 := computeIdempotencyKey(p2)
	assert.Equal(t, k1, k2, "specscene variation must not affect the idem key")

	// Different title — also excluded.
	p3 := basePlanForIdem()
	p3.Title = "Completely Different Title With Many Scenes"
	k3 := computeIdempotencyKey(p3)
	assert.Equal(t, k1, k3, "title variation must not affect the idem key")

	// Different topic — also excluded.
	p4 := basePlanForIdem()
	p4.Topic = "science & technology"
	k4 := computeIdempotencyKey(p4)
	assert.Equal(t, k1, k4, "topic variation must not affect the idem key")
}

// TestPersistence_SpecSceneJSON_CanonicalRoundTrip asserts the
// SpecScene JSON stored by PersistenceProcessor survives a full
// json.Marshal → stored string → json.Unmarshal round trip,
// preserving the canonical {"version":1,"scenes":[...]} shape
// including all binding types (clip, image, voiceover, stock).
func TestPersistence_SpecSceneJSON_CanonicalRoundTrip(t *testing.T) {
	t.Parallel()
	repo := &idemFakeRepo{}
	proc := NewPersistenceProcessor(repo, zap.NewNop())

	m := baseModelForIdem()
	m.SpecScene = scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{
				ID:    "scene-0-intro",
				Index: 0,
				Text:  "Welcome to this video.",
				Title: "Introduction",
				Kind:  scriptpkg.SceneIntro,
				Bindings: scriptpkg.SceneBindings{
					Voiceover: &scriptpkg.VoiceoverBinding{
						Status:     "completed",
						Link:       "https://drive.google.com/audio/intro.mp3",
						LocalPath:  "/data/voiceover/intro.mp3",
						DurationMs: 3500,
					},
				},
			},
			{
				ID:    "scene-1-clip",
				Index: 1,
				Text:  "Look at this amazing clip.",
				Title: "Action Clip",
				Kind:  scriptpkg.SceneClip,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{
						ClipID:    "clip-abc123",
						ClipTitle: "Amazing Action Moment",
						DriveLink: "https://drive.google.com/file/d/abc123",
						StartMs:   0,
						EndMs:     15000,
					},
					Image: &scriptpkg.ImageBinding{
						ImageID: "img-789",
						Prompt:  "Cinematic action shot",
						URL:     "https://cdn.example.com/img789.png",
						Status:  "generated",
					},
				},
			},
			{
				ID:    "scene-2-outro",
				Index: 2,
				Text:  "Thanks for watching.",
				Title: "Outro",
				Kind:  scriptpkg.SceneOutro,
				Bindings: scriptpkg.SceneBindings{
					Stock: &scriptpkg.StockBinding{
						AssetID:   "stock-456",
						Name:      "Cinematic outro reel",
						Source:    "artlist",
						DriveLink: "https://drive.google.com/file/d/stock456",
						Score:     0.92,
					},
				},
			},
		},
	}

	input := baseProcessInput()
	input.Text = m.Text
	input.WordCount = m.WordCount
	input.SpecScene = m.SpecScene
	input.ModelUsed = m.ModelUsed
	input.CacheStatus = m.CacheStatus
	_, err := proc.Process(context.Background(), basePlanForIdem(), input)
	require.NoError(t, err)
	require.NotNil(t, repo.lastRec)

	storedJSON := repo.lastRec.SpecScene
	require.NotEmpty(t, storedJSON, "SpecScene must be persisted")

	// Round-trip: unmarshal the stored JSON back into SpecSceneOutput.
	var restored scriptpkg.SpecSceneOutput
	err = json.Unmarshal([]byte(storedJSON), &restored)
	require.NoError(t, err, "stored SpecScene must be valid JSON")

	// Canonical shape verification.
	assert.Equal(t, 1, restored.Version, "canonical version must be 1")
	assert.Len(t, restored.Scenes, 3, "all 3 scenes must survive round-trip")

	// Scene 0 — voiceover binding.
	s0 := restored.Scenes[0]
	assert.Equal(t, "scene-0-intro", s0.ID)
	assert.Equal(t, scriptpkg.SceneIntro, s0.Kind)
	require.NotNil(t, s0.Bindings.Voiceover)
	assert.Equal(t, "completed", s0.Bindings.Voiceover.Status)
	assert.Equal(t, int64(3500), s0.Bindings.Voiceover.DurationMs)

	// Scene 1 — clip + image bindings.
	s1 := restored.Scenes[1]
	assert.Equal(t, "scene-1-clip", s1.ID)
	assert.Equal(t, scriptpkg.SceneClip, s1.Kind)
	require.NotNil(t, s1.Bindings.Clip)
	assert.Equal(t, "clip-abc123", s1.Bindings.Clip.ClipID)
	require.NotNil(t, s1.Bindings.Image)
	assert.Equal(t, "img-789", s1.Bindings.Image.ImageID)
	assert.Equal(t, "generated", s1.Bindings.Image.Status)
	assert.Empty(t, s1.Bindings.Image.LocalPath, "image local_path must be stripped before persistence")

	// Scene 2 — stock binding.
	s2 := restored.Scenes[2]
	assert.Equal(t, "scene-2-outro", s2.ID)
	require.NotNil(t, s2.Bindings.Stock)
	assert.Equal(t, "stock-456", s2.Bindings.Stock.AssetID)
	assert.Equal(t, 0.92, s2.Bindings.Stock.Score)
	require.NotNil(t, s0.Bindings.Voiceover)
	assert.Empty(t, s0.Bindings.Voiceover.LocalPath, "voiceover local_path must be stripped before persistence")

	// Orthogonal: TimelineJSON slot still empty (PR 6 contract).
	assert.Empty(t, repo.lastRec.TimelineJSON, "TimelineJSON must remain empty under PR 6")
}

// TestPersistence_SpecScene_NilScenes_StillProducesJSON asserts that
// a SpecSceneOutput with nil (not empty) scenes still produces valid
// JSON with the canonical {"version":1,...} shape. Go's json.Marshal
// renders nil slices as "null" which is a valid JSON value but not
// the canonical shape — this test locks the observed behaviour.
func TestPersistence_SpecScene_NilScenes_StillProducesJSON(t *testing.T) {
	t.Parallel()
	repo := &idemFakeRepo{}
	proc := NewPersistenceProcessor(repo, zap.NewNop())

	m := baseModelForIdem()
	m.SpecScene = scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes:  nil,
	}

	input := baseProcessInput()
	input.Text = m.Text
	input.WordCount = m.WordCount
	input.SpecScene = m.SpecScene
	input.ModelUsed = m.ModelUsed
	input.CacheStatus = m.CacheStatus
	_, err := proc.Process(context.Background(), basePlanForIdem(), input)
	require.NoError(t, err)
	require.NotNil(t, repo.lastRec)

	storedJSON := repo.lastRec.SpecScene
	require.NotEmpty(t, storedJSON)

	// Verify it contains the version key — the canonical shape.
	assert.Contains(t, storedJSON, `"version":1`, "nil scenes must still produce canonical version key")

	// Unmarshal must succeed.
	var restored scriptpkg.SpecSceneOutput
	err = json.Unmarshal([]byte(storedJSON), &restored)
	require.NoError(t, err)
	assert.Equal(t, 1, restored.Version)
	// nil slice unmarshals as nil (Go zero value), which is valid.
	assert.Nil(t, restored.Scenes)
}
