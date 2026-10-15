package overlays

import (
	"strings"
)

// removedGeneratedPhraseMotions is the set of motion IDs that must never be
// assigned to a generated overlay. The retired short-phrase IDs are absent from
// the catalogs entirely; this guard additionally blocks legacy explicit
// payloads from selecting them while old jobs are still in circulation.
var removedGeneratedPhraseMotions = func() map[string]struct{} {
	removed := map[string]struct{}{"chromatic_aberration_pop": {}}
	for _, id := range retiredShortPhraseRecipes {
		removed[id] = struct{}{}
	}
	for _, id := range removedGeneratedPhraseMotionIDs {
		removed[id] = struct{}{}
	}
	return removed
}()

// retiredShortPhraseRecipes is a compatibility denylist for retired wire IDs.
// These entries must not appear in current catalogues, suggestion pools, or
// generated plans; keep the literal IDs here solely to reject stale payloads.
var retiredShortPhraseRecipes = []string{
	"short_phrase_editorial_claude_diff_patch",
	"short_phrase_editorial_claude_prompt_response",
	"short_phrase_editorial_lift_and_rule",
	"short_phrase_editorial_rule_handoff",
	"short_phrase_editorial_underline_draw",
	"short_phrase_phrase_build_focus",
	"short_phrase_product_decrypted_text",
	"short_phrase_product_fold_text",
	"short_phrase_product_glare_hover",
	"short_phrase_product_line_by_line_slide",
	"short_phrase_product_scramble_reveal",
	"short_phrase_product_shape_blur",
	"short_phrase_product_warp_text",
	"short_phrase_product_weight_wave",
	"short_phrase_scale_settle_word",
	"short_phrase_semantic_chain_curve",
	"short_phrase_semantic_two_line",
	"short_phrase_simple_progressive_phrase",
	"short_phrase_single_word_swap",
	"short_phrase_word_cascade_sentence",
	"short_phrase_word_mask_sequence",
	"short_phrase_character_write_on",
	"short_phrase_editorial_paper_title_card",
	"short_phrase_typewriter",
}

