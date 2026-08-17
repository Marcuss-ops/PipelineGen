// Package scriptgeneration — runner_timeline_only_test.go certifies the
// semantic separation between GenerateTimeline (metadata planning) and audio
// materialization:
//
//	GenerateTimeline=true, audio NONE → canonical timeline compiled from
//	    Drive-only clip references (no Path, no SHA-256), no final audio,
//	    no render enqueue, run completes.
//
// PipelineGen is audio-only: there is no video render seal anymore.
// This is the regression guard for the "46 Love clip" workflow: a script +
// canonical timeline must be producible from Drive-backed clips without
// downloading any MP4 locally.
package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

func driveOnlyClipScenes() []Scene {
	clips := []*ClipReference{
		{ID: "clip-drive-1", Title: "Drive clip 1", DriveLink: "https://drive.google.com/file/d/abc", SourceInMS: 0, SourceOutMS: 12000, Duration: 12},
		{ID: "clip-drive-2", Title: "Drive clip 2", DriveLink: "https://drive.google.com/file/d/def", SourceInMS: 0, SourceOutMS: 8000, Duration: 8},
	}
	intents := []capabilityaudio.AudioIntent{
		{Mode: capabilityaudio.AudioClip, ClipAssetID: clips[0].ID, SourceInUS: 0, SourceDurationUS: 12_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 12_000_000, UseOriginalAudio: true},
		{Mode: capabilityaudio.AudioClip, ClipAssetID: clips[1].ID, SourceInUS: 0, SourceDurationUS: 8_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 8_000_000, UseOriginalAudio: true},
	}
	return []Scene{
		{ID: "scene-0", Index: 0, DurationMS: 12000, DurationUS: 12_000_000, Clip: clips[0], Text: map[Language]string{"en": "First scene over a drive-only clip."}, Audio: intents[0], AudioIntents: []capabilityaudio.AudioIntent{intents[0]}},
		{ID: "scene-1", Index: 1, DurationMS: 8000, DurationUS: 8_000_000, Clip: clips[1], Text: map[Language]string{"en": "Second scene over a drive-only clip."}, Audio: intents[1], AudioIntents: []capabilityaudio.AudioIntent{intents[1]}},
	}
}

// TestRunner_GenerateTimeline_DriveOnlyClipsDoesNotRequireLocalBinary
// certifies that GenerateTimeline=true + audio NONE
// compiles the canonical timeline from Drive-only clip references (empty
// Path and SHA-256), never produces a video render, never enqueues a render
// job, and never generates voiceovers (audio NONE gate).
func TestRunner_GenerateTimeline_DriveOnlyClipsDoesNotRequireLocalBinary(t *testing.T) {
	runner, repo, textGen, _, voiceoverGen, _, renderEnq := newTestRunner()
	textGen.scenes = driveOnlyClipScenes()

	req := defaultTestRequest()
	req.GenerateTimeline = true
	req.Audio = capabilityaudio.AudioModeNone
	req.Docs = DocumentsConfig{Enabled: false}

	runID := "run-timeline-driveonly-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status, "timeline-only run must complete without local binaries: %s", final.ErrorMessage)

	require.NotNil(t, final.Result.CanonicalTimeline, "GenerateTimeline=true must produce a canonical timeline")
	assert.Equal(t, 0, renderEnq.callCount, "renderEnq must not be called")
	assert.Equal(t, 0, voiceoverGen.callCount, "audio NONE must not generate voiceovers")
	for _, s := range final.Result.Scenes {
		assert.Empty(t, s.Voiceover, "scene %s must have no voiceover with audio NONE", s.ID)
	}
}

// TestRunner_AudioModeNone_SkipsVoiceoverPhase certifies the voiceover
// gate: with audio.mode NONE (or omitted → NONE) the runner must skip the
// voiceover stage entirely — zero TTS calls, zero staged/published
// voiceover artifacts — while the rest of the pipeline still completes.
func TestRunner_AudioModeNone_SkipsVoiceoverPhase(t *testing.T) {
	runner, repo, _, _, voiceoverGen, _, _ := newTestRunner()
	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeNone
	req.Docs = DocumentsConfig{Enabled: false}

	runID := "run-none-vo-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)

	assert.Equal(t, 0, voiceoverGen.callCount, "audio NONE must skip the voiceover phase")
	for _, s := range final.Result.Scenes {
		assert.Empty(t, s.Voiceover, "scene %s must have no voiceover with audio NONE", s.ID)
	}
}

// TestRunner_ChunkedVoiceover_GeneratesVoiceovers is the positive side of
// the gate: CHUNKED_VOICEOVER (the default test request) still generates
// per-scene voiceovers.
func TestRunner_ChunkedVoiceover_GeneratesVoiceovers(t *testing.T) {
	runner, repo, _, _, voiceoverGen, _, _ := newTestRunner()
	req := defaultTestRequest()
	req.Docs = DocumentsConfig{Enabled: false}

	runID := "run-chunked-vo-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)

	assert.Greater(t, voiceoverGen.callCount, 0, "CHUNKED_VOICEOVER must generate voiceovers")
	for _, s := range final.Result.Scenes {
		assert.NotEmpty(t, s.Voiceover["en"].ID, "scene %s must have an EN voiceover", s.ID)
	}
}

// TestRunner_GenerateTimelineFalse_SkipsTimeline: when no timeline is
// requested and audio is NONE, the audio compile phase stays skipped and no
// canonical timeline is produced.
func TestRunner_GenerateTimelineFalse_SkipsTimeline(t *testing.T) {
	runner, repo, _, _, _, _, renderEnq := newTestRunner()
	req := defaultTestRequest()
	req.GenerateTimeline = false
	req.Audio = capabilityaudio.AudioModeNone
	req.Docs = DocumentsConfig{Enabled: false}

	runID := "run-notimeline-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)

	assert.Nil(t, final.Result.CanonicalTimeline)
	assert.Equal(t, 0, renderEnq.callCount)
}
