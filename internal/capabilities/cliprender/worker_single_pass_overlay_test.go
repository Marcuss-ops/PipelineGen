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
		WithOverlaySegmentResolver(resolver)

	req := baseRenderRequest()
	req.Overlays = []OverlayRefSpec{{
		RenderJobID:        "render-overlay-001",
		PlanFingerprint:    "fp-overlay",
		RenderKey:          "rk-overlay-001",
		SourceVideoAssetID: "source-video-asset-001",
		StartUS:            50000,
		EndUS:              950000,
	}}

	if _, err := handleRendered(t, context.Background(), w, "job-single-pass", req); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	// The resolver ran exactly once, BEFORE the plan was sealed.
	if resolver.got.RenderJobID != "render-overlay-001" || resolver.got.RenderKey != "rk-overlay-001" {
		t.Fatalf("overlay resolver input = %+v", resolver.got)
	}

	// The SEALED plan carries the timed overlay (the single-encode evidence).
	overlay := renderer.plan.Overlay
	if overlay == nil || len(overlay.Segments) != 1 {
		t.Fatalf("sealed plan must carry the resolved overlay, got %+v", overlay)
	}
	seg := overlay.Segments[0]
	if seg.SHA256 != singlePassSegmentSHA || seg.Path != "/work/overlay-segment.mp4" {
		t.Fatalf("sealed overlay segment = %+v", seg)
	}
	if seg.RenderJobID != "render-overlay-001" || seg.RenderKey != "rk-overlay-001" {
		t.Fatalf("sealed overlay lineage = %+v", seg)
	}
	// 50000us → 50ms, 950000us → 950ms.
	if seg.StartMS != 50 || seg.EndMS != 950 {
		t.Fatalf("sealed overlay window = [%d, %d)ms, want [50, 950)", seg.StartMS, seg.EndMS)
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
	req.Overlays = []OverlayRefSpec{{RenderJobID: "render-x", RenderKey: "rk-x", StartUS: 0, EndUS: 1000000}}

	if _, err := w.Handle(context.Background(), &job.Job{ID: "job-single-pass-nonres", Payload: renderJobPayload(t, req)}, nil); err == nil {
		t.Fatal("overlay without a resolver must fail closed")
	}
	if renderer.called != 0 {
		t.Fatalf("renderer calls = %d, want 0 (fail closed before submission)", renderer.called)
	}
}

