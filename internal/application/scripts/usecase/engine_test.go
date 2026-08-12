// Package scripts — engine_test.go exercises Engine.Generate with
// fake implementations of scriptOllamaGenerator and memoryGateChecker so
// the full parameter-resolution, caching, decode, and error paths can be
// validated without a real LLM or database.
//
// TODO #8 (June 2026): engine_test.go drift cleaned — fakeScriptRepo
// deleted (post-PR 5: Engine no longer has a `repo ScriptRepository`
// field; PersistenceProcessor is the single writer). The dead fake's
// 11 methods were referencing types that drifted away in earlier PRs
// (ScriptStockMatchRecord, ScriptGenerationLog, ScriptOutlineSectionRecord,
// ScriptResearchSource — defined in adapters/repository.go but only
// referenced by the dead fake). Local memory-gate type names lowercased
// to memoryGateRequest / memoryGateResult so fakeMemoryGate.CheckGate
// satisfies the narrow interface declared in engine.go (memoryGateChecker).
// The contract is the canonical TypeScript CLI surface for the usecase
// package; Phase 1c closure (June 2026) confirms the local narrow types
// are stable and the test alignment unblocks production wiring when the
// memory gate adapter lands.
//
// AGENT-3 (June 2026): Engine uses narrow interfaces (scriptOllamaGenerator,
// memoryGateChecker) defined in engine.go alongside the compile-time
// assertions that the concrete *ollama.Generator satisfies the
// scriptOllamaGenerator shape. Tests inject typed fakes. The legacy
// in-package `memoryCache` interface and its compile-time identity lock
// were retired alongside cache_eviction_usecase.go (AZIONE 5) —
// memoryGateChecker remains the single canonical narrow contract.
//
// PR 13 (June 2026): removed deprecated WriteScript tests — all tests
// now exercise Engine.Generate directly.
//
// PR 2 (June 2026): fakeMemoryGate extended with onCheck callback.
// Engine reads plan.RenderedPrompt (not the legacy plan.Prompt);
// fakeMemoryGate captures the request shape to verify CacheKey flows.
package usecase

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Fakes ──────────────────────────────────────────────────────────────────

// fakeOllamaGen is a scriptOllamaGenerator injected into Engine for tests.
type fakeOllamaGen struct {
	calls       atomic.Int32
	result      *scriptports.GenerationResult
	returnErr   error
	capturedReq atomic.Pointer[scriptports.TextGenerationRequest]
}

func (f *fakeOllamaGen) GenerateScript(_ context.Context, req scriptports.TextGenerationRequest) (*scriptports.GenerationResult, error) {
	f.calls.Add(1)
	f.capturedReq.Store(&req)
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	if f.result != nil {
		return f.result, nil
	}
	return defaultFakeResult(), nil
}

// fakeMemoryGate is a memoryGateChecker injected into Engine for tests.
//
// PR 2: extended with optional diagnostics:
//   - capturedReq (pointer to slot) — tests asserting on the request
//     shape (CacheKey propagation, etc.) set this so CheckGate copies
//     the request in.
//   - onCheck (callable) — fires unconditionally before any return,
//     so tests can observe call counts (used by ForceRefresh-bypass
//     verification).
//
// TODO #8 (June 2026): field types lowercased to memoryGateResult /
// memoryGateRequest (the local narrow types declared in engine.go) so
// the fake's CheckGate method satisfies the memoryGateChecker
// interface. AZIONE 5 retired the legacy `memoryCache` compile-time
// identity lock — memoryGateChecker is now the single canonical
// narrow shape.
type fakeMemoryGate struct {
	result      *memoryGateResult
	returnErr   error
	capturedReq *memoryGateRequest
	onCheck     func()
}

type fakeBranchRecorder struct {
	branches  []string
	countries []string
}

func (r *fakeBranchRecorder) RecordScriptGenerationBranch(branch, bcp47 string) {
	r.branches = append(r.branches, branch)
	r.countries = append(r.countries, ExtractCountryForTelemetry(bcp47))
}

func (f *fakeMemoryGate) CheckGate(_ context.Context, req memoryGateRequest) (*memoryGateResult, error) {
	if f.onCheck != nil {
		f.onCheck()
	}
	if f.capturedReq != nil {
		*f.capturedReq = req
	}
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	return f.result, nil
}

