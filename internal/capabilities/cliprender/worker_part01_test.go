package cliprender

import (
	"context"
	"encoding/json"
	"errors"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
	"testing"
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

// fakeOverlayResolver returns a canned segment for the declared render_job_id.
type fakeOverlayResolver struct {
	segment *OverlaySegment
	err     error
	got     OverlayResolveInput
}

func (f *fakeOverlayResolver) Resolve(_ context.Context, in OverlayResolveInput) (*OverlaySegment, error) {
	f.got = in
	if f.err != nil {
		return nil, f.err
	}
	return f.segment, nil
}

// fakeOverlayCompositor records the composite input and returns a canned
// composited output, mirroring the real pass contract (a new hashed file).
type fakeOverlayCompositor struct {
	composite *OverlayCompositeResult
	err       error
	got       OverlayCompositeInput
}

func (f *fakeOverlayCompositor) Composite(_ context.Context, in OverlayCompositeInput) (*OverlayCompositeResult, error) {
	f.got = in
	if f.err != nil {
		return nil, f.err
	}
	return f.composite, nil
}

// fakeOutputProber returns a canned probe for post-render certification.
type fakeOutputProber struct {
	probe *OutputProbe
	err   error
}

func (f *fakeOutputProber) ProbeOutput(_ context.Context, _ string) (*OutputProbe, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.probe, nil
}

func TestRenderedResult_LegacyFieldsAreReadOnlyProjections(t *testing.T) {
	outcome := &RenderOutcome{OutputPath: "/work/out.mp4", SizeBytes: 1, DurationSec: 1, FFmpegMS: 1234, SubtitleRasterCPU: boolPtr(true), GPUCopyBytes: uint64Ptr(99), Metrics: NewRenderMetricsV2()}
	outcome.Metrics.CompositeMS = 1234
	outcome.Metrics.GPUCopyBytes = 99
	outcome.Metrics.SubtitleRasterCPU = true
	result := renderedResult(&job.Job{ID: "job-projection"}, &RenderRequest{SourceAssetID: "asset-source", Transcript: &TranscriptSpec{Mode: "reuse_or_generate"}}, &Prepared{Contract: &ResolvedContract{}, Source: &MaterializedAsset{}, Transcript: &TranscriptResult{}}, ClipRenderPlanV1{}, nil, outcome, nil, nil)
	render, ok := result["render"].(map[string]any)
	if !ok {
		t.Fatalf("render result = %v", result["render"])
	}
	metrics, ok := render["metrics_v2"].(*RenderMetricsV2)
	if !ok || metrics == nil {
		t.Fatalf("metrics_v2 = %v", render["metrics_v2"])
	}
	if render["ffmpeg_ms"] != outcome.FFmpegMS || metrics.CompositeMS != outcome.Metrics.CompositeMS {
		t.Fatalf("legacy/canonical projection mismatch: render=%v metrics=%+v", render, metrics)
	}
	if render["gpu_copy_bytes"] != outcome.GPUCopyBytes || metrics.GPUCopyBytes != outcome.Metrics.GPUCopyBytes {
		t.Fatalf("gpu projection mismatch: render=%v metrics=%+v", render, metrics)
	}
	if render["subtitle_raster_cpu"] != outcome.SubtitleRasterCPU || metrics.SubtitleRasterCPU != *outcome.SubtitleRasterCPU {
		t.Fatalf("subtitle projection mismatch: render=%v metrics=%+v", render, metrics)
	}
}

func boolPtr(v bool) *bool       { return &v }
func uint64Ptr(v uint64) *uint64 { return &v }

func TestWorker_ExecutesSealedPlanThroughRenderExecutor(t *testing.T) {
	w, _, _ := newTestWorker(t)
	renderer := &fakeRenderExecutor{outcome: &RenderOutcome{
		OutputPath:  "/work/rendered-clip.mp4",
		SizeBytes:   4096,
		DurationSec: 3,
		Width:       1920,
		Height:      1080,
		FPSNum:      24,
		FPSDen:      1,
		Backend:     BackendChrononVulkan,
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
	// V2 report envelope: the worker folds the job-level total into the
	// adapter report and exposes it in the job result. Phases without real
	// instrumentation (subtitles disabled here) stay NOT_INSTRUMENTED.
	metrics, ok := render["metrics_v2"].(*RenderMetricsV2)
	if !ok || metrics == nil {
		t.Fatalf("metrics_v2 = %v, want *RenderMetricsV2 in the render block", render["metrics_v2"])
	}
	// A fast fake can legitimately measure 0 ms of wall time — the worker must
	// still have MEASURED the job total (never the NOT_INSTRUMENTED sentinel).
	if int64(metrics.TotalMS) == NotInstrumented {
		t.Fatalf("total_ms = %d, want a measured job-level wall time", int64(metrics.TotalMS))
	}
	if metrics.Frames != 72 {
		t.Fatalf("frames = %d, want 72 (3s × 24fps)", metrics.Frames)
	}
	if int64(metrics.SubtitleCompileMS) != NotInstrumented {
		t.Fatalf("subtitle_compile_ms = %d, want NOT_INSTRUMENTED (subtitles disabled)", int64(metrics.SubtitleCompileMS))
	}
	// asset_materialize_ms folds the preparer's materialize phase walls into
	// the report (the real preparer records materialize_source even through
	// the fake materializer), so the benchmark can attribute the "bring the
	// assets to disk" cost instead of leaving it in the unaccounted gap.
	if int64(metrics.AssetMaterializeMS) == NotInstrumented {
		t.Fatalf("asset_materialize_ms = %d, want the measured materialize phase wall", int64(metrics.AssetMaterializeMS))
	}
}

// TestMaterializeWallMS verifies the phase-fold helper: materialize_* walls
// sum into asset_materialize_ms; a preparation without materialize phases
// (all cached, nothing recorded) stays NOT_INSTRUMENTED (-1).
func TestMaterializeWallMS(t *testing.T) {
	summed := materializeWallMS(PreparationTimings{Phases: []PhaseTiming{
		{Phase: "resolve_source", WallMS: 12},
		{Phase: "materialize_source", WallMS: 340},
		{Phase: "materialize_watermark", WallMS: 60},
		{Phase: "transcript_resolve", WallMS: 5},
	}})
	if summed != 400 {
		t.Fatalf("materializeWallMS = %d, want 400 (340+60)", summed)
	}
	if got := materializeWallMS(PreparationTimings{Phases: []PhaseTiming{
		{Phase: "resolve_source", WallMS: 12},
	}}); got != -1 {
		t.Fatalf("materializeWallMS without materialize phases = %d, want -1 (NOT_INSTRUMENTED)", got)
	}
	if got := materializeWallMS(PreparationTimings{}); got != -1 {
		t.Fatalf("materializeWallMS(empty) = %d, want -1", got)
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
	if got := result["contract_id"]; got != OutputContractVeloxAssemblyReadyV1 {
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
