package cliprender

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

func newTestWorker(t *testing.T) (*Worker, *fakeMaterializer, *fakeTranscriptResolver) {
	t.Helper()
	resolver := newFakeAssetResolver(map[string]AssetRef{
		"asset-source": {AssetID: "asset-source"},
	})
	mat := &fakeMaterializer{}
	tr := &fakeTranscriptResolver{
		existing: &TranscriptResult{
			AssetID:  "asset-source",
			Language: "en",
			Text:     "existing",
			Cues:     []Cue{{StartMs: 0, EndMs: 1000, Text: "existing"}},
			Reused:   true,
		},
		existingOK: true,
	}
	preparer := newTestPreparer(resolver, mat, tr)
	w, err := NewWorker(preparer, t.TempDir(), zap.NewNop())
	if err != nil {
		panic(err)
	}
	return w, mat, tr
}

func renderJobPayload(t *testing.T, req *RenderRequest) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return raw
}

type fakeRenderExecutor struct {
	called  int
	plan    ClipRenderPlanV1
	outcome *RenderOutcome
}

func (f *fakeRenderExecutor) Render(_ context.Context, plan ClipRenderPlanV1) (*RenderOutcome, error) {
	f.called++
	f.plan = plan
	return f.outcome, nil
}

func TestWorker_ExecutesSealedPlanThroughRenderExecutor(t *testing.T) {
	w, _, _ := newTestWorker(t)
	renderer := &fakeRenderExecutor{outcome: &RenderOutcome{
		OutputPath:  "/work/rendered-clip.mp4",
		SizeBytes:   4096,
		DurationSec: 3,
		Width:       1080,
		Height:      1920,
		FPSNum:      60,
		FPSDen:      1,
		FFmpegMS:    1234,
	}}
	w.WithRenderExecutor(renderer)

	result, err := w.Handle(context.Background(), &job.Job{ID: "job-render", Payload: renderJobPayload(t, baseRenderRequest())}, nil)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if renderer.called != 1 || renderer.plan.PlanSHA256 == "" {
		t.Fatalf("renderer calls/plan = %d/%+v", renderer.called, renderer.plan)
	}
	if result["phase"] != "rendered" {
		t.Fatalf("phase = %v, want rendered", result["phase"])
	}
	render, ok := result["render"].(map[string]any)
	if !ok || render["ffmpeg_ms"] != int64(1234) {
		t.Fatalf("render result = %v", result["render"])
	}
}

// TestWorker_ValidPayload_PreparesAndFailsClosed verifies the full worker
// path: decode → validate → prepare → result envelope emitted + typed
// terminal error (render phase not implemented — fail-closed, never a
// silent success).
func TestWorker_ValidPayload_PreparesAndFailsClosed(t *testing.T) {
	w, mat, _ := newTestWorker(t)

	req := baseRenderRequest()
	payload := renderJobPayload(t, req)

	tools := &job.JobExecutionTools{
		Progress: func(int, string) {},
		Event:    func(string, string, map[string]any) {},
	}
	result, err := w.Handle(context.Background(), &job.Job{ID: "job-1", Payload: payload}, tools)

	if !errors.Is(err, ErrRenderPhaseNotImplemented) {
		t.Fatalf("expected ErrRenderPhaseNotImplemented, got %v", err)
	}
	if result == nil {
		t.Fatal("expected result envelope with prepared artifacts")
	}
	if result["phase"] != "plan_sealed" {
		t.Errorf("phase: got %v, want plan_sealed", result["phase"])
	}
	plan, ok := result["plan"].(map[string]any)
	if !ok || plan["plan_sha256"] == "" {
		t.Errorf("plan envelope: got %v", result["plan"])
	}
	if got := result["contract_id"]; got != OutputContractVeloxEditingClipV1 {
		t.Errorf("contract_id: got %v", got)
	}
	if len(mat.calls) != 1 || mat.calls[0] != "asset-source" {
		t.Errorf("expected source materialization only, got %v", mat.calls)
	}
}

// TestWorker_InvalidPayload_Terminal verifies an undecodable payload fails
// with the typed terminal sentinel before any preparation runs.
func TestWorker_InvalidPayload_Terminal(t *testing.T) {
	w, mat, _ := newTestWorker(t)

	result, err := w.Handle(context.Background(), &job.Job{ID: "job-2", Payload: json.RawMessage(`{not json`)}, nil)
	if !errors.Is(err, ErrInvalidJobPayload) {
		t.Fatalf("expected ErrInvalidJobPayload, got %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result on invalid payload, got %v", result)
	}
	if len(mat.calls) != 0 {
		t.Errorf("preparation must not run on invalid payload, got %v", mat.calls)
	}
}