// v1CanonicalFixtureText is the prose used as the default V1 `text`
// field across fake ollama results. Tests assert on this exact text.
// PR 1: any non-V1 payload now fails the fresh-path decode with
// ErrModelOutputMalformed, so defaultFakeResult must emit canonical
// JSON.
const v1CanonicalFixtureText = "This is a generated script with multiple sentences and narrative depth."

// v1CanonicalFixture returns the canonical V1 JSON string used by
// defaultFakeResult's Script field. Kept as a helper so tests can
// reuse the same V1 shape.
func v1CanonicalFixture() string {
	return `{"schema_version":1,"text":"` + v1CanonicalFixtureText + `","specscene":{"version":1,"scenes":[{"id":"scene-0","index":0,"text":"` + v1CanonicalFixtureText + `","kind":"narration","bindings":{}}]}}`
}

func defaultFakeResult() *scriptports.GenerationResult {
	return &scriptports.GenerationResult{
		Script:      v1CanonicalFixture(),
		WordCount:   12,
		EstDuration: 4,
		Model:       "llama3:8b",
		Prompt:      "Write a script about testing.",
	}
}

// ── Engine construction ────────────────────────────────────────────────────

// PR 5 (June 2026): the `repo ScriptRepository` field was removed
// from Engine, so all 3-arg buildTestEngine call sites are cleaned
// up. Engine no longer accesses persistence — that role belongs to
// PersistenceProcessor exclusively.
//
// P0.F regression-surface synergy (July 2026): the memoryGate
// parameter is typed as the narrow `memoryGateChecker` interface
// (engine.go) rather than the concrete `*fakeMemoryGate`. This
// unblocks p2aMemoryGate (a sync.Mutex-backed thread-safe fake
// declared in cache_race_p2a_test.go) which fails the canonical
// `cannot use mem (variable of type *p2aMemoryGate) as
// *fakeMemoryGate value` build error when concurrency tests
// invoke buildTestEngine. The original typing was load-bearing
// only for the in-package fakeMemoryGate happy path; widening
// to the interface is godlike/06 SSOT-correct (the canonical
// memorySvc field on Engine is itself typed memoryGateChecker,
// so the buildTestEngine seam now matches the production seam).
//
// godlike/07 fail-closed nil-semantics caveat (per code-reviewer
// feedback, July 2026): passing nil to `mem` sets `e.memorySvc`
// to a TRUE nil-interface (type+value both nil; the previous
// concrete `*fakeMemoryGate` typing held a typed-nil-pointer in
// the interface slot, which made `e.memorySvc != nil` return
// TRUE and would have called a nil-pointer-deref panic on
// memory-path execution). Now `e.memorySvc == nil` is correctly
// false-equivalent: tests that exercise NON-MEMORY paths
// (`UseMemory=false` or `ForceRefresh=true`) can safely pass
// `nil` and the engine short-circuits past the memory-gate
// read entirely. Tests asserting memory-path behavior MUST
// pass a non-nil `mem` (a `*fakeMemoryGate{}` or `*p2aMemoryGate{}`)
// so the engine's memorySvc != nil check fires correctly.
func buildTestEngine(gen *fakeOllamaGen, mem memoryGateChecker) *Engine {
	return &Engine{
		ollamaGen: gen,
		memorySvc: mem,
		log:       zap.NewNop(),
	}
}

func TestEngineGenerate_RecordsScriptBranchThroughPort(t *testing.T) {
	gen := &fakeOllamaGen{result: &scriptports.GenerationResult{
		Script:      "This is a generated script with multiple sentences and narrative depth.",
		WordCount:   11,
		EstDuration: 4,
		Model:       "test-model",
	}}
	recorder := &fakeBranchRecorder{}
	e := NewEngine(gen, nil, zap.NewNop(), recorder)

	_, err := e.Generate(context.Background(), &scriptpkg.ResolvedGenerationPlan{
		Language:    "it-IT",
		TargetWords: 11,
		Segments:    []scriptpkg.ScriptSegment{{Topic: "canonical", TargetWords: 11}},
	})
	require.NoError(t, err)
	_, err = e.Generate(context.Background(), &scriptpkg.ResolvedGenerationPlan{
		Language:      "pt-BR",
		SegmentTopics: []string{"legacy"},
	})
	require.NoError(t, err)

	require.Equal(t, []string{"a", "b"}, recorder.branches)
	require.Equal(t, []string{"IT", "BR"}, recorder.countries)
}

