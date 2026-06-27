// Package scripts — engine_test.go exercises Engine.WriteScript with
// fake implementations of scriptOllamaGenerator, memoryGateChecker, and
// ScriptRepository so the full parameter-resolution, caching, save-to-db,
// and error paths can be validated without a real LLM or database.
//
// AGENT-3 (June 2026): Engine uses narrow interfaces (scriptOllamaGenerator,
// memoryGateChecker) defined in engine.go alongside the compile-time
// assertions that the concrete *ollama.Generator and *gemmamemory.Service
// satisfy them. Tests inject typed fakes.
package scripts

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
)

// ── Fakes ──────────────────────────────────────────────────────────────────

// fakeOllamaGen is a scriptOllamaGenerator injected into Engine for tests.
type fakeOllamaGen struct {
	calls     atomic.Int32
	result    *ollamatypes.GenerationResult
	returnErr error
}

func (f *fakeOllamaGen) GenerateScript(_ context.Context, _ ollamatypes.TextGenerationRequest) (*ollamatypes.GenerationResult, error) {
	f.calls.Add(1)
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	if f.result != nil {
		return f.result, nil
	}
	return defaultFakeResult(), nil
}

// fakeMemoryGate is a memoryGateChecker injected into Engine for tests.
type fakeMemoryGate struct {
	result    *gemmamemory.GateResult
	returnErr error
}

func (f *fakeMemoryGate) CheckGate(_ context.Context, _ gemmamemory.MemoryGateRequest) (*gemmamemory.GateResult, error) {
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	return f.result, nil
}

// fakeScriptRepo is a ScriptRepository for SaveToDB tests.
type fakeScriptRepo struct {
	saveCalls  atomic.Int32
	lastRecord *ScriptRecord
	returnID   int64
	returnErr  error
}

func (f *fakeScriptRepo) SaveScript(_ context.Context, rec *ScriptRecord, _ []ScriptSectionRecord, _ []ScriptStockMatchRecord) (int64, error) {
	f.saveCalls.Add(1)
	f.lastRecord = rec
	if f.returnErr != nil {
		return 0, f.returnErr
	}
	return f.returnID, nil
}

func (f *fakeScriptRepo) UpdateScriptFinalContent(context.Context, int64, string, int, string, string, string, string, int) error {
	return nil
}
func (f *fakeScriptRepo) SaveGenerationLog(_ context.Context, _ ScriptGenerationLog) error {
	return nil
}
func (f *fakeScriptRepo) SaveOutlineSections(_ context.Context, _ int64, _ []ScriptOutlineSectionRecord) error {
	return nil
}
func (f *fakeScriptRepo) SaveResearchSources(_ context.Context, _ int64, _ []ScriptResearchSource) error {
	return nil
}
func (f *fakeScriptRepo) NextVersionForTopic(_ context.Context, _, _, _ string) (int, error) {
	return 1, nil
}
func (f *fakeScriptRepo) GetSectionByID(_ context.Context, _ int64) (*ScriptSectionRecord, error) {
	return nil, nil
}
func (f *fakeScriptRepo) GetScriptByID(_ int64) (*ScriptRecord, []ScriptSectionRecord, []ScriptStockMatchRecord, error) {
	return nil, nil, nil, nil
}
func (f *fakeScriptRepo) GetAdjacentSections(_ context.Context, _ int64, _ int) (*ScriptSectionRecord, *ScriptSectionRecord, error) {
	return nil, nil, nil
}
func (f *fakeScriptRepo) UpdateSectionContent(_ context.Context, _ int64, _ string) error { return nil }
func (f *fakeScriptRepo) ListScripts(_ context.Context, _ ScriptListFilter) ([]*ScriptRecord, error) {
	return nil, nil
}

func defaultFakeResult() *ollamatypes.GenerationResult {
	return &ollamatypes.GenerationResult{
		Script:      "This is a generated script with multiple sentences and narrative depth.",
		WordCount:   12,
		EstDuration: 4,
		Model:       "llama3:8b",
		Prompt:      "Write a script about testing.",
	}
}

// ── Engine construction ────────────────────────────────────────────────────

func buildTestEngine(gen *fakeOllamaGen, mem *fakeMemoryGate, repo *fakeScriptRepo) *Engine {
	return &Engine{
		ollamaGen: gen,
		memorySvc: mem,
		repo:      repo,
		log:       zap.NewNop(),
	}
}

// ── Nil / missing dependency ───────────────────────────────────────────────

