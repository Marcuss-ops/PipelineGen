package scriptgeneration

import (
	"context"
	"testing"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/stretchr/testify/require"
)

func translationCoverageRequest() GenerateRequest {
	return GenerateRequest{SourceLanguage: "en", Languages: []Language{"en", "it"}}
}

func TestValidateTranslationCoverageAcceptsTranslatedScenes(t *testing.T) {
	result := &GenerateResult{Scenes: []Scene{{
		ID:   "scene-1",
		Text: map[Language]string{"en": "A source sentence long enough to compare.", "it": "Una frase sorgente abbastanza lunga da confrontare."},
	}}}
	require.NoError(t, validateTranslationCoverage(result, translationCoverageRequest()))
}

func TestValidateTranslationCoverageRejectsMissingTarget(t *testing.T) {
	result := &GenerateResult{Scenes: []Scene{{
		ID:   "scene-1",
		Text: map[Language]string{"en": "A source sentence long enough to compare."},
	}}}
	err := validateTranslationCoverage(result, translationCoverageRequest())
	require.ErrorIs(t, err, ErrTranslationIncomplete)
	require.Contains(t, err.Error(), "missing it translation")
}

func TestValidateTranslationCoverageRejectsSourceEcho(t *testing.T) {
	const source = "A source sentence long enough to compare."
	result := &GenerateResult{Scenes: []Scene{{
		ID:   "scene-1",
		Text: map[Language]string{"en": source, "it": source},
	}}}
	err := validateTranslationCoverage(result, translationCoverageRequest())
	require.ErrorIs(t, err, ErrTranslationIncomplete)
	require.Contains(t, err.Error(), "identical to the en source")
}

// TestTranslationPhaseFailsWhenTargetIsSourceEcho proves the postcondition is
// wired into the durable phase, not only unit-tested in isolation.
func TestTranslationPhaseFailsWhenTargetIsSourceEcho(t *testing.T) {
	runner, repo, textGen, translator, _, _ := newTestRunner()
	textGen.scenes = []Scene{{
		ID:   "scene-0",
		Text: map[Language]string{"en": "A source sentence long enough to compare."},
	}}
	translator.translate = func(_ context.Context, in TranslationInput) (string, error) {
		return in.SourceText, nil
	}
	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeNone
	req.Project = ""
	runID := "run-translation-echo"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))

	runner.Execute(context.Background(), runID, req)
	run := awaitCompletion(t, repo, runID, time.Second)
	require.Equal(t, RunStatusFailed, run.Status)
}
