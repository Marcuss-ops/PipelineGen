package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestPlanVisualWindowsUsesSemanticPhraseBlocks(t *testing.T) {
	layers, err := PlanVisualWindows(VisualWindowPlanningInput{
		SceneID: "scene-1", SegmentID: "segment-1", DurationMs: 18000,
		PhraseTimings: []VisualPhraseTiming{
			{Text: "the factory opens", StartMs: 0, EndMs: 6000},
			{Text: "robots assemble parts", StartMs: 6000, EndMs: 12000},
			{Text: "production scales", StartMs: 12000, EndMs: 18000},
		},
		Profile: scriptpkg.SegmentSemanticProfile{SegmentID: "segment-1", TextHash: "hash"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 3 {
		t.Fatalf("layers=%d, want 3", len(layers))
	}
	for i, layer := range layers {
		if layer.Slot != "primary_video" || layer.StartMs != int64(i)*6000 || layer.EndMs != int64(i+1)*6000 || layer.DurationMs != 6000 {
			t.Fatalf("layer %d = %+v", i, layer)
		}
	}
}

func TestPlanVisualWindowsFallbackCoversSceneWithUniformWindows(t *testing.T) {
	layers, err := PlanVisualWindows(VisualWindowPlanningInput{
		SceneID: "scene-2", SegmentID: "segment-2", Text: "automation changes modern factories", DurationMs: 24000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 3 {
		t.Fatalf("layers=%d, want 3", len(layers))
	}
	if layers[0].StartMs != 0 || layers[len(layers)-1].EndMs != 24000 {
		t.Fatalf("layers do not cover exact duration: %+v", layers)
	}
	for i, layer := range layers {
		if layer.DurationMs < 4000 || layer.DurationMs > 12000 {
			t.Fatalf("layer %d duration=%d outside fallback bounds", i, layer.DurationMs)
		}
		if i > 0 && layer.StartMs != layers[i-1].EndMs {
			t.Fatalf("gap before layer %d", i)
		}
	}
}

func TestPlanVisualWindowsIgnoresInvalidPhraseTimingAndFallsBack(t *testing.T) {
	layers, err := PlanVisualWindows(VisualWindowPlanningInput{
		SceneID: "scene-3", SegmentID: "segment-3", Text: "fallback scene", DurationMs: 8000,
		PhraseTimings: []VisualPhraseTiming{{Text: "invalid", StartMs: 7000, EndMs: 6000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 1 || layers[0].StartMs != 0 || layers[0].EndMs != 8000 {
		t.Fatalf("layers=%+v, want one exact fallback layer", layers)
	}
}
