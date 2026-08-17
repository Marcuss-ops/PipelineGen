package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// runAudioOnlyCombinedTimeline executes a COMBINED_TIMELINE run with a
// pure text source (no clips, no video segments). It returns the completed
// run, failing the test when the run does not reach COMPLETED.
func runAudioOnlyCombinedTimeline(t *testing.T, runner *Runner, repo *inMemRunRepository, runID string) *GenerationRun {
	t.Helper()
	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	if err := repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}); err != nil {
		t.Fatal(err)
	}
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "audio-only run must complete: %s", final.ErrorMessage)
	return final
}

func newAudioOnlyRunner(t *testing.T) (*Runner, *inMemRunRepository, *stubCombinedAudioRenderer, *stubRenderEnqueuer) {
	t.Helper()
	runner, repo, _, _, _, _, renderEnq := newTestRunner()
	renderer := &stubCombinedAudioRenderer{}
	runner.SetCombinedAudioRenderer(renderer)
	return runner, repo, renderer, renderEnq
}

// TestRunner_AudioOnlyCombinedTimelineProducesFinalAudio certifies that the
// audio phase runs even without a video render: the certified final audio,
// the canonical timeline and the compiled audio plan must all be persisted.
func TestRunner_AudioOnlyCombinedTimelineProducesFinalAudio(t *testing.T) {
	runner, repo, _, _ := newAudioOnlyRunner(t)
	final := runAudioOnlyCombinedTimeline(t, runner, repo, "run-audio-only-produces")

	res := final.Result
	require.NotNil(t, res, "result must be present")
	require.NotNil(t, res.FinalAudio, "FinalAudio must be produced by the audio-only run")
	require.NotNil(t, res.CanonicalTimeline, "CanonicalTimeline must be produced by the audio-only run")
	require.NotNil(t, res.AudioPlan, "AudioPlan must be produced by the audio-only run")
}

// TestRunner_AudioOnlyCombinedTimelineDoesNotRequireVideoSegments certifies
// that an audio-only run never demands materialized video segments: the
// text-source scenes carry no clips and the run still completes.
func TestRunner_AudioOnlyCombinedTimelineDoesNotRequireVideoSegments(t *testing.T) {
	runner, repo, _, _ := newAudioOnlyRunner(t)
	final := runAudioOnlyCombinedTimeline(t, runner, repo, "run-audio-only-no-segments")

	for _, scene := range final.Result.Scenes {
		require.Nil(t, scene.Clip, "audio-only text source must not carry clip segments")
		require.Len(t, scene.Clips, 0, "audio-only text source must not carry clip segments")
	}
}

// TestRunner_AudioOnlyTextSourceSucceeds certifies the exact failing
// combination from the bug report: source.type=text with no clips, combined
// audio mode and no video render completes end to end.
func TestRunner_AudioOnlyTextSourceSucceeds(t *testing.T) {
	runner, repo, _, _ := newAudioOnlyRunner(t)
	final := runAudioOnlyCombinedTimeline(t, runner, repo, "run-audio-only-text-source")

	require.NotNil(t, final.Result.FinalAudio)
	require.Len(t, final.Result.Scenes, 3, "all text-source scenes must survive")
}

// TestRunner_AudioOnlyDoesNotEnqueueVideoRender certifies that no video
// render job is enqueued: the enqueuer is never invoked and no render-job
// reference is published.
func TestRunner_AudioOnlyDoesNotEnqueueVideoRender(t *testing.T) {
	runner, repo, _, renderEnq := newAudioOnlyRunner(t)
	runAudioOnlyCombinedTimeline(t, runner, repo, "run-audio-only-no-enqueue")

	require.Equal(t, 0, renderEnq.callCount, "video render enqueuer must never be called")
}

// TestRunner_AudioOnlyStillUsesCombinedAudioRenderer certifies that the
// audio-only path still renders through the Rust CombinedAudioRenderer —
// the audio master is never bypassed or weakened.
func TestRunner_AudioOnlyStillUsesCombinedAudioRenderer(t *testing.T) {
	runner, repo, renderer, _ := newAudioOnlyRunner(t)
	runAudioOnlyCombinedTimeline(t, runner, repo, "run-audio-only-renderer")

	require.Equal(t, 1, renderer.calls, "CombinedAudioRenderer must be invoked exactly once")
}