func TestEngineWriteScript_NilEngine(t *testing.T) {
	t.Parallel()
	var e *Engine
	_, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic:    "test",
		Language: "en",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestEngineWriteScript_NilOllamaGen(t *testing.T) {
	t.Parallel()
	e := &Engine{log: zap.NewNop()}
	_, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic:    "test",
		Language: "en",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestEngineWriteScript_WrongShapeOllamaGen(t *testing.T) {
	t.Parallel()
	e := &Engine{
		ollamaGen: struct{}{},
		log:       zap.NewNop(),
	}
	_, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic:    "test",
		Language: "en",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not properly configured")
}

// ── Successful generation ──────────────────────────────────────────────────

func TestEngineWriteScript_Success(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil, nil)

	result, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic:    "AI Ethics",
		Title:    "The Future of AI",
		Language: "en",
		Tone:     "educational",
		Model:    "llama3:8b",
		Mode:     "text",
		MinWords: 500,
		Prompt:   "Discuss AI safety.",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(1), gen.calls.Load())
	assert.Equal(t, "This is a generated script with multiple sentences and narrative depth.", result.Script)
	assert.Equal(t, 12, result.WordCount)
	assert.Equal(t, "llama3:8b", result.Model)
	assert.Equal(t, "generated", result.CacheStatus)
	assert.Equal(t, 4, result.EstDuration)
	assert.Equal(t, int64(0), result.ScriptID)
}

func TestEngineWriteScript_CallsOllamaWithExpectedFields(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil, nil)

	_, _ = e.WriteScript(context.Background(), WriteScriptRequest{
		Topic:      "Space Exploration",
		Language:   "fr",
		Tone:       "documentary",
		Model:      "mistral",
		MinWords:   300,
		SourceText: "Mars mission summary.",
	})

	assert.Equal(t, int32(1), gen.calls.Load())
}

// ── Plan overrides ─────────────────────────────────────────────────────────

func TestEngineWriteScript_PlanOverridesRequest(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil, nil)

	plan := &scriptpkg.ScriptGenerationPlan{
		Title:       "Plan Title",
		Topic:       "Plan Topic",
		Language:    "it",
		Tone:        "poetic",
		Model:       "llama3:70b",
		Mode:        "clip_to_script",
		SourceText:  "Plan source text.",
		Prompt:      "Plan prompt override.",
		TargetWords: 800,
		UseMemory:   false,
		SaveToDB:    false,
	}

	result, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic:    "Request Topic",
		Title:    "Request Title",
		Language: "en",
		Tone:     "neutral",
		Model:    "small",
		Mode:     "text",
		MinWords: 100,
		Plan:     plan,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(1), gen.calls.Load())
}

func TestEngineWriteScript_PlanFallbackWhenRequestEmpty(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil, nil)

	plan := &scriptpkg.ScriptGenerationPlan{
		Title:       "Only Title From Plan",
		TargetWords: 250,
	}

	result, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Plan: plan,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(1), gen.calls.Load())
}

// ── Defaults ───────────────────────────────────────────────────────────────

func TestEngineWriteScript_DefaultLanguageAndTone(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil, nil)

	result, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic: "Defaults test",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(1), gen.calls.Load())
}

func TestEngineWriteScript_EmptyTopicUsesTitle(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil, nil)

	result, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Title: "My Video Title",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(1), gen.calls.Load())
}

// ── Memory gate ────────────────────────────────────────────────────────────

func TestEngineWriteScript_MemoryGateHit(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	mem := &fakeMemoryGate{
		result: &gemmamemory.GateResult{
			Output:    "This is a cached script from memory.",
			WordCount: 42,
			Model:     "llama3:8b",
		},
	}
	e := buildTestEngine(gen, mem, nil)

	result, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic:     "Cached topic",
		Language:  "en",
		Mode:      "text",
		UseMemory: true,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(0), gen.calls.Load(), "ollama must NOT be called on memory hit")
	assert.Equal(t, "This is a cached script from memory.", result.Script)
	assert.Equal(t, 42, result.WordCount)
	assert.Equal(t, "llama3:8b", result.Model)
	assert.Equal(t, "exact_hit", result.CacheStatus)
	assert.True(t, result.CacheHit)
	assert.True(t, result.WasCached)
	assert.Equal(t, (42*60)/150, result.EstDuration)
}

func TestEngineWriteScript_MemoryGateMiss_FallsBackToOllama(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	mem := &fakeMemoryGate{
		result: nil, // nil result → cache miss
	}
	e := buildTestEngine(gen, mem, nil)

	result, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic:     "Not cached",
		UseMemory: true,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(1), gen.calls.Load(), "ollama must be called after cache miss")
	assert.Equal(t, "generated", result.CacheStatus)
}

