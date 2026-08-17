package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// clipIdentityGateScene returns a timeline-only scene whose narration names a
// person ("Tom Holland") but whose bound clip carries no subject identity —
// the exact mismatch the scene↔clip identity gate must catch.
func clipIdentityGateScene() Scene {
	clip := &ClipReference{ID: "clip-wrong", DriveLink: "https://drive.google.com/file/d/wrong", SourceInMS: 0, SourceOutMS: 12000, Duration: 12}
	intent := capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioClip, ClipAssetID: "clip-wrong", SourceInUS: 0, SourceDurationUS: 12_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 12_000_000, UseOriginalAudio: true}
	return Scene{
		ID: "scene-0", Index: 0, DurationMS: 12000, DurationUS: 12_000_000,
		Clip:         clip,
		Annotations:  personAnnotations("Tom Holland"),
		Audio:        intent,
		AudioIntents: []capabilityaudio.AudioIntent{intent},
		Text:         map[Language]string{"en": "Tom Holland scene."},
	}
}

// TestRunnerClipIdentityGateReportOnlyDoesNotBlock certifies the default
// report-only mode: a scene↔clip identity mismatch is recorded (metric +
// warning) but must NOT fail the run, so existing runs are not surprised.
func TestRunnerClipIdentityGateReportOnlyDoesNotBlock(t *testing.T) {
	runner, repo, textGen, _, _, _, _ := newTestRunner()
	textGen.scenes = []Scene{clipIdentityGateScene()}

	req := defaultTestRequest()
	req.GenerateTimeline = true
	req.Audio = capabilityaudio.AudioModeNone
	req.Docs = DocumentsConfig{Enabled: false}
	// req.EnforceClipIdentity defaults to false → report-only.

	runID := "run-clip-identity-report-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status, "report-only identity gate must not block: %s", final.ErrorMessage)
}

// TestRunnerClipIdentityGateEnforceFailsClosed certifies the opt-in
// fail-closed mode: with EnforceClipIdentity=true the same mismatch blocks the
// run at the render-payload stage before any payload is built.
func TestRunnerClipIdentityGateEnforceFailsClosed(t *testing.T) {
	runner, repo, textGen, _, _, _, _ := newTestRunner()
	textGen.scenes = []Scene{clipIdentityGateScene()}

	req := defaultTestRequest()
	req.GenerateTimeline = true
	req.Audio = capabilityaudio.AudioModeNone
	req.Docs = DocumentsConfig{Enabled: false}
	req.EnforceClipIdentity = true

	runID := "run-clip-identity-enforce-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusFailed, final.Status, "enforced identity gate must fail closed")
	require.Equal(t, StageCompilingAudio, final.FailedStage)
	require.Contains(t, final.ErrorMessage, "scene↔clip identity certification failed")
}
