package overlays

import "testing"

// TestBuildPlanSplitsHeavyPhrasesIntoTheProminentLane pins goal E2's motion
// mapping: when the caller declares a heavy-phrase priority, the phrases at or
// above it take a certified VISIBLE entrance, the phrases below it keep the
// calm rotation bit-for-bit, and every emitted motion stays inside the
// callable phrase catalog.
func TestBuildPlanSplitsHeavyPhrasesIntoTheProminentLane(t *testing.T) {
	heavy := CertifiedHeavyPhraseMotions()
	if len(heavy) < 2 {
		t.Fatalf("heavy entrance pool has %d motions, want at least 2 to rotate", len(heavy))
	}
	for _, id := range heavy {
		if !containsString(CertifiedPhraseMotions(), id) {
			t.Fatalf("heavy entrance %q is not a certified phrase motion", id)
		}
	}

	phrases := []TimedAnnotation{
		{Text: "the heaviest grounded phrase", StartMs: 0, EndMs: 1000, StartUS: 0, DurationUS: 1_000_000, Score: 0.97},
		{Text: "a light grounded phrase", StartMs: 1200, EndMs: 2200, StartUS: 1_200_000, DurationUS: 1_000_000, Score: 0.10},
	}
	input := PlanInput{
		PlanID: "heavy", VideoID: "video-heavy", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		HeavyPhrasePriority: 0.85,
		Scenes:              []SceneInput{{ID: "scene-1", Phrases: phrases}},
	}
	byText := func(plan PlanInput) map[string]OverlayItem {
		t.Helper()
		built, err := BuildPlan(plan, AllCandidatesPlannerConfig(plan.Scenes))
		if err != nil {
			t.Fatalf("BuildPlan: %v", err)
		}
		items := map[string]OverlayItem{}
		for _, item := range built.Items {
			if item.Kind == "text_phrase" {
				items[item.Text] = item
			}
		}
		return items
	}

	split := byText(input)
	heavyItem, ok := split["the heaviest grounded phrase"]
	if !ok {
		t.Fatal("the heavy phrase did not reach the plan")
	}
	if !containsString(heavy, heavyItem.MotionID) {
		t.Fatalf("heavy phrase motion %q is outside the prominent entrance pool %v", heavyItem.MotionID, heavy)
	}
	lightItem, ok := split["a light grounded phrase"]
	if !ok {
		t.Fatal("the light phrase did not reach the plan")
	}
	if !containsString(CertifiedPhraseMotions(), lightItem.MotionID) {
		t.Fatalf("light phrase motion %q is not a certified phrase motion", lightItem.MotionID)
	}
	if lightItem.MotionID == heavyItem.MotionID {
		t.Fatalf("heavy and light phrases share motion %q", lightItem.MotionID)
	}

	// With the split disabled the plan must be the previous single rotation:
	// the calm lane keeps its ordinals, so the non-heavy phrase's motion is
	// identical and the heavy phrase falls back onto the calm rotation too.
	without := input
	without.HeavyPhrasePriority = 0
	calm := byText(without)
	if calm["a light grounded phrase"].MotionID != lightItem.MotionID {
		t.Fatalf("the calm rotation changed for non-heavy phrases: %q vs %q",
			calm["a light grounded phrase"].MotionID, lightItem.MotionID)
	}
	if calm["the heaviest grounded phrase"].MotionID == heavyItem.MotionID {
		t.Fatalf("disabling the split did not restore the calm motion for the heavy phrase")
	}
}
