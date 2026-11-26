package scriptgeneration

import (
	"context"
	"strings"
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
		{name: "narrated concrete stock visual is untouched", scene: Scene{ID: "stock-voiceover", Stock: &scriptpkg.StockBinding{AssetID: "yt_stock_0_60_v1"}, Voiceover: map[Language]AudioReference{"pt": {ID: "vo-stock"}}}, want: false},
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
		produced := *e.producedVideo
		produced.SceneID = in.SceneID
		produced.SceneIndex = in.SceneIndex
		produced.Language = in.Language
		produced.ClipID = in.ClipID
		return in.OnRendered(produced)
	}
	return nil
}

func (e *recordingLocalizedRenderEnqueuer) snapshot() []LocalizedRenderInput {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]LocalizedRenderInput(nil), e.inputs...)
}

// TestRunner_LocalizedRenderResumePublishesStagedChrononOutput pins recovery:
// a staged local render is uploaded without running Chronon a second time.
func TestRunner_LocalizedRenderResumePublishesStagedChrononOutput(t *testing.T) {
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
		Render:     scriptpkg.VideoRenderSpec{Enabled: true},
		ResumeFrom: &staged,
	})
	require.NoError(t, err)
	require.Len(t, enq.recovered, 1, "staged output should use the upload-only recovery path")
	require.Empty(t, enq.snapshot(), "resume must not enqueue a fresh render")
}

// TestRunner_LocalizedRenderFanout_CliplessScenesDoNotDispatch ensures text-only
// scenes do not create fake local video render work.
func TestRunner_LocalizedRenderFanout_CliplessScenesDoNotDispatch(t *testing.T) {
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

	require.Empty(t, enq.snapshot(), "text-only scenes have no visual clip for Chronon")
}

func TestRunner_SourceClipsAudioNoneDispatchesLocalChrononRenders(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	enq := &recordingLocalizedRenderEnqueuer{producedVideo: &LocalizedRenderResult{
		AssetID: "rendered-asset", SHA256: strings.Repeat("a", 64), DriveFileID: "drive-1", DriveLink: "https://drive.test/rendered", DurationMS: 1000, Status: "UPLOADED",
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

	require.Len(t, enq.snapshot(), 6, "three source clips plus three fixed intro/outro clips should render locally")
}

// TestRunner_LocalizedRenderFanout_StreamingCliplessDoesNotDispatch keeps the
// streaming path covered when generated scenes have no visual clip.
func TestRunner_LocalizedRenderFanout_StreamingCliplessDoesNotDispatch(t *testing.T) {
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
	require.Empty(t, enq.snapshot(), "clipless scene has no render to dispatch")

	close(streamer.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("streaming run did not complete")
	}
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, time.Second).Status)
	require.Empty(t, enq.snapshot(), "clipless streamed scenes must not create render work")
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

func TestRunner_LocalizedRenderFanout_SourceClipScenesDispatches(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	enq := &recordingLocalizedRenderEnqueuer{producedVideo: &LocalizedRenderResult{
		AssetID: "rendered-asset", SHA256: strings.Repeat("a", 64), DriveFileID: "drive-1",
		DriveLink: "https://drive.test/rendered", DurationMS: 12_000, Status: "UPLOADED",
	}}
	runner.SetLocalizedRenderEnqueuer(enq)
	runner.SetTTSConcurrency(1)
	runner.SetTranslationConcurrency(1)
	for i := range runner.textGen.(*stubTextGenerator).scenes {
		runner.textGen.(*stubTextGenerator).scenes[i].Clip = &ClipReference{
			ID: "clip-source", SHA256: "cccc", DurationUS: 12_000_000,
		}
		runner.textGen.(*stubTextGenerator).scenes[i].Text["en"] = "This narrated scene explains the important background and context behind the selected story in detail."
	}

	req := defaultTestRequest()
	req.Render.Subtitles = &scriptpkg.VideoSubtitlesSpec{Enabled: true}
	req.Render.Enabled = true
	req.Source.Type = SourceClips
	req.Source.ClipIDs = []string{"clip-source", "clip-source", "clip-source"}
	req.Languages = []Language{"en"}
	runID := "run-localized-render-clip"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, final.ErrorMessage)

	require.Len(t, enq.snapshot(), 3, "source-clip scenes should be rendered locally")
}

