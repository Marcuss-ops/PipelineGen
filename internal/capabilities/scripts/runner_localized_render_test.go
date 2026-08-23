package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// recordingLocalizedRenderEnqueuer records every enqueued localized render in
// submission order. err makes EnqueueLocalizedRender fail closed. When
// producedVideo is non-nil, the enqueuer simulates a successful fan-out by
// invoking the input's OnRendered sink with that certified video — proving the
// runner records the produced MP4 on its result.
type recordingLocalizedRenderEnqueuer struct {
	mu            sync.Mutex
	inputs        []LocalizedRenderInput
	err           error
	producedVideo *LocalizedRenderResult
}

func (e *recordingLocalizedRenderEnqueuer) EnqueueLocalizedRender(_ context.Context, in LocalizedRenderInput) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	e.inputs = append(e.inputs, in)
	if e.producedVideo != nil && in.OnRendered != nil {
		return in.OnRendered(*e.producedVideo)
	}
	return nil
}

func (e *recordingLocalizedRenderEnqueuer) snapshot() []LocalizedRenderInput {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]LocalizedRenderInput(nil), e.inputs...)
}

// TestRunner_LocalizedRenderFanout_EnqueuesPerSceneLanguage pins the batch
// path fan-out: the moment each (scene, language) TTS completes, a localized
// render is enqueued — one per language per scene.
// P0.4 async render: render fire is a goroutine so enqueue order IS
// non-deterministic; the test asserts only the set of enqueued work, not
// the order.
func TestRunner_LocalizedRenderFanout_EnqueuesPerSceneLanguage(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	enq := &recordingLocalizedRenderEnqueuer{}
	runner.SetLocalizedRenderEnqueuer(enq)
	runner.SetTTSConcurrency(1)
	runner.SetTranslationConcurrency(1)

	req := defaultTestRequest() // en + es, 3 scenes, CHUNKED_VOICEOVER + docs

	runID := "run-localized-render-fanout"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)

	inputs := enq.snapshot()
	require.Len(t, inputs, 6, "3 scenes × 2 languages must enqueue 6 localized renders")

	want := []string{
		"scene-0:en", "scene-0:es",
		"scene-1:en", "scene-1:es",
		"scene-2:en", "scene-2:es",
	}
	got := make([]string, len(inputs))
	for i, in := range inputs {
		got[i] = in.SceneID + ":" + string(in.Language)
		require.Equal(t, runID, in.RunID)
		require.NotEmpty(t, in.Text, "localized render must carry the scene text")
		require.NotEmpty(t, in.Voiceover.ID, "localized render must carry the voiceover asset")
	}
	require.ElementsMatch(t, want, got, "fan-out must enqueue all (scene, language) pairs")
}

// TestRunner_LocalizedRenderFanout_RenderStartsBeforeNextSceneReady pins the
// streaming overlap: scene 0's localized renders are enqueued while the LLM is
// still blocked before emitting scene 1 — Rust can start on scene 0 without
// waiting for scene 1 (or the whole run).
func TestRunner_LocalizedRenderFanout_RenderStartsBeforeNextSceneReady(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	streamer := newGatedStreamingTextGenerator(defaultTestScenes())
	enq := &recordingLocalizedRenderEnqueuer{}
	runner.textGen = streamer
	runner.SetLocalizedRenderEnqueuer(enq)

	req := defaultTestRequest()
	runID := "run-localized-render-stream"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	}))

	done := make(chan struct{})
	go func() { defer close(done); runner.Execute(context.Background(), runID, req) }()

	// Wait for scene 0 to be emitted; the streamer is blocked before scene 1.
	select {
	case <-streamer.emitted:
	case <-time.After(5 * time.Second):
		t.Fatal("streamer did not emit scene 0")
	}

	// Scene 0's downstream fan-out (translate + TTS + render enqueue) must
	// complete while generation is still blocked before scene 1.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if len(enq.snapshot()) >= 2 { // scene-0/en + scene-0/es
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scene-0 localized renders were not enqueued: %v", enq.snapshot())
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, in := range enq.snapshot() {
		require.NotEqual(t, "scene-1", in.SceneID, "scene-1 render must not be enqueued while generation is blocked before scene 1")
	}

	close(streamer.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("streaming run did not complete")
	}
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, time.Second).Status)
	require.Len(t, enq.snapshot(), 6, "all 3 scenes × 2 languages must be enqueued by the end")
}

