package overlays

// shortPhraseRecipeWordGroups is the exact-cardinality routing table for the
// ChrononTemplate short-phrase family: every surviving recipe grouped by the
// word_count its own definition declares, in catalog order.
//
// Derivation (ChrononTemplate/catalog/short_phrase_motion.v1.json is the
// authority): keep every recipe whose declared word_count is 1..5 and drop
// the retired ids from removedGeneratedPhraseMotions.
// TestShortPhraseWordGroupsAreGeneratedFromCatalog re-derives the whole table
// from that catalog, so any drift fails the tests instead of silently
// rerouting generated short phrases.
//
// Recipes declared for 6+ words and retired recipes are deliberately absent.
// The C++ burst recipe is assigned to its authored three-word group and joins
// the automatic short-phrase rotation (short_phrase_typewriter was retired
// with the typewriter family and is rejected as a stale payload).
var shortPhraseRecipeWordGroups = map[int][]string{
	1: {
		"short_phrase_product_text_shimmer",
		"short_phrase_product_chromatic_fringe_title",
	},
	2: {
		"short_phrase_character_tracking_reveal",
		"short_phrase_character_cascade_shapes",
		"short_phrase_editorial_quiet_zoom",
		"short_phrase_product_focus_blur_resolve",
		"short_phrase_product_split_flap_text",
	},
	3: {
		"short_phrase_burst",
		"short_phrase_editorial_claude_terminal_focus",
		"short_phrase_product_blur_out_up",
		"short_phrase_product_bottom_up_letters",
		"short_phrase_product_kinetic_center_build",
		"short_phrase_product_per_word_rise",
		"short_phrase_product_text_match_cut",
		"short_phrase_product_masked_heading",
		"short_phrase_product_scrambled_text",
		"short_phrase_product_gradual_blur",
	},
	4: {
		"short_phrase_editorial_baseline_rise",
		"short_phrase_editorial_tracking_close",
		"short_phrase_editorial_glyph_curtain",
		"short_phrase_editorial_contrast_sweep",
		"short_phrase_product_perspective_marquee",
		"short_phrase_product_scroll_reveal",
		"short_phrase_product_strikethrough_replace",
	},
	5: {
		"short_phrase_editorial_side_glide",
		"short_phrase_editorial_focus_resolve",
		"short_phrase_shape_phrase_wipe",
	},
}

// copyShortPhraseWordGroups returns the routing table so a caller can never
// mutate the package-owned groups.
func copyShortPhraseWordGroups() map[int][]string {
	out := make(map[int][]string, len(shortPhraseRecipeWordGroups))
	for wordCount, ids := range shortPhraseRecipeWordGroups {
		out[wordCount] = append([]string(nil), ids...)
	}
	return out
}
