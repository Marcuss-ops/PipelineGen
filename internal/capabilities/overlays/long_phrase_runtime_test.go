package overlays

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuildPlanAutomaticLongPhrasesUseLongRotation(t *testing.T) {
	phrases := make([]TimedAnnotation, 15)
	for i := range phrases {
		phrases[i] = TimedAnnotation{
			Text:    fmt.Sprintf("una frase importante lunga con molte parole diverse per il video numero %02d", i),
			StartMs: int64(i * 2200), EndMs: int64(i*2200 + 2000),
			StartUS: int64(i*2200) * 1000, DurationUS: 2_000_000,
			Score: 1 - float64(i)*.01,
		}
		if len(strings.Fields(phrases[i].Text)) < 6 {
			t.Fatalf("test phrase %d is not long", i)
		}
	}
	input := PlanInput{
		PlanID: "automatic-long-phrase", VideoID: "video-a", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: []SceneInput{{ID: "scene", Phrases: phrases}},
	}
	config := AllCandidatesPlannerConfig(input.Scenes)
	config.MaxPhrases = len(phrases)
	config.RunLevelPhraseOverlayLimit = len(phrases)

	first, err := BuildPlan(input, config)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	second, err := BuildPlan(input, config)
	if err != nil {
		t.Fatalf("second BuildPlan: %v", err)
	}
	seen := make(map[string]bool)
	phraseIndex := 0
	for _, item := range first.Items {
		if item.Kind != "text_phrase" {
			continue
		}
		if !containsString(longPhraseMotionCandidates, item.MotionID) {
			t.Fatalf("automatic long phrase selected %q outside the long pool", item.MotionID)
		}
		if seen[item.MotionID] {
			t.Fatalf("automatic long phrase repeated %q before pool exhaustion", item.MotionID)
		}
		seen[item.MotionID] = true
		if second.Items[phraseIndex].MotionID != item.MotionID {
			t.Fatalf("selection is not deterministic: %q vs %q", item.MotionID, second.Items[phraseIndex].MotionID)
		}
		phraseIndex++
	}
	if phraseIndex != len(phrases) || len(seen) != len(phrases) {
		t.Fatalf("planned %d phrases across %d motions, want %d distinct", phraseIndex, len(seen), len(phrases))
	}
	if len(seen) <= len(documentaryCleanPhraseMotionCandidates) {
		t.Fatalf("automatic long phrases still use the small documentary pool: %d motions", len(seen))
	}
}

func TestAutomaticLongPhraseMotionSeedIncludesVideoID(t *testing.T) {
	makePlan := func(videoID string) OverlayPlan {
		input := PlanInput{
			PlanID: "same-plan", VideoID: videoID, Width: 1280, Height: 720, FPSNum: 24, FPSDen: 1,
			Scenes: []SceneInput{{ID: "scene", Phrases: []TimedAnnotation{{
				Text: "questa è una frase importante lunga per testare l'identità del video", StartMs: 0, EndMs: 2000,
			}}}},
		}
		config := AllCandidatesPlannerConfig(input.Scenes)
		plan, err := BuildPlan(input, config)
		if err != nil {
			t.Fatalf("BuildPlan(%s): %v", videoID, err)
		}
		return plan
	}
	first, second := makePlan("video-a"), makePlan("video-b")
	if first.Items[0].MotionID == second.Items[0].MotionID {
		t.Fatalf("different videos with the same plan id started on the same long motion %q", first.Items[0].MotionID)
	}
}
