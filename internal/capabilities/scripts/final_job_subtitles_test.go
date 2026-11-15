package scriptgeneration

import (
	"context"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

// capturingFinalJobSubmitter observes how many certified renders the run had
// accumulated at the moment the remote PREPARE/FINALIZE was submitted.
type capturingFinalJobSubmitter struct {
	localizedRenders int
	calls            int
}

func (s *capturingFinalJobSubmitter) SubmitFinalJob(_ context.Context, _ string, _ GenerateRequest, result *GenerateResult) (RemoteFinalJobResult, error) {
	s.calls++
	if result != nil {
		s.localizedRenders = len(result.LocalizedRenders)
	}
	return RemoteFinalJobResult{JobID: "remote-1", Status: "SUCCEEDED"}, nil
}

func fixedSubtitleScene() Scene {
	return Scene{
		ID:            "scene-intro",
		Index:         0,
		ExecutionMode: scriptpkg.SceneExecutionFixedMedia,
		Text:          map[Language]string{"it": "Apertura"},
		Clips: []*ClipReference{
			{ID: "intro-clip-1"},
			{ID: "intro-clip-2"},
		},
	}
}

func fixedSubtitleResult(scene Scene) *GenerateResult {
	return &GenerateResult{Scenes: []Scene{scene}}
}

// perClipSubtitleEnqueuer simulates the real adapter: the produced artifact
// carries the identity of the clip that was requested.
type perClipSubtitleEnqueuer struct {
	inputs []LocalizedRenderInput
}

func (e *perClipSubtitleEnqueuer) EnqueueLocalizedRender(_ context.Context, in LocalizedRenderInput) error {
	e.inputs = append(e.inputs, in)
	if in.OnRendered == nil {
		return nil
	}
	rendered := certifiedSubtitleRender(in.SceneID, in.ClipID)
	rendered.Language = in.Language
	return in.OnRendered(rendered)
}

func certifiedSubtitleRender(sceneID, clipID string) LocalizedRenderResult {
	sha := strings.Repeat("a", 64)
	return LocalizedRenderResult{
		SceneID: sceneID, ClipID: clipID, Language: "it",
		AssetID: "derived-" + clipID, DriveFileID: "drive-" + clipID,
		SHA256: sha, DurationMS: 5000, SizeBytes: 1024,
		StorageKey: sha, ArtifactURL: "https://objects.example/objects/" + sha,
		Status: "UPLOADED",
	}
}

// TestFinalJobFixedMediaSubtitlesRenderEveryClip pins the missing half of the
// final-job handoff: when subtitles are requested, every protected fixed-media
// clip gets a certified localized render before the Master plan is built.
func TestFinalJobFixedMediaSubtitlesRenderEveryClip(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: "run-subtitles"}))
	enqueuer := &perClipSubtitleEnqueuer{}
	runner.SetLocalizedRenderEnqueuer(enqueuer)
	req := defaultTestRequest()
	req.SourceLanguage = "it"
	req.Render.Subtitles = &scriptpkg.VideoSubtitlesSpec{Enabled: true, Mode: "burn"}
	scene := fixedSubtitleScene()
	result := fixedSubtitleResult(scene)

	if err := runner.ensureFinalJobFixedMediaSubtitles(context.Background(), "run-subtitles", req, result); err != nil {
		t.Fatalf("ensureFinalJobFixedMediaSubtitles: %v", err)
	}
	inputs := enqueuer.inputs
	if len(inputs) != 2 {
		t.Fatalf("localized render calls = %d, want one per fixed clip", len(inputs))
	}
	for i, input := range inputs {
		if input.ClipID != scene.Clips[i].ID || input.Language != "it" || input.Text != "Apertura" {
			t.Fatalf("input %d = %+v, want clip %s with the source caption", i, input, scene.Clips[i].ID)
		}
		if input.Render.Subtitles == nil || !input.Render.Subtitles.Enabled {
			t.Fatalf("input %d did not request subtitle burn: %+v", i, input.Render.Subtitles)
		}
	}
	if len(result.LocalizedRenders) == 0 {
		t.Fatal("a certified produced clip must be recorded on the run result")
	}
	if _, ok := certifiedLocalizedRenderForClip(result, "scene-intro", "intro-clip-1", "it"); !ok {
		t.Fatal("the certified fixed-media render was not recorded")
	}
}