// TestWorker_ValidationFailure_Terminal verifies an invalid (non-normalized)
// request fails with the typed terminal sentinel.
func TestWorker_ValidationFailure_Terminal(t *testing.T) {
	w, mat, _ := newTestWorker(t)

	// Missing source_asset_id — fails Validate after Normalize.
	raw, _ := json.Marshal(&RenderRequest{})
	result, err := w.Handle(context.Background(), &job.Job{ID: "job-3", Payload: raw}, nil)
	if !errors.Is(err, ErrInvalidJobPayload) {
		t.Fatalf("expected ErrInvalidJobPayload, got %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result on validation failure, got %v", result)
	}
	if len(mat.calls) != 0 {
		t.Errorf("preparation must not run on invalid request, got %v", mat.calls)
	}
}

// fakeSubtitleCompiler records the compile input and returns a deterministic
// artifact (path + content hash).
type fakeSubtitleCompiler struct {
	inputs []SubtitleCompileInput
}

func (f *fakeSubtitleCompiler) Compile(_ context.Context, in SubtitleCompileInput) (*SubtitleArtifact, error) {
	f.inputs = append(f.inputs, in)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", in.StyleID, in.Language, len(in.Cues))))
	return &SubtitleArtifact{
		LocalPath: filepath.Join(in.OutputDir, "subtitles.ass"),
		SHA256:    hex.EncodeToString(sum[:]),
		Mode:      in.Mode,
		StyleID:   in.StyleID,
	}, nil
}

// TestWorker_SubtitlesEnabled_CompilesFromTranscriptAndSeals verifies the
// ASS artifact is compiled from the canonical transcript cues (never a
// re-transcription) and referenced by the sealed plan with path + sha256.
func TestWorker_SubtitlesEnabled_CompilesFromTranscriptAndSeals(t *testing.T) {
	w, _, _ := newTestWorker(t)
	compiler := &fakeSubtitleCompiler{}
	w.WithSubtitleCompiler(compiler)

	req := baseRenderRequest()
	req.Subtitles = &SubtitlesSpec{Enabled: true, Mode: SubtitlesModeBurn, StyleID: "shorts-v1"}
	payload := renderJobPayload(t, req)

	var emitted []string
	tools := &job.JobExecutionTools{
		Progress: func(int, string) {},
		Event: func(eventType, _ string, _ map[string]any) {
			emitted = append(emitted, eventType)
		},
	}
	result, err := w.Handle(context.Background(), &job.Job{ID: "job-sub", Payload: payload}, tools)
	if !errors.Is(err, ErrRenderPhaseNotImplemented) {
		t.Fatalf("expected ErrRenderPhaseNotImplemented, got %v", err)
	}
	if len(compiler.inputs) != 1 {
		t.Fatalf("compile calls = %d, want 1", len(compiler.inputs))
	}
	in := compiler.inputs[0]
	if in.AssetID != "asset-source" || in.Mode != SubtitlesModeBurn || in.StyleID != "shorts-v1" {
		t.Errorf("compile input: got %+v", in)
	}
	// Cues come from the prepared transcript — the compiler never sees a
	// "generate" request (speech recognition is never re-run for subtitles).
	if len(in.Cues) != 1 || in.Cues[0].Text != "existing" {
		t.Errorf("compile input must carry the canonical transcript cues, got %+v", in.Cues)
	}
	if !contains(emitted, "clip.render.subtitles.compiled") || !contains(emitted, "clip.render.plan.sealed") {
		t.Errorf("expected compile+seal events, got %v", emitted)
	}
	plan := result["plan"].(map[string]any)
	if plan["plan_sha256"] == "" {
		t.Errorf("plan must be sealed, got %v", plan)
	}
	sub := result["subtitles"].(map[string]any)
	if sub["mode"] != SubtitlesModeBurn || sub["sha256"] == "" {
		t.Errorf("result subtitles block: got %v", sub)
	}
}

