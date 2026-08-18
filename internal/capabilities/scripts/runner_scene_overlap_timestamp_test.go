// Package scriptgeneration — runner_scene_overlap_timestamp_test.go is the
// decisive streaming-overlap test. It asserts the durable per-scene timestamps
// the SceneReadyCoordinator records, so the proof lives on the result instead
// of on test-side wall-clock observations:
//
//	scene[0].translation_started_at < scene[1].text_ready_at
//	scene[0].tts_started_at         < scene[1].text_ready_at
//
// Under a global barrier (all scenes generated before any translation) the
// first inequality would be inverted — scene 1's text_ready_at would precede
// scene 0's translation start — so this assertion is exactly the boundary that
// separates real streaming from an all-or-nothing batch.
package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSceneTextStreaming_TranslationTTSStartBeforeNextSceneReady pins the
// decisive timestamp contract. The streamer emits scene 0 and then blocks
// before scene 1; the SceneReadyCoordinator starts scene 0's translation and
// TTS while generation is still blocked, so scene 0's start timestamps must
// precede scene 1's text-ready timestamp in the final durable result.
func TestSceneTextStreaming_TranslationTTSStartBeforeNextSceneReady(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	streamer := newGatedStreamingTextGenerator(defaultTestScenes())
	translator := &readyProbeTranslator{started: make(chan struct{})}
	voiceover := &readyProbeVoiceover{started: make(chan struct{})}
	runner.textGen = streamer
	runner.translator = translator
	runner.voiceoverGen = voiceover

	req := defaultTestRequest()
	runID := "run-overlap-timestamps-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() { defer close(done); runner.Execute(context.Background(), runID, req) }()

	// Prove the downstream actually ran while generation was still blocked
	// before scene 1: scene 0 must be emitted, then its translation and TTS
	// must start — all before we release the streamer.
	select {
	case <-streamer.emitted:
	case <-time.After(5 * time.Second):
		t.Fatal("scene 0 was not emitted")
	}
	select {
	case <-translator.started:
	case <-time.After(5 * time.Second):
		t.Fatal("scene 0 translation did not start before the streamer was released")
	}
	select {
	case <-voiceover.started:
	case <-time.After(5 * time.Second):
		t.Fatal("scene 0 TTS did not start before the streamer was released")
	}

	close(streamer.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("streaming run did not complete")
	}

	final := awaitCompletion(t, repo, runID, time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)
	require.NotNil(t, final.Result)
	require.Len(t, final.Result.Scenes, 3, "all three scenes must survive")

	scene0 := final.Result.Scenes[0]
	scene1 := final.Result.Scenes[1]
	scene2 := final.Result.Scenes[2]

	require.False(t, scene0.TranslationStartedAt.IsZero(), "scene 0 must record its translation start")
	require.False(t, scene0.TTSStartedAt.IsZero(), "scene 0 must record its TTS start")
	require.False(t, scene1.TextReadyAt.IsZero(), "scene 1 must record its text-ready instant")
	require.False(t, scene2.TextReadyAt.IsZero(), "scene 2 must record its text-ready instant")

	// The decisive inequalities: scene 0's downstream branches begin before
	// scene 1's text is even ready — the streaming overlap, made durable.
	require.True(t, scene0.TranslationStartedAt.Before(scene1.TextReadyAt),
		"scene_1.translation_started_at (%v) must precede scene_2.text_ready_at (%v)",
		scene0.TranslationStartedAt, scene1.TextReadyAt)
	require.True(t, scene0.TTSStartedAt.Before(scene1.TextReadyAt),
		"scene_1.tts_started_at (%v) must precede scene_2.text_ready_at (%v)",
		scene0.TTSStartedAt, scene1.TextReadyAt)

	// Scenes become ready in canonical order: the per-scene ready boundary is
	// monotonic, never a single shared instant (which a global barrier would
	// collapse into).
	require.True(t, scene0.TextReadyAt.Before(scene1.TextReadyAt),
		"scene 0 text_ready_at must precede scene 1 text_ready_at")
	require.True(t, scene1.TextReadyAt.Before(scene2.TextReadyAt),
		"scene 1 text_ready_at must precede scene 2 text_ready_at")
}