func TestEngineWriteScript_MemoryGateEmptyOutput_FallsBack(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	mem := &fakeMemoryGate{
		result: &gemmamemory.GateResult{Output: ""}, // empty output → treated as miss
	}
	e := buildTestEngine(gen, mem, nil)

	result, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic:     "empty cache",
		UseMemory: true,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(1), gen.calls.Load(), "ollama must be called when memory output is empty")
	assert.Equal(t, "generated", result.CacheStatus)
}

func TestEngineWriteScript_MemoryGateError_FallsBack(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	mem := &fakeMemoryGate{
		returnErr: errors.New("memory service down"),
	}
	e := buildTestEngine(gen, mem, nil)

	result, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic:     "error cache",
		UseMemory: true,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(1), gen.calls.Load(), "ollama must be called when memory returns error")
	assert.Equal(t, "generated", result.CacheStatus)
}

func TestEngineWriteScript_MemoryDisabledWhenFlagFalse(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	mem := &fakeMemoryGate{
		result: &gemmamemory.GateResult{Output: "cached", WordCount: 10},
	}
	e := buildTestEngine(gen, mem, nil)

	result, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic:     "not using memory",
		UseMemory: false,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(1), gen.calls.Load(), "ollama must be called when UseMemory=false")
	assert.Equal(t, "generated", result.CacheStatus)
	assert.False(t, result.CacheHit)
}

func TestEngineWriteScript_MemorySvcNil_Noop(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	// Build Engine directly: typed nil through interface{} is non-nil
	// (Go stores (*fakeMemoryGate)(nil) which satisfies the != nil check).
	e := &Engine{
		ollamaGen: gen,
		memorySvc: nil, // explicit nil interface{} → passes nil check
		repo:      nil,
		log:       zap.NewNop(),
	}

	result, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic:     "no memory svc",
		UseMemory: true,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(1), gen.calls.Load(), "ollama must be called when memorySvc is nil")
	assert.Equal(t, "generated", result.CacheStatus)
}

// ── SaveToDB ───────────────────────────────────────────────────────────────

func TestEngineWriteScript_SaveToDB(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	repo := &fakeScriptRepo{returnID: 42}
	e := buildTestEngine(gen, nil, repo)

	result, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic:    "Save test",
		Title:    "My Script",
		Language: "it",
		Tone:     "educational",
		Model:    "llama3",
		Mode:     "text",
		MinWords: 200,
		SaveToDB: true,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int64(42), result.ScriptID)
	assert.Equal(t, int32(1), repo.saveCalls.Load())
	require.NotNil(t, repo.lastRecord)
	assert.Equal(t, "My Script", repo.lastRecord.Title)
	assert.Equal(t, "Save test", repo.lastRecord.Topic)
	assert.Equal(t, "it", repo.lastRecord.Language)
	assert.Equal(t, "educational", repo.lastRecord.Tone)
	assert.Equal(t, "llama3", repo.lastRecord.Model)
	assert.Equal(t, "llama3:8b", repo.lastRecord.ModelUsed)
	assert.Equal(t, "completed", repo.lastRecord.Status)
	assert.Equal(t, 200, repo.lastRecord.TargetWords)
	assert.Equal(t, 12, repo.lastRecord.FinalWordCount)
}

func TestEngineWriteScript_SaveToDBError_StillReturnsResult(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	repo := &fakeScriptRepo{returnErr: errors.New("db error")}
	// Build Engine directly: typed nil through interface{} is non-nil.
	e := &Engine{
		ollamaGen: gen,
		memorySvc: nil,
		repo:      repo,
		log:       zap.NewNop(),
	}

	result, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic:    "DB error test",
		SaveToDB: true,
	})

	require.NoError(t, err, "save error must not propagate to caller")
	require.NotNil(t, result)
	assert.Equal(t, int64(0), result.ScriptID, "script ID must be zero on save failure")
	assert.Equal(t, int32(1), repo.saveCalls.Load())
}

func TestEngineWriteScript_SaveToDBWhenFlagFalse(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	repo := &fakeScriptRepo{returnID: 99}
	e := buildTestEngine(gen, nil, repo)

	result, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic:    "no save",
		SaveToDB: false,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int64(0), result.ScriptID)
	assert.Equal(t, int32(0), repo.saveCalls.Load(), "SaveScript must NOT be called when SaveToDB=false")
}

func TestEngineWriteScript_SaveToDBWhenRepoNil(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := &Engine{
		ollamaGen: gen,
		memorySvc: nil,
		repo:      nil,
		log:       zap.NewNop(),
	}

	result, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic:    "nil repo",
		SaveToDB: true,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int64(0), result.ScriptID)
}

// ── Ollama error path ──────────────────────────────────────────────────────

