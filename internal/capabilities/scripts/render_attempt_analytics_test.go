package scriptgeneration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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
	got := BuildRenderAttemptAnalyticsWithWait("attempt-1", plan, artifact, RenderCompletionMetrics{
		CompletionWait: 2*time.Second + 100*time.Millisecond,
		PollingSleep:   2 * time.Second,
		PollInterval:   2 * time.Second,
		PollCount:      2,
	})

	if got.AttemptID != "attempt-1" || got.JobID != "plan-1" {
		t.Fatalf("identity = %q/%q, want attempt-1/plan-1", got.AttemptID, got.JobID)
	}
	if got.Content.Phrases != 1 || got.Content.Words != 1 || got.Content.Images != 1 || got.Content.Leaks != 1 {
		t.Fatalf("content = %+v, want one of each", got.Content)
	}
	if got.SHA256 != "abc" || got.RenderMS != 900 || got.EncodeMS != 300 ||
		got.CompletionWaitMS != 2100 || got.PollingSleepMS != 2000 || got.PollingIntervalMS != 2000 || got.PollCount != 2 ||
		got.DriveFileID != "file-1" || got.DriveLink != "https://drive.google.com/file/d/file-1/view" ||
		got.Width != 1280 || got.Height != 720 || got.FrameCount != 150 ||
		got.DurationUS != 5_000_000 || got.SizeBytes != 1024 {
		t.Fatalf("artifact projection lost fields: %+v", got)
	}

	// Nil artifact: census still recorded, output metrics stay zero/empty.
	empty := BuildRenderAttemptAnalytics("attempt-2", plan, nil)
	if empty.AttemptID != "attempt-2" || empty.Content.Phrases != 1 || empty.SHA256 != "" || empty.RenderMS != 0 || empty.CompletionWaitMS != 0 {
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

// TestBuildRenderAttemptAnalyticsPerOverlayProjectsItemIdentityAndPhases pins
// the per-overlay tracing contract: the builder preserves item_id when the
// plan carries exactly one item (the production separate-item path), copies
// every worker-reported phase and profile field verbatim, and marshals the
// bounded Chronon telemetry + metrics map without re-derivation.
func TestBuildRenderAttemptAnalyticsPerOverlayProjectsItemIdentityAndPhases(t *testing.T) {
	plan := capoverlay.OverlayPlan{PlanID: "plan-1", Items: []capoverlay.OverlayItem{
		{ID: "phrase-hello", TemplateID: "IMPORTANT_PHRASE"},
	}}
	telemetry := json.RawMessage(`{"job":{"plan_compile_ms":4.1},"summary":{"p50_frame_ms":12.3}}`)
	artifact := &RenderArtifact{
		SHA256:         "abc",
		Width:          1920,
		Height:         1080,
		FPSNum:         30,
		FPSDen:         1,
		FrameCount:     135,
		DurationUS:     4500000,
		SizeBytes:      102400,
		RenderMS:       3400,
		EncodeMS:       900,
		MaterializeMS:  420,
		PlanMS:         12,
		ProbeMS:        110,
		HashMS:         80,
		UploadMS:       80,
		DrivePublishMS: 650,
		Backend:        "vulkan",
		ChrononVersion: "chronon-0.9.1",
		ProfileID:      "velox-h264-1080p30-v1",
		Codec:          "h264",
		CodecProfile:   "high",
		Container:      "mp4",
		PixelFormat:    "yuv420p",
		DriveFileID:    "drive-1",
		DriveLink:      "https://drive.google.com/file/d/drive-1/view",
		Metrics:        map[string]float64{"chronon_job_gpu_upload_bytes": 1048576, "gpu_lane_wait_ms": 820},
		ChrononTelemetry: telemetry,
		ChrononTimingStorageKey:  "chronon/timing/abc.json",
		ChrononTimingURL:         "https://store/chronon/timing/abc.json",
		ChrononTimingSHA256:      "sha-timing",
		ChrononTimingSizeBytes:   5120,
		ChrononTimingContentType: "application/json",
	}
	got := BuildRenderAttemptAnalytics("attempt-1", plan, artifact)

	if got.ItemID != "phrase-hello" {
		t.Fatalf("item_id = %q, want phrase-hello (single-item plan)", got.ItemID)
	}
	if got.Backend != "vulkan" || got.ChrononVersion != "chronon-0.9.1" || got.ProfileID != "velox-h264-1080p30-v1" ||
		got.Codec != "h264" || got.CodecProfile != "high" || got.Container != "mp4" || got.PixelFormat != "yuv420p" {
		t.Fatalf("profile not projected verbatim: backend=%q chronon_version=%q profile=%q codec=%q/%q container=%q pixfmt=%q", got.Backend, got.ChrononVersion, got.ProfileID, got.Codec, got.CodecProfile, got.Container, got.PixelFormat)
	}
	if got.MaterializeMS != 420 || got.PlanMS != 12 || got.ProbeMS != 110 || got.HashMS != 80 || got.UploadMS != 80 || got.DrivePublishMS != 650 {
		t.Fatalf("per-phase durations not projected: mat=%d plan=%d probe=%d hash=%d upload=%d drive=%d", got.MaterializeMS, got.PlanMS, got.ProbeMS, got.HashMS, got.UploadMS, got.DrivePublishMS)
	}
	if got.FrameCount != 135 || got.Width != 1920 || got.Height != 1080 || got.FPSNum != 30 || got.DurationUS != 4500000 {
		t.Fatalf("output facts not projected: %+v", got)
	}
	if got.ChrononTelemetryJSON != string(telemetry) {
		t.Fatalf("chronon_telemetry not relayed verbatim: %q vs %q", got.ChrononTelemetryJSON, string(telemetry))
	}
	var metrics map[string]float64
	if err := json.Unmarshal([]byte(got.MetricsJSON), &metrics); err != nil {
		t.Fatalf("metrics_json unmarshal: %v (was %q)", err, got.MetricsJSON)
	}
	if metrics["chronon_job_gpu_upload_bytes"] != 1048576 || metrics["gpu_lane_wait_ms"] != 820 {
		t.Fatalf("metrics not projected verbatim: %v", metrics)
	}
	if got.ChrononTimingStorageKey != "chronon/timing/abc.json" || got.ChrononTimingSHA256 != "sha-timing" || got.ChrononTimingSizeBytes != 5120 {
		t.Fatalf("timing sidecar refs not projected: %+v", got)
	}
	// Multi-item (full-timeline legacy) path keeps item_id empty: the row
	// is not attributable to one overlay.
	multi := capoverlay.OverlayPlan{PlanID: "plan-multi", Items: []capoverlay.OverlayItem{
		{ID: "a", TemplateID: "IMPORTANT_PHRASE"},
		{ID: "b", TemplateID: "IMPORTANT_WORD"},
	}}
	if got2 := BuildRenderAttemptAnalytics("attempt-2", multi, artifact); got2.ItemID != "" {
		t.Fatalf("multi-item plan item_id = %q, want empty", got2.ItemID)
	}
	// Nil telemetry/metrics remain empty keys — no fabricated JSON.
	if got3 := BuildRenderAttemptAnalytics("attempt-3", plan, &RenderArtifact{SHA256: "x"}); got3.ChrononTelemetryJSON != "" || got3.MetricsJSON != "" {
		t.Fatalf("empty telemetry must stay empty: %+v", got3)
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
