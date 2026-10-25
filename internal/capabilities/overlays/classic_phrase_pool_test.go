package overlays

// The Classic Apple long-phrase motions are authored in ChrononTemplate and
// registered for the long lane. This test is the selection half of that
// contract: the run must be able to deal every one of them to a long phrase,
// and each must stay inside the long-copy whitelist and the family rotation.

import "testing"

var classicAppleLongPhraseMotions = []string{
	"classic_playfair_baseline_rise",
	"classic_playfair_breath_in",
	"classic_playfair_depth_push",
	"classic_playfair_focus_resolve",
	"classic_playfair_glyph_curtain",
	"classic_playfair_glyph_glide",
	"classic_playfair_hero_settle",
	"classic_playfair_letter_tick",
	"classic_playfair_line_float",
	"classic_playfair_quiet_dissolve",
	"classic_playfair_quiet_zoom",
	"classic_playfair_soft_push_in",
	"classic_playfair_tracking_close",
	"classic_playfair_tracking_open",
	"classic_playfair_word_lift",
}

func TestClassicAppleMotionsJoinTheLongPhrasePool(t *testing.T) {
	pool := LongPhraseMotionCandidates()
	for _, id := range classicAppleLongPhraseMotions {
		if !AnimationSelectable(id, "phrase.long") {
			t.Errorf("%q is not an active, renderer-supported phrase.long motion", id)
		}
		if !containsMotion(pool, id) {
			t.Errorf("%q is missing from the long phrase pool", id)
		}
		if IsRemovedGeneratedPhraseMotion(id) {
			t.Errorf("%q is treated as retired", id)
		}
		if isLongCopyCrashingMotion(id) {
			t.Errorf("%q is marked as crashing long copy", id)
		}
		if got := longPhraseFamilyBucketOf(id); got != longFamilyClassicApple {
			t.Errorf("%q lands in family bucket %d, want the classic Apple bucket", id, got)
		}
	}
}

func TestClassicAppleMotionsAreDealtByTheLongRotation(t *testing.T) {
	pool := LongPhraseMotionCandidates()
	dealt := make(map[string]bool, len(pool))
	for ordinal := 0; ordinal < len(pool); ordinal++ {
		dealt[selectLongPhraseMotionLimited("classic-apple-pool", "run", ordinal, nil, 0)] = true
	}
	for _, id := range classicAppleLongPhraseMotions {
		if !dealt[id] {
			t.Errorf("the long rotation never dealt %q across %d ordinals", id, len(pool))
		}
	}
}