// TestRunner_LocalizedRenderFanout_RecordsLocalVideo verifies the runner
// persists each produced Chronon clip for the remote final worker.
func TestRunner_LocalizedRenderFanout_RecordsLocalVideo(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	enq := &recordingLocalizedRenderEnqueuer{
		producedVideo: &LocalizedRenderResult{
			SceneID:     "scene-0",
			Language:    "en",
			ClipID:      "clip-source",
			AssetID:     "final-video-asset-1",
			SHA256:      strings.Repeat("a", 64),
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
	req.Render.Enabled = true
	req.Source.Type = SourceClips
	req.Source.ClipIDs = []string{"clip-source", "clip-source", "clip-source"}
	req.Languages = []Language{"en"}
	for i := range runner.textGen.(*stubTextGenerator).scenes {
		runner.textGen.(*stubTextGenerator).scenes[i].Clip = &ClipReference{ID: "clip-source", SHA256: strings.Repeat("b", 64), DurationUS: 12_000_000}
		runner.textGen.(*stubTextGenerator).scenes[i].Text["en"] = "This narrated scene explains the important background and context behind the selected story in detail."
	}
	runID := "run-localized-render-recorded-video"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, final.ErrorMessage)

	require.NotNil(t, final.Result, "run result must be populated")
	require.GreaterOrEqual(t, len(final.Result.LocalizedRenders), 3, "local Chronon renders should be recorded")
	require.Empty(t, final.Result.LocalizedRenderFailures, "the local render lane must record no render failures")
}

// TestEnqueueLocalizedRenderFailsClosedWithoutEnqueuer pins the wiring
// contract: a missing enqueuer is a typed composition error, not a silent
// no-op that only surfaces as INCOMPLETE_RENDER_SET at the end of the run.
func TestEnqueueLocalizedRenderFailsClosedWithoutEnqueuer(t *testing.T) {
	runner, _, _, _, _, _ := newTestRunner()
	err := runner.enqueueLocalizedRender(context.Background(), LocalizedRenderInput{
		Render: scriptpkg.VideoRenderSpec{Enabled: true},
	})
	require.ErrorIs(t, err, ErrLocalizedRendererNotWired)
}

// TestEnqueueLocalizedRenderSkipsNonRenderRuns pins the complement: an
// audio-only/timeline scene may own a clip, but it never asked for a video,
// so the absent renderer is not an error.
func TestEnqueueLocalizedRenderSkipsNonRenderRuns(t *testing.T) {
	runner, _, _, _, _, _ := newTestRunner()
	require.NoError(t, runner.enqueueLocalizedRender(context.Background(), LocalizedRenderInput{}))
}

// TestSetSceneRenderExpectationsRequiresEnqueuer pins the preflight: a
// render-enabled source-clip run with an expected render unit must refuse to
// start the fan-out when the localized renderer is not wired.
func TestSetSceneRenderExpectationsRequiresEnqueuer(t *testing.T) {
	runner, _, _, _, _, _ := newTestRunner()
	req := defaultTestRequest()
	req.Source.Type = SourceClips
	req.Source.ClipIDs = []string{"clip-0"}
	req.Render.Enabled = true
	scenes := []Scene{{ID: "scene-0", Index: 0, Clip: &ClipReference{ID: "clip-0"}}}
	result := &GenerateResult{}

	err := runner.setSceneRenderExpectations(req, scenes, result)
	require.ErrorIs(t, err, ErrLocalizedRendererNotWired)
	require.Equal(t, 1, result.ExpectedRenderCount, "the preflight must still record the expected unit")

	runner.SetLocalizedRenderEnqueuer(&recordingLocalizedRenderEnqueuer{})
	require.NoError(t, runner.setSceneRenderExpectations(req, scenes, result))
	require.Equal(t, 1, result.ExpectedRenderCount)
}