// TestWorker_SubtitlesEnabled_NoCompilerFailsClosed verifies subtitles
// enabled without a wired compiler is a typed failure — never a plan sealed
// without its ASS artifact.
func TestWorker_SubtitlesEnabled_NoCompilerFailsClosed(t *testing.T) {
	w, _, _ := newTestWorker(t) // no WithSubtitleCompiler

	req := baseRenderRequest()
	req.Subtitles = &SubtitlesSpec{Enabled: true, Mode: SubtitlesModeSidecar}
	payload := renderJobPayload(t, req)

	_, err := w.Handle(context.Background(), &job.Job{ID: "job-sub-noc", Payload: payload}, nil)
	if !errors.Is(err, ErrSubtitleCompileUnavailable) {
		t.Fatalf("expected ErrSubtitleCompileUnavailable, got %v", err)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// fakeRenderPublisher records the publish input and returns a deterministic
// publication so the full worker path (render + publish + result envelope)
// can be exercised without Drive or SQLite.
type fakeRenderPublisher struct {
	input RenderPublishInput
	out   RenderPublishResult
}

func (f *fakeRenderPublisher) Publish(_ context.Context, in RenderPublishInput) (*RenderPublishResult, error) {
	f.input = in
	out := f.out
	if out.AssetID == "" {
		out = RenderPublishResult{
			AssetID:     "final-video-asset-001",
			DriveFileID: "drive-file-001",
			DriveLink:   "https://drive.google.com/file/d/drive-file-001/view",
			SizeBytes:   in.Outcome.SizeBytes,
		}
	}
	return &out, nil
}

// TestWorker_OverlayLineageProjectedIntoResult certifies Gate 7's final
// binding: a clip.render request that declares an overlay must surface the
// complete overlay lineage (render job id + plan fingerprint + render key +
// source video asset id) on the final video result alongside the published
// asset (final_video_asset_id + Drive file), so the final video asset proves
// WHICH overlay it composites.
func TestWorker_OverlayLineageProjectedIntoResult(t *testing.T) {
	w, _, _ := newTestWorker(t)
	renderer := &fakeRenderExecutor{outcome: &RenderOutcome{
		OutputPath:  "/work/rendered-clip.mp4",
		SizeBytes:   4096,
		DurationSec: 3,
		Width:       1080,
		Height:      1920,
		FPSNum:      60,
		FPSDen:      1,
	}}
	publisher := &fakeRenderPublisher{}
	w.WithRenderExecutor(renderer)
	w.WithRenderPublisher(publisher)

	req := baseRenderRequest()
	req.Overlay = &OverlayRefSpec{
		RenderJobID:        "render-michael-jordan-overlay-001",
		PlanFingerprint:    "fp-michael-jordan",
		RenderKey:          "rk-michael-jordan",
		SourceVideoAssetID: "source-video-asset-001",
	}

	result, err := w.Handle(context.Background(), &job.Job{ID: "job-overlay-lineage", Payload: renderJobPayload(t, req)}, nil)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	overlay, ok := result["overlay"].(map[string]any)
	if !ok {
		t.Fatalf("result must carry an overlay block, got %+v", result)
	}
	if overlay["render_job_id"] != "render-michael-jordan-overlay-001" {
		t.Errorf("overlay render_job_id = %v", overlay["render_job_id"])
	}
	if overlay["plan_fingerprint"] != "fp-michael-jordan" {
		t.Errorf("overlay plan_fingerprint = %v", overlay["plan_fingerprint"])
	}
	if overlay["render_key"] != "rk-michael-jordan" {
		t.Errorf("overlay render_key = %v", overlay["render_key"])
	}
	if overlay["source_video_asset_id"] != "source-video-asset-001" {
		t.Errorf("overlay source_video_asset_id = %v", overlay["source_video_asset_id"])
	}

	// The final video asset block carries the Drive identity of the derived
	// asset: source_video_asset_id (request) → final_video_asset_id + Drive.
	assetBlock, ok := result["asset"].(map[string]any)
	if !ok {
		t.Fatalf("result must carry a published asset block, got %+v", result)
	}
	if assetBlock["asset_id"] != "final-video-asset-001" {
		t.Errorf("final video asset_id = %v", assetBlock["asset_id"])
	}
	if assetBlock["drive_file_id"] != "drive-file-001" {
		t.Errorf("final video drive_file_id = %v", assetBlock["drive_file_id"])
	}
	if result["source_asset_id"] != "asset-source" {
		t.Errorf("source_asset_id = %v", result["source_asset_id"])
	}
}

// TestWorker_PrepareFailure_Wrapped verifies a preparation failure surfaces
// as a wrapped error, never a silent success.
func TestWorker_PrepareFailure_Wrapped(t *testing.T) {
	resolver := newFakeAssetResolver(map[string]AssetRef{})
	mat := &fakeMaterializer{}
	tr := &fakeTranscriptResolver{}
	preparer := newTestPreparer(resolver, mat, tr)
	w, err := NewWorker(preparer, t.TempDir(), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	req := baseRenderRequest()
	payload := renderJobPayload(t, req)
	_, err = w.Handle(context.Background(), &job.Job{ID: "job-4", Payload: payload}, nil)
	if err == nil {
		t.Fatal("expected error when asset resolution fails")
	}
	if errors.Is(err, ErrRenderPhaseNotImplemented) {
		t.Fatalf("resolution failure must not be misreported as render-phase sentinel, got %v", err)
	}
	if !strings.Contains(err.Error(), "resolve source") {
		t.Fatalf("expected the wrapped resolution failure, got %v", err)
	}
}
