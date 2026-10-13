package overlays

import (
	"fmt"
	"testing"
)

// TestShortPhraseFamilyRunRotatesDistinctRecipes pins the production path the
// 2026-10-13 review complained about: a run that names the
// short_phrase_product_v1 family used to rotate the same four documentary-clean
// entrances for every short phrase, so a handful of phrase overlays repeated
// before the lane was exhausted. It must now rotate a distinct surviving
// exact-cardinality recipe for every planned phrase, and never fall back to a
// retired one.
func TestShortPhraseFamilyRunRotatesDistinctRecipes(t *testing.T) {
	const phraseCount = 10
	annotations := make([]TimedAnnotation, phraseCount)
	for i := range annotations {
		start := int64(i * 2_200)
		annotations[i] = TimedAnnotation{
			Text:       fmt.Sprintf("word word p%02d", i), // three words: word group 3
			StartMs:    start,
			EndMs:      start + 2_000,
			StartUS:    start * 1000,
			DurationUS: 2_000_000,
			Score:      1 - float64(i)*0.01,
		}
	}
	input := PlanInput{
		PlanID: "family-variety", VideoID: "family-variety", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		PhraseMotionFamily: "short_phrase_product_v1",
		// Mirrors the production payload: a short-phrase lane count is an item
		// budget and must not narrow the recipe pool this test measures.
		AnimationCounts: map[string]int{"short_important_phrase": phraseCount},
		Scenes:          []SceneInput{{ID: "scene-1", Phrases: annotations}},
	}
	cfg := AllCandidatesPlannerConfig(input.Scenes)
	cfg.MaxPhrases = phraseCount
	plan, err := BuildPlan(input, cfg)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	seen := map[string]bool{}
	planned := 0
	for _, item := range plan.Items {
		if item.Kind != "text_phrase" {
			continue
		}
		planned++
		if IsRemovedGeneratedPhraseMotion(item.MotionID) {
			t.Fatalf("run assigned the retired recipe %q", item.MotionID)
		}
		if !IsShortPhraseRecipe(item.MotionID) {
			t.Fatalf("family run assigned %q, want an exact short-phrase recipe", item.MotionID)
		}
		if seen[item.MotionID] {
			t.Fatalf("run repeated %q after %d of its planned phrases; word group 3 holds %d recipes",
				item.MotionID, planned, len(ShortPhraseMotionsForWordCount(3)))
		}
		seen[item.MotionID] = true
	}
	if planned < 3 {
		t.Fatalf("planner reduced the run to %d phrase overlays; the variety check needs a real run", planned)
	}
	if len(seen) != planned {
		t.Fatalf("run used %d distinct recipes for %d phrases, want one recipe per phrase", len(seen), planned)
	}
}
