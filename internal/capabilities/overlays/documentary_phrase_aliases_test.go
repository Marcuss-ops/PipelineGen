package overlays

import (
	"fmt"
	"testing"
)

func TestDocumentaryPhraseAliasesRotateAcrossAdaptiveFamilies(t *testing.T) {
	ids := make([]string, 0, len(documentaryPhraseMotionAliases))
	for id := range documentaryPhraseMotionAliases {
		ids = append(ids, id)
	}
	if len(ids) != 15 {
		t.Fatalf("documentary phrase aliases = %d, want 15", len(ids))
	}

	input := PlanInput{
		PlanID: "documentary-alias-rotation", VideoID: "garlasco",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		PhraseMotions:   ids,
		AnimationCounts: map[string]int{laneImportantPhrase: 15},
	}
	phrases := make([]TimedAnnotation, 0, 15)
	for i := 0; i < 15; i++ {
		phrases = append(phrases, TimedAnnotation{
			Text:    fmt.Sprintf("Questa frase lunga si adatta alla famiglia motion selezionata durante la generazione del video numero %02d.", i),
			StartMs: int64(i * 2200), EndMs: int64(i*2200 + 1800),
			StartUS: int64(i*2200) * 1000, DurationUS: 1_800_000, Score: 1 - float64(i)*0.01,
		})
	}
	input.Scenes = []SceneInput{{ID: "scene-main", Phrases: phrases}}
	cfg := AllCandidatesPlannerConfig(input.Scenes)
	cfg.MaxPhrases = 15
	cfg.RunLevelPhraseOverlayLimit = 15
	plan, err := BuildPlan(input, cfg)
	if err != nil {
		t.Fatalf("BuildPlan with documentary preset IDs: %v", err)
	}

	families := map[string]int{"documentary": 0}
	nativeIDs := make(map[string]bool, len(documentaryPhraseMotionAliases))
	for _, motionID := range documentaryPhraseMotionAliases {
		nativeIDs[motionID] = true
	}
	seen := make(map[string]bool)
	for _, item := range plan.Items {
		if item.Kind != "text_phrase" {
			continue
		}
		if seen[item.MotionID] {
			t.Errorf("motion %q repeated before all 15 presets were visited", item.MotionID)
		}
		seen[item.MotionID] = true
		if !containsString(LongPhraseMotionCandidates(), item.MotionID) {
			t.Errorf("resolved motion %q is outside the long-phrase lane", item.MotionID)
		}
		if nativeIDs[item.MotionID] {
			families["documentary"]++
		} else {
			t.Errorf("motion resolved outside the native documentary set: %q", item.MotionID)
		}
	}
	if len(seen) != 15 {
		t.Fatalf("long-phrase plan selected %d unique motions, want 15", len(seen))
	}
	wantFamilies := map[string]int{"documentary": 15}
	for family, want := range wantFamilies {
		if families[family] != want {
			t.Errorf("family %q selected %d times, want %d", family, families[family], want)
		}
	}
}

func TestLongPhrasePoolKeepsOnlySoftBrushAccents(t *testing.T) {
	soft := map[string]bool{
		"brush_phrase_gold_marker":       true,
		"brush_phrase_white_light_sweep": true,
	}
	for _, id := range brushPhraseMotionCandidates {
		if !soft[id] {
			t.Errorf("harsh Brush motion %q remains selectable", id)
		}
	}
	if len(brushPhraseMotionCandidates) != len(soft) {
		t.Fatalf("soft Brush pool has %d motions, want %d", len(brushPhraseMotionCandidates), len(soft))
	}
	for _, id := range []string{
		"brush_phrase_arrow_point", "brush_phrase_circle_focus", "brush_phrase_lower_rule",
		"brush_phrase_red_brush_underline", "brush_phrase_red_endpoint_rule",
		"brush_phrase_red_underline", "brush_phrase_signature_flourish",
		"brush_phrase_white_double_underline", "brush_phrase_white_underline",
	} {
		if containsString(LongPhraseMotionCandidates(), id) {
			t.Errorf("removed Brush motion %q remains in the long-phrase lane", id)
		}
	}
}
