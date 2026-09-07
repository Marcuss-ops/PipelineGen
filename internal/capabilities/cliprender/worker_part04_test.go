package cliprender

import (
	"context"
	"errors"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
	"strings"
	"testing"
)

// TestWorker_OverlayCompositing_FailClosedOnResolutionError certifies that
// an unresolvable segment or a failed blend aborts the job — the published
// video never claims an overlay it does not carry.
func TestWorker_OverlayCompositing_FailClosedOnResolutionError(t *testing.T) {
	for name, setup := range map[string]func() (*OverlaySegment, error){
		"resolver error": func() (*OverlaySegment, error) { return nil, errors.New("overlay.render job not found") },
		"compositor error": func() (*OverlaySegment, error) {
			return &OverlaySegment{RenderJobID: "render-job-001", RenderKey: "key-001", LocalPath: "/work/seg.mp4", SHA256: "s"}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
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
			}})
			publisher := &fakeRenderPublisher{}
			w.WithRenderPublisher(publisher)

			segment, resolverErr := setup()
			resolver := &fakeOverlayResolver{segment: segment, err: resolverErr}
			compositor := &fakeOverlayCompositor{err: errors.New("blend failed")}
			w.WithOverlaySegmentResolver(resolver)
			w.WithOverlayCompositor(compositor)

			req := baseRenderRequest()
			req.Overlay = &OverlayRefSpec{
				RenderJobID:        "render-job-001",
				PlanFingerprint:    "fp-001",
				RenderKey:          "key-001",
				SourceVideoAssetID: "source-video-001",
				StartUS:            50000,
				EndUS:              950000,
			}
			_, err := w.Handle(context.Background(), &job.Job{ID: "job-overlay-fail", Payload: renderJobPayload(t, req)}, nil)
			if err == nil {
				t.Fatal("overlay compositing failure must fail the job")
			}
			if publisher.called != 0 {
				t.Error("publisher must not run when compositing fails")
			}
		})
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
