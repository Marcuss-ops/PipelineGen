package overlays

import (
	"fmt"
	"testing"
)

// TestSelectEntityImagePresetUsesOnlyRenderSafeCandidates pins the generated
// entity-image preset contract: every selection comes from the render-safe
// candidate set (never modern_rounded_pop, whose native mask path is not
// implemented), stays bit-identical for a retry of the same job, and still
// varies across identities so one run does not render five identical motions.
func TestSelectEntityImagePresetUsesOnlyRenderSafeCandidates(t *testing.T) {
	safe := make(map[string]bool, len(imagePresetCandidates))
	for _, id := range imagePresetCandidates {
		safe[id] = true
	}
	if safe["modern_rounded_pop"] {
		t.Fatal("modern_rounded_pop must not be selectable for generated entity images")
	}

	variants := map[string]bool{}
	for i := 0; i < 64; i++ {
		jobID := fmt.Sprintf("job-%d", i)
		itemID := fmt.Sprintf("overlay-scene-0-entity-%d", i)
		preset := SelectEntityImagePreset(jobID, "scene-0", itemID)
		if !safe[preset] {
			t.Fatalf("selected preset %q is not in the render-safe image candidate set", preset)
		}
		// A retry of the same job must resolve to the same preset.
		if again := SelectEntityImagePreset(jobID, "scene-0", itemID); again != preset {
			t.Fatalf("preset selection is not deterministic: %q vs %q", preset, again)
		}
		variants[preset] = true
	}
	if len(variants) < 2 {
		t.Fatalf("entity image preset selection never varies across identities: %v", variants)
	}
}

func TestPresetSelectionIsDeterministicAndUsesKnownFamilies(t *testing.T) {
	first := SelectEntityNamePreset("job-1", "scene-1", "entity-1", "PERSON")
	second := SelectEntityNamePreset("job-1", "scene-1", "entity-1", "PERSON")
	if first == "" || first != second {
		t.Fatalf("entity preset selection is not deterministic: %q vs %q", first, second)
	}

	plan, err := BuildPlan(PlanInput{
		PlanID: "job-1", VideoID: "video-1", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1,
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