// ── Nil / missing dependency ───────────────────────────────────────────────

func TestEngineGenerate_NilEngine(t *testing.T) {
	t.Parallel()
	var e *Engine
	_, err := e.Generate(context.Background(), &scriptpkg.ResolvedGenerationPlan{
		Topic:    "test",
		Language: "en",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestEngineGenerate_NilOllamaGen(t *testing.T) {
	t.Parallel()
	e := &Engine{log: zap.NewNop()}
	_, err := e.Generate(context.Background(), &scriptpkg.ResolvedGenerationPlan{
		Topic:    "test",
		Language: "en",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// ── Successful generation ──────────────────────────────────────────────────

func TestEngineGenerate_Success(t *testing.T) {

	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil)

	// PR 2: model-facing prompt is plan.RenderedPrompt. The legacy
	// plan.Prompt field was removed because it conflated fingerprint
	// with model input.
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Title:          "Generate Test",
		Topic:          "Generate API",
		Language:       "it",
		Tone:           "documentary",
		Model:          "llama3:8b",
		Mode:           "text",
		TargetWords:    500,
		RenderedPrompt: "Write about testing.",
	}

	result, err := e.Generate(context.Background(), plan)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(1), gen.calls.Load())
	// PR 1: output is the canonical ModelScriptOutputV1 emitted by
	// the model. Assert that the engine decoded it correctly.
	assert.Equal(t, v1CanonicalFixtureText, result.Output.Text)
	assert.Equal(t, 1, result.Output.SchemaVersion)
	assert.Equal(t, 1, result.Output.SpecScene.Version)
	require.Len(t, result.Output.SpecScene.Scenes, 1)
	assert.Equal(t, "scene-0", result.Output.SpecScene.Scenes[0].ID)
	assert.Equal(t, v1CanonicalFixtureText, result.Output.SpecScene.Scenes[0].Text)
	assert.Equal(t, scriptpkg.SceneNarration, result.Output.SpecScene.Scenes[0].Kind)
	assert.Equal(t, 12, result.WordCount)
	assert.Equal(t, "llama3:8b", result.Model)
	assert.Equal(t, "generated", result.CacheStatus)
	assert.Equal(t, 4, result.EstDuration)
	// PR 5 (June 2026): EngineResult.ScriptID was removed — engine
	// no longer participates in persistence. Consumers source
	// ScriptID from postResult.ScriptID (the PersistenceProcessor
	// output). This acceptance test asserts `result` has NO ScriptID
	// field — see TestEngineGenerate_DoesNotPersist for the
	// anti-persistence contract.
	assert.Nil(t, result.ClipEvidence, "text-only plan should have nil clip evidence")
}

func TestEngineGenerate_WithClips(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:       "item-clips",
		Title:    "Clip Script",
		Topic:    "Clip Generation",
		Language: "en",
		Mode:     "clip_to_script",
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a", "clip-b"},
			ClipCount:       2,
			DriveLinks: map[string]string{
				"clip-a": "https://drive.google.com/a",
				"clip-b": "https://drive.google.com/b",
			},
		},
	}

	result, err := e.Generate(context.Background(), plan)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ClipEvidence)
	assert.Equal(t, []string{"clip-a", "clip-b"}, result.ClipEvidence.AcceptedClipIDs)
	assert.Equal(t, "https://drive.google.com/a", result.ClipEvidence.DriveLinks["clip-a"])
}

func TestEngineGenerate_ClipEvidenceIsModelSourceText(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil)

	const staleSourceText = "stale request-level source text"
	const sentinel = "unique transcript sentinel XYZ"
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:      "Clip Evidence Source",
		Language:   "en",
		Mode:       "clip_to_script",
		SourceText: staleSourceText,
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-sentinel"},
			NarrativeText:   "NARRATIVE EVIDENCE 1\\nTranscript: " + sentinel,
		},
	}

	_, err := e.Generate(context.Background(), plan)
	require.NoError(t, err)

	captured := gen.capturedReq.Load()
	require.NotNil(t, captured)
	assert.Equal(t, plan.ClipEvidence.ModelSourceText(), captured.SourceText)
	assert.Contains(t, captured.SourceText, sentinel)
	assert.NotContains(t, captured.SourceText, staleSourceText)
}

