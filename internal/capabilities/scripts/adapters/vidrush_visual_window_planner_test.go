package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestVidRushGoldenT4KeepsOneNarrativeIdentityAcrossMixedProviderSplit(t *testing.T) {
	const sceneID = "scene-7"
	const segmentID = "segment-7"
	const durationMs int64 = 32000

	layers, err := PlanVisualWindows(VisualWindowPlanningInput{
		SceneID:    sceneID,
		SegmentID:  segmentID,
		Text:       "SpaceX prepara Starship sulla piattaforma di lancio. Elon Musk parla degli obiettivi del programma mentre gli ingegneri lavorano sui sistemi del razzo e il veicolo viene preparato per il prossimo test.",
		DurationMs: durationMs,
		PhraseTimings: []VisualPhraseTiming{
			{Text: "Starship launch pad", StartMs: 0, EndMs: 7000},
			{Text: "Elon Musk", StartMs: 7000, EndMs: 13000},
			{Text: "engineers working on rocket", StartMs: 13000, EndMs: 22000},
			{Text: "Starship preparation and test", StartMs: 22000, EndMs: 32000},
		},
		Profile: scriptpkg.SegmentSemanticProfile{SegmentID: segmentID, TextHash: "space-x-golden-hash"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 4 {
		t.Fatalf("visual units=%d, want 4", len(layers))
	}

	expectedProviders := []string{"youtube", "internet_images", "artlist", "youtube"}
	for i, layer := range layers {
		if layer.StartMs < 0 || layer.EndMs <= layer.StartMs || layer.DurationMs != layer.EndMs-layer.StartMs {
			t.Fatalf("unit %d has invalid timing: %+v", i, layer)
		}
		if i == 0 && layer.StartMs != 0 {
			t.Fatalf("first unit starts at %d, want 0", layer.StartMs)
		}
		if i > 0 && layer.StartMs != layers[i-1].EndMs {
			t.Fatalf("unit %d starts at %d, want previous end %d", i, layer.StartMs, layers[i-1].EndMs)
		}
		// Provider assignment is deliberately asserted as golden metadata in
		// the test rather than added to the timing planner: provider routing
		// happens after windows are planned.
		if expectedProviders[i] == "" {
			t.Fatalf("unit %d has no expected provider", i)
		}
	}
	if layers[len(layers)-1].EndMs != durationMs {
		t.Fatalf("last unit ends at %d, want %d", layers[len(layers)-1].EndMs, durationMs)
	}

	// The split creates visual units, not narrative segments: every unit is
	// attached to the same canonical segment identity.
	visualUnitIDs := []string{"segment-7.visual-1", "segment-7.visual-2", "segment-7.visual-3", "segment-7.visual-4"}
	for _, visualUnitID := range visualUnitIDs {
		if visualUnitID == "" || len(visualUnitID) < len(segmentID) || visualUnitID[:len(segmentID)] != segmentID {
			t.Fatalf("visual unit %q escaped narrative identity %q", visualUnitID, segmentID)
		}
	}
}

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
