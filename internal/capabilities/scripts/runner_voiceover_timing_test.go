// Package scriptgeneration — runner_voiceover_timing_test.go certifies the
// fail-closed timing-policy propagation on the durable runner:
//
//   - req.Timing reaches every per-scene VoiceoverInput so the per-item
//     pipeline can enforce the required/best-effort semantics;
//   - a required-timing failure surfaced by the voiceover generator FAILS
//     the run (never completes with plausible-but-wrong timestamps).
package scriptgeneration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// recordingTimingPolicyGenerator records the VoiceoverInput it receives and,
// when failOnRequired is set, fails closed for a required timing policy —
// simulating the per-item pipeline surfacing "timing unavailable" (zero word
// boundaries) under a required policy.
type recordingTimingPolicyGenerator struct {
	mu             sync.Mutex
	inputs         []VoiceoverInput
	failOnRequired bool
}

func (g *recordingTimingPolicyGenerator) Generate(_ context.Context, input VoiceoverInput) (AudioReference, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inputs = append(g.inputs, input)
	if g.failOnRequired && input.Timing != nil && input.Timing.Mode == capabilityaudio.TimingRequired {
		return AudioReference{}, errors.New("voiceover timing unavailable: no word boundaries for required timing")
	}
	return AudioReference{
		ID:       "vo-" + input.SceneID + "-" + string(input.Language),
		FilePath: "/tmp/voiceover-" + input.SceneID + "-" + string(input.Language) + ".mp3",
		Duration: 1.0,
	}, nil
}

func (g *recordingTimingPolicyGenerator) capturedTimings() []*capabilityaudio.TimingRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]*capabilityaudio.TimingRequest, 0, len(g.inputs))
	for _, in := range g.inputs {
		out = append(out, in.Timing)
	}
	return out
}

func requiredTiming() *capabilityaudio.TimingRequest {
	return &capabilityaudio.TimingRequest{
		Mode:         capabilityaudio.TimingRequired,
		BoundaryMode: capabilityaudio.BoundaryWord,
		Formats:      []capabilityaudio.TimingFormat{capabilityaudio.TimingJSON},
	}
}

// TestRunner_ForwardsTimingPolicyToVoiceoverInput pins the propagation: the
// request-level voiceover timing policy must reach every per-scene
// VoiceoverInput.Timing so the per-item pipeline (ProcessSegmentUseCase →
// publishTimingBundle) can enforce the required/best-effort fail-closed
// semantics instead of silently degrading to best_effort.
func TestRunner_ForwardsTimingPolicyToVoiceoverInput(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()
	voiceoverGen := &recordingTimingPolicyGenerator{}
	docPub := newStubDocumentPublisher()

	runner := NewRunner(repo, textGen, translator, voiceoverGen, docPub, canonicalTestDocumentRenderer{})
	runner.SetLogger(zap.NewNop())
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	req.Timing = requiredTiming()

	runID := "run-forwards-timing"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "run must complete: %s", final.ErrorMessage)

	timings := voiceoverGen.capturedTimings()
	require.NotEmpty(t, timings, "voiceover generator must be invoked")
	for _, policy := range timings {
		require.NotNil(t, policy, "timing policy must be forwarded to VoiceoverInput")
		require.Equal(t, capabilityaudio.TimingRequired, policy.Mode)
	}
}

// TestRunner_RequiredTimingFailureFailsRun pins the fail-closed contract: a
// required-timing failure surfaced by the voiceover generator must FAIL the
// run — never complete with plausible-but-wrong timestamps.
func TestRunner_RequiredTimingFailureFailsRun(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()
	voiceoverGen := &recordingTimingPolicyGenerator{failOnRequired: true}
	docPub := newStubDocumentPublisher()

	runner := NewRunner(repo, textGen, translator, voiceoverGen, docPub, canonicalTestDocumentRenderer{})
	runner.SetLogger(zap.NewNop())
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	req.Timing = requiredTiming()

	runID := "run-required-timing-fail"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusFailed, final.Status, "required-timing failure must fail the run")
	require.Equal(t, StageGeneratingVoiceovers, final.FailedStage)
}

// TestRunner_BestEffortTimingMissingDoesNotFailRun pins the policy boundary:
// best_effort timing (no explicit required policy) must NOT fail the run when
// the voiceover carries no timing artifact — the projection stays empty
// rather than aborting.
func TestRunner_BestEffortTimingMissingDoesNotFailRun(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()
	voiceoverGen := &recordingTimingPolicyGenerator{}
	docPub := newStubDocumentPublisher()

	runner := NewRunner(repo, textGen, translator, voiceoverGen, docPub, canonicalTestDocumentRenderer{})
	runner.SetLogger(zap.NewNop())
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	// Timing left nil → canonical defaults (best_effort). The generator
	// returns audio WITHOUT timing, which must be a legitimate no-op.
	req.Timing = nil

	runID := "run-best-effort-no-timing"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "best-effort timing-less run must complete: %s", final.ErrorMessage)
}