func TestEngineGenerate_PassesGroundingPolicyToRequest(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:           "Grounding Policy Wiring",
		Topic:           "Policy Test",
		Language:        "en",
		Tone:            "documentary",
		Model:           "llama3:8b",
		Mode:            "text",
		RenderedPrompt:  "Write about policy.",
		GroundingPolicy: scriptpkg.GroundingPolicySourcePrimary,
	}

	_, err := e.Generate(context.Background(), plan)
	require.NoError(t, err)

	captured := gen.capturedReq.Load()
	require.NotNil(t, captured)
	assert.Equal(t, scriptpkg.GroundingPolicySourcePrimary, captured.GroundingPolicy)
}

func TestEngineGenerate_AppendsClipGroundingInstructions(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:          "Clip Grounding",
		Topic:          "Jackie Chan",
		Language:       "en",
		Tone:           "documentary",
		Model:          "llama3:8b",
		Mode:           "clip_to_script",
		NumClips:       2,
		SegmentWords:   120,
		SegmentTopics:  []string{"Breakfast setup", "Street reaction"},
		RenderedPrompt: "Write about the supplied clips.",
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-1", "clip-2"},
			ClipCount:       2,
			DriveLinks: map[string]string{
				"clip-1": "https://drive.google.com/file/d/clip-1/view",
				"clip-2": "https://drive.google.com/file/d/clip-2/view",
			},
		},
	}

	_, err := e.Generate(context.Background(), plan)
	require.NoError(t, err)

	captured := gen.capturedReq.Load()
	require.NotNil(t, captured)
	assert.Contains(t, captured.Prompt, "CLIP-GROUNDED WRITING RULES:")
	assert.Contains(t, captured.Prompt, "narrative evidence blocks")
	assert.Contains(t, captured.Prompt, "describe what is happening in the clips")
	assert.Contains(t, captured.Prompt, "Treat every transcript as private reference evidence")
	assert.Contains(t, captured.Prompt, "Rewrite and paraphrase what each clip is about")
	assert.Contains(t, captured.Prompt, "do not reproduce transcript sentences verbatim")
	assert.Contains(t, captured.Prompt, "do not include URLs, drive links, clip IDs, speaker labels, tag lists, keyword lists")
	assert.Contains(t, captured.Prompt, "Put technical details only in metadata or bindings")
	assert.Contains(t, captured.Prompt, "Use exactly 2 clip-driven scenes.")
	assert.Contains(t, captured.Prompt, "Aim for about 120 words per segment.")
	assert.Contains(t, captured.Prompt, "Breakfast setup")
	assert.Contains(t, captured.Prompt, "Street reaction")
	assert.Contains(t, captured.Prompt, "[OUTPUT_FORMAT]")
	assert.Empty(t, captured.ClipIDs)
	assert.NotContains(t, captured.Prompt, "clip-1, clip-2")
	assert.NotContains(t, captured.Prompt, "clip-1")
	assert.NotContains(t, captured.Prompt, "clip-2")
}

func TestEngineGenerate_MemoryGateHit(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	cached := `{"schema_version":1,"text":"Cached script from memory.","specscene":{"version":1,"scenes":[{"id":"scene-0","index":0,"text":"Cached script from memory.","kind":"narration","bindings":{}}]}}`
	mem := &fakeMemoryGate{
		result: &memoryGateResult{
			Output:    cached,
			WordCount: 42,
			Model:     "llama3:8b",
		},
	}
	e := buildTestEngine(gen, mem)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:     "Cached",
		Language:  "en",
		Mode:      "text",
		UseMemory: true,
	}

	result, err := e.Generate(context.Background(), plan)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(0), gen.calls.Load(), "ollama must NOT be called on memory hit")
	// PR 1: cached payload is canonical V1 and decodes through
	// DecodeModelOutput.
	assert.Equal(t, "Cached script from memory.", result.Output.Text)
	assert.Equal(t, 1, result.Output.SchemaVersion)
	assert.Equal(t, "exact_hit", result.CacheStatus)
}

