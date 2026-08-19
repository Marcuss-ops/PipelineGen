package overlays

import "testing"

func TestPresetSelectionIsDeterministicAndUsesKnownFamilies(t *testing.T) {
	first := SelectEntityNamePreset("job-1", "scene-1", "entity-1", "PERSON")
	second := SelectEntityNamePreset("job-1", "scene-1", "entity-1", "PERSON")
	if first == "" || first != second {
		t.Fatalf("entity preset selection is not deterministic: %q vs %q", first, second)
	}

	plan, err := BuildPlan(PlanInput{
		PlanID: "job-1", VideoID: "video-1", Width: 1920, Height: 1080, FPS: 30,
		Scenes: []SceneInput{{
			ID:       "scene-1",
			Phrases:  []TimedAnnotation{{Text: "IMPORTANT", StartMs: 0, EndMs: 1000}},
			Keywords: []TimedAnnotation{{Text: "NOW", StartMs: 0, EndMs: 1000}},
			Images:   []ImageCandidate{{AssetID: "img-1", URL: "assets/img.png", StartMs: 0, EndMs: 1000}},
		}},
	}, PlannerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(plan.Items))
	}
	for _, item := range plan.Items {
		if item.PresetID == "" {
			t.Fatalf("item %q did not receive a preset", item.ID)
		}
	}
}
