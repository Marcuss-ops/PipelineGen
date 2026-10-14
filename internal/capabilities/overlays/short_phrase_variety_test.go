package overlays

import (
	"fmt"
	"testing"
)

// TestShortPhraseFamilyRunStaysAnimated pins the production policy: every
// short-phrase overlay is routed to a certified entrance motion.
func TestShortPhraseFamilyRunStaysAnimated(t *testing.T) {
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
		// Short phrases deliberately have no runtime motion family.
		AnimationCounts: map[string]int{"short_important_phrase": phraseCount},
		Scenes:          []SceneInput{{ID: "scene-1", Phrases: annotations}},
	}
	cfg := AllCandidatesPlannerConfig(input.Scenes)
	cfg.MaxPhrases = phraseCount
	plan, err := BuildPlan(input, cfg)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	planned := 0
	for _, item := range plan.Items {
		if item.Kind != "text_phrase" {
			continue
		}
		planned++
		if item.MotionID == "" {
			t.Fatalf("short-phrase overlay %q must be animated, got no motion", item.ID)
		}
	}
	if planned < 3 {
		t.Fatalf("planner reduced the run to %d phrase overlays; the variety check needs a real run", planned)
	}
}