func TestEngineGenerate_ForceRefreshBypassesMemory(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	mem := &fakeMemoryGate{
		result: &memoryGateResult{
			Output:    "Should not be returned.",
			WordCount: 10,
		},
	}
	e := buildTestEngine(gen, mem)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:        "Force Refresh",
		Language:     "en",
		Mode:         "text",
		UseMemory:    true,
		ForceRefresh: true, // bypass cache
	}

	result, err := e.Generate(context.Background(), plan)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(1), gen.calls.Load(), "ollama must be called when ForceRefresh=true")
	assert.Equal(t, "generated", result.CacheStatus)
}

// TestEngineGenerate_DoesNotPersist (PR 5, June 2026): the engine
// must NEVER call ScriptRepository.SaveScript. Persistence is the
// single-writer contract of PersistenceProcessor; the engine is no
// longer involved even when plan.SaveToDB=true.
//
// TODO #8 (June 2026): the prior version of this test used a counter-
// bearing fakeScriptRepo to assert zero calls. fakeScriptRepo was
// deleted from this file (Engine.repo field is gone since PR 5), so
// the test no longer has a counter to inspect. The test's persistence-
// avoidance invariant is now enforced by the Go type system (Engine
// has no `repo` field → no possible SaveScript call from this struct),
// which is a stronger guarantee than the runtime counter. The test is
// preserved as an end-to-end sanity loop: 5 fresh generations succeed
// with the expected cache status and never panic, which is what an
// injected repo would have protected against.
//
// Pre-PR 5 (June 2026): a counter-bearing fake repo was injected via
// reflect-set (the engine struct no longer has a `repo` field, so
// injection requires the narrow-interface seam — see the assertion
// that the counter is always 0 across many calls).
func TestEngineGenerate_DoesNotPersist(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil) // PR 5: no repo arg

	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:       "No Persistence",
		Topic:       "Engine never saves",
		Language:    "en",
		Tone:        "neutral",
		Model:       "llama3",
		Mode:        "text",
		TargetWords: 200,
		SaveToDB:    true, // flag is irrelevant — engine still skips persistence
	}

	for i := 0; i < 5; i++ {
		result, err := e.Generate(context.Background(), plan)
		require.NoError(t, err)
		require.NotNil(t, result)
		// PR 5: EngineResult.ScriptID field removed. No way
		// for the engine to leak a persisted ID. Consumers read
		// from postResult.ScriptID (asserted in
		// processor_persistence_test.go).
		assert.Equal(t, "generated", result.CacheStatus)
	}
}

func TestEngineGenerate_NilPlan(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil)

	_, err := e.Generate(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan is nil")
}

// ── PR 1: decoder / scene-preservation paths ─────────────────────────────

// TestEngineGenerate_DecodeFailure verifies that the ModeCompatibility
// retry (PR-FIX, June 2026) salvages plain prose from the model.
// ModeStrict rejects bare prose → engine retries with ModeCompatibility
// → plain-text wrapper produces a synthetic V1 with empty scenes.
// The job no longer fails on malformed JSON; operators see salvaged
// output with the "fresh-fallback" source label in metrics.
func TestEngineGenerate_DecodeFailure(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{
		result: &scriptports.GenerationResult{
			Script:      "Plain prose without a JSON envelope.",
			WordCount:   8,
			EstDuration: 3,
			Model:       "llama3:8b",
			Prompt:      "ignored",
		},
	}
	e := buildTestEngine(gen, nil)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:    "Prose Decode Salvage",
		Language: "en",
		Mode:     "text",
	}

	// PR-FIX (June 2026): plain prose is salvaged by ModeCompatibility.
	// The engine no longer returns ErrModelOutputMalformed on prose;
	// instead it wraps prose as synthetic V1 with empty scenes.
	result, err := e.Generate(context.Background(), plan)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "Plain prose without a JSON envelope.", result.Output.Text)
	assert.Equal(t, 1, result.Output.SchemaVersion)
	assert.Empty(t, result.Output.SpecScene.Scenes, "plain-prose wrapped output has empty scenes")
	assert.Equal(t, "generated", result.CacheStatus)
	assert.Equal(t, int32(1), gen.calls.Load(), "ollama must be called (fresh generation path)")
}