// removedGeneratedPhraseMotionIDs are phrase animations retired from generated
// overlays by request. They are rejected like the retired short-phrase IDs.
var removedGeneratedPhraseMotionIDs = []string{
	"phrase_apple_clean_14_scale_bounce_clean",
	// Retired 2026-10-15 after visual review: conspicuous Brush line/shape
	// accents are out of the generated long-phrase pool; keep the soft gold
	// marker and light sweep as the only Brush phrase options.
	"brush_phrase_arrow_point",
	"brush_phrase_circle_focus",
	"brush_phrase_lower_rule",
	"brush_phrase_red_brush_underline",
	"brush_phrase_red_endpoint_rule",
	"brush_phrase_red_underline",
	"brush_phrase_signature_flourish",
	"brush_phrase_white_double_underline",
	"brush_phrase_white_underline",
	// Retired 2026-10-15: look-alike prune. Each removed motion shares its
	// animation recipe with a surviving sibling that stays callable (kept in
	// parentheses): the rotation keeps variety instead of near-identical
	// entrances. The documentary_clean_v1 family key was retired the same day;
	// its four motions stay callable through modern_apple and the long lane.
	"apple_focus_rise",                  // keep apple_soft_scale
	"apple_scale_push",                  // keep apple_soft_scale
	"apple_line_sweep",                  // keep apple_line_cascade
	"apple_word_pulse",                  // keep apple_word_cascade
	"precision_tracking_lock",           // keep apple_tracking_reveal
	"precision_word_stagger",            // keep kinetic_keyword_lock
	"apple_hero_statement",              // keep hero_scale_focus
	"glass_morphism_fade",               // keep hero_scale_focus
	"editorial_push_in",                 // keep cinematic_credits_drift
	"soft_clay_press",                   // keep duotone_block_rise
	"high_specular_light_sweep",         // keep fluid_gradient_text_flow
	"micro_tracker_kerning_compression", // keep fluid_gradient_text_flow
	"staggered_char_float",              // keep isometric_3d_fold
	"pixel_grid_alpha_matrix",           // keep depth_of_field_rack_focus
	"focus_pull_macro",                  // keep premium_soft_reveal
	"ink_bleed_spread",                  // keep premium_soft_reveal
	"liquid_glass_ripple",               // keep premium_soft_reveal
	"magnetic_letters_converge",         // keep premium_soft_reveal
	"shutter_blade_reveal",              // keep hero_scale_focus
	"soft_edge_spotlight_dissolve",      // keep premium_soft_reveal
	"spectrum_shimmer_wave",             // keep premium_soft_reveal
	"spotlight_iris_open",               // keep hero_scale_focus
	// Retired 2026-10-14 with the ChrononTemplate typewriter/highlight
	// removal (catalog no longer resolves them; kept literal to reject
	// stale payloads fail-closed).
	"phrase_burst_reveal",
	"phrase_center_dash",
	"phrase_center_out_underline",
	"phrase_delayed_accent",
	"phrase_double_rule",
	"phrase_gold_draw_on",
	"phrase_ivory_underline",
	"phrase_offset_double_rule",
	"phrase_red_marker_pulse",
	"phrase_red_underline_sweep",
	"phrase_short_keyword_rule",
	"phrase_slow_reveal",
	"phrase_soft_pulse",
	"phrase_split_marker",
	"phrase_staggered_lines",
	"phrase_triple_editorial_rule",
	"phrase_warm_marker_sweep",
	"phrase_yellow_highlighter_sweep",
	"typewriter_blur_focus",
	"typewriter_clean",
	"typewriter_cursor",
	"typewriter_cursor_blink",
	"typewriter_cursor_hold",
	"typewriter_cursor_leads",
	"typewriter_double_blink",
	"typewriter_fade",
	"typewriter_fast",
	"typewriter_glitch",
	"typewriter_lift",
	"typewriter_modern_01_monospace_block_cursor",
	"typewriter_modern_02_kinetic_scramble",
	"typewriter_modern_03_soft_opacity_ramp",
	"typewriter_modern_04_character_bounce",
	"typewriter_modern_05_backspace_correction",
	"typewriter_modern_06_glow_beam_sweep",
	"typewriter_modern_07_word_snap",
	"typewriter_modern_08_mechanical_y_shift",
	"typewriter_modern_09_highlighter_expansion",
	"typewriter_modern_10_weight_ramp",
	"typewriter_modern_11_dynamic_auto_wrap",
	"typewriter_modern_12_glitch_pop",
	"typewriter_modern_13_elastic_leading_cursor",
	"typewriter_modern_14_focal_blur_dissolve",
	"typewriter_modern_15_paper_punch_stencil",
	"typewriter_neon",
	"typewriter_pop",
	"typewriter_scale_up",
	"typewriter_slide_in",
	"typewriter_slow",
	"typewriter_soft",
	"typewriter_soft_lift",
	"typewriter_tracking",
	"typewriter_tracking_assemble",
}

// IsRemovedGeneratedPhraseMotion reports whether an animation was retired from
// generated overlays. Payload validation uses it to fail closed instead of
// silently accepting a motion no selector may emit.
func IsRemovedGeneratedPhraseMotion(motionID string) bool {
	_, removed := removedGeneratedPhraseMotions[strings.TrimSpace(motionID)]
	return removed
}

// ensureDistinctPhraseMotions is a final run-wide guard over the category
// selectors. Short, long, heavy, and regular phrase lanes can each rotate
// without repeats internally while still colliding with one another; resolve
// those collisions against the same word-count-compatible catalog before the
// plan is sealed.
// EnsureDistinctPhraseMotions applies the run-wide no-repeat rule after
// scene-local plans have been joined. It is safe to call both during plan
// construction and at the final run boundary.
func EnsureDistinctPhraseMotions(items []OverlayItem, input PlanInput) {
	EnsureDistinctPhraseMotionsPinned(items, input, nil)
}

