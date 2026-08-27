package adapters

// cliprender_executor_adapter_test.go — composition-root adapter test for
// the RenderExecutor port: maps the rustexec.ClipRenderResult into the
// capability-owned RenderOutcome verbatim and fails closed when the Rust
// renderer is not wired.

import (
	"context"
	"errors"
	"testing"
	"time"

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
	startupMS := int64(187)
	rustPublishMS := int64(9)
	opMS := int64(1450)
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
		StartupMS:         &startupMS,
		PublishMS:         &rustPublishMS,
		OpMS:              &opMS,
	}}
	adapter := &ClipRenderExecutorAdapter{
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
	// V2 report: selection facts, coarse render mapped onto composite_ms, and
	// derived aggregates must be populated by the adapter.
	if outcome.Metrics == nil {
		t.Fatal("Metrics = nil, want the V2 report populated")
	}
	m := outcome.Metrics
	if m.BackendSelected != cliprender.BackendFFmpegFallback {
		t.Fatalf("backend_selected = %q, want ffmpeg_fallback", m.BackendSelected)
	}
	if m.BackendAttempts != 1 || m.FallbackCount != 0 || m.FallbackReason != "" {
		t.Fatalf("selection counters = attempts=%d fallback=%d reason=%q, want 1/0/none", m.BackendAttempts, m.FallbackCount, m.FallbackReason)
	}
	if int64(m.BackendProbeMS) < 0 || int64(m.BackendResolveMS) < 0 {
		t.Fatalf("probe/resolve ms = %d/%d, must be measured (>= 0)", int64(m.BackendProbeMS), int64(m.BackendResolveMS))
	}
	if m.CompositeMS != 1250 {
		t.Fatalf("composite_ms = %d, want 1250 (ffmpeg_ms mapped onto the composite phase)", int64(m.CompositeMS))
	}
	// The Rust boundary's own phase timings now populate the benchmark
	// decomposition: renderer_startup_ms (pre-ffmpeg wall) and publish_ms
	// (Rust-side output publish) map only when the boundary reported them.
	if int64(m.RendererStartupMS) != 187 {
		t.Fatalf("renderer_startup_ms = %d, want 187 (Rust pre-ffmpeg wall)", int64(m.RendererStartupMS))
	}
	if int64(m.PublishMS) != 9 {
		t.Fatalf("publish_ms = %d, want 9 (Rust-side publish, worker overwrites with its full publication wall)", int64(m.PublishMS))
	}
	if m.Frames != 1830 {
		t.Fatalf("frames = %d, want 1830 (30.5s × 60fps)", m.Frames)
	}
	if !m.SubtitleRasterCPU {
		t.Fatal("subtitle_raster_cpu = false, want true (from the Rust metadata)")
	}
	// A fast fake can legitimately measure 0 ms — what matters is that the
	// adapter MEASURED the total (never the NOT_INSTRUMENTED sentinel).
	if int64(m.TotalMS) == cliprender.NotInstrumented {
		t.Fatalf("total_ms = %d, want a measured adapter wall time", int64(m.TotalMS))
	}
	// The coarse boundary reports ONE ffmpeg_ms: the remaining phases have no
	// real instrumentation and MUST stay NOT_INSTRUMENTED — never fake zeros.
	for name, phase := range map[string]int64{
		"decode_ms":         int64(m.DecodeMS),
		"encode_ms":         int64(m.EncodeMS),
		"audio_mux_ms":      int64(m.AudioMuxMS),
		"gpu_readback":      int64(m.GPUReadbackBytes),
		"nv12_to_rgba":      int64(m.NV12ToRGBAFrames),
		"encoder_staging":   int64(m.EncoderStagingCopyBytes),
		"failed_backend_ms": int64(m.FailedBackendMS),
	} {
		if phase != cliprender.NotInstrumented {
			t.Errorf("%s = %d, want NOT_INSTRUMENTED (no real instrumentation on this boundary)", name, phase)
		}
	}
	if int64(m.UnaccountedMS) != cliprender.NotInstrumented && int64(m.UnaccountedMS) < 0 {
		t.Fatalf("unaccounted_ms = %d, must never be negative", int64(m.UnaccountedMS))
	}
}

