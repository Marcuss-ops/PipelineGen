// Package scripts — generate_e2e_mandatory_test.go is the mandatory
// end-to-end acceptance suite for POST /api/script/generate.
//
// Each test exercises the full GenerateOneUseCase.Execute path with
// controlled fakes so the scenarios are deterministic and do not need
// a running Ollama / Drive / database.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
)

// buildUsecaseWithClipResolver returns a GenerateOneUseCase wired with a
// fake clip resolver, a fake Ollama generator, and a minimal postprocessor
// registry. Callers can optionally register extra postprocessors before the
// registry is frozen.
func buildUsecaseWithClipResolver(gen *fakeOllamaGen, clipResolver *fakeClipResolver) *GenerateOneUseCase {
	reg := adapters.NewSourceRegistry(zap.NewNop())
	if clipResolver != nil {
		reg.Register(scriptpkg.SourceClips, NewClipsSourceResolver(NewClipSourceBuilder(clipResolver, nil, zap.NewNop()), zap.NewNop()))
	}
	reg.Register(scriptpkg.SourceText, NewTextSourceResolver())
	reg.Freeze()

	e := buildTestEngine(gen, nil)
	ppReg := adapters.NewPostProcessorRegistry(zap.NewNop())
	// Sprint 1.0: extraProcs parameter dropped — the only caller
	// (TestGenerateE2E_DocumentServiceUnavailable) was retired when
	// inline document creation was decommissioned.
	// Wire the real clip-bindings processor so clip-source plans can
	// synthesise scenes when the engine returns plain text.
	ppReg.Register(adapters.NewClipBindingsProcessor(zap.NewNop()))
	ppReg.Register(&stubPostProcessor{
		name:   "persistence",
		result: &adapters.PostProcessResult{Changed: true},
	})
	ppReg.Freeze()

	return NewGenerateOneUseCase(adapters.NormalizationConfig{}, reg, e, ppReg, zap.NewNop())
}

// TestGenerateE2E_OneClipWithoutSourceText verifies that a single clip
// source with no explicit source_text resolves successfully and produces
// a script whose ClipEvidence contains the accepted clip.
func TestGenerateE2E_OneClipWithoutSourceText(t *testing.T) {
	t.Parallel()

	clipResolver := newFakeClipResolver()
	clipResolver.AddClip(makeTestClip("clip-1", "First Clip", 30*time.Second))

	gen := &fakeOllamaGen{result: &ollamatypes.GenerationResult{
		Script: canonicalSceneJSON(1, []string{"clip-1"}, ""), WordCount: 10, EstDuration: 3, Model: "llama3:8b",
	}}

	uc := buildUsecaseWithClipResolver(gen, clipResolver)
	item := makeClipsItem("e2e-one-clip-no-text", []string{"clip-1"}, "")

	result, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, []string{"clip-1"}, result.Source.AcceptedClipIDs)
}

// TestGenerateE2E_OneClipWithCompatibleSourceText verifies that a clip
// source plus an explicit source_text results in an Ollama prompt that
// contains both the clip context and the source_text.
func TestGenerateE2E_OneClipWithCompatibleSourceText(t *testing.T) {
	t.Parallel()

	clipResolver := newFakeClipResolver()
	clipResolver.AddClip(makeTestClip("clip-1", "First Clip", 30*time.Second))

	sourceText := "Use this editorial angle about the quick brown fox."
	gen := &fakeOllamaGen{result: &ollamatypes.GenerationResult{
		Script: canonicalSceneJSON(1, []string{"clip-1"}, ""), WordCount: 10, EstDuration: 3, Model: "llama3:8b",
	}}

	uc := buildUsecaseWithClipResolver(gen, clipResolver)
	item := makeClipsItem("e2e-one-clip-with-text", []string{"clip-1"}, sourceText)

	_, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
	require.NoError(t, err)

	captured := gen.capturedReq.Load()
	require.NotNil(t, captured)
	assert.Contains(t, captured.Prompt, "CLIP-GROUNDED WRITING RULES:")
	assert.Contains(t, captured.Prompt, "Use this editorial angle")
}

// TestGenerateE2E_MultipleClips verifies that three accepted clips are
// reflected in ClipEvidence and the Ollama prompt.

// TestGenerateE2E_SourcePrimaryGroundingPolicy verifies that the
// source_primary grounding policy is forwarded to the Ollama request
// and that the resulting prompt contains source-primary instructions.
func TestGenerateE2E_SourcePrimaryGroundingPolicy(t *testing.T) {
	t.Parallel()

	sourceText := "The quick brown fox jumps over the lazy dog."
	gen := &fakeOllamaGen{result: &ollamatypes.GenerationResult{
		Script: canonicalSceneJSON(1, nil, sourceText), WordCount: 10, EstDuration: 3, Model: "llama3:8b",
	}}

	uc := buildUsecaseWithClipResolver(gen, nil)
	item := makeTextOnlyItem("e2e-source-primary", sourceText)
	item.Source.GroundingPolicy = scriptpkg.GroundingPolicySourcePrimary

	_, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
	require.NoError(t, err)

	captured := gen.capturedReq.Load()
	require.NotNil(t, captured)
	assert.Equal(t, scriptpkg.GroundingPolicySourcePrimary, captured.GroundingPolicy)
}