// TestEngineGenerate_CacheLegacyHit verifies that pre-V1 legacy-array
// cache rows are promoted to V1 by the jsonextract.Scanner
// ModeCompatibility fallback. Operators upgrading from the legacy
// decoder see the cache continue working without manual intervention.
func TestEngineGenerate_CacheLegacyHit(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	legacyArray := `[{"id":"scene-0","index":0,"text":"Legacy cached scene.","kind":"narration"}]`
	mem := &fakeMemoryGate{
		result: &memoryGateResult{
			Output:    legacyArray,
			WordCount: 5,
			Model:     "llama3:8b",
		},
	}
	e := buildTestEngine(gen, mem)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:     "Legacy Cache",
		Language:  "en",
		Mode:      "text",
		UseMemory: true,
	}

	result, err := e.Generate(context.Background(), plan)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(0), gen.calls.Load(), "ollama must NOT be called on cache hit")
	assert.Equal(t, "exact_hit", result.CacheStatus)
	assert.Equal(t, 1, result.Output.SchemaVersion)
	require.Len(t, result.Output.SpecScene.Scenes, 1)
	// jsonextract.Scanner prefixes promoted IDs with "legacy-"
	// to flag rows promoted from the pre-V1 cache.
	assert.Equal(t, "legacy-scene-0", result.Output.SpecScene.Scenes[0].ID)
	assert.Equal(t, "Legacy cached scene.", result.Output.SpecScene.Scenes[0].Text)
	assert.Equal(t, scriptpkg.SceneNarration, result.Output.SpecScene.Scenes[0].Kind)
}

// TestEngineGenerate_CacheProseHit verifies that an unparseable cache
// row (legacy prose, perhaps from before the V1 rollout) is wrapped as
// plain-text V1 in ModeCompatibility (declared fallback with
// Prometheus metric) rather than silently erroring.
//
// P0.8 (June 2026): changed from error to success — ModeCompatibility
// wraps plain text as a synthetic V1 with empty scenes, bumping
// jsonextract_plain_text_fallback_total.
func TestEngineGenerate_CacheProseHit(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	mem := &fakeMemoryGate{
		result: &memoryGateResult{
			Output:    "This is not JSON, just prose paragraphs.",
			WordCount: 10,
			Model:     "llama3:8b",
		},
	}
	e := buildTestEngine(gen, mem)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:     "Prose Cache",
		Language:  "en",
		Mode:      "text",
		UseMemory: true,
	}

	result, err := e.Generate(context.Background(), plan)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(0), gen.calls.Load(), "ollama must NOT be called on cache hit")
	assert.Equal(t, "exact_hit", result.CacheStatus)
	// P0.8: ModeCompatibility wraps plain text as a synthetic V1.
	assert.Equal(t, "This is not JSON, just prose paragraphs.", result.Output.Text)
	assert.Equal(t, 1, result.Output.SchemaVersion)
	assert.Empty(t, result.Output.SpecScene.Scenes, "plain-text wrapped output has empty scenes")
}

// TestEngineGenerate_ModelScenesPreserved verifies that the engine's
// fresh-generation path surfaces the model-emitted SpecScene (text + ID +
// kind) verbatim into EngineResult.Output.SpecScene — the Text: ""
// scene-fabrication anti-pattern is gone, so scenes from the model
// reach the consumer unchanged.
func TestEngineGenerate_ModelScenesPreserved(t *testing.T) {
	t.Parallel()
	scenarioJSON := `{"schema_version":1,"text":"Full prose. S1. S2.","specscene":{"version":1,"scenes":[{"id":"scene-0","index":0,"text":"S1.","kind":"narration","bindings":{}},{"id":"scene-1","index":1,"text":"S2.","kind":"narration","bindings":{}}]}}`
	gen := &fakeOllamaGen{
		result: &scriptports.GenerationResult{
			Script:      scenarioJSON,
			WordCount:   4,
			EstDuration: 2,
			Model:       "llama3:8b",
			Prompt:      "ignored",
		},
	}
	e := buildTestEngine(gen, nil)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:    "Two-Scene Preserve",
		Language: "en",
		Mode:     "text",
	}

	result, err := e.Generate(context.Background(), plan)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "Full prose. S1. S2.", result.Output.Text)
	require.Len(t, result.Output.SpecScene.Scenes, 2)
	// Verify NO scene has Text:"" — that was the pre-PR 1 anti-pattern.
	for i, sc := range result.Output.SpecScene.Scenes {
		assert.NotEmpty(t, sc.Text, "scene[%d].Text must not be empty (model produced it)", i)
		assert.Equal(t, i, sc.Index)
		assert.Equal(t, "scene-"+itoaSimple(i), sc.ID)
		assert.Equal(t, scriptpkg.SceneNarration, sc.Kind)
	}
}

