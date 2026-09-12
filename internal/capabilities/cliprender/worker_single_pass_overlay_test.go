package cliprender

import (
	"context"
	"strings"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// singlePassSegmentSHA is a valid 64-hex digest for the resolved overlay
// segment (the sealed plan validates it fail-closed).
var singlePassSegmentSHA = strings.Repeat("b", 64)

func singlePassOverlayProbe() *fakeOutputProber {
	return &fakeOutputProber{probe: &OutputProbe{
		Container: "mp4", HasVideo: true, HasAudio: true,
		VideoCodec: "h264", VideoProfile: "high", PixelFormat: "yuv420p",
		Width: 1920, Height: 1080, FPS: 24.0, FPSNum: 24, FPSDen: 1,
		AudioCodec: "aac", AudioProfile: "LC", SampleRate: 48000, Channels: 2,
		ChannelLayout: "stereo", AudioBitrate: "128k",
		VideoStreams: 1, AudioStreams: 1, StartPTS: 0,
	}}
}

// TestWorker_OverlaySealsSegmentIntoPlan pins the single-encode contract: the
// resolved overlay segment is part of the SEALED plan handed to the render
// boundary, so the clip is encoded exactly once with the overlay composited
// inside the Chronon pass. There is no post-render compositor left to invoke.
func TestWorker_OverlaySealsSegmentIntoPlan(t *testing.T) {
	w, _, _ := newTestWorker(t)
	renderer := &fakeRenderExecutor{outcome: &RenderOutcome{
		OutputPath:  "/work/rendered-clip.mp4",
		SizeBytes:   4096,
		SHA256:      strings.Repeat("c", 64),
		DurationSec: 3,
		Width:       1920,
		Height:      1080,
		FPSNum:      24,
		FPSDen:      1,
		Backend:     BackendChrononVulkan,
	}}
	publisher := &fakeRenderPublisher{}
	resolver := &fakeOverlayResolver{segment: &OverlaySegment{
		RenderJobID: "render-overlay-001",
		RenderKey:   "rk-overlay-001",
		LocalPath:   "/work/overlay-segment.mp4",
		SHA256:      singlePassSegmentSHA,
		SizeBytes:   2048,
	}}
	w.WithRenderExecutor(renderer).
		WithRenderPublisher(publisher).
		WithOverlaySegmentResolver(resolver).
		WithOutputProber(singlePassOverlayProbe())

	req := baseRenderRequest()
	req.Overlay = &OverlayRefSpec{
		RenderJobID:        "render-overlay-001",
		PlanFingerprint:    "fp-overlay",
		RenderKey:          "rk-overlay-001",
		SourceVideoAssetID: "source-video-asset-001",
		StartUS:            50000,
		EndUS:              950000,
	}

	if _, err := w.Handle(context.Background(), &job.Job{ID: "job-single-pass", Payload: renderJobPayload(t, req)}, nil); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	// The resolver ran exactly once, BEFORE the plan was sealed.
	if resolver.got.RenderJobID != "render-overlay-001" || resolver.got.RenderKey != "rk-overlay-001" {
		t.Fatalf("overlay resolver input = %+v", resolver.got)
	}

	// The SEALED plan carries the timed overlay (the single-encode evidence).
	overlay := renderer.plan.Overlay
	if overlay == nil {
		t.Fatal("sealed plan must carry the resolved overlay")
	}
	if overlay.SHA256 != singlePassSegmentSHA || overlay.Path != "/work/overlay-segment.mp4" {
		t.Fatalf("sealed overlay segment = %+v", overlay)
	}
	if overlay.RenderJobID != "render-overlay-001" || overlay.RenderKey != "rk-overlay-001" {
		t.Fatalf("sealed overlay lineage = %+v", overlay)
	}
	// 50000us → 50ms, 950000us → 950ms.
	if overlay.StartMS != 50 || overlay.EndMS != 950 {
		t.Fatalf("sealed overlay window = [%d, %d)ms, want [50, 950)", overlay.StartMS, overlay.EndMS)
	}

	// Publication must target the RENDER output (no composited intermediate),
	// certified by the render boundary's own digest.
	if publisher.input.OutputPath != "/work/rendered-clip.mp4" {
		t.Fatalf("published path = %q, want the single render output", publisher.input.OutputPath)
	}
	if publisher.input.CertifiedSHA256 != strings.Repeat("c", 64) {
		t.Fatalf("certified digest = %q, want the render output digest", publisher.input.CertifiedSHA256)
	}
}

// TestWorker_OverlayFailsClosedWithoutResolver pins the fail-closed wiring
// rule: declaring an overlay without a segment resolver is a typed error
// before any render is submitted.
func TestWorker_OverlayFailsClosedWithoutResolver(t *testing.T) {
	w, _, _ := newTestWorker(t)
	renderer := &fakeRenderExecutor{outcome: &RenderOutcome{
		OutputPath: "/work/rendered-clip.mp4", SizeBytes: 4096, SHA256: strings.Repeat("c", 64),
		DurationSec: 3, Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, Backend: BackendChrononVulkan,
	}}
	w.WithRenderExecutor(renderer)

	req := baseRenderRequest()
	req.Overlay = &OverlayRefSpec{RenderJobID: "render-x", RenderKey: "rk-x", StartUS: 0, EndUS: 1000000}

	if _, err := w.Handle(context.Background(), &job.Job{ID: "job-single-pass-nonres", Payload: renderJobPayload(t, req)}, nil); err == nil {
		t.Fatal("overlay without a resolver must fail closed")
	}
	if renderer.called != 0 {
		t.Fatalf("renderer calls = %d, want 0 (fail closed before submission)", renderer.called)
	}
}