// TestGenerateE2E_IncompatibleInput_FiveSecondClipNineHundredWords
// verifies that a 5-second clip paired with a 900-word target is rejected
// before a successful generation (validation or quality gate). The
// exact failure surface is implementation-defined, so the test only
// asserts a non-nil error and no panic.
func TestGenerateE2E_IncompatibleInput_FiveSecondClipNineHundredWords(t *testing.T) {
	t.Parallel()

	clipResolver := newFakeClipResolver()
	clipResolver.AddClip(makeTestClip("short-clip", "Short", 5*time.Second))

	sourceText := strings.Repeat("word ", 950)
	gen := &fakeOllamaGen{result: &ollamatypes.GenerationResult{
		Script: canonicalSceneJSON(1, []string{"short-clip"}, sourceText), WordCount: 10, EstDuration: 3, Model: "llama3:8b",
	}}

	uc := buildUsecaseWithClipResolver(gen, clipResolver)
	item := makeClipsItem("e2e-incompatible", []string{"short-clip"}, sourceText)
	item.ScriptParams.TargetWords = 900

	_, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
	require.Error(t, err, "incompatible input must be rejected")
}

// TestGenerateE2E_NonexistentClip verifies that a clip ID that cannot be
// resolved produces a typed source-resolution error.
func TestGenerateE2E_NonexistentClip(t *testing.T) {
	t.Parallel()

	clipResolver := newFakeClipResolver()
	gen := &fakeOllamaGen{result: &ollamatypes.GenerationResult{
		Script: canonicalSceneJSON(1, []string{"does-not-exist"}, ""), WordCount: 10, EstDuration: 3, Model: "llama3:8b",
	}}

	uc := buildUsecaseWithClipResolver(gen, clipResolver)
	item := makeClipsItem("e2e-missing-clip", []string{"does-not-exist"}, "")

	_, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, scriptpkg.ErrSourceResolutionFailed) || errors.Is(err, scriptpkg.ErrPlanInvalid),
		"missing clip must surface a typed source/plan error, got %v", err)
}

// TestGenerateE2E_OllamaUnavailable verifies that an Ollama failure
// surfaces the typed retryable generation error. A worker that crashes
// or is restarted will re-attempt jobs that fail with this error.
func TestGenerateE2E_OllamaUnavailable(t *testing.T) {
	t.Parallel()

	gen := &fakeOllamaGen{returnErr: errors.New("ollama connection refused")}
	uc := buildUsecaseWithClipResolver(gen, nil)
	item := makeTextOnlyItem("e2e-ollama-down", "Some source text for the script.")

	_, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, scriptpkg.ErrGenerationFailed),
		"Ollama failure must surface ErrGenerationFailed, got %v", err)
}

func TestGenerateE2E_ClipsPlainTextSynthesizesScenes(t *testing.T) {
	t.Parallel()

	clipResolver := newFakeClipResolver()
	clipResolver.AddClip(makeTestClip("clip-1", "First Clip", 30*time.Second))
	clipResolver.AddClip(makeTestClip("clip-2", "Second Clip", 30*time.Second))

	// Engine returns plain prose with a valid V1 envelope but no scenes.
	// Use text that overlaps with the clip evidence so the quality gate
	// passes without needing model-emitted scenes.
	plainText := buildOverlappingText(1, defaultClipSearchText)
	gen := &fakeOllamaGen{result: &ollamatypes.GenerationResult{
		Script:      fmt.Sprintf(`{"schema_version":1,"text":%q,"specscene":{"version":1,"scenes":[]}}`, plainText),
		WordCount:   10,
		EstDuration: 4,
		Model:       "llama3:8b",
	}}

	uc := buildUsecaseWithClipResolver(gen, clipResolver)
	item := makeClipsItem("e2e-clips-plain-text", []string{"clip-1", "clip-2"}, "")

	result, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, scriptpkg.ItemStatusSucceededWithWarnings, result.Status,
		"clip-native plan with valid evidence must succeed (with warnings after central classify) even when the engine emits no scenes")
	require.NotNil(t, result.ModeInfo)
	require.Equal(t, "clip_native", result.ModeInfo.UsedMode)
	require.False(t, result.ModeInfo.FallbackUsed,
		"synthesised clip-native scenes must not be treated as a fallback")

	require.Len(t, result.Output.SpecScene.Scenes, 2,
		"ClipBindingsProcessor must synthesise one scene per accepted clip")
	for i, sc := range result.Output.SpecScene.Scenes {
		require.NotEmpty(t, sc.Text, "synthesised scene[%d].Text must be populated", i)
		require.NotNil(t, sc.Bindings.Clip, "synthesised scene[%d] must have a clip binding", i)
		require.NotEmpty(t, sc.Bindings.Clip.ClipID, "synthesised scene[%d] must bind a clip_id", i)
	}
	require.Equal(t, "clip-1", result.Output.SpecScene.Scenes[0].Bindings.Clip.ClipID)
	require.Equal(t, "clip-2", result.Output.SpecScene.Scenes[1].Bindings.Clip.ClipID)
}

// TestGenerateE2E_Concurrency runs many generation requests in parallel
// and asserts that all complete without data races or panics.
func TestGenerateE2E_Concurrency(t *testing.T) {
	t.Parallel()

	clipResolver := newFakeClipResolver()
	for i := 0; i < 5; i++ {
		clipResolver.AddClip(makeTestClip(fmt.Sprintf("clip-%d", i), fmt.Sprintf("Clip %d", i), 10*time.Second))
	}

	gen := &fakeOllamaGen{result: &ollamatypes.GenerationResult{
		Script: canonicalSceneJSON(2, []string{"clip-0", "clip-1"}, ""), WordCount: 10, EstDuration: 6, Model: "llama3:8b",
	}}

	uc := buildUsecaseWithClipResolver(gen, clipResolver)

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			item := makeClipsItem(fmt.Sprintf("e2e-concurrent-%d", idx), []string{"clip-0", "clip-1"}, "")
			_, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "concurrent request %d failed", i)
	}
}