// TestFinalJobFixedMediaSubtitlesSkipAlreadyCertifiedClip keeps retries
// idempotent: a clip with a certified render is not rendered (or uploaded)
// again.
func TestFinalJobFixedMediaSubtitlesSkipAlreadyCertifiedClip(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: "run-subtitles"}))
	enqueuer := &perClipSubtitleEnqueuer{}
	runner.SetLocalizedRenderEnqueuer(enqueuer)
	req := defaultTestRequest()
	req.SourceLanguage = "it"
	req.Render.Subtitles = &scriptpkg.VideoSubtitlesSpec{Enabled: true, Mode: "burn"}
	result := fixedSubtitleResult(fixedSubtitleScene())
	existing := certifiedSubtitleRender("scene-intro", "intro-clip-1")
	result.LocalizedRenders = append(result.LocalizedRenders, existing)

	if err := runner.ensureFinalJobFixedMediaSubtitles(context.Background(), "run-subtitles", req, result); err != nil {
		t.Fatalf("ensureFinalJobFixedMediaSubtitles: %v", err)
	}
	inputs := enqueuer.inputs
	if len(inputs) != 1 || inputs[0].ClipID != "intro-clip-2" {
		t.Fatalf("localized render calls = %+v, want only the uncertified clip", inputs)
	}
}

// TestFinalJobFixedMediaSubtitlesUseTheRunRenderLanguage prevents a late
// source-language render from silently switching the whole remote video away
// from the language the run already certified.
func TestFinalJobFixedMediaSubtitlesUseTheRunRenderLanguage(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: "run-subtitles"}))
	enqueuer := &perClipSubtitleEnqueuer{}
	runner.SetLocalizedRenderEnqueuer(enqueuer)
	req := defaultTestRequest()
	req.SourceLanguage = "en"
	req.Render.Subtitles = &scriptpkg.VideoSubtitlesSpec{Enabled: true, Mode: "burn"}
	scene := fixedSubtitleScene()
	scene.Text = map[Language]string{"en": "Opening", "it": "Apertura"}
	result := fixedSubtitleResult(scene)
	existing := certifiedSubtitleRender("scene-body", "body-clip")
	result.LocalizedRenders = append(result.LocalizedRenders, existing)

	if err := runner.ensureFinalJobFixedMediaSubtitles(context.Background(), "run-subtitles", req, result); err != nil {
		t.Fatalf("ensureFinalJobFixedMediaSubtitles: %v", err)
	}
	inputs := enqueuer.inputs
	if len(inputs) != 2 {
		t.Fatalf("localized render calls = %d, want one per fixed clip", len(inputs))
	}
	for _, input := range inputs {
		if input.Language != "it" {
			t.Fatalf("render language = %q, want the run's certified language it", input.Language)
		}
	}
}

// TestFinalJobFixedMediaSubtitlesDisabledDoesNothing keeps the opt-out
// contract: no subtitle request means no local render and no change to the
// library-clip fallback.
func TestFinalJobFixedMediaSubtitlesDisabledDoesNothing(t *testing.T) {
	runner, _, _, _, _, _ := newTestRunner()
	enqueuer := &recordingLocalizedRenderEnqueuer{}
	runner.SetLocalizedRenderEnqueuer(enqueuer)
	req := defaultTestRequest()
	req.SourceLanguage = "it"
	result := fixedSubtitleResult(fixedSubtitleScene())

	if err := runner.ensureFinalJobFixedMediaSubtitles(context.Background(), "run-subtitles", req, result); err != nil {
		t.Fatalf("ensureFinalJobFixedMediaSubtitles: %v", err)
	}
	if calls := enqueuer.snapshot(); len(calls) != 0 {
		t.Fatalf("subtitles disabled must not render: %+v", calls)
	}
}