// TestRunner_AudioOnlyFinalAudioIsCertified certifies the full certification
// surface of the audio-only master: final mix, copy eligibility and the
// strategy remain FINAL_AUDIO_COPY.
func TestRunner_AudioOnlyFinalAudioIsCertified(t *testing.T) {
	runner, repo, _, _ := newAudioOnlyRunner(t)
	final := runAudioOnlyCombinedTimeline(t, runner, repo, "run-audio-only-certified")

	fa := final.Result.FinalAudio
	require.NotNil(t, fa)
	require.True(t, fa.FinalMix, "FinalAudio.FinalMix must be true")
	require.True(t, fa.CopyEligible, "FinalAudio.CopyEligible must be true")
	require.NotEmpty(t, fa.FinalAudioSHA256, "FinalAudio must carry its integrity hash")
	require.Equal(t, capabilityaudio.FinalAudioCopy, final.Result.AudioStrategy)
}

// TestRunner_AudioOnlyKeepsDuckedClipAudio certifies the full production flow
// (not just the compile helper): an audio-only COMBINED_TIMELINE run with a
// real clip must persist an audio plan whose mix policy is
// VOICEOVER_DUCKED_CLIP with clip-audio events and ducking automation — the
// original clip audio is audible in the master even without a video render.
func TestRunner_AudioOnlyKeepsDuckedClipAudio(t *testing.T) {
	runner, repo, textGen, _, _, _, renderEnq := newTestRunner()
	renderer := &stubCombinedAudioRenderer{}
	runner.SetCombinedAudioRenderer(renderer)
	textGen.scenes = []Scene{
		{
			ID:         "scene-0",
			Index:      0,
			DurationUS: 8_000_000,
			Clip:       &ClipReference{ID: "clip-12", AudioPath: "/media/clip-12.mp4", SourceInMS: 1000, SourceOutMS: 13000},
			Clips:      []*ClipReference{{ID: "clip-12", AudioPath: "/media/clip-12.mp4", SourceInMS: 1000, SourceOutMS: 13000}},
			Audio:      capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioClip, ClipAssetID: "clip-12", SourceInUS: 1_000_000, SourceDurationUS: 12_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 12_000_000, UseOriginalAudio: true},
			AudioIntents: []capabilityaudio.AudioIntent{
				{Mode: capabilityaudio.AudioClip, ClipAssetID: "clip-12", SourceInUS: 1_000_000, SourceDurationUS: 12_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 12_000_000, UseOriginalAudio: true},
			},
			Voiceover: map[Language]AudioReference{"en": {ID: "vo-0", FilePath: "/tmp/vo-0.mp3", Duration: 8.0}},
			Text:      map[Language]string{"en": "A comedian clip with original audio under the narration."},
		},
	}

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.SourceLanguage = "en"
	req.Languages = []Language{"en"}
	runID := "run-audio-only-ducked-clip"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "audio-only clip run must complete: %s", final.ErrorMessage)

	res := final.Result
	require.NotNil(t, res.AudioPlan, "audio plan must be persisted")
	require.Equal(t, capabilityaudio.MixVoiceoverWithDuckedClip, res.AudioPlan.MixPolicy, "audio-only master must duck the clip under the voiceover")
	require.NotNil(t, res.CanonicalTimeline, "canonical timeline must be persisted")
	require.Equal(t, 0, renderEnq.callCount, "video render enqueuer must never be called")

	clipEvents := eventsForRole(*res.AudioPlan, capabilityaudio.TrackClipAudio)
	require.Len(t, clipEvents, 1, "clip audio event must be part of the audio-only master")
	require.Equal(t, capabilityaudio.DuckClipBaseGainDB, clipEvents[0].GainDB, "clip audio must sit at the ducked base gain")
	require.NotEmpty(t, res.AudioPlan.Automation, "ducking automation must be part of the audio-only master")
}
