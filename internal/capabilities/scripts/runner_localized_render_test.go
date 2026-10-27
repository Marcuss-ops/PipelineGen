package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

func TestSceneRenderSpecSubtitlePolicy(t *testing.T) {
	req := GenerateRequest{SourceLanguage: "pt", Render: scriptpkg.VideoRenderSpec{Enabled: true, Subtitles: &scriptpkg.VideoSubtitlesSpec{Enabled: true, Mode: "burn"}}}
	tests := []struct {
		name  string
		mode  scriptpkg.MediaMode
		scene Scene
		want  bool
	}{
		{name: "stock without clip", scene: Scene{ID: "stock-image"}, want: false},
		{name: "stock binding takes precedence over source audio clip", scene: Scene{ID: "stock-scene", Clip: &ClipReference{ID: "yt_source_audio_0_10_v1"}, Stock: &scriptpkg.StockBinding{FolderID: "1Sr15dwjKquNn9yaPBav8TwNS_lfEzd-"}}, want: false},
		{name: "source clip", scene: Scene{Clip: &ClipReference{ID: "yt_abc_0_10_v1"}}, want: true},
		{name: "stock-only never inherits clip subtitles", mode: scriptpkg.MediaModeStockOnly, scene: Scene{Clip: &ClipReference{ID: "yt_abc_0_10_v1"}}, want: false},
		{name: "artlist clip", scene: Scene{Clip: &ClipReference{ID: "artlist_asset_42"}}, want: false},
		{name: "fixed media with caption", scene: Scene{ExecutionMode: scriptpkg.SceneExecutionFixedMedia, Text: map[Language]string{"pt": "Abertura"}}, want: true},
		{name: "fixed media without caption", scene: Scene{ExecutionMode: scriptpkg.SceneExecutionFixedMedia}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testReq := req
			testReq.MediaMode = tt.mode
			got := sceneRenderSpec(testReq, tt.scene)
			require.NotNil(t, got.Subtitles)
			require.Equal(t, tt.want, got.Subtitles.Enabled)
			require.True(t, got.Enabled, "localized clipping/normalization render remains enabled")
			require.True(t, req.Render.Subtitles.Enabled, "per-scene projection must not mutate request")
		})
	}
}

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

type recoveryLocalizedRenderEnqueuer struct {
	recordingLocalizedRenderEnqueuer
	recovered []LocalizedRenderResult
}

func (e *recoveryLocalizedRenderEnqueuer) UploadRendered(_ context.Context, _ LocalizedRenderInput, staged LocalizedRenderResult) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.recovered = append(e.recovered, staged)
	return nil
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

// Dispatch-seal contract (docs/stato/refactored.md "Divieto Strutturale
// Rendering Master Video", 2026-10-26): PipelineGen never dispatches
// localized scene renders — not on fan-out, not on resume, not on fixed
// media, not while streaming. The external final worker (Master 51 /
// BrainRust) owns rendering. Every test below pins BOTH halves of that
// contract: zero dispatch AND a run that still reaches COMPLETED (an
// expectation left live under the seal would fail the run with
// INCOMPLETE_RENDER_SET at completion).

// TestRunner_LocalizedRenderResumeIsSealed pins the resume path: a staged
// RENDERED result offered back to the runner must neither be re-uploaded nor
// re-enqueued — upload and render belong to the external final worker, and
// the seal swallows the call without an error.
func TestRunner_LocalizedRenderResumeIsSealed(t *testing.T) {
	enq := &recoveryLocalizedRenderEnqueuer{}
	runner, _, _, _, _, _ := newTestRunner()
	runner.SetLocalizedRenderEnqueuer(enq)
	staged := LocalizedRenderResult{
		SceneID: "scene-0", Language: "en", ClipID: "clip-0", Status: "RENDERED",
		LocalPath: "/tmp/clip-0.mp4", SHA256: "sha-0",
	}
	err := runner.enqueueLocalizedRender(context.Background(), LocalizedRenderInput{
		RunID: "run-recovery", SceneID: staged.SceneID, Language: staged.Language,
		ClipID: staged.ClipID, ClipAssetID: staged.ClipID,
		ResumeFrom: &staged,
	})
	require.NoError(t, err, "the seal is a silent skip, never an error")
	require.Empty(t, enq.recovered, "resume must not take the upload-only path")
	require.Empty(t, enq.snapshot(), "resume must not enqueue a fresh render")
}

// TestRunner_LocalizedRenderFanout_NeverDispatches pins the batch path under
// the seal: even after every (scene, language) TTS completes, nothing is
// handed to the render enqueuer and the run still finishes COMPLETED.
func TestRunner_LocalizedRenderFanout_NeverDispatches(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	enq := &recordingLocalizedRenderEnqueuer{}
	runner.SetLocalizedRenderEnqueuer(enq)
	runner.SetTTSConcurrency(1)
	runner.SetTranslationConcurrency(1)

	req := defaultTestRequest() // en + es, 3 scenes, CHUNKED_VOICEOVER + docs
	req.Render.Subtitles = &scriptpkg.VideoSubtitlesSpec{Enabled: true}

	runID := "run-localized-render-fanout"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, final.ErrorMessage)

	require.Empty(t, enq.snapshot(),
		"3 scenes × 2 languages must dispatch nothing: rendering belongs to the external final worker")
}