// TestWorker_OverlaySealsEveryDeclaredSegment pins the multi-item contract at
// the worker boundary: a clip declaring N certified overlay artifacts resolves
// and seals ALL N, each with its own lineage and window, inside the one render.
// A scene that composites a phrase AND an entity card must not silently lose
// the second.
func TestWorker_OverlaySealsEveryDeclaredSegment(t *testing.T) {
	w, _, _ := newTestWorker(t)
	renderer := &fakeRenderExecutor{outcome: &RenderOutcome{
		OutputPath: "/work/rendered-clip.mp4", SizeBytes: 4096, SHA256: strings.Repeat("c", 64),
		DurationSec: 8, Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, Backend: BackendChrononVulkan,
	}}
	segmentSHA := func(c string) string { return strings.Repeat(c, 64) }
	resolver := &fakeOverlayResolver{byKey: map[string]*OverlaySegment{
		"rk-phrase":  {RenderJobID: "job-phrase", RenderKey: "rk-phrase", LocalPath: "/work/phrase.mp4", SHA256: segmentSHA("b"), SizeBytes: 1024},
		"rk-entity":  {RenderJobID: "job-entity", RenderKey: "rk-entity", LocalPath: "/work/entity.mp4", SHA256: segmentSHA("d"), SizeBytes: 2048},
		"rk-keyword": {RenderJobID: "job-keyword", RenderKey: "rk-keyword", LocalPath: "/work/keyword.mp4", SHA256: segmentSHA("e"), SizeBytes: 512},
	}}
	w.WithRenderExecutor(renderer).WithRenderPublisher(&fakeRenderPublisher{}).WithOverlaySegmentResolver(resolver)

	req := baseRenderRequest()
	req.Overlays = []OverlayRefSpec{
		{RenderJobID: "job-phrase", PlanFingerprint: "fp", RenderKey: "rk-phrase", SourceVideoAssetID: "src", StartUS: 0, EndUS: 2_000_000},
		{RenderJobID: "job-entity", PlanFingerprint: "fp", RenderKey: "rk-entity", SourceVideoAssetID: "src", StartUS: 2_000_000, EndUS: 5_000_000},
		{RenderJobID: "job-keyword", PlanFingerprint: "fp", RenderKey: "rk-keyword", SourceVideoAssetID: "src", StartUS: 5_000_000, EndUS: 7_500_000},
	}

	if _, err := handleRendered(t, context.Background(), w, "job-multi-overlay", req); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resolver.calls != 3 {
		t.Fatalf("resolver calls = %d, want one per declared lineage", resolver.calls)
	}
	overlay := renderer.plan.Overlay
	if overlay == nil || len(overlay.Segments) != 3 {
		t.Fatalf("sealed segments = %+v, want all 3 declared overlays", overlay)
	}
	for i, want := range []struct {
		key            string
		sha            string
		startMS, endMS int64
	}{
		{"rk-phrase", segmentSHA("b"), 0, 2000},
		{"rk-entity", segmentSHA("d"), 2000, 5000},
		{"rk-keyword", segmentSHA("e"), 5000, 7500},
	} {
		got := overlay.Segments[i]
		if got.RenderKey != want.key || got.SHA256 != want.sha || got.StartMS != want.startMS || got.EndMS != want.endMS {
			t.Fatalf("sealed segment %d = %+v, want key=%s window=[%d,%d)", i, got, want.key, want.startMS, want.endMS)
		}
	}
}

// TestWorker_OverlayOneUnresolvableFailsClosed pins that a partial fan-out is
// never rendered: when one of N declared overlays cannot be resolved, the whole
// clip fails closed with zero submissions instead of shipping a clip that lost
// an overlay the caller declared.
func TestWorker_OverlayOneUnresolvableFailsClosed(t *testing.T) {
	w, _, _ := newTestWorker(t)
	renderer := &fakeRenderExecutor{outcome: &RenderOutcome{
		OutputPath: "/work/rendered-clip.mp4", SizeBytes: 4096, SHA256: strings.Repeat("c", 64),
		DurationSec: 8, Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, Backend: BackendChrononVulkan,
	}}
	// byKey deliberately lacks rk-missing, so the second resolution misses.
	resolver := &fakeOverlayResolver{byKey: map[string]*OverlaySegment{
		"rk-ok": {RenderJobID: "job-ok", RenderKey: "rk-ok", LocalPath: "/work/ok.mp4", SHA256: singlePassSegmentSHA},
	}}
	w.WithRenderExecutor(renderer).WithRenderPublisher(&fakeRenderPublisher{}).WithOverlaySegmentResolver(resolver)

	req := baseRenderRequest()
	req.Overlays = []OverlayRefSpec{
		{RenderJobID: "job-ok", PlanFingerprint: "fp", RenderKey: "rk-ok", SourceVideoAssetID: "src", StartUS: 0, EndUS: 1_000_000},
		{RenderJobID: "job-missing", PlanFingerprint: "fp", RenderKey: "rk-missing", SourceVideoAssetID: "src", StartUS: 1_000_000, EndUS: 2_000_000},
	}

	if _, err := w.Handle(context.Background(), &job.Job{ID: "job-partial-overlay", Payload: renderJobPayload(t, req)}, nil); err == nil {
		t.Fatal("an unresolvable overlay among the declared set must fail closed")
	}
	if renderer.called != 0 {
		t.Fatalf("renderer calls = %d, want 0 (fail closed before submission)", renderer.called)
	}
}
