package cliprender

import "testing"

// TestRenderedResultHasNoDuplicateFacts pins CR-21: one fact, one location.
// The source lives only under materialization.source, and the render wall only
// in the canonical metrics_v2 report.
func TestRenderedResultHasNoDuplicateFacts(t *testing.T) {
	prepared := &Prepared{Source: &MaterializedAsset{
		AssetID: "source-1", LocalPath: "/tmp/source.mp4", SHA256: "abc", SizeBytes: 10,
	}}
	metrics := NewRenderMetricsV2()
	metrics.RenderWallMS = 42
	outcome := &RenderOutcome{Metrics: metrics}

	result := renderedResult(nil, nil, prepared, ClipRenderPlanV1{}, nil, outcome, nil)

	if _, ok := result["source"]; ok {
		t.Fatal("result.source is a duplicate of materialization.source")
	}
	materialization, ok := result["materialization"].(map[string]any)
	if !ok {
		t.Fatalf("materialization block = %T", result["materialization"])
	}
	if _, ok := materialization["source"]; !ok {
		t.Fatal("materialization.source is the canonical source block and must exist")
	}
	renderBlock, ok := result["render"].(map[string]any)
	if !ok {
		t.Fatalf("render block = %T", result["render"])
	}
	if _, ok := renderBlock["render_wall_ms"]; ok {
		t.Fatal("render.render_wall_ms duplicates metrics_v2.render_wall_ms")
	}
	if renderBlock["metrics_v2"] == nil {
		t.Fatal("metrics_v2 is the canonical render report and must exist")
	}
}
