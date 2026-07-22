package adapters

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// Test 1: traduzione riuscita
func TestVoiceoverProcessor_UsesTranslatedTextAndLanguage(t *testing.T) {
	t.Parallel()

	stub := &stubItemExecutor{}
	proc := NewVoiceoverProcessor(stub, zap.NewNop())

	scenes := []scriptpkg.SpecScene{
		{ID: "scene-0", Index: 0, Text: "testo italiano", Kind: scriptpkg.SceneNarration},
	}
	spec := &scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes}

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:          "proj-1",
		Title:       "Test Project",
		Language:    "en",
		TranslateTo: "it",
	}

	input := ProcessInput{
		Text:              "testo italiano",
		SpecScene:         *spec,
		EffectiveLanguage: "it",
	}

	result, err := proc.Process(context.Background(), plan, input)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Voiceovers, 1)
	assert.Equal(t, "completed", result.Voiceovers[0].Status)
	assert.Equal(t, int32(1), stub.calls.Load())
}

// Test 2: traduzione fallita
func TestVoiceoverProcessor_SkipsWhenTranslationFailed(t *testing.T) {
	t.Parallel()

	stub := &stubItemExecutor{}
	proc := NewVoiceoverProcessor(stub, zap.NewNop())

	scenes := []scriptpkg.SpecScene{
		{ID: "scene-0", Index: 0, Text: "english text", Kind: scriptpkg.SceneNarration},
	}
	spec := &scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes}

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:          "proj-2",
		Title:       "Test Project 2",
		Language:    "en",
		TranslateTo: "it",
	}

	input := ProcessInput{
		Text:              "english text",
		SpecScene:         *spec,
		EffectiveLanguage: "en",
	}

	result, err := proc.Process(context.Background(), plan, input)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Voiceovers, 1)
	assert.Equal(t, "skipped", result.Voiceovers[0].Status)
	assert.Equal(t, int32(0), stub.calls.Load())
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "voiceover skipped")
}

// Test 3: nessuna traduzione
func TestVoiceoverProcessor_UsesSourceLanguageWithoutTranslation(t *testing.T) {
	t.Parallel()

	stub := &stubItemExecutor{}
	proc := NewVoiceoverProcessor(stub, zap.NewNop())

	scenes := []scriptpkg.SpecScene{
		{ID: "scene-0", Index: 0, Text: "english text", Kind: scriptpkg.SceneNarration},
	}
	spec := &scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes}

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:          "proj-3",
		Title:       "Test Project 3",
		Language:    "en",
		TranslateTo: "",
	}

	input := ProcessInput{
		Text:              "english text",
		SpecScene:         *spec,
		EffectiveLanguage: "en",
	}

	result, err := proc.Process(context.Background(), plan, input)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Voiceovers, 1)
	assert.Equal(t, "completed", result.Voiceovers[0].Status)
	assert.Equal(t, int32(1), stub.calls.Load())
}

// Test 4: stessa lingua
func TestVoiceoverProcessor_AllowsSameSourceAndTargetLanguage(t *testing.T) {
	t.Parallel()

	stub := &stubItemExecutor{}
	proc := NewVoiceoverProcessor(stub, zap.NewNop())

	scenes := []scriptpkg.SpecScene{
		{ID: "scene-0", Index: 0, Text: "testo italiano", Kind: scriptpkg.SceneNarration},
	}
	spec := &scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes}

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:          "proj-4",
		Title:       "Test Project 4",
		Language:    "it",
		TranslateTo: "it",
	}

	input := ProcessInput{
		Text:              "testo italiano",
		SpecScene:         *spec,
		EffectiveLanguage: "it",
	}

	result, err := proc.Process(context.Background(), plan, input)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Voiceovers, 1)
	assert.Equal(t, "completed", result.Voiceovers[0].Status)
	assert.Equal(t, int32(1), stub.calls.Load())
}
