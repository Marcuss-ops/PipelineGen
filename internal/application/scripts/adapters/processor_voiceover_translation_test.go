package adapters

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestVoiceoverProcessor_UsesTranslatedSceneTextAndTargetLanguage(t *testing.T) {
	var gotText string
	var gotLanguage string
	stub := &stubItemExecutor{
		fn: func(text, lang, filename string) (*voiceover.VoiceoverItemResult, error) {
			gotText = text
			gotLanguage = lang
			return &voiceover.VoiceoverItemResult{
				Status:    voiceover.StatusCompleted,
				Language:  voiceover.Language(lang),
				Filename:  filename,
				LocalPath: "/tmp/" + filename,
				DriveLink: "https://drive.example.test/" + filename,
			}, nil
		},
	}
	proc := NewVoiceoverProcessor(stub, zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:          "translated-voiceover",
		Title:       "Translated Voiceover",
		Language:    "en",
		TranslateTo: "it",
	}
	originalSpec := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{{
			ID:    "scene-1",
			Index: 0,
			Title: "Original title",
			Text:  "English source scene.",
			Kind:  scriptpkg.SceneNarration,
		}},
	}
	translatedSpec := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{{
			ID:    "scene-1",
			Index: 0,
			Title: "Titolo tradotto",
			Text:  "Scena sorgente tradotta in italiano.",
			Kind:  scriptpkg.SceneNarration,
		}},
	}

	result, err := proc.Process(context.Background(), plan, ProcessInput{
		Text:              "Testo completo tradotto in italiano.",
		SpecScene:         translatedSpec,
		OriginalText:      "Complete English source text.",
		OriginalSpecScene: originalSpec,
		EffectiveLanguage: "it",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Voiceovers, 1)
	assert.Equal(t, int32(1), stub.calls.Load())
	assert.Equal(t, "Scena sorgente tradotta in italiano.", gotText)
	assert.Equal(t, "it", gotLanguage)
	assert.Equal(t, "completed", result.Voiceovers[0].Status)
	assert.Empty(t, result.Warnings)
}

func TestVoiceoverProcessor_PropagatesSceneDuration(t *testing.T) {
	stub := &stubItemExecutor{
		fn: func(text, lang, filename string) (*voiceover.VoiceoverItemResult, error) {
			return &voiceover.VoiceoverItemResult{
				Status:     voiceover.StatusCompleted,
				Language:   voiceover.Language(lang),
				Filename:   filename,
				DriveLink:  "https://drive.example.test/" + filename,
				LocalPath:  "/tmp/" + filename,
				DurationMs: 4321,
			}, nil
		},
	}
	proc := NewVoiceoverProcessor(stub, zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "duration-voiceover", Title: "Duration", Language: "en"}
	result, err := proc.Process(context.Background(), plan, ProcessInput{
		Text:      "Narration text.",
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{ID: "scene-0", Index: 0, Text: "Narration text."}}},
	})
	require.NoError(t, err)
	require.Len(t, result.Voiceovers, 1)
	assert.Equal(t, int64(4321), result.Voiceovers[0].DurationMs)
}

func TestVoiceoverProcessor_SkipsWhenRequestedTranslationDidNotComplete(t *testing.T) {
	stub := &stubItemExecutor{}
	proc := NewVoiceoverProcessor(stub, zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:          "failed-translation-voiceover",
		Title:       "Failed Translation Voiceover",
		Language:    "en",
		TranslateTo: "it",
	}
	originalSpec := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{{
			ID:    "scene-1",
			Index: 0,
			Text:  "English source scene.",
			Kind:  scriptpkg.SceneNarration,
		}},
	}

	result, err := proc.Process(context.Background(), plan, ProcessInput{
		Text:              "Complete English source text.",
		SpecScene:         originalSpec,
		EffectiveLanguage: "en",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(0), stub.calls.Load(), "TTS must not run when target-language translation is unavailable")
	require.Len(t, result.Voiceovers, 1)
	assert.Equal(t, "skipped", result.Voiceovers[0].Status)
	require.Len(t, result.Warnings, 1)
	assert.True(t, strings.Contains(result.Warnings[0], "requested translation to \"it\" was not completed"),
		"warning must explain why voiceover was skipped; got %q", result.Warnings[0])
}