// EnsureDistinctPhraseMotionsPinned is the policy-aware form: indexes in
// pinned keep their MotionID verbatim (explicit animation_policies choices
// outrank automatic rotation) while their motions are pre-registered as used
// so unpinned items rotate away from them. Duplicate motions between two
// pinned items are the caller's explicit responsibility and are left alone.
func EnsureDistinctPhraseMotionsPinned(items []OverlayItem, input PlanInput, pinned map[int]bool) {
	// Documentary preset IDs are public aliases, not renderer motion IDs. They
	// can reach this final run-wide pass after scene planning, so canonicalize
	// both the candidate pool and assigned items before duplicate resolution.
	input.PhraseMotions = resolveDocumentaryPhraseMotions(input.PhraseMotions)
	for i := range items {
		items[i].MotionID = ResolveDocumentaryPhraseMotion(items[i].MotionID)
	}
	used := make(map[string]struct{})
	for i := range items {
		if !pinned[i] || items[i].Kind != "text_phrase" {
			continue
		}
		if motion := strings.TrimSpace(items[i].MotionID); motion != "" {
			if _, removed := removedGeneratedPhraseMotions[motion]; !removed {
				used[motion] = struct{}{}
			}
		}
	}
	seed := strings.TrimSpace(input.MotionSeed)
	if seed == "" {
		seed = input.PlanID
		if input.VideoID != "" {
			seed += ":" + input.VideoID
		}
	}
	shortLimit := laneAnimationCount(input.AnimationCounts, laneShortPhrase)
	phraseLimit := laneAnimationCount(input.AnimationCounts, laneImportantPhrase)
	// A caller-supplied singleton pool is an explicit motion choice, not a
	// request for variety. Preserve it across items; automatic pools still
	// receive the normal run-wide uniqueness pass below.
	explicitPool := uniqueMotionPool(input.PhraseMotions)
	explicitSingleton := len(explicitPool) == 1
	for i := range items {
		item := &items[i]
		if item.Kind != "text_phrase" || strings.TrimSpace(item.MotionID) == "" {
			continue
		}
		_, removed := removedGeneratedPhraseMotions[item.MotionID]
		_, duplicate := used[item.MotionID]
		if pinned[i] && !removed {
			continue
		}
		if explicitSingleton && !removed && item.MotionID == explicitPool[0] {
			used[item.MotionID] = struct{}{}
			continue
		}
		if !removed && !duplicate {
			used[item.MotionID] = struct{}{}
			continue
		}
		candidates := phraseMotionCandidatesForItem(*item, input, seed, shortLimit, phraseLimit)
		candidates = excludeRemovedGeneratedPhraseMotions(candidates)
		if len(candidates) == 0 {
			continue
		}
		start := 0
		for n, candidate := range candidates {
			if candidate == item.MotionID {
				start = n + 1
				break
			}
		}
		for n := 0; n < len(candidates); n++ {
			candidate := candidates[(start+n)%len(candidates)]
			if _, exists := used[candidate]; exists {
				continue
			}
			item.MotionID = candidate
			used[candidate] = struct{}{}
			break
		}
	}
}

func excludeRemovedGeneratedPhraseMotions(candidates []string) []string {
	filtered := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if IsRemovedGeneratedPhraseMotion(candidate) {
			continue
		}
		filtered = append(filtered, candidate)
	}
	return filtered
}

// EnsureDistinctNumberMotions rotates date and metric presentation recipes
// across the complete plan. Their stable per-item selector can otherwise
// choose the same motion independently in separate scenes.
func EnsureDistinctNumberMotions(items []OverlayItem, animationCounts map[string]int) {
	EnsureDistinctNumberMotionsPinned(items, animationCounts, nil)
}

