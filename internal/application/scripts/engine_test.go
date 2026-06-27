// Package scripts — engine_test.go exercises Engine.Generate with
// fake implementations of scriptOllamaGenerator, memoryGateChecker, and
// ScriptRepository so the full parameter-resolution, caching, save-to-db,
// and error paths can be validated without a real LLM or database.
//
// AGENT-3 (June 2026): Engine uses narrow interfaces (scriptOllamaGenerator,
// memoryGateChecker) defined in engine.go alongside the compile-time
// assertions that the concrete *ollama.Generator and *gemmamemory.Service
// satisfy them. Tests inject typed fakes.
//
// PR 13 (June 2026): removed deprecated WriteScript tests — all tests
// now exercise Engine.Generate directly.
package scripts

import (
	"context"
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

func TestEngineGenerate_WrongShapeOllamaGen(t *testing.T) {
	t.Parallel()
	e := &Engine{
		ollamaGen: struct{}{},
		log:       zap.NewNop(),
	}
	_, err := e.Generate(context.Background(), &scriptpkg.ResolvedGenerationPlan{
		Topic:    "test",
		Language: "en",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not properly configured")
}

// ── Successful generation ──────────────────────────────────────────────────

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