// TestLocalizedRenderClipFields pins the source-clip resolution helper: the
// primary Clip wins, multi-clip bindings fall back to the first entry, and an
// audio-only scene yields an empty reference.
func TestLocalizedRenderClipFields(t *testing.T) {
	primary := Scene{Clip: &ClipReference{ID: "clip-a", SHA256: "aa", DurationUS: 12_500_000}}
	clipID, assetID, sha, dur := localizedRenderClipFields(primary)
	require.Equal(t, "clip-a", clipID)
	require.Equal(t, "clip-a", assetID)
	require.Equal(t, "aa", sha)
	require.Equal(t, int64(12_500), dur)

	multi := Scene{Clips: []*ClipReference{{ID: "clip-b", SHA256: "bb", DurationUS: 8_000_000}}}
	clipID, _, sha, dur = localizedRenderClipFields(multi)
	require.Equal(t, "clip-b", clipID)
	require.Equal(t, "bb", sha)
	require.Equal(t, int64(8_000), dur)

	clipID, assetID, sha, dur = localizedRenderClipFields(Scene{})
	require.Empty(t, clipID)
	require.Empty(t, assetID)
	require.Empty(t, sha)
	require.Zero(t, dur)
}

// TestRunner_LocalizedRenderFanout_CarriesSourceClip proves the fan-out hands
// the source-clip reference to the enqueuer, so the real Rust adapter can
// resolve (asset_id, sha256, duration) without re-deriving them.
// P0.4 async render: enqueue happens in goroutines; wait up to 5s for all 6.
func TestRunner_LocalizedRenderFanout_CarriesSourceClip(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	enq := &recordingLocalizedRenderEnqueuer{}
	runner.SetLocalizedRenderEnqueuer(enq)
	runner.SetTTSConcurrency(1)
	runner.SetTranslationConcurrency(1)
	for i := range runner.textGen.(*stubTextGenerator).scenes {
		runner.textGen.(*stubTextGenerator).scenes[i].Clip = &ClipReference{
			ID: "clip-source", SHA256: "cccc", DurationUS: 12_000_000,
		}
	}

	req := defaultTestRequest()
	runID := "run-localized-render-clip"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)

	// Async render goroutines may still be in-flight after run completion;
	// wait for all 6 to land.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if len(enq.snapshot()) >= 6 {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	inputs := enq.snapshot()
	require.Len(t, inputs, 6, "3 scenes × 2 languages")
	for _, in := range inputs {
		require.Equal(t, "clip-source", in.ClipID, "enqueued input must carry the source clip id")
		require.Equal(t, "clip-source", in.ClipAssetID, "enqueued input must carry the media asset id")
		require.Equal(t, "cccc", in.ClipSHA256, "enqueued input must carry the clip sha256")
		require.Equal(t, int64(12_000), in.ClipDurationMS, "enqueued input must carry the clip duration")
	}
}

// TestRunner_LocalizedRenderFanout_RecordsProducedVideo certifies that a
// certified produced video of the fan-out (asset id, sha256, Drive link) is
// recorded on the run result — the final MP4 is never orphaned from the run
// that produced it. This is the deterministic surface the E2E visual audit
// reads to prove "this run rendered this video".
func TestRunner_LocalizedRenderFanout_RecordsProducedVideo(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	enq := &recordingLocalizedRenderEnqueuer{
		producedVideo: &LocalizedRenderResult{
			SceneID:     "scene-0",
			Language:    "en",
			ClipID:      "clip-source",
			AssetID:     "final-video-asset-1",
			SHA256:      "0123456789abcdef",
			DriveFileID: "drive-final-1",
			DriveLink:   "https://drive.google.com/file/d/drive-final-1/view",
			DurationMS:  12_000,
			Status:      "UPLOADED",
		},
	}
	runner.SetLocalizedRenderEnqueuer(enq)
	runner.SetTTSConcurrency(1)
	runner.SetTranslationConcurrency(1)

	req := defaultTestRequest()
	runID := "run-localized-render-recorded-video"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)

	require.NotNil(t, final.Result, "run result must be populated")
	deadline := time.Now().Add(5 * time.Second)
	for len(final.Result.LocalizedRenders) < 6 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		final = awaitCompletion(t, repo, runID, time.Second)
	}
	require.Len(t, final.Result.LocalizedRenders, 6, "every (scene, language) fan-out must record its produced video")
	for _, rendered := range final.Result.LocalizedRenders {
		require.Equal(t, "final-video-asset-1", rendered.AssetID, "produced video asset id must be recorded")
		require.Equal(t, "0123456789abcdef", rendered.SHA256, "produced video sha256 must be recorded")
		require.Equal(t, "drive-final-1", rendered.DriveFileID, "produced video drive file id must be recorded")
		require.NotEmpty(t, rendered.DriveLink, "produced video drive link must be recorded")
		require.Equal(t, int64(12_000), rendered.DurationMS)
		require.Equal(t, "UPLOADED", rendered.Status)
	}
}
