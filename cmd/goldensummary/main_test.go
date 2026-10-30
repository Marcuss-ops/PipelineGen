package main

import (
	"database/sql"
	"testing"

	rendermetrics "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/rendermetrics"
)

func TestBatchAttributionSeparatesObservedIntervalsAndMissingMetrics(t *testing.T) {
	timestamp := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	start := timestamp("2026-10-29T12:00:00Z")
	one := timestamp("2026-10-29T12:00:01Z")
	three := timestamp("2026-10-29T12:00:03Z")
	five := timestamp("2026-10-29T12:00:05Z")
	a, err := attributeBatch(start, one, one, three, five, start, five, `{"gpu_lane_wait_ms":0,"render_ms":1250.5,"chronon_job_gpu_nvenc_frames":285,"chronon_summary_p95_frame_ms":53.9}`)
	if err != nil {
		t.Fatal(err)
	}
	if a.SubmitMS == nil || *a.SubmitMS != 1000 || a.QueueResidenceMS == nil || *a.QueueResidenceMS != 2000 || a.QueueServiceMS == nil || *a.QueueServiceMS != 2000 || a.CompletionWaitMS == nil || *a.CompletionWaitMS != 5000 {
		t.Fatalf("incorrect intervals: %+v", a)
	}
	if a.GPULaneWaitMS == nil || *a.GPULaneWaitMS != 0 || a.WorkerMetrics["render_ms"] != 1250.5 {
		t.Fatalf("worker measurements lost: %+v", a)
	}
	if a.ChrononMetrics["chronon_job_gpu_nvenc_frames"] != 285 || a.ChrononMetrics["chronon_summary_p95_frame_ms"] != 53.9 {
		t.Fatalf("frame cost metrics lost: %+v", a)
	}
	if _, ok := a.ChrononMetrics["chronon_job_gpu_software_encode_frames"]; ok {
		t.Fatal("missing frame metric must not be fabricated")
	}
	if _, ok := a.WorkerMetrics["plan_ms"]; ok {
		t.Fatal("missing worker phase must not be fabricated")
	}
	b, err := attributeBatch(three, one, sql.NullString{}, three, five, start, five, "")
	if err != nil || b.SubmitMS != nil || b.QueueResidenceMS != nil || b.GPULaneWaitMS != nil {
		t.Fatalf("missing or unordered intervals must stay unknown: %+v, %v", b, err)
	}
	if _, err := attributeBatch(start, one, one, three, five, start, five, "{"); err == nil {
		t.Fatal("malformed stored metrics must not be silently treated as absent")
	}
}

func TestOverlayRenderBatchesChecksWorkerItemCount(t *testing.T) {
	result := map[string]any{
		"overlay_render": map[string]any{
			"batches": []any{map[string]any{
				"batch_id": "batch-1", "item_ids": []any{"a", "b"},
				"artifact": map[string]any{"metrics": map[string]any{
					"overlay_item_count": float64(2), "overlay_render_batch": float64(1),
				}},
			}},
		},
	}
	batches := overlayRenderBatches(result, nil)
	if len(batches) != 1 || !batches[0].WorkerCountKnown || !batches[0].WorkerCountMatches || !batches[0].WorkerMarkedBatch {
		t.Fatalf("matching worker cardinality was not certified: %+v", batches)
	}

	result["overlay_render"].(map[string]any)["batches"].([]any)[0].(map[string]any)["artifact"].(map[string]any)["metrics"].(map[string]any)["overlay_item_count"] = float64(1)
	batches = overlayRenderBatches(result, []batchAttemptSample{{BatchID: "batch-1", Attempt: rendermetrics.ItemSample{ItemID: "batch:batch-1", RenderMS: 1200}}})
	if len(batches) != 1 || !batches[0].WorkerCountKnown || batches[0].WorkerCountMatches || batches[0].RenderMS != 1200 {
		t.Fatalf("worker cardinality mismatch or DB timing was hidden: %+v", batches)
	}
}

func TestOverlayRenderBatchesFallsBackToDurableAnalyticsMembership(t *testing.T) {
	result := map[string]any{"overlay_render": map[string]any{"batches": []any{map[string]any{"batch_id": "batch-2"}}}}
	samples := []batchAttemptSample{{
		BatchID: "batch-2", ItemIDs: []string{"phrase-a", "image-b"}, StartUS: 500_000, EndUS: 2_500_000,
		Attempt: rendermetrics.ItemSample{ItemID: "batch:batch-2", RenderMS: 1000},
	}}
	batches := overlayRenderBatches(result, samples)
	if len(batches) != 1 || len(batches[0].ItemIDs) != 2 || batches[0].StartUS != 500_000 || batches[0].EndUS != 2_500_000 || batches[0].RenderMS != 1000 {
		t.Fatalf("durable batch analytics were not projected: %+v", batches)
	}
}
