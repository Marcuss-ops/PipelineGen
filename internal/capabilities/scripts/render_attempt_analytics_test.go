package scriptgeneration

import (
	"context"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// TestBuildRenderAttemptAnalyticsProjectsContentAndArtifact pins the builder:
// the content census comes from the plan, the durations/output metrics/
// SHA-256/Drive identity come verbatim from the certified artifact, and a nil
// artifact yields an empty-output record without error.
func TestBuildRenderAttemptAnalyticsProjectsContentAndArtifact(t *testing.T) {
	plan := capoverlay.OverlayPlan{PlanID: "plan-1", Items: []capoverlay.OverlayItem{
		{ID: "p", TemplateID: "IMPORTANT_PHRASE"},
		{ID: "w", TemplateID: "IMPORTANT_WORD"},
		{ID: "i", TemplateID: "IMAGE_OVERLAY"},
		{ID: "l", TemplateID: "LIGHT_LEAK"},
	}}
	artifact := &RenderArtifact{
		SHA256:      "abc",
		SizeBytes:   1024,
		Width:       1280,
		Height:      720,
		FPSNum:      30,
		FPSDen:      1,
		FrameCount:  150,
		DurationUS:  5_000_000,
		RenderMS:    900,
		EncodeMS:    300,
		DriveFileID: "file-1",
		DriveLink:   "https://drive.google.com/file/d/file-1/view",
	}
	got := BuildRenderAttemptAnalytics("attempt-1", plan, artifact)

	if got.AttemptID != "attempt-1" || got.JobID != "plan-1" {
		t.Fatalf("identity = %q/%q, want attempt-1/plan-1", got.AttemptID, got.JobID)
	}
	if got.Content.Phrases != 1 || got.Content.Words != 1 || got.Content.Images != 1 || got.Content.Leaks != 1 {
		t.Fatalf("content = %+v, want one of each", got.Content)
	}
	if got.SHA256 != "abc" || got.RenderMS != 900 || got.EncodeMS != 300 ||
		got.DriveFileID != "file-1" || got.DriveLink != "https://drive.google.com/file/d/file-1/view" ||
		got.Width != 1280 || got.Height != 720 || got.FrameCount != 150 ||
		got.DurationUS != 5_000_000 || got.SizeBytes != 1024 {
		t.Fatalf("artifact projection lost fields: %+v", got)
	}

	// Nil artifact: census still recorded, output metrics stay zero/empty.
	empty := BuildRenderAttemptAnalytics("attempt-2", plan, nil)
	if empty.AttemptID != "attempt-2" || empty.Content.Phrases != 1 || empty.SHA256 != "" || empty.RenderMS != 0 {
		t.Fatalf("nil-artifact record = %+v, want census + empty output", empty)
	}
}

// TestBuildRenderAttemptAnalyticsDeterministic pins re-record determinism: two
// builds over the same inputs produce identical records.
func TestBuildRenderAttemptAnalyticsDeterministic(t *testing.T) {
	plan := capoverlay.OverlayPlan{PlanID: "p", Items: []capoverlay.OverlayItem{
		{ID: "l", TemplateID: "LIGHT_LEAK"},
	}}
	art := &RenderArtifact{SHA256: "x", RenderMS: 1}
	a := BuildRenderAttemptAnalytics("a", plan, art)
	b := BuildRenderAttemptAnalytics("a", plan, art)
	if a != b {
		t.Fatalf("builder is not deterministic: %+v vs %+v", a, b)
	}
}

// fakeAttemptRecorder captures recorded attempts for the enqueuer test.
type fakeAttemptRecorder struct {
	recorded []RenderAttemptAnalytics
	err      error
}

func (f *fakeAttemptRecorder) RecordAttempt(_ context.Context, a RenderAttemptAnalytics) error {
	if f.err != nil {
		return f.err
	}
	f.recorded = append(f.recorded, a)
	return nil
}
