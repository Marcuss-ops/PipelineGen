package cliprender

// metrics_projection_test.go pins the typed projection of the renderer-owned
// phase timings (Chronon/CUDA/FFmpeg) from the canonical V2 report onto the
// kernel Run: each measured phase becomes ONE Run.Operation with the
// component of the backend that actually ran, durations come from the
// owner's report (never a second timer), non-instrumented phases stay
// absent, and GPU byte counters carry Bytes instead of a fake duration.

import (
	"context"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

func TestProjectRendererPhases_ProjectsMeasuredPhasesAsChrononOperations(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-1", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)

	m := NewRenderMetricsV2()
	m.RendererStartupMS = 120
	m.ProbeMS = 40
	m.DecodeMS = 340
	m.CompositeMS = 4200
	m.SubtitleRasterMS = 420
	m.WatermarkRasterMS = 150
	m.FrameConversionMS = 300
	m.EncodeMS = 1800
	m.AudioMuxMS = 90
	m.GPUCopyBytes = 1_000_000
	m.GPUReadbackBytes = 500_000

	projectRendererPhases(ctx, BackendChrononVulkan, m)
	run.Finish()

	ops := run.Report().Operations
	if len(ops) != 11 {
		t.Fatalf("operations = %d, want 11 (9 phases + 2 GPU byte counters), got %+v", len(ops), ops)
	}
	byName := map[string]kernobs.OperationReport{}
	for _, op := range ops {
		if op.Stage != string(StageClipRender) || op.Component != string(kernobs.ComponentChronon) {
			t.Errorf("operation %s = %s/%s, want clip.render/chronon", op.Operation, op.Stage, op.Component)
		}
		byName[op.Operation] = op
	}
	for op, wantMS := range map[string]int64{
		"renderer_startup": 120, "probe": 40, "decode": 340, "composite": 4200,
		"subtitle_raster": 420, "watermark_raster": 150, "frame_conversion": 300,
		"encode": 1800, "audio_mux": 90,
	} {
		if got := byName[op].DurationMs; got != wantMS {
			t.Errorf("operation %s duration = %d ms, want %d", op, got, wantMS)
		}
	}
	// GPU byte counters carry Bytes, never a fake duration.
	if byName["gpu_copy"].Bytes != 1_000_000 || byName["gpu_copy"].DurationMs != 0 {
		t.Errorf("gpu_copy = %+v, want Bytes=1000000 duration=0", byName["gpu_copy"])
	}
	if byName["gpu_readback"].Bytes != 500_000 || byName["gpu_readback"].DurationMs != 0 {
		t.Errorf("gpu_readback = %+v, want Bytes=500000 duration=0", byName["gpu_readback"])
	}
}

func TestProjectRendererPhases_SkipsNotInstrumentedPhases(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-2", AttemptID: "attempt-2"})
	ctx := kernobs.WithRun(context.Background(), run)

	// Everything NOT_INSTRUMENTED → no operations at all (never fake zeros).
	projectRendererPhases(ctx, BackendChrononVulkan, NewRenderMetricsV2())

	// A partial report projects only the measured phases.
	m := NewRenderMetricsV2()
	m.SubtitleRasterMS = 420
	m.EncodeMS = 1500
	projectRendererPhases(ctx, BackendChrononVulkan, m)
	run.Finish()

	ops := run.Report().Operations
	if len(ops) != 2 {
		t.Fatalf("operations = %d, want 2 (only measured phases), got %+v", len(ops), ops)
	}
}

func TestProjectRendererPhases_MapsComponentByBackend(t *testing.T) {
	project := func(backend RenderBackend) string {
		run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-3", AttemptID: "attempt-3"})
		ctx := kernobs.WithRun(context.Background(), run)
		m := NewRenderMetricsV2()
		m.CompositeMS = 100
		projectRendererPhases(ctx, backend, m)
		run.Finish()
		return run.Report().Operations[0].Component
	}
	if got := project(BackendChrononVulkan); got != string(kernobs.ComponentChronon) {
		t.Errorf("chronon_vulkan component = %q, want chronon", got)
	}
	if got := project(BackendCudaNative); got != string(kernobs.ComponentCUDA) {
		t.Errorf("cuda_native component = %q, want cuda", got)
	}
	if got := project(BackendFFmpegFallback); got != string(kernobs.ComponentFFmpeg) {
		t.Errorf("ffmpeg_fallback component = %q, want ffmpeg", got)
	}
}

func TestProjectRendererPhases_NilReportAndNoRunAreNoops(t *testing.T) {
	// Nil report: no panic, nothing recorded.
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-4", AttemptID: "attempt-4"})
	ctx := kernobs.WithRun(context.Background(), run)
	projectRendererPhases(ctx, BackendChrononVulkan, nil)
	run.Finish()
	if len(run.Report().Operations) != 0 {
		t.Fatal("nil report must record nothing")
	}
	// No run bound: safe no-op, never a panic.
	projectRendererPhases(context.Background(), BackendChrononVulkan, NewRenderMetricsV2())
}

// TestWorker_ProjectsRendererPhasesOntoRun pins the end-to-end wiring: a
// worker handling a render whose outcome carries a measured V2 report must
// project the engine-owned phases onto the run bound to ctx (typed
// projection), alongside the existing rust.render_clip wall operation.
func TestWorker_ProjectsRendererPhasesOntoRun(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-obs-3", AttemptID: "attempt-3"})
	ctx := kernobs.WithRun(context.Background(), run)

	m := NewRenderMetricsV2()
	m.SubtitleRasterMS = 420
	m.CompositeMS = 2100
	m.EncodeMS = 1500
	w, _, _ := newTestWorker(t)
	w.WithRenderExecutor(&fakeRenderExecutor{outcome: &RenderOutcome{
		OutputPath:  "/work/rendered-clip.mp4",
		SizeBytes:   4096,
		DurationSec: 3,
		Width:       1920,
		Height:      1080,
		FPSNum:      24,
		FPSDen:      1,
		Backend:     BackendChrononVulkan,
		Metrics:     m,
	}})
	w.WithRenderPublisher(&fakeRenderPublisher{})

	if _, err := w.Handle(ctx, &job.Job{ID: "job-obs-3", Payload: renderJobPayload(t, baseRenderRequest())}, nil); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	run.Finish()

	got := map[string]int64{}
	for _, op := range run.Report().Operations {
		if op.Component == string(kernobs.ComponentChronon) {
			got[op.Operation] = op.DurationMs
		}
	}
	for op, wantMS := range map[string]int64{"subtitle_raster": 420, "composite": 2100, "encode": 1500} {
		if got[op] != wantMS {
			t.Errorf("chronon operation %s = %d ms, want %d (got %v)", op, got[op], wantMS, got)
		}
	}
}
