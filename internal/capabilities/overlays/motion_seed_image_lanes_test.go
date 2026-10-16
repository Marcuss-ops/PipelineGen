package overlays

import (
	"testing"
)

// An explicit MotionSeed must rotate the image lanes, not just the phrase
// lanes. Regression: the planner selected image/caption motions from PlanID,
// so two renders sharing the plan identity but carrying different motion
// seeds drew identical image entrances.
func TestExplicitMotionSeedRotatesImageLanes(t *testing.T) {
	base := func(seed string) PlanInput {
		return PlanInput{
			PlanID: "seed-image-lanes", VideoID: "v1",
			Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
			MotionSeed: seed,
			ImageMotions: []string{
				"image_fade_reveal", "image_focus_reveal", "image_scale_reveal",
				"image_slide_left_reveal", "image_slide_right_reveal",
			},
			Scenes: []SceneInput{{ID: "scene-1", Images: []ImageCandidate{
				{AssetID: "img1", SHA256: "a", MediaType: "image", StartMs: 100, EndMs: 4000, Score: 1},
				{AssetID: "img2", SHA256: "b", MediaType: "image", StartMs: 4100, EndMs: 8000, Score: 0.9},
				{AssetID: "img3", SHA256: "c", MediaType: "image", StartMs: 8100, EndMs: 12000, Score: 0.8},
			}}},
		}
	}
	motions := func(seed string) []string {
		plan, err := BuildPlan(base(seed), PlannerConfig{MaxImages: 10})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, item := range plan.Items {
			if item.Kind == "image" {
				out = append(out, item.MotionID)
			}
		}
		return out
	}
	first := motions("seed-alpha")
	if len(first) != 3 {
		t.Fatalf("image motions = %v, want 3 admitted images", first)
	}
	again := motions("seed-alpha")
	for i := range first {
		if again[i] != first[i] {
			t.Fatalf("same seed gave different motions: %v vs %v", first, again)
		}
	}
	seen := map[string]bool{joinMotions(first): true}
	for _, seed := range []string{"seed-beta", "seed-gamma", "seed-delta"} {
		seen[joinMotions(motions(seed))] = true
	}
	if len(seen) < 2 {
		t.Fatalf("explicit motion seeds did not rotate the image lane: %v", seen)
	}
}

func joinMotions(motions []string) string {
	out := ""
	for i, motion := range motions {
		if i > 0 {
			out += "\x00"
		}
		out += motion
	}
	return out
}