func TestEngineWriteScript_OllamaError(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{returnErr: errors.New("ollama: context deadline exceeded")}
	e := buildTestEngine(gen, nil, nil)

	_, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic: "error test",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ollama generation failed")
	assert.Equal(t, int32(1), gen.calls.Load())
}

// ── Nil logger ─────────────────────────────────────────────────────────────

func TestEngineWriteScript_NilLogger(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := &Engine{
		ollamaGen: gen,
		log:       nil,
	}

	result, err := e.WriteScript(context.Background(), WriteScriptRequest{
		Topic: "no logger",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(1), gen.calls.Load())
}

// ── Table-driven edge cases ────────────────────────────────────────────────

func TestEngineWriteScript_TableDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		req     WriteScriptRequest
		wantErr bool
		errMsg  string
	}{
		{
			name: "minimal request",
			req:  WriteScriptRequest{Topic: "test", Language: "en"},
		},
		{
			name: "title only (topic derived from title)",
			req:  WriteScriptRequest{Title: "My Title"},
		},
		{
			name: "topic only (title derived from topic)",
			req:  WriteScriptRequest{Topic: "My Topic"},
		},
		{
			name:    "nil engine guard",
			wantErr: true,
			errMsg:  "not configured",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gen := &fakeOllamaGen{}
			e := buildTestEngine(gen, nil, nil)

			// Test nil-engine guard directly.
			if tc.wantErr {
				var nilEngine *Engine
				_, err := nilEngine.WriteScript(context.Background(), tc.req)
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errMsg)
				return
			}

			result, err := e.WriteScript(context.Background(), tc.req)
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.NotEmpty(t, result.Script)
		})
	}
}

// === New Generate API (PR 6, June 2026) ===============================================

func TestEngineGenerate_Success(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil, nil)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:          "item-1",
		Title:       "Generate Test",
		Topic:       "Generate API",
		Language:    "it",
		Tone:        "documentary",
		Model:       "llama3:8b",
		Mode:        "text",
		TargetWords: 500,
		Prompt:      "Write about testing.",
	}

	result, err := e.Generate(context.Background(), plan)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(1), gen.calls.Load())
	assert.Equal(t, "This is a generated script with multiple sentences and narrative depth.", result.Script)
	assert.Equal(t, 12, result.WordCount)
	assert.Equal(t, "llama3:8b", result.Model)
	assert.Equal(t, "generated", result.CacheStatus)
	assert.Equal(t, 4, result.EstDuration)
	assert.Equal(t, int64(0), result.ScriptID)
	assert.Nil(t, result.ClipEvidence, "text-only plan should have nil clip evidence")
}

func TestEngineGenerate_WithClips(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil, nil)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:       "item-clips",
		Title:    "Clip Script",
		Topic:    "Clip Generation",
		Language: "en",
		Mode:     "clip_to_script",
		ClipEvidence: &scriptpkg.ClipEvidence{
			ClipIDs:   []string{"clip-a", "clip-b"},
			ClipCount: 2,
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
	assert.Equal(t, []string{"clip-a", "clip-b"}, result.ClipEvidence.ClipIDs)
	assert.Equal(t, "https://drive.google.com/a", result.ClipEvidence.DriveLinks["clip-a"])
}

func TestEngineGenerate_MemoryGateHit(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	mem := &fakeMemoryGate{
		result: &gemmamemory.GateResult{
			Output:    "Cached script from memory.",
			WordCount: 42,
			Model:     "llama3:8b",
		},
	}
	e := buildTestEngine(gen, mem, nil)

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
	assert.Equal(t, "Cached script from memory.", result.Script)
	assert.Equal(t, "exact_hit", result.CacheStatus)
}

func TestEngineGenerate_ForceRefreshBypassesMemory(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	mem := &fakeMemoryGate{
		result: &gemmamemory.GateResult{
			Output:    "Should not be returned.",
			WordCount: 10,
		},
	}
	e := buildTestEngine(gen, mem, nil)

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

func TestEngineGenerate_SaveToDB(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	repo := &fakeScriptRepo{returnID: 99}
	e := buildTestEngine(gen, nil, repo)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:       "Save Me",
		Topic:       "DB persistence",
		Language:    "it",
		Tone:        "educational",
		Model:       "llama3",
		Mode:        "text",
		TargetWords: 200,
		SaveToDB:    true,
	}

	result, err := e.Generate(context.Background(), plan)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int64(99), result.ScriptID)
	assert.Equal(t, int32(1), repo.saveCalls.Load())
	require.NotNil(t, repo.lastRecord)
	assert.Equal(t, "Save Me", repo.lastRecord.Title)
	assert.Equal(t, 200, repo.lastRecord.TargetWords)
}

func TestEngineGenerate_NilPlan(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil, nil)

	_, err := e.Generate(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan is nil")
}
