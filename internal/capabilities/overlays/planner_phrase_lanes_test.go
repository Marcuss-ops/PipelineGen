package overlays

import (
	"strings"
	"testing"
)

// The internal run-level budget must classify phrases by the explicit lane
// policy when short/long caps are configured: short candidates overlapping a
// long phrase survive (the short lane is admitted first), while the default
// long-first heuristic keeps the long phrase and drops the overlapping
// shorts before the compile-level lane selector could ever see them.
func TestBuildPlanLaneCapsAdmitShortPhrasesBeforeLongOverlap(t *testing.T) {
	scenes := []SceneInput{{ID: "scene-1", Phrases: []TimedAnnotation{
		{Text: "This is a long grounded headline phrase for the scene", StartMs: 100, EndMs: 900, StartUS: 100_000, DurationUS: 800_000, Score: 0.9},
		{Text: "short callout one", StartMs: 200, EndMs: 400, StartUS: 200_000, DurationUS: 200_000, Score: 0.9},
		{Text: "short callout two", StartMs: 500, EndMs: 700, StartUS: 500_000, DurationUS: 200_000, Score: 0.9},
	}}}
	build := func(lanes bool) map[string]bool {
		config := AllCandidatesPlannerConfig(scenes)
		config.RunLevelPhraseOverlayLimit = 10
		if lanes {
			config.RunLevelShortPhraseLimit = 10
			config.RunLevelLongPhraseLimit = 10
		}
		plan, err := BuildPlan(PlanInput{
			PlanID: "phrase-lanes", VideoID: "video-phrase-lanes",
			Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1, Scenes: scenes,
		}, config)
		if err != nil {
			t.Fatal(err)
		}
		kept := map[string]bool{}
		for _, item := range plan.Items {
			if item.Kind == "text_phrase" {
				kept[strings.ToLower(strings.Join(strings.Fields(item.Text), " "))] = true
			}
		}
		return kept
	}
	defaultKept := build(false)
	if !defaultKept["this is a long grounded headline phrase for the scene"] || defaultKept["short callout one"] || defaultKept["short callout two"] {
		t.Fatalf("default budget kept %v, want only the long phrase", defaultKept)
	}
	laneKept := build(true)
	if !laneKept["short callout one"] || !laneKept["short callout two"] {
		t.Fatalf("lane budget kept %v, want both short phrases admitted first", laneKept)
	}
	if laneKept["this is a long grounded headline phrase for the scene"] {
		t.Fatalf("lane budget kept %v, want the long phrase blocked by the admitted shorts", laneKept)
	}
}

// Lane caps are opt-in: a zero short or long cap keeps the certified default
// single-budget behavior instead of selecting an empty lane.
func TestBuildPlanLaneCapsRequireBothLanesPositive(t *testing.T) {
	scenes := []SceneInput{{ID: "scene-1", Phrases: []TimedAnnotation{
		{Text: "short callout one", StartMs: 200, EndMs: 400, StartUS: 200_000, DurationUS: 200_000, Score: 0.9},
	}}}
	config := AllCandidatesPlannerConfig(scenes)
	config.RunLevelPhraseOverlayLimit = 10
	config.RunLevelShortPhraseLimit = 10
	plan, err := BuildPlan(PlanInput{
		PlanID: "phrase-lanes-partial", VideoID: "video-phrase-lanes-partial",
		Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1, Scenes: scenes,
	}, config)
	if err != nil {
		t.Fatal(err)
	}
	phrases := 0
	for _, item := range plan.Items {
		if item.Kind == "text_phrase" {
			phrases++
		}
	}
	if phrases != 1 {
		t.Fatalf("partial lane caps kept %d phrases, want the default budget to keep the single candidate", phrases)
	}
}