// TestSubmitFinalJobBurnsSubtitlesBeforeHandoff pins the call site: the
// pre-render runs before the submitter sees the result, so a requested burn
// cannot be lost between the local run and the remote plan.
func TestSubmitFinalJobBurnsSubtitlesBeforeHandoff(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: "run-subtitles"}))
	runner.SetLocalizedRenderEnqueuer(&perClipSubtitleEnqueuer{})
	submitter := &capturingFinalJobSubmitter{}
	runner.SetFinalJobSubmitter(submitter)
	req := defaultTestRequest()
	req.FinalJob = true
	req.SourceLanguage = "it"
	req.Render.Subtitles = &scriptpkg.VideoSubtitlesSpec{Enabled: true, Mode: "burn"}
	result := fixedSubtitleResult(fixedSubtitleScene())

	if ok := runner.submitFinalJob(context.Background(), "run-subtitles", req, result); !ok {
		t.Fatal("submitFinalJob failed")
	}
	if submitter.calls != 1 {
		t.Fatalf("submit calls = %d, want 1", submitter.calls)
	}
	if submitter.localizedRenders != 2 {
		t.Fatalf("submitter saw %d certified renders, want 2 before PREPARE", submitter.localizedRenders)
	}
}

// TestFinalJobFixedMediaSubtitlesWithoutFixedScenesDoNothing keeps the
// pre-render scoped: a stock-only final job with subtitles enabled must not be
// rejected because some unrelated render language is ambiguous.
func TestFinalJobFixedMediaSubtitlesWithoutFixedScenesDoNothing(t *testing.T) {
	runner, _, _, _, _, _ := newTestRunner()
	runner.SetLocalizedRenderEnqueuer(&perClipSubtitleEnqueuer{})
	req := defaultTestRequest()
	req.SourceLanguage = "en"
	req.Render.Subtitles = &scriptpkg.VideoSubtitlesSpec{Enabled: true, Mode: "burn"}
	result := &GenerateResult{
		Scenes: []Scene{{ID: "scene-body", Stock: &scriptpkg.StockBinding{FolderID: "folder-1"}}},
		LocalizedRenders: []LocalizedRenderResult{
			certifiedSubtitleRender("scene-body", "body-clip"),
			{SceneID: "scene-body", ClipID: "body-clip", Language: "es", SHA256: strings.Repeat("b", 64), DriveFileID: "drive-es", DurationMS: 5000},
		},
	}

	if err := runner.ensureFinalJobFixedMediaSubtitles(context.Background(), "run-subtitles", req, result); err != nil {
		t.Fatalf("ensureFinalJobFixedMediaSubtitles: %v", err)
	}
}

// TestFinalJobFixedMediaSubtitlesFailClosedWithoutRenderer prevents the
// silent un-subtitled fallback: a requested burn with no renderer is an error,
// not a library-clip handoff.
func TestFinalJobFixedMediaSubtitlesFailClosedWithoutRenderer(t *testing.T) {
	runner, _, _, _, _, _ := newTestRunner()
	req := defaultTestRequest()
	req.SourceLanguage = "it"
	req.Render.Subtitles = &scriptpkg.VideoSubtitlesSpec{Enabled: true, Mode: "burn"}
	result := fixedSubtitleResult(fixedSubtitleScene())

	err := runner.ensureFinalJobFixedMediaSubtitles(context.Background(), "run-subtitles", req, result)
	if err == nil || !strings.Contains(err.Error(), "localized renderer is not wired") {
		t.Fatalf("error = %v, want the fail-closed missing-renderer error", err)
	}
}