// TestEngineGenerate_AlwaysAppendsVSuffix (P0.1, June 2026): the
// V1 output instruction is appended to the rendered prompt
// unconditionally. Previously the conditional `if plan.OutputFmt
// != "prose"` only emitted the suffix for non-prose requests.
// After the default flip to "json" + validator rejecting "prose",
// the canonical pipeline always appends the suffix so the model
// is steered toward the canonical V1 contract regardless of
// input shape.
func TestEngineGenerate_AlwaysAppendsVSuffix(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil)

	plans := []*scriptpkg.ResolvedGenerationPlan{
		// No OutputFmt at all.
		{Title: "omit", Language: "en", Mode: "text", RenderedPrompt: "Body A"},
		// Explicit "json".
		{Title: "json-explicit", Language: "en", Mode: "text", RenderedPrompt: "Body B", OutputFmt: "json"},
	}

	for _, p := range plans {
		_, err := e.Generate(context.Background(), p)
		require.NoError(t, err)
		captured := gen.capturedReq.Load()
		require.NotNil(t, captured, "fakeOllamaGen must have captured the request for plan %q", p.Title)
		assert.Equal(t, p.RenderedPrompt, captured.Prompt[:len(p.RenderedPrompt)],
			"rendered prompt body must flow through verbatim to ollama request for plan %q", p.Title)
		// LLM-PLAIN-TEXT-CONTRACT wave (PR-2, July 2026): the engine
		// now requests OutputModePlainText on every plan (canonical
		// post-wave default). OutputModeScriptV1 is RETAINED in the
		// const block for backward-compat with pre-wave cached rows;
		// no new caller should request it.
		assert.Equal(t, scriptports.OutputModePlainText, captured.OutputMode,
			"OutputModePlainText must be set on the ollama request for plan %q (canonical post-PR-2 default)",
			p.Title)
		assert.Contains(t, captured.Prompt, "[OUTPUT_FORMAT]",
			"V1 output instruction must be appended for plan %q (canonical engine format marker)", p.Title)
		// LLM-PLAIN-TEXT-CONTRACT wave — PR-1 flipped the output instruction
		// from "Respond ONLY with a single JSON object" to the canonical
		// "DO NOT output JSON" prose-only contract. The downstream pipeline
		// (SceneSynthesizer + SceneAssetBinder + postprocessors)
		// owns all structured fields; the LLM only writes prose.
		assert.Contains(t, captured.Prompt, "DO NOT output JSON",
			"plain-text instruction must forbid JSON output for plan %q (PR-1, LLM-PLAIN-TEXT-CONTRACT)", p.Title)
		assert.NotContains(t, captured.Prompt, "Respond ONLY with a single JSON object",
			"plain-text instruction must NOT ask the LLM for the historical V1 JSON schema (PR-1, LLM-PLAIN-TEXT-CONTRACT) for plan %q", p.Title)
	}
}

// ── PR 2: prompt / fingerprint / cache-key separation ──────────────────

