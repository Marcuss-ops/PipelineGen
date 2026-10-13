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
// Recipes declared for 6+ words, the retired recipes and the typewriter glyph
// animators are deliberately absent: the first two are out of the short-phrase
// lane by contract, and the typewriter ids rendered blank clips on the
// installed Vulkan runtime, so they stay available to explicit plans only.
var shortPhraseRecipeWordGroups = map[int][]string{
	1: {
		"short_phrase_scale_settle_word",
		"short_phrase_product_scramble_reveal",
		"short_phrase_product_text_shimmer",
		"short_phrase_product_chromatic_fringe_title",
		"short_phrase_product_fold_text",
	},
	2: {
		"short_phrase_character_tracking_reveal",
		"short_phrase_character_cascade_shapes",
		"short_phrase_editorial_quiet_zoom",
		"short_phrase_product_focus_blur_resolve",
		"short_phrase_product_split_flap_text",
	},
	3: {
		"short_phrase_word_mask_sequence",
		"short_phrase_word_cascade_sentence",
		"short_phrase_single_word_swap",
		"short_phrase_character_write_on",
		"short_phrase_product_blur_out_up",
		"short_phrase_product_bottom_up_letters",
		"short_phrase_product_kinetic_center_build",
		"short_phrase_product_per_word_rise",
		"short_phrase_product_text_match_cut",
		"short_phrase_product_masked_heading",
		"short_phrase_product_scrambled_text",
		"short_phrase_product_gradual_blur",
		"short_phrase_product_shape_blur",
		"short_phrase_editorial_paper_title_card",
	},
	4: {
		"short_phrase_semantic_two_line",
		"short_phrase_simple_progressive_phrase",
		"short_phrase_editorial_tracking_close",
		"short_phrase_editorial_rule_handoff",
		"short_phrase_editorial_glyph_curtain",
		"short_phrase_editorial_contrast_sweep",
		"short_phrase_product_perspective_marquee",
		"short_phrase_product_strikethrough_replace",
		"short_phrase_product_scroll_reveal",
	},
	5: {
		"short_phrase_shape_phrase_wipe",
		"short_phrase_editorial_focus_resolve",
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
