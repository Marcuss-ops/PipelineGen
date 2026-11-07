package overlays

import (
	"fmt"
	"strings"
	"testing"
)

// removedShortPhraseMotions are the 2026-11-06 editorial removals: ten Apple
// Clean entrances plus the two short-phrase editorial recipes. They were
// deleted from the source catalog and the animation registry, so the planner
// must not be able to name them even through an explicit payload pool.
var removedShortPhraseMotions = []string{
	"phrase_apple_clean_06_blur_gravity",
	"phrase_apple_clean_07_slide_up_soft",
	"phrase_apple_clean_09_slide_down_catch",
	"phrase_apple_clean_10_slide_from_right_apple",
	"phrase_apple_clean_11_slide_left_ease",
	"phrase_apple_clean_14_scale_bounce_clean",
	"phrase_apple_clean_20_tracking_spread_clean",
	"phrase_apple_clean_21_tracking_magnetic",
	"phrase_apple_clean_22_tracking_word_focus",
	"phrase_apple_clean_23_tracking_precision_lock",
	"short_phrase_editorial_baseline_rise",
	"short_phrase_editorial_side_glide",
}

// TestRemovedShortPhraseMotionsAreAbsentFromPlannerPools is the static half:
// no candidate list, family, word group, or default sequence may carry one of
// the removed IDs, and the registry must not mark them selectable.
func TestRemovedShortPhraseMotionsAreAbsentFromPlannerPools(t *testing.T) {
	pools := map[string][]string{
		"CertifiedPhraseMotions":             CertifiedPhraseMotions(),
		"ShortPhraseMotionCandidates":        ShortPhraseMotionCandidates(),
		"ShortPhraseVisibleMotionCandidates": ShortPhraseVisibleMotionCandidates(),
		"LongPhraseMotionCandidates":         LongPhraseMotionCandidates(),
		"registry phrase.all":                AnimationCandidates("phrase.all"),
		"registry phrase.short":              AnimationCandidates("phrase.short"),
		"registry phrase.long":               AnimationCandidates("phrase.long"),
		"registry phrase.apple_clean":        AnimationCandidates("phrase.apple_clean"),
		"registry phrase.modern_apple":       AnimationCandidates("phrase.modern_apple"),
		"registry phrase.classic_apple":      AnimationCandidates("phrase.classic_apple"),
		"defaultPhraseMotionSequence":        defaultPhraseMotionSequence("removed-pool-check", "run"),
	}
	for wordCount := 1; wordCount <= 5; wordCount++ {
		pools[fmt.Sprintf("ShortPhraseMotionsForWordCount(%d)", wordCount)] = ShortPhraseMotionsForWordCount(wordCount)
	}
	for _, family := range defaultPhraseMotionFamilies {
		pools["family "+family] = certifiedPhraseFamily(family)
	}

	for _, id := range removedShortPhraseMotions {
		for name, pool := range pools {
			if containsMotion(pool, id) {
				t.Errorf("removed motion %q is still in %s", id, name)
			}
		}
		for _, group := range []string{"phrase.all", "phrase.short", "phrase.apple_clean"} {
			if AnimationSelectable(id, group) {
				t.Errorf("removed motion %q is still selectable in registry group %q", id, group)
			}
		}
		if err := validatePhraseMotionPool([]string{id}); err == nil {
			t.Errorf("validatePhraseMotionPool accepted removed motion %q", id)
		}
	}
}

// TestShortPhrasePlansNeverSelectRemovedMotions is the dynamic half: it builds
// real short-phrase plans over several seeds, all five short word counts, and
// both the full and count-limited short pools. Every planned phrase must land
// on the short lane and none may carry a removed ID.
func TestShortPhrasePlansNeverSelectRemovedMotions(t *testing.T) {
	removed := make(map[string]bool, len(removedShortPhraseMotions))
	for _, id := range removedShortPhraseMotions {
		removed[id] = true
	}

	plans := 0
	motions := 0
	for seed := 0; seed < 16; seed++ {
		for wordCount := 1; wordCount <= 5; wordCount++ {
			input := removedMotionPlanInput(fmt.Sprintf("%d-%d", seed, wordCount), wordCount, 6)
			if seed%2 == 0 {
				input.AnimationCounts = map[string]int{laneShortPhrase: 4}
			}
			config := AllCandidatesPlannerConfig(input.Scenes)
			config.MaxPhrases = len(input.Scenes[0].Phrases)
			config.RunLevelPhraseOverlayLimit = len(input.Scenes[0].Phrases)

			plan, err := BuildPlan(input, config)
			if err != nil {
				t.Fatalf("BuildPlan(seed %d, words %d): %v", seed, wordCount, err)
			}
			plans++
			for _, item := range plan.Items {
				if item.Kind != "text_phrase" {
					continue
				}
				if section := EditorialSectionForItem(item); section != EditorialSectionShortPhrase {
					t.Fatalf("fixture phrase %q routed to %s, want %s", item.Text, section, EditorialSectionShortPhrase)
				}
				motions++
				if removed[item.MotionID] {
					t.Errorf("BuildPlan(seed %d, words %d) selected removed motion %q for %q", seed, wordCount, item.MotionID, item.Text)
				}
			}
		}
	}
	if plans == 0 || motions == 0 {
		t.Fatalf("fixture exercised %d plans and %d phrase motions; it must exercise both", plans, motions)
	}
	t.Logf("checked %d short-phrase plans and %d phrase motions", plans, motions)
}

// TestShortPhrasePlannerRejectsEachRemovedMotionExplicitly covers the
// payload-supplied pool path: even an operator naming one removed ID must get
// a fail-closed build error, never a plan.
func TestShortPhrasePlannerRejectsEachRemovedMotionExplicitly(t *testing.T) {
	for _, id := range removedShortPhraseMotions {
		input := removedMotionPlanInput("explicit-"+id, 3, 1)
		input.PhraseMotions = []string{id}
		if _, err := BuildPlan(input, AllCandidatesPlannerConfig(input.Scenes)); err == nil {
			t.Errorf("BuildPlan accepted removed motion %q as an explicit phrase pool", id)
		}
	}
}

func removedMotionPlanInput(seed string, wordCount, phraseCount int) PlanInput {
	phrases := make([]TimedAnnotation, phraseCount)
	for i := range phrases {
		text := strings.TrimSpace(strings.Repeat("parola ", wordCount-1) + fmt.Sprintf("frase%d", i))
		phrases[i] = TimedAnnotation{
			Text:       text,
			StartMs:    int64(i * 2200),
			EndMs:      int64(i*2200 + 2000),
			StartUS:    int64(i*2200) * 1000,
			DurationUS: 2_000_000,
			Score:      1 - float64(i)*0.01,
		}
	}
	return PlanInput{
		PlanID: "removed-check-" + seed, VideoID: "video-removed-check", MotionSeed: "motion-" + seed,
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: []SceneInput{{ID: "scene-1", Phrases: phrases}},
	}
}