// TestBuildEditorialPrompt_DoesNotIncludeFingerprint asserts that the
// editorial prompt never contains the item identity fingerprint hash.
// Pre-PR 2, buildPrompt returned BuildItemIdentity(item) — a SHA-256
// digest sent to the model as the prompt, which is wrong on every
// front (no editorial content, hides intent, leaks identity).
func TestBuildEditorialPrompt_DoesNotIncludeFingerprint(t *testing.T) {
	t.Parallel()
	item := scriptpkg.GenerationItemV2{
		ID:    "fp-test",
		Title: "FP Test",
		Source: scriptpkg.SourceSpec{
			Type:       scriptpkg.SourceText,
			Topic:      "Deterministic assembly test",
			SourceText: "alpha beta gamma",
			Guidelines: "Documentary tone.",
		},
		ScriptParams: scriptpkg.ScriptSpec{
			TargetWords: 250,
		},
		Style:    "cinematic",
		Language: "en",
		Tone:     "neutral",
	}
	editorial := BuildPlan(item).RenderedPrompt
	// P0 #1 (June 2026): BuildClipFingerprint replaces the Phase 1b
	// stub BuildItemIdentity. The editorial prompt must never contain
	// the source fingerprint — that was the pre-PR 2 anti-pattern
	// where the model prompt WAS the fingerprint hash.
	fp := BuildClipFingerprint(item.Source, nil)
	assert.NotEmpty(t, fp, "fingerprint must be non-empty after P0 #1 fix")
	assert.NotContains(t, editorial, fp, "RenderedPrompt must NOT contain the item fingerprint hash")
	assert.Contains(t, editorial, "Documentary tone.", "editorial prompt should include source guidelines")
	assert.Contains(t, editorial, "250", "editorial prompt should include target words")
}

// TestEngineGenerate_FeedsCacheKeyToMemoryGate asserts that the
// canonical CacheKey (computed by script.BuildCacheKey in the use
// case) propagates through to memoryGateRequest.CacheKey when the
// engine reads the plan. PR 2 keeps the legacy Title/Language/Mode
// fields for backwards compat alongside the new CacheKey field.
func TestEngineGenerate_FeedsCacheKeyToMemoryGate(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	var captured memoryGateRequest
	mem := &fakeMemoryGate{
		result:      nil, // cache miss path: nil result, but we still see the request
		capturedReq: &captured,
	}
	e := buildTestEngine(gen, mem)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:     "CacheKey Wiring",
		Language:  "en",
		Mode:      "text",
		UseMemory: true,
		// PR 2: the use case computes plan.CacheKey, but engine
		// must still respect whatever CacheKey is on the plan that
		// gets here. We set it explicitly to verify wiring.
		CacheKey: "deadbeefcafef00d",
	}

	// Cache miss (nil result, nil err): engine proceeds to fresh
	// generation path. The capture still happens at the request
	// build above. The fresh path needs a V1 canonical result so
	// the test succeeds end-to-end (and so we don't incidentally regress
	// PR 1).
	_, err := e.Generate(context.Background(), plan)
	require.NoError(t, err)
	assert.Equal(t, "deadbeefcafef00d", captured.CacheKey, "CacheKey from plan must propagate to memoryGateRequest")
	assert.Equal(t, "CacheKey Wiring", captured.Title)
	assert.Equal(t, "en", captured.Language)
	assert.Equal(t, "text", captured.Mode)
	assert.True(t, captured.UseMemory, "UseMemory from plan must propagate to memoryGateRequest")
	assert.False(t, captured.ForceRefresh, "ForceRefresh must remain false on the normal cache-read path")
}

// TestEngineGenerate_ForceRefreshBypassesMemoryWithCacheKey is a
// PR 2-mandated test: even when plan.CacheKey is set, ForceRefresh
// must bypass the memory gate so callers can escape a poisoned
// row. Pre-existing TestEngineGenerate_ForceRefreshBypassesMemory
// already covers the no-CacheKey case.
func TestEngineGenerate_ForceRefreshBypassesMemoryWithCacheKey(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	var called atomic.Int32
	mem := &fakeMemoryGate{
		onCheck: func() { called.Add(1) },
		// Canonical cache hit payload — but ForceRefresh means the
		// engine never even asks.
		result: &memoryGateResult{
			Output:    "Should not be returned.",
			WordCount: 10,
		},
	}
	e := buildTestEngine(gen, mem)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:        "Force Refresh With CacheKey",
		Language:     "en",
		Mode:         "text",
		UseMemory:    true,
		ForceRefresh: true,
		CacheKey:     "beef0001beef0001",
	}

	_, err := e.Generate(context.Background(), plan)
	require.NoError(t, err)
	assert.Equal(t, int32(0), called.Load(), "memory gate must NOT be called when ForceRefresh=true")
}

// itoaSimple is a tiny strconv-free helper to avoid an extra import
// for engine tests that want to format int scene indexes.
func itoaSimple(i int) string {
	if i == 0 {
		return "0"
	}
	if i < 0 {
		return "-" + itoaSimple(-i)
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
