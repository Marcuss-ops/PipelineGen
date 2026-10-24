package main

import (
	"testing"

	rendermetrics "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/rendermetrics"
)

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