// EnsureDistinctNumberMotionsPinned is the policy-aware form for date/metric
// cards: pinned indexes keep their MotionID while unpinned duplicates rotate
// away. See EnsureDistinctPhraseMotionsPinned.
func EnsureDistinctNumberMotionsPinned(items []OverlayItem, animationCounts map[string]int, pinned map[int]bool) {
	used := map[string]map[string]struct{}{}
	for i := range items {
		if !pinned[i] || items[i].Kind != "number" {
			continue
		}
		if motion := strings.TrimSpace(items[i].MotionID); motion != "" {
			if used[items[i].TemplateID] == nil {
				used[items[i].TemplateID] = map[string]struct{}{}
			}
			used[items[i].TemplateID][motion] = struct{}{}
		}
	}
	for i := range items {
		item := &items[i]
		if item.Kind != "number" || strings.TrimSpace(item.MotionID) == "" {
			continue
		}
		var key string
		var candidates []string
		var limit int
		switch item.TemplateID {
		case "TIMELINE_DATE_CARD":
			key = item.TemplateID
			candidates = DatePresentationMotionCandidates()
			limit = laneAnimationCount(animationCounts, laneTimelineDate)
		case "METRIC_STAT_CARD":
			key = item.TemplateID
			candidates = MetricPresentationMotionCandidates()
			limit = laneAnimationCount(animationCounts, laneMetricStat)
		default:
			continue
		}
		if used[key] == nil {
			used[key] = map[string]struct{}{}
		}
		if pinned[i] {
			continue
		}
		if _, duplicate := used[key][item.MotionID]; !duplicate {
			used[key][item.MotionID] = struct{}{}
			continue
		}
		candidates = limitedMotionPool(candidates, limit)
		start := 0
		for n, candidate := range candidates {
			if candidate == item.MotionID {
				start = n + 1
				break
			}
		}
		for n := 0; n < len(candidates); n++ {
			candidate := candidates[(start+n)%len(candidates)]
			if _, exists := used[key][candidate]; exists {
				continue
			}
			item.MotionID = candidate
			used[key][candidate] = struct{}{}
			break
		}
	}
}

func phraseMotionCandidatesForItem(item OverlayItem, input PlanInput, seed string, shortLimit, phraseLimit int) []string {
	words := len(strings.Fields(item.Text))
	var candidates []string
	switch {
	case EditorialSectionForItem(item) == EditorialSectionShortPhrase:
		candidates = ShortPhraseVisibleMotionCandidates()
		recipes := ShortPhraseMotionsForWordCount(words)
		candidates = append(candidates, recipes...)
		candidates = uniqueMotionPool(candidates)
		if len(input.PhraseMotions) > 0 {
			compatible := intersectMotionPool(candidates, input.PhraseMotions)
			if len(compatible) > 0 {
				candidates = compatible
			} else if len(recipes) > 0 {
				candidates = recipes
			}
		}
		return excludeRemovedGeneratedPhraseMotions(candidates)
	case input.HeavyPhrasePriority > 0 && itemPriority(item) >= input.HeavyPhrasePriority:
		return excludeRemovedGeneratedPhraseMotions(visiblePhraseEntrancePool(input.PhraseMotions))
	case words >= 6:
		candidates = append([]string(nil), longPhraseMotionCandidates...)
		if len(input.PhraseMotions) > 0 {
			compatible := intersectMotionPool(candidates, input.PhraseMotions)
			if len(compatible) > 0 {
				candidates = compatible
			} else {
				// Preserve an explicit channel pool even when it has no overlap
				// with the long-copy compatibility set.
				candidates = append([]string(nil), input.PhraseMotions...)
			}
		}
		// The same lane seed as the assignment keeps one capped window per run:
		// a different window here would let the guard rotate duplicates into
		// recipes the lane never drew, exceeding the requested style count.
		lane := "long_phrase_default"
		if len(input.PhraseMotions) > 0 {
			lane = "long_phrase_pool"
		}
		if phraseLimit > 0 {
			candidates = limitedShuffledMotionPool(candidates, phraseLimit, seed+"\x00"+lane)
		}
		return excludeRemovedGeneratedPhraseMotions(candidates)
	default:
		if len(input.PhraseMotions) > 0 {
			candidates = limitedShuffledMotionPool(input.PhraseMotions, phraseLimit, seed+"\x00important_phrase_pool")
			return excludeRemovedGeneratedPhraseMotions(candidates)
		}
		candidates = defaultPhraseMotionSequence(seed, "run")
		if phraseLimit > 0 {
			candidates = limitedMotionPool(candidates, phraseLimit)
		}
		return excludeRemovedGeneratedPhraseMotions(candidates)
	}
}

func intersectMotionPool(candidates, allowed []string) []string {
	if len(allowed) == 0 {
		return candidates
	}
	set := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		set[id] = struct{}{}
	}
	out := make([]string, 0, len(candidates))
	for _, id := range candidates {
		if _, ok := set[id]; ok {
			out = append(out, id)
		}
	}
	return uniqueMotionPool(out)
}
