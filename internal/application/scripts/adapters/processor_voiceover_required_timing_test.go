package adapters

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func requiredTimingPlan() *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		ID:       "item-vo-required",
		Title:    "Required Timing",
		Language: "en",
		Timing:   &audio.TimingRequest{Mode: audio.TimingRequired, BoundaryMode: audio.BoundaryWord, Formats: []audio.TimingFormat{audio.TimingJSON}},
	}
}

func singleSceneSpec() *scriptpkg.SpecSceneOutput {
	return &scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
		{ID: "scene-0", Index: 0, Text: "scene zero", Kind: scriptpkg.SceneNarration},
	}}
}

// TestVoiceoverProcessor_Policy_RequiredTimingIsRequired pins that the
// required-timing policy upgrades the voiceover processor to
// ProcessorRequired, so a Process error fails the job via the registry's
// requiredFails gate.
func TestVoiceoverProcessor_Policy_RequiredTimingIsRequired(t *testing.T) {
	proc := NewVoiceoverProcessor(&stubItemExecutor{}, zap.NewNop())

	require.Equal(t, ProcessorRequired, proc.Policy(&scriptpkg.ResolvedGenerationPlan{
		Timing: &audio.TimingRequest{Mode: audio.TimingRequired},
	}))
	require.Equal(t, ProcessorBestEffort, proc.Policy(&scriptpkg.ResolvedGenerationPlan{
		Timing: &audio.TimingRequest{Mode: audio.TimingBestEffort},
	}))
	require.Equal(t, ProcessorBestEffort, proc.Policy(&scriptpkg.ResolvedGenerationPlan{}))
	require.Equal(t, ProcessorBestEffort, proc.Policy(nil))
}

// TestVoiceoverProcessor_RequiredTiming_FailureFailsJob pins the fail-closed
// contract: required timing + a failing scene must return an error from
// Process (the job fails), never degrade into a warning.
func TestVoiceoverProcessor_RequiredTiming_FailureFailsJob(t *testing.T) {
	stub := &stubItemExecutor{
		fn: func(text, lang, filename string) (*voiceover.VoiceoverItemResult, error) {
			return nil, errors.New("VOICEOVER_TIMING_UNAVAILABLE: TTS produced no word boundaries for required timing")
		},
	}
	proc := NewVoiceoverProcessor(stub, zap.NewNop())

	_, err := proc.Process(context.Background(), requiredTimingPlan(), ProcessInput{
		Text:      "Generated body.",
		SpecScene: *singleSceneSpec(),
	})
	require.Error(t, err, "required timing + voiceover failure must fail the job")
	require.Contains(t, err.Error(), "required-timing")
	require.Contains(t, err.Error(), "scene 0")
}

// TestVoiceoverProcessor_BestEffortTiming_FailureWarnsNotFails pins the
// non-required contract: a best-effort timing plan keeps the legacy
// warning-collection behavior (no error, warning surfaced).
func TestVoiceoverProcessor_BestEffortTiming_FailureWarnsNotFails(t *testing.T) {
	stub := &stubItemExecutor{
		fn: func(text, lang, filename string) (*voiceover.VoiceoverItemResult, error) {
			return nil, errors.New("tts python socket closed")
		},
	}
	proc := NewVoiceoverProcessor(stub, zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:       "item-vo-be",
		Title:    "Best Effort",
		Language: "en",
		Timing:   &audio.TimingRequest{Mode: audio.TimingBestEffort},
	}

	result, err := proc.Process(context.Background(), plan, ProcessInput{
		Text:      "Generated body.",
		SpecScene: *singleSceneSpec(),
	})
	require.NoError(t, err, "best-effort timing must not fail the job on a voiceover failure")
	require.NotNil(t, result)
	require.NotEmpty(t, result.Warnings)
}
