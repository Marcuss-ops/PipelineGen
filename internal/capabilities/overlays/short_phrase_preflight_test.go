package overlays

import (
	"fmt"
	"testing"
)

// shortPhraseTestInput builds a single-scene run of short phrases (three words
// each, two seconds long), the shape the short-phrase lane actually receives.
func shortPhraseTestInput(phraseCount int, pool []string) (PlanInput, PlannerConfig) {
	annotations := make([]TimedAnnotation, phraseCount)
	for i := range annotations {
		start := int64(i * 2_200)
		annotations[i] = TimedAnnotation{
			Text:       fmt.Sprintf("word word p%02d", i),
			StartMs:    start,
			EndMs:      start + 2_000,
			StartUS:    start * 1000,
			DurationUS: 2_000_000,
			Score:      1 - float64(i)*0.01,
		}
	}
	input := PlanInput{
		PlanID: "short-phrase-preflight", VideoID: "short-phrase-preflight",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		AnimationCounts: map[string]int{"short_important_phrase": phraseCount},
		Scenes:          []SceneInput{{ID: "scene-1", Phrases: annotations}},
		PhraseMotions:   pool,
	}
	cfg := AllCandidatesPlannerConfig(input.Scenes)
	cfg.MaxPhrases = phraseCount
	return input, cfg
}

// TestShortPhrasePreflightPlanIsRenderable fails in milliseconds, before any
// render, when a planned short phrase names a motion the registry cannot
// render or lacks a settled reading window after its entrance.
func TestShortPhrasePreflightPlanIsRenderable(t *testing.T) {
	input, cfg := shortPhraseTestInput(20, nil)
	plan, err := BuildPlan(input, cfg)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	certified := make(map[string]bool)
	for _, id := range CertifiedPhraseMotions() {
		certified[id] = true
	}
	for wordCount := 1; wordCount <= 5; wordCount++ {
		for _, id := range ShortPhraseMotionsForWordCount(wordCount) {
			certified[id] = true
		}
	}
	const wantEntranceFrames = 16 // one third of a 2 s clip at 24 fps
	planned := 0
	for _, item := range plan.Items {
		if item.Kind != "text_phrase" || EditorialSectionForItem(item) != EditorialSectionShortPhrase {
			continue
		}
		planned++
		if !certified[item.MotionID] || IsRemovedGeneratedPhraseMotion(item.MotionID) {
			t.Errorf("short phrase %q planned non-renderable motion %q", item.ID, item.MotionID)
		}
		got, _ := item.MotionParams["enter_frames"].(int)
		if got != wantEntranceFrames {
			t.Errorf("short phrase %q enter_frames = %d, want %d so two thirds remain settled", item.ID, got, wantEntranceFrames)
		}
	}
	if planned == 0 {
		t.Fatalf("planner produced no short-phrase items; the check needs a real run")
	}
}

// TestShortPhraseRotationDistributesDistinctMotions asserts the declared
// no-repeat policy: with N pool entries and 20 phrases, the run uses
// min(20, N) distinct motions.
func TestShortPhraseRotationDistributesDistinctMotions(t *testing.T) {
	// The short-phrase lane only ever draws from the renderer-visible set, so
	// pools are sized from that set; a caller pool is intersected with it.
	visible := ShortPhraseVisibleMotionCandidates()
	for _, poolSize := range []int{5, 15} {
		if len(visible) < poolSize {
			t.Fatalf("only %d visible short-phrase motions, need %d", len(visible), poolSize)
		}
		pool := visible[:poolSize]
		input, cfg := shortPhraseTestInput(20, pool)
		plan, err := BuildPlan(input, cfg)
		if err != nil {
			t.Fatalf("pool %d: BuildPlan: %v", poolSize, err)
		}
		distinct := make(map[string]bool)
		admitted := 0
		for _, item := range plan.Items {
			if item.Kind == "text_phrase" && EditorialSectionForItem(item) == EditorialSectionShortPhrase {
				admitted++
				distinct[item.MotionID] = true
			}
		}
		// The visual budget admits at most MaxPhraseOverlaysPerRun phrase overlays,
		// so the phrases that reach the rotation are capped there, not at 20.
		if wantAdmitted := min(20, MaxPhraseOverlaysPerRun); admitted != wantAdmitted {
			t.Fatalf("pool %d: admitted %d short phrases, want %d", poolSize, admitted, wantAdmitted)
		}
		want := min(admitted, poolSize)
		if len(distinct) != want {
			t.Errorf("pool %d: distinct short-phrase motions = %d, want %d", poolSize, len(distinct), want)
		}
	}
}
