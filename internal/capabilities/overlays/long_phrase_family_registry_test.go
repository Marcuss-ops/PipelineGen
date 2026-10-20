// Package overlays — long_phrase_family_registry_test.go: the long-phrase
// family buckets are registry data, not an ID naming convention. The table
// below pins the exact bucket every active phrase.long motion had under the
// retired prefix classifier, so a registry metadata mistake (wrong family on
// an entry, typo in a bucket family constant) cannot silently reshuffle the
// long-lane rotation. Adding a motion with a new bucket membership is a
// registry entry, not a switch edit — this table is updated by that registry
// change, never by renaming an id.
package overlays

import "testing"

// longPhraseFamilyRegistryParity pins id → bucket for every active
// phrase.long registry entry. Expected values were captured from the
// prefix-based classifier at the moment of the registry-driven rewrite; new
// phrase.long entries append their expected bucket here when added to the
// registry.
var longPhraseFamilyRegistryParity = []struct {
	id     string
	bucket int
}{
	{"apple_expand_from_center", longFamilyModernApple},
	{"apple_scale_settle", longFamilyModernApple},
	{"apple_soft_scale", longFamilyModernApple},
	{"brush_phrase_gold_marker", longFamilyBrush},
	{"brush_phrase_white_light_sweep", longFamilyBrush},
	{"cinematic_camera_push", longFamilyModernApple},
	{"doc_01_yellow_marker_sweep", longFamilyDocumentary},
	{"doc_02_multiline_stagger_highlight", longFamilyDocumentary},
	{"doc_03_documentary_quote_marker", longFamilyDocumentary},
	{"doc_04_archival_source_highlight", longFamilyDocumentary},
	{"doc_05_selective_phrase_tracking", longFamilyDocumentary},
	{"doc_06_red_keyword_punch", longFamilyDocumentary},
	{"doc_07_word_impact_reveal", longFamilyDocumentary},
	{"doc_08_mixed_font_emphasis", longFamilyDocumentary},
	{"doc_09_cinematic_underline_draw", longFamilyDocumentary},
	{"doc_10_selective_contrast_focus", longFamilyDocumentary},
	{"doc_11_headline_cinematic_zoom", longFamilyDocumentary},
	{"doc_12_quote_line_by_line_reveal", longFamilyDocumentary},
	{"doc_13_analog_scanner_highlight", longFamilyDocumentary},
	{"doc_14_dual_highlight_sequence", longFamilyDocumentary},
	{"doc_15_documentary_source_focus", longFamilyDocumentary},
	{"counter_scroll_reveal", longFamilyClassicApple},
	{"hero_scale_focus", longFamilyModernApple},
	{"isometric_3d_fold", longFamilyClassicApple},
	{"masked_upward_reveal", longFamilyClassicApple},
	{"phrase_apple_clean_01_blur_soft_reveal", longFamilyModernApple},
	{"phrase_apple_clean_02_blur_focus_snap", longFamilyModernApple},
	{"phrase_apple_clean_03_blur_scale_clean", longFamilyModernApple},
	{"phrase_apple_clean_05_blur_apple_fade", longFamilyModernApple},
	{"phrase_apple_clean_07_slide_up_soft", longFamilyModernApple},
	{"phrase_apple_clean_08_slide_up_spring", longFamilyModernApple},
	{"phrase_apple_clean_10_slide_from_right_apple", longFamilyModernApple},
	{"phrase_apple_clean_13_scale_soft_pop", longFamilyModernApple},
	{"phrase_apple_clean_16_scale_line_build", longFamilyModernApple},
	{"phrase_apple_clean_25_opacity_soft_reveal", longFamilyModernApple},
	{"phrase_apple_clean_26_opacity_depth_push", longFamilyModernApple},
	{"phrase_apple_clean_27_opacity_parallax", longFamilyModernApple},
	{"phrase_apple_clean_28_opacity_cinematic", longFamilyModernApple},
	{"phrase_apple_clean_29_opacity_hero_settle", longFamilyModernApple},
	{"phrase_apple_clean_30_opacity_clean_apple", longFamilyModernApple},
	{"text_3d_camera_push", longFamily3D},
	{"text_3d_double_axis_reveal", longFamily3D},
	{"text_3d_orbit_lock", longFamily3D},
	{"text_3d_perspective_drop", longFamily3D},
	{"text_3d_pitch_lift", longFamily3D},
	{"text_3d_roll_depth", longFamily3D},
	{"text_3d_tilt_rise", longFamily3D},
	{"text_3d_word_cascade", longFamily3D},
	{"text_3d_yaw_flip_in", longFamily3D},
	{"text_3d_yaw_sweep", longFamily3D},
}

func TestLongPhraseFamilyBucketsAreRegistryDriven(t *testing.T) {
	for _, pinned := range longPhraseFamilyRegistryParity {
		if got := longPhraseFamilyBucketOf(pinned.id); got != pinned.bucket {
			t.Errorf("longPhraseFamilyBucketOf(%q) = %d, want %d (pinned registry bucket)", pinned.id, got, pinned.bucket)
		}
	}
}

func TestLongPhraseFamilyBucketCoversEveryActiveLongEntry(t *testing.T) {
	// Every active phrase.long registry entry must be part of the pinned
	// parity table: a new registry motion without a pinned bucket would make
	// the family rotation depend on an unreviewed default.
	covered := make(map[string]bool, len(longPhraseFamilyRegistryParity))
	for _, pinned := range longPhraseFamilyRegistryParity {
		covered[pinned.id] = true
	}
	for _, id := range longPhraseMotionCandidates {
		if !covered[id] {
			t.Errorf("active phrase.long motion %q is not pinned in longPhraseFamilyRegistryParity", id)
		}
	}
}

func TestLongPhraseFamilyBucketFailsClosedOnUnknownID(t *testing.T) {
	// An id unknown to the registry has no family metadata, so it must land
	// in the classic bucket (the classifier's only load-bearing default).
	if got := longPhraseFamilyBucketOf("not_an_registry_motion_at_all"); got != longFamilyClassicApple {
		t.Errorf("longPhraseFamilyBucketOf(unknown) = %d, want longFamilyClassicApple", got)
	}
}
