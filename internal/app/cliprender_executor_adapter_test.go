package app

// cliprender_executor_adapter_test.go — composition-root adapter test for
// the RenderExecutor port: maps the rustexec.ClipRenderResult into the
// capability-owned RenderOutcome verbatim and fails closed when the Rust
// renderer is not wired.

import (
	"context"
	"errors"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
)

// fakeClipRenderExecutor stands in for the rustexec.ClipRenderer (narrow
// seam) so the mapping is tested without the Rust process.
type fakeClipRenderExecutor struct {
	result     rustexec.ClipRenderResult
	err        error
	gotBackend cliprender.RenderBackend
}

func (f *fakeClipRenderExecutor) RenderClip(_ context.Context, _ cliprender.ClipRenderPlanV1, backend cliprender.RenderBackend) (rustexec.ClipRenderResult, error) {
	f.gotBackend = backend
	return f.result, f.err
}

func TestClipRenderExecutorAdapter_MapsOutcomeVerbatim(t *testing.T) {
	eligible := true
	passes := 0
	cpuSubs := true
	fake := &fakeClipRenderExecutor{result: rustexec.ClipRenderResult{
		OutputPath:        "/scratch/run-1/rendered-clip.mp4",
		SizeBytes:         1024,
		DurationSec:       30.5,
		Width:             1080,
		Height:            1920,
		FPSNum:            60,
		FPSDen:            1,
		FFmpegMS:          1250,
		AudioCopyEligible: &eligible,
		AudioEncodePasses: &passes,
		SubtitleRasterCPU: &cpuSubs,
	}}
	adapter := &clipRenderExecutorAdapter{
		renderer: fake,
		resolver: cliprender.NewRenderBackendResolver(nil),
		probe:    emptyCapabilityProbe{},
	}

	outcome, err := adapter.Render(context.Background(), cliprender.ClipRenderPlanV1{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if outcome.OutputPath != "/scratch/run-1/rendered-clip.mp4" || outcome.SizeBytes != 1024 || outcome.DurationSec != 30.5 {
		t.Fatalf("output facts: %+v", outcome)
	}
	if outcome.Width != 1080 || outcome.Height != 1920 || outcome.FPSNum != 60 || outcome.FPSDen != 1 || outcome.FFmpegMS != 1250 {
		t.Fatalf("media facts: %+v", outcome)
	}
	if outcome.Backend != cliprender.BackendFFmpegFallback {
		t.Fatalf("backend = %q, want ffmpeg_fallback (explicit empty probe → intentional software path)", outcome.Backend)
	}
	if outcome.AudioCopyEligible == nil || !*outcome.AudioCopyEligible {
		t.Fatalf("AudioCopyEligible = %v, want true", outcome.AudioCopyEligible)
	}
	if outcome.AudioEncodePasses == nil || *outcome.AudioEncodePasses != 0 {
		t.Fatalf("AudioEncodePasses = %v, want 0", outcome.AudioEncodePasses)
	}
	if outcome.SubtitleRasterCPU == nil || !*outcome.SubtitleRasterCPU {
		t.Fatalf("SubtitleRasterCPU = %v, want true", outcome.SubtitleRasterCPU)
	}
}

type fullCudaProbe struct{}

func (fullCudaProbe) ProbeCapabilities(context.Context) (cliprender.RendererCapabilities, error) {
	return cliprender.RendererCapabilities{
		NVDEC: true, NVENCH264: true, GPUScale: true, GPUBlur: true, GPUAlpha: true,
	}, nil
}

func TestClipRenderExecutorAdapter_ResolvesCudaBackend(t *testing.T) {
	fake := &fakeClipRenderExecutor{result: rustexec.ClipRenderResult{OutputPath: "/out.mp4", SizeBytes: 1, DurationSec: 1}}
	adapter := &clipRenderExecutorAdapter{
		renderer: fake,
		resolver: cliprender.NewRenderBackendResolver(nil),
		probe:    fullCudaProbe{},
	}

	outcome, err := adapter.Render(context.Background(), cliprender.ClipRenderPlanV1{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if outcome.Backend != cliprender.BackendCudaNative {
		t.Fatalf("backend = %q, want cuda_native", outcome.Backend)
	}
	if fake.gotBackend != cliprender.BackendCudaNative {
		t.Fatalf("executor received backend %q, want cuda_native", fake.gotBackend)
	}
}

func TestClipRenderExecutorAdapter_PropagatesFailure(t *testing.T) {
	fake := &fakeClipRenderExecutor{err: errors.New("rust render_clip: boom")}
	adapter := &clipRenderExecutorAdapter{
		renderer: fake,
		resolver: cliprender.NewRenderBackendResolver(nil),
		probe:    emptyCapabilityProbe{},
	}

	_, err := adapter.Render(context.Background(), cliprender.ClipRenderPlanV1{})
	if err == nil || err.Error() != "rust render_clip: boom" {
		t.Fatalf("expected propagated failure, got %v", err)
	}
}

// emptyCapabilityProbe returns empty capabilities so the resolver selects
// the FFmpeg fallback explicitly — a CPU-only worker that declares its
// intent, never a silent degrading wiring bug.
type emptyCapabilityProbe struct{}

func (emptyCapabilityProbe) ProbeCapabilities(context.Context) (cliprender.RendererCapabilities, error) {
	return cliprender.RendererCapabilities{}, nil
}

func TestClipRenderExecutorAdapter_FailsClosedWhenBackendProbeIsUnwired(t *testing.T) {
	fake := &fakeClipRenderExecutor{result: rustexec.ClipRenderResult{OutputPath: "/out", SizeBytes: 1, DurationSec: 1}}
	// No resolver, no probe → ResolveBackend fails with ErrBackendUnavailable
	// instead of silently degrading to FFmpeg.
	adapter := &clipRenderExecutorAdapter{renderer: fake}

	_, err := adapter.Render(context.Background(), cliprender.ClipRenderPlanV1{})
	if err == nil {
		t.Fatal("expected fail-closed error for unwired backend probe, got nil")
	}
	if !errors.Is(err, cliprender.ErrBackendUnavailable) {
		t.Fatalf("expected ErrBackendUnavailable, got %v", err)
	}
}

func TestClipRenderExecutorAdapter_FailsClosedWhenUnwired(t *testing.T) {
	adapter := &clipRenderExecutorAdapter{} // nil renderer

	_, err := adapter.Render(context.Background(), cliprender.ClipRenderPlanV1{})
	if err == nil {
		t.Fatal("expected fail-closed error for unwired renderer, got nil")
	}
	if !errors.Is(err, cliprender.ErrRenderPhaseNotImplemented) {
		t.Fatalf("expected ErrRenderPhaseNotImplemented, got %v", err)
	}
}