// slowExecutor wraps a fake with a real sleep so the adapter's wall-time
// measurement is deterministically > 0 ms even for an instant fake boundary.
type slowExecutor struct {
	fake *fakeClipRenderExecutor
	ms   int
}

func (s *slowExecutor) RenderClip(ctx context.Context, plan cliprender.ClipRenderPlanV1, backend cliprender.RenderBackend) (rustexec.ClipRenderResult, error) {
	time.Sleep(time.Duration(s.ms) * time.Millisecond)
	return s.fake.RenderClip(ctx, plan, backend)
}

// TestClipRenderExecutorAdapter_MetricsDerivedAggregates verifies the report
// computes total_fps / realtime_factor from the measured total and duration.
func TestClipRenderExecutorAdapter_MetricsDerivedAggregates(t *testing.T) {
	fake := &fakeClipRenderExecutor{result: rustexec.ClipRenderResult{
		OutputPath:  "/out.mp4",
		SizeBytes:   1024,
		DurationSec: 8,
		Width:       1280,
		Height:      720,
		FPSNum:      30,
		FPSDen:      1,
		FFmpegMS:    4720,
	}}
	adapter := &ClipRenderExecutorAdapter{
		renderer: &slowExecutor{fake: fake, ms: 10},
		resolver: cliprender.NewRenderBackendResolver(nil),
		probe:    emptyCapabilityProbe{},
	}

	outcome, err := adapter.Render(context.Background(), cliprender.ClipRenderPlanV1{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	m := outcome.Metrics
	if m.Frames != 240 {
		t.Fatalf("frames = %d, want 240", m.Frames)
	}
	if m.CompositeMS != 4720 {
		t.Fatalf("composite_ms = %d, want 4720", int64(m.CompositeMS))
	}
	total := int64(m.TotalMS)
	if total < 10 {
		t.Fatalf("total_ms = %d, want >= 10 (the slow executor's sleep is included)", total)
	}
	wantFPS := 240.0 / (float64(total) / 1000.0)
	if m.TotalFPS < wantFPS*0.99 || m.TotalFPS > wantFPS*1.01 {
		t.Fatalf("total_fps = %.2f, want ~%.2f", m.TotalFPS, wantFPS)
	}
	// An instant fake can be faster than realtime — the factor simply mirrors
	// the formula, it is not clamped to [0, 1].
	wantRTF := 8.0 / (float64(total) / 1000.0)
	if m.RealtimeFactor < wantRTF*0.99 || m.RealtimeFactor > wantRTF*1.01 {
		t.Fatalf("realtime_factor = %.2f, want ~%.2f", m.RealtimeFactor, wantRTF)
	}
}

type fullCudaProbe struct{}

func (fullCudaProbe) ProbeCapabilities(context.Context) (cliprender.RendererCapabilities, error) {
	return cliprender.RendererCapabilities{
		NVDEC: true, NVENCH264: true, GPUScale: true, GPUBlur: true, GPUAlpha: true,
	}, nil
}

// TestClipRenderExecutorAdapter_GPUHostWithoutChrononResolvesCudaHybrid locks
// the PATH B rule: on a host with the NVDEC/NVENC chain but NO certified
// Chronon, an eligible (device-local) plan resolves to cuda_native — the GPU
// hybrid is the intermediate path between Chronon (primary, certified) and
// the software baseline.
func TestClipRenderExecutorAdapter_GPUHostWithoutChrononResolvesCudaHybrid(t *testing.T) {
	fake := &fakeClipRenderExecutor{result: rustexec.ClipRenderResult{OutputPath: "/out.mp4", SizeBytes: 1, DurationSec: 1}}
	adapter := &ClipRenderExecutorAdapter{
		renderer: fake,
		resolver: cliprender.NewRenderBackendResolver(nil),
		probe:    fullCudaProbe{},
	}

	outcome, err := adapter.Render(context.Background(), cliprender.ClipRenderPlanV1{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if outcome.Backend != cliprender.BackendCudaNative {
		t.Fatalf("backend = %q, want cuda_native (PATH B hybrid)", outcome.Backend)
	}
	if fake.gotBackend != cliprender.BackendCudaNative {
		t.Fatalf("executor received backend %q, want cuda_native", fake.gotBackend)
	}
}

func TestClipRenderExecutorAdapter_PropagatesFailure(t *testing.T) {
	fake := &fakeClipRenderExecutor{err: errors.New("rust render_clip: boom")}
	adapter := &ClipRenderExecutorAdapter{
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
	adapter := &ClipRenderExecutorAdapter{renderer: fake}

	_, err := adapter.Render(context.Background(), cliprender.ClipRenderPlanV1{})
	if err == nil {
		t.Fatal("expected fail-closed error for unwired backend probe, got nil")
	}
	if !errors.Is(err, cliprender.ErrBackendUnavailable) {
		t.Fatalf("expected ErrBackendUnavailable, got %v", err)
	}
}

func TestClipRenderExecutorAdapter_FailsClosedWhenUnwired(t *testing.T) {
	adapter := &ClipRenderExecutorAdapter{} // nil renderer

	_, err := adapter.Render(context.Background(), cliprender.ClipRenderPlanV1{})
	if err == nil {
		t.Fatal("expected fail-closed error for unwired renderer, got nil")
	}
	if !errors.Is(err, cliprender.ErrRenderPhaseNotImplemented) {
		t.Fatalf("expected ErrRenderPhaseNotImplemented, got %v", err)
	}
}

// chrononCapabilityProbe reports full CUDA capabilities PLUS the Chronon
// backend CERTIFIED, so the resolver can select chronon_vulkan for plans
// that need it (the registry gates chronon on the certification, never on
// binary presence).
type chrononCapabilityProbe struct{}

func (chrononCapabilityProbe) ProbeCapabilities(context.Context) (cliprender.RendererCapabilities, error) {
	return cliprender.RendererCapabilities{
		NVDEC: true, NVENCH264: true, GPUScale: true, GPUBlur: true, GPUAlpha: true,
		ChrononVulkan: true, ChrononNativeCertified: true,
	}, nil
}

// uncertifiedChrononProbe reports the Chronon binary CONFIGURED but NOT
// certified — the certification gate refused it, so the resolver must route
// away from chronon_vulkan and the report must say why.
type uncertifiedChrononProbe struct{}

func (uncertifiedChrononProbe) ProbeCapabilities(context.Context) (cliprender.RendererCapabilities, error) {
	return cliprender.RendererCapabilities{
		NVDEC: true, NVENCH264: true, GPUScale: true, GPUBlur: true, GPUAlpha: true,
		ChrononVulkan: true,
	}, nil
}

// TestClipRenderExecutorAdapter_UncertifiedChrononRoutesAwayAndReportsReason
// verifies a configured-but-uncertified Chronon binary is never selected and
// the metrics report surfaces the certification gate as fallback_reason
// (informational: fallback_count stays 0 — selection is single-authority).
func TestClipRenderExecutorAdapter_UncertifiedChrononRoutesAwayAndReportsReason(t *testing.T) {
	fake := &fakeClipRenderExecutor{result: rustexec.ClipRenderResult{OutputPath: "/out.mp4", SizeBytes: 1, DurationSec: 1}}
	adapter := &ClipRenderExecutorAdapter{
		renderer: fake,
		resolver: cliprender.NewRenderBackendResolver(nil),
		probe:    uncertifiedChrononProbe{},
	}

	outcome, err := adapter.Render(context.Background(), blurPlan())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if outcome.Backend != cliprender.BackendFFmpegFallback {
		t.Fatalf("backend = %q, want ffmpeg_fallback (chronon uncertified and PATH B cannot blur)", outcome.Backend)
	}
	if outcome.Metrics == nil {
		t.Fatal("Metrics = nil, want the V2 report")
	}
	if outcome.Metrics.FallbackReason != "chronon_native_not_certified" {
		t.Fatalf("fallback_reason = %q, want chronon_native_not_certified", outcome.Metrics.FallbackReason)
	}
	if outcome.Metrics.FallbackCount != 0 {
		t.Fatalf("fallback_count = %d, want 0 (no fallback happened — informational reason only)", outcome.Metrics.FallbackCount)
	}
	if fake.gotBackend != cliprender.BackendFFmpegFallback {
		t.Fatalf("executor received backend %q, want ffmpeg_fallback", fake.gotBackend)
	}
}

// blurPlan carries a blur_source background — satisfied by certified Chronon
// (primary) and by the software fallback; PATH B cannot blur without a
// readback, so the resolver never routes it to the CUDA hybrid.
func blurPlan() cliprender.ClipRenderPlanV1 {
	return cliprender.ClipRenderPlanV1{
		Background: &cliprender.PlanBackground{Mode: cliprender.BackgroundModeBlurSource},
	}
}

// TestClipRenderExecutorAdapter_DispatchesToChrononWhenSelected verifies the
// adapter routes execution to the executor owning the resolver-selected
// backend — no adapter-level try-and-fall-back.
func TestClipRenderExecutorAdapter_DispatchesToChrononWhenSelected(t *testing.T) {
	fake := &fakeClipRenderExecutor{result: rustexec.ClipRenderResult{OutputPath: "/out.mp4", SizeBytes: 1, DurationSec: 1}}
	chronon := &fakeClipRenderExecutor{result: rustexec.ClipRenderResult{OutputPath: "/chronon.mp4", SizeBytes: 2, DurationSec: 1}}
	adapter := &ClipRenderExecutorAdapter{
		renderer: fake,
		chronon:  chronon,
		resolver: cliprender.NewRenderBackendResolver(nil),
		probe:    chrononCapabilityProbe{},
	}

	outcome, err := adapter.Render(context.Background(), blurPlan())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if outcome.Backend != cliprender.BackendChrononVulkan || outcome.OutputPath != "/chronon.mp4" {
		t.Fatalf("outcome = %+v, want chronon output", outcome)
	}
	if chronon.gotBackend != cliprender.BackendChrononVulkan {
		t.Fatalf("chronon executor got backend %q, want chronon_vulkan", chronon.gotBackend)
	}
	if fake.gotBackend != "" {
		t.Fatalf("rust renderer must not run when chronon is selected, got %q", fake.gotBackend)
	}
}

// TestClipRenderExecutorAdapter_FailsClosedWhenChrononSelectedButNotWired
// verifies a selected chronon_vulkan backend without a Chronon executor is a
// typed error — never a silent reroute to another backend.
func TestClipRenderExecutorAdapter_FailsClosedWhenChrononSelectedButNotWired(t *testing.T) {
	fake := &fakeClipRenderExecutor{result: rustexec.ClipRenderResult{OutputPath: "/out.mp4", SizeBytes: 1, DurationSec: 1}}
	adapter := &ClipRenderExecutorAdapter{
		renderer: fake,
		resolver: cliprender.NewRenderBackendResolver(nil),
		probe:    chrononCapabilityProbe{},
	}

	_, err := adapter.Render(context.Background(), blurPlan())
	if err == nil {
		t.Fatal("expected fail-closed error when chronon is selected but not wired")
	}
	if !errors.Is(err, cliprender.ErrBackendUnavailable) {
		t.Fatalf("expected ErrBackendUnavailable, got %v", err)
	}
	if fake.gotBackend != "" {
		t.Fatalf("rust renderer must not receive a plan routed to chronon, got %q", fake.gotBackend)
	}
}

// TestClipRenderExecutorAdapter_BlurRoutesToFFmpegWithoutChronon verifies a
// blur plan on a host without Chronon lands directly on the FFmpeg fallback
// — the resolver picks ONE backend, no wasted Chronon attempt.
func TestClipRenderExecutorAdapter_BlurRoutesToFFmpegWithoutChronon(t *testing.T) {
	fake := &fakeClipRenderExecutor{result: rustexec.ClipRenderResult{OutputPath: "/out.mp4", SizeBytes: 1, DurationSec: 1}}
	adapter := &ClipRenderExecutorAdapter{
		renderer: fake,
		resolver: cliprender.NewRenderBackendResolver(nil),
		probe:    fullCudaProbe{},
	}

	outcome, err := adapter.Render(context.Background(), blurPlan())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if outcome.Backend != cliprender.BackendFFmpegFallback {
		t.Fatalf("backend = %q, want ffmpeg_fallback (PATH B cannot blur, chronon absent)", outcome.Backend)
	}
	if fake.gotBackend != cliprender.BackendFFmpegFallback {
		t.Fatalf("executor received backend %q, want ffmpeg_fallback", fake.gotBackend)
	}
}
