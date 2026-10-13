package overlays

import (
	"strings"
	"testing"
)

// TestRetiredShortPhraseRecipesAreNeverSelectable pins the 2026-10-13 review
// removals. A retired recipe must stay resolvable in the ChrononTemplate
// catalog for historical plans, but it must not appear in any pool the planner
// rotates over, in a motion family, or in a payload-supplied pool.
func TestRetiredShortPhraseRecipesAreNeverSelectable(t *testing.T) {
	if len(retiredShortPhraseRecipes) == 0 {
		t.Fatal("the retirement list is empty")
	}
	for _, id := range retiredShortPhraseRecipes {
		if !strings.HasPrefix(id, "short_phrase_") {
			t.Fatalf("retired id %q is not a short-phrase recipe", id)
		}
		if !IsRemovedGeneratedPhraseMotion(id) {
			t.Fatalf("retired recipe %q is not marked removed", id)
		}
		if containsMotion(CertifiedPhraseMotions(), id) {
			t.Errorf("retired recipe %q is still in the certified phrase pool", id)
		}
		if containsMotion(LongPhraseMotionCandidates(), id) {
			t.Errorf("retired recipe %q is still in the long-phrase pool", id)
		}
		if containsMotion(ShortPhraseMotionCandidates(), id) {
			t.Errorf("retired recipe %q is still in the short-phrase pool", id)
		}
		for wordCount := 1; wordCount <= 5; wordCount++ {
			if containsMotion(ShortPhraseMotionsForWordCount(wordCount), id) {
				t.Errorf("retired recipe %q is still in word group %d", id, wordCount)
			}
		}
		for _, family := range []string{
			"modern_product_v1", "short_phrase_product_v1", "modern_apple",
			"typewriter", "brush_v1", "text_3d_v1", "classic_apple", "documentary_clean_v1",
		} {
			if containsMotion(certifiedPhraseFamily(family), id) {
				t.Errorf("retired recipe %q is still reachable through family %q", id, family)
			}
		}
		if err := validatePhraseMotionPool([]string{id}); err == nil {
			t.Errorf("validatePhraseMotionPool accepted retired recipe %q", id)
		}
	}
}

// TestRetiredShortPhraseRecipeIsRefusedAsExplicitPolicy proves the refusal is
// not only pool-wide: a plan that names a retired motion explicitly fails
// closed instead of rendering it.
func TestRetiredShortPhraseRecipeIsRefusedAsExplicitPolicy(t *testing.T) {
	retired := retiredShortPhraseRecipes[0]
	input := PlanInput{
		PlanID: "retired-policy", VideoID: "v", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		PhraseMotions: []string{retired},
		Scenes: []SceneInput{{ID: "scene-1", Phrases: []TimedAnnotation{{
			Text: "distinct spoken phrase", StartMs: 0, EndMs: 2000, StartUS: 0, DurationUS: 2_000_000, Score: 1,
		}}}},
	}
	cfg := AllCandidatesPlannerConfig(input.Scenes)
	if _, err := BuildPlan(input, cfg); err == nil {
		t.Fatalf("BuildPlan accepted the retired recipe %q", retired)
	}
}