// TestRunner_SourceClipsAudioNone_CompletesSealed pins the regression the
// dispatch seal creates if the expectation counter is left live: a SourceClips
// run with Render.Enabled and audio=NONE expects 6 renders, so with dispatch
// sealed the expectation must be sealed too — otherwise completion fails with
// INCOMPLETE_RENDER_SET (expected=6 successful=0) instead of finishing the
// script run.
func TestRunner_SourceClipsAudioNone_CompletesSealed(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	enq := &recordingLocalizedRenderEnqueuer{producedVideo: &LocalizedRenderResult{
		AssetID: "rendered-asset", SHA256: "0123456789abcdef0123456789abcdef", DriveFileID: "drive-1", DriveLink: "https://drive.test/rendered", DurationMS: 1000, Status: "UPLOADED",
	}}
	runner.SetLocalizedRenderEnqueuer(enq)
	req := defaultTestRequest()
	req.Render.Subtitles = &scriptpkg.VideoSubtitlesSpec{Enabled: true}
	req.Source.Type = SourceClips
	req.Source.ClipIDs = []string{"clip-0", "clip-1", "clip-2"}
	req.Source.SourceText = "source clips"
	req.Render.Enabled = true
	req.Audio = "NONE"
	req.VoiceoverLanguages = nil
	clipNarration := []string{
		"Dolly Parton tells Conan about her first New York hotel experience and the surprising misunderstanding that followed.",
		"Dolly Parton describes walking through Manhattan with Judy and explains how their unusual appearance attracted attention.",
		"Dolly Parton jokes with Conan about fame, family, confidence, and the stories that made her career unforgettable.",
	}
	for i, scene := range runner.textGen.(*stubTextGenerator).scenes {
		scene.Text["en"] = clipNarration[i]
		scene.Clip = &ClipReference{ID: req.Source.ClipIDs[i], SHA256: "sha-" + string(rune('a'+i)), DurationUS: 1_000_000}
		runner.textGen.(*stubTextGenerator).scenes[i] = scene
	}

	runID := "run-localized-render-no-audio"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, final.ErrorMessage)

	require.Empty(t, enq.snapshot(),
		"audio NONE with sealed dispatch must not hand any (scene, language) unit to the render lane")
}

// TestRunner_LocalizedRenderFanout_StreamingCompletesSealed keeps the gated
// streaming path covered under the seal: while generation is blocked before
// scene 1 nothing is dispatched (grace window included, so a late async
// dispatch would be caught), and after release the run completes with a
// permanently empty render lane.
func TestRunner_LocalizedRenderFanout_StreamingCompletesSealed(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	streamer := newGatedStreamingTextGenerator(defaultTestScenes())
	enq := &recordingLocalizedRenderEnqueuer{}
	runner.textGen = streamer
	runner.SetLocalizedRenderEnqueuer(enq)

	req := defaultTestRequest()
	req.Render.Subtitles = &scriptpkg.VideoSubtitlesSpec{Enabled: true}
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

	// Give scene 0's downstream fan-out its window to (not) dispatch.
	time.Sleep(100 * time.Millisecond)
	require.Empty(t, enq.snapshot(), "no render may dispatch while generation is blocked before scene 1")

	close(streamer.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("streaming run did not complete")
	}
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, time.Second).Status)
	require.Empty(t, enq.snapshot(), "the render lane must stay empty through completion")
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

// TestRunner_LocalizedRenderFanout_SourceClipScenesCompleteSealed keeps the
// source-clip scenario covered under the seal: scenes bound to source clips
// run to completion without handing a single clip reference to the render
// lane. Clip resolution itself stays pinned by TestLocalizedRenderClipFields.
func TestRunner_LocalizedRenderFanout_SourceClipScenesCompleteSealed(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
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
	req.Render.Subtitles = &scriptpkg.VideoSubtitlesSpec{Enabled: true}
	runID := "run-localized-render-clip"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, final.ErrorMessage)

	require.Empty(t, enq.snapshot(), "source-clip scenes must not dispatch renders under the seal")
}

// TestRunner_LocalizedRenderFanout_RecordsNoLocalVideo pins the result
// surface under the seal: even with a producing sink wired, the run result
// carries no LocalizedRenders and no render failures — produced videos are
// facts of the external final worker (Master 51 / BrainRust), never of the
// planning run.
func TestRunner_LocalizedRenderFanout_RecordsNoLocalVideo(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
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
	req.Render.Subtitles = &scriptpkg.VideoSubtitlesSpec{Enabled: true}
	runID := "run-localized-render-recorded-video"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, final.ErrorMessage)

	require.NotNil(t, final.Result, "run result must be populated")
	require.Empty(t, final.Result.LocalizedRenders, "the sealed lane must record no local renders")
	require.Empty(t, final.Result.LocalizedRenderFailures, "the sealed lane must record no render failures")
}
