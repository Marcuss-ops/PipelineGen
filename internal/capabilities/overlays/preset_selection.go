package overlays

import "strings"

// Generated phrases use RenderingGen's canonical phrase_default visual style
// and an independently selected catalog motion. PipelineGen transports IDs.
// Ids are owned by ChrononTemplate/catalog. PipelineGen only selects
// deterministically and transports the opaque id.
var (
	// phrase_default is the official animated phrase style. Motion ids carry
	// Apple-clean animation independently; static_text_smoke is the fallback.
	namePresetRenderSafeCandidates = []string{"phrase_default"}
	phrasePresetCandidates         = []string{"phrase_default"}
	wordPresetCandidates           = []string{"phrase_default"}
	imagePresetCandidates          = []string{
		// Auto-selected images use an entrance long enough to remain visible.
		// image_fast_fade is an explicit short variant (<1.5 s), so it stays
		// available for editorial plans but is not picked for generated overlays.
		"image_focus_in", "image_fade_in", "image_scale_in",
		"image_slide_left", "image_slide_right",
		// modern_rounded_pop adds rounded-corner masking. The current strict
		// Vulkan/NVENC image path rejects that mask and has no legacy fallback;
		// keep the preset available for explicit certification plans, but never
		// select it for generated overlays until the native mask path lands.
		"bottom_card_rise",
	}
	imageAnimationCandidates = []string{
		"fade_in", "reveal_from_bottom", "scale_drop", "fade_shift_vertical",
	}
	// RenderingGen catalog inventories: all 18 image motions and the full
	// phrase families (42 classic Apple, 60 modern Apple and 5 typewriter).
	renderSafeImageMotions = []string{
		"image_25d_blur_focus_in",
		"image_25d_blur_scale_in",
		"image_25d_card_swing",
		"image_25d_depth_float_in",
		"image_25d_pitch_lift",
		"image_25d_pop_z_bounce",
		"image_25d_swipe_3d",
		"image_25d_yaw_flip_in",
		"image_card_push",
		"image_diagonal_sweep",
		"image_fade_reveal",
		"image_focus_reveal",
		"image_parallax_depth_reveal",
		"image_scale_reveal",
		"image_slide_left_reveal",
		"image_slide_right_reveal",
		"image_soft_focus_reveal",
		"image_tilt_settle",
	}
	imageMotionCandidates        = renderSafeImageMotions
	classicAppleMotionCandidates = []string{
		"air_rise_type_on",
		"apple_phrase_v2",
		"aurora_gradient_sweep",
		"chromatic_aberration_pop",
		"cinematic_credits_drift",
		"counter_scroll_reveal",
		"crisp_mask_center_open",
		"depth_of_field_rack_focus",
		"duotone_block_rise",
		"dynamic_island_expansion",
		"editorial_push_in",
		"fluid_gradient_text_flow",
		"focus_pull_macro",
		"glassmorphism_card_tilt",
		"glitch_slice_band",
		"high_specular_light_sweep",
		"hologram_scanline_build",
		"ink_bleed_spread",
		"isometric_3d_fold",
		"kinetic_split_word",
		"kinetic_stamp_impact",
		"letterbox_wipe",
		"liquid_glass_ripple",
		"magnetic_letters_converge",
		"masked_upward_reveal",
		"micro_tracker_kerning_compression",
		"neon_flicker_ignite",
		"parallax_depth_stack",
		"pixel_grid_alpha_matrix",
		"pulse_emphasis_beat",
		"risograph_offset_print",
		"shutter_blade_reveal",
		"soft_clay_press",
		"soft_edge_spotlight_dissolve",
		"spectrum_shimmer_wave",
		"spotlight_iris_open",
		"staggered_char_float",
		"underline_swipe_bold",
		"velocity_inertia_snap",
		"vertical_reel_snap",
		"vertical_rolling_counter",
		"weightless_float_settle",
	}
	modernAppleMotionCandidates = []string{
		"apple_cinematic_exit",
		"apple_compress_in",
		"apple_expand_from_center",
		"apple_focus_rise",
		"apple_hero_statement",
		"apple_line_cascade",
		"apple_line_sweep",
		"apple_precision_type",
		"apple_scale_push",
		"apple_scale_settle",
		"apple_soft_scale",
		"apple_tracking_reveal",
		"apple_vertical_glyph_lift",
		"apple_word_cascade",
		"apple_word_pulse",
		"cinematic_camera_push",
		"depth_parallax_reveal",
		"editorial_line_build",
		"glass_morphism_fade",
		"hero_scale_focus",
		"isometric_plane_fold",
		"kinetic_keyword_lock",
		"light_sweep_reveal",
		"magnetic_word_focus",
		"masked_vertical_lift",
		"phrase_apple_clean_01_blur_soft_reveal",
		"phrase_apple_clean_02_blur_focus_snap",
		"phrase_apple_clean_03_blur_scale_clean",
		"phrase_apple_clean_04_blur_tracking_drift",
		"phrase_apple_clean_05_blur_apple_fade",
		"phrase_apple_clean_06_blur_gravity",
		"phrase_apple_clean_07_slide_up_soft",
		"phrase_apple_clean_08_slide_up_spring",
		"phrase_apple_clean_09_slide_down_catch",
		"phrase_apple_clean_10_slide_from_right_apple",
		"phrase_apple_clean_11_slide_left_ease",
		"phrase_apple_clean_12_slide_diagonal_pop",
		"phrase_apple_clean_13_scale_soft_pop",
		"phrase_apple_clean_14_scale_bounce_clean",
		"phrase_apple_clean_15_scale_hero_focus",
		"phrase_apple_clean_16_scale_line_build",
		"phrase_apple_clean_17_scale_in_place",
		"phrase_apple_clean_18_scale_card_tilt",
		"phrase_apple_clean_19_tracking_tighten",
		"phrase_apple_clean_20_tracking_spread_clean",
		"phrase_apple_clean_21_tracking_magnetic",
		"phrase_apple_clean_22_tracking_word_focus",
		"phrase_apple_clean_23_tracking_precision_lock",
		"phrase_apple_clean_24_tracking_soft_kinetic",
		"phrase_apple_clean_25_opacity_soft_reveal",
		"phrase_apple_clean_26_opacity_depth_push",
		"phrase_apple_clean_27_opacity_parallax",
		"phrase_apple_clean_28_opacity_cinematic",
		"phrase_apple_clean_29_opacity_hero_settle",
		"phrase_apple_clean_30_opacity_clean_apple",
		"precision_tracking_lock",
		"precision_word_stagger",
		"premium_soft_reveal",
		"quiet_hero_settle",
		"soft_kinetic_rise",
	}
	typewriterMotionCandidates = []string{
		"typewriter_clean",
		"typewriter_glitch",
		"typewriter_neon",
		"typewriter_pop",
		"typewriter_tracking",
	}
	phraseAppleCleanMotionCandidates = []string{
		"phrase_apple_clean_01_blur_soft_reveal",
		"phrase_apple_clean_02_blur_focus_snap",
		"phrase_apple_clean_03_blur_scale_clean",
		"phrase_apple_clean_04_blur_tracking_drift",
		"phrase_apple_clean_05_blur_apple_fade",
		"phrase_apple_clean_06_blur_gravity",
		"phrase_apple_clean_07_slide_up_soft",
		"phrase_apple_clean_08_slide_up_spring",
		"phrase_apple_clean_09_slide_down_catch",
		"phrase_apple_clean_10_slide_from_right_apple",
		"phrase_apple_clean_11_slide_left_ease",
		"phrase_apple_clean_12_slide_diagonal_pop",
		"phrase_apple_clean_13_scale_soft_pop",
		"phrase_apple_clean_14_scale_bounce_clean",
		"phrase_apple_clean_15_scale_hero_focus",
		"phrase_apple_clean_16_scale_line_build",
		"phrase_apple_clean_17_scale_in_place",
		"phrase_apple_clean_18_scale_card_tilt",
		"phrase_apple_clean_19_tracking_tighten",
		"phrase_apple_clean_20_tracking_spread_clean",
		"phrase_apple_clean_21_tracking_magnetic",
		"phrase_apple_clean_22_tracking_word_focus",
		"phrase_apple_clean_23_tracking_precision_lock",
		"phrase_apple_clean_24_tracking_soft_kinetic",
		"phrase_apple_clean_25_opacity_soft_reveal",
		"phrase_apple_clean_26_opacity_depth_push",
		"phrase_apple_clean_27_opacity_parallax",
		"phrase_apple_clean_28_opacity_cinematic",
		"phrase_apple_clean_29_opacity_hero_settle",
		"phrase_apple_clean_30_opacity_clean_apple",
	}
	phraseMotionCandidates      = combineMotionPools(classicAppleMotionCandidates, modernAppleMotionCandidates, typewriterMotionCandidates)
	renderSafeTextMotions       = phraseAppleCleanMotionCandidates
	generatedPhraseMotions      = phraseMotionCandidates
	generatedTextMotions        = phraseAppleCleanMotionCandidates
	defaultPhraseMotionFamilies = []string{"classic_apple", "modern_apple", "typewriter"}
)

// ImagePresetCandidates returns a copy of the render-safe generated-image
// preset ids in selection order. It is the read-only projection of the SINGLE
// owner of that list: a consumer (including a structural test) must never keep
// a second copy that can drift from the ids the planner can actually emit.
func ImagePresetCandidates() []string {
	return append([]string(nil), imagePresetCandidates...)
}

func selectPreset(jobID, sceneID, itemID, family string, candidates []string) string {
	return DefaultDeterministicPresetSampler.Sample(PresetSampleInput{
		JobFingerprint: jobID,
		SceneID:        sceneID,
		SemanticID:     itemID,
		PresetFamily:   family,
		Presets:        candidates,
	}).Preset
}

// SelectEntityNamePreset chooses a stable-but-varied name treatment for one
// entity occurrence. A new job fingerprint can select another treatment,
// while retries of the same job remain bit-identical.
func SelectEntityNamePreset(jobID, sceneID, itemID, entityType string) string {
	return selectPreset(jobID, sceneID, itemID, "entity_name:"+entityType, namePresetRenderSafeCandidates)
}

func selectPhrasePreset(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "important_phrase", phrasePresetCandidates)
}

func selectPhraseMotion(jobID, sceneID string, ordinal int, pool []string) string {
	// Make the entrance visible often enough in normal generated scripts: each
	// group of three phrase overlays starts with a motion whose name and design
	// explicitly reveal text through movement, scale, blur or typing. Keep the
	// remaining slots on the wider rotation for visual variety.
	visibleEntrances := visiblePhraseEntrancePool(pool)
	if len(visibleEntrances) > 0 && ordinal%3 == 0 {
		return selectMotionFromPool(jobID, sceneID, "visible_entrance", ordinal/3, visibleEntrances)
	}
	if len(pool) > 0 {
		return selectMotionFromPool(jobID, sceneID, "explicit", ordinal, pool)
	}
	sequence := defaultPhraseMotionSequence(jobID, sceneID)
	if len(sequence) == 0 {
		return ""
	}
	return sequence[ordinal%len(sequence)]
}

// visiblePhraseEntrancePool limits the guaranteed entrance slot to certified
// motions with an unmistakable reveal. The general phrase pool remains fully
// available in the other slots, including calmer fades and settles.
//
// When a caller supplies an explicit rotation pool, the pool is honoured
// verbatim: a calmer pool with no visible entrances stays calmer, because
// the caller explicitly asked for it (channel profile). The visibility floor
// applies only to the default (empty) rotation, where sourcing a guaranteed
// entrance from the global certified pool is safe and does not break a
// user-supplied contract. Falling back to the global pool for an explicit
// calmer rotation would make "phrase motion outside channel pool" test
// failures and breaks the profile-as-override promise.
func visiblePhraseEntrancePool(pool []string) []string {
	if len(pool) == 0 {
		pool = phraseMotionCandidates
	}
	out := make([]string, 0, len(pool))
	for _, id := range pool {
		if strings.HasPrefix(id, "typewriter_") ||
			strings.Contains(id, "slide") || strings.Contains(id, "reveal") ||
			strings.Contains(id, "stagger") || strings.Contains(id, "cascade") ||
			strings.Contains(id, "lift") || strings.Contains(id, "fold") ||
			strings.Contains(id, "pop") || strings.Contains(id, "scale_in") ||
			strings.Contains(id, "scale_push") || strings.Contains(id, "focus_rise") {
			out = append(out, id)
		}
	}
	return out
}

func defaultPhraseMotionSequence(jobID, sceneID string) []string {
	families := append([]string(nil), defaultPhraseMotionFamilies...)
	if len(families) == 0 {
		return nil
	}
	seededFamily := selectPreset(jobID, sceneID, "run", "important_phrase_motion_family", families)
	for i, family := range families {
		if family == seededFamily {
			families = append(families[i:], families[:i]...)
			break
		}
	}
	rotated := make(map[string][]string, len(families))
	maxLen := 0
	for _, family := range families {
		candidates := phraseMotionFamilyCandidates(family)
		if len(candidates) == 0 {
			continue
		}
		seeded := selectPreset(jobID, sceneID, "run", "important_phrase_motion:"+family, candidates)
		start := 0
		for i, candidate := range candidates {
			if candidate == seeded {
				start = i
				break
			}
		}
		order := make([]string, len(candidates))
		for i := range candidates {
			order[i] = candidates[(start+i)%len(candidates)]
		}
		rotated[family] = order
		if len(order) > maxLen {
			maxLen = len(order)
		}
	}
	sequence := make([]string, 0, len(phraseMotionCandidates))
	for rank := 0; rank < maxLen; rank++ {
		for _, family := range families {
			if motions := rotated[family]; rank < len(motions) {
				sequence = append(sequence, motions[rank])
			}
		}
	}
	return sequence
}

func selectMotionFromPool(jobID, sceneID, family string, ordinal int, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	seeded := selectPreset(jobID, sceneID, "run", "important_phrase_motion:"+family, candidates)
	start := 0
	for i, candidate := range candidates {
		if candidate == seeded {
			start = i
			break
		}
	}
	return candidates[(start+ordinal)%len(candidates)]
}

// CertifiedPhraseMotions returns the certified render-safe motion pool this
// build rotates over. It is the membership authority a caller-supplied pool is
// validated against (see PlanInput.PhraseMotions): every id is a catalog motion
// that lowers to composition tracks only, so an id outside this list either
// needs a text-animator stack or a glow the native text lane rejects — it
// cannot render here.
//
// The returned slice is a copy — callers may keep it without pinning the
// package's own storage.
func CertifiedPhraseMotions() []string {
	return append([]string(nil), phraseMotionCandidates...)
}

// certifiedPhraseFamily returns the subset of the production-safe phrase
// vocabulary that belongs to a public motion family. Families with no
// render-safe members are intentionally rejected by the planner.
func certifiedPhraseFamily(family string) []string {
	return append([]string(nil), phraseMotionFamilyCandidates(family)...)
}

func phraseMotionFamilyCandidates(family string) []string {
	switch family {
	case "modern_apple":
		return modernAppleMotionCandidates
	case "typewriter":
		return typewriterMotionCandidates
	case "classic_apple":
		return classicAppleMotionCandidates
	default:
		return nil
	}
}

func combineMotionPools(pools ...[]string) []string {
	total := 0
	for _, pool := range pools {
		total += len(pool)
	}
	out := make([]string, 0, total)
	for _, pool := range pools {
		out = append(out, pool...)
	}
	return out
}

// RenderSafeTextMotions returns the render-safe text-motion vocabulary. It is
// the read-only projection of the single owner of that list (the rotation pool
// itself), so a consumer — including a structural test — never keeps a second
// copy that could drift from what the planner can actually emit.
func RenderSafeTextMotions() []string {
	return append([]string(nil), renderSafeTextMotions...)
}

// SelectTextMotion chooses the entrance motion of one generated TEXT item
// (entity name card, quote, important word, number, keyword). The item's
// preset alone would fall back to the preset's own motion — which on the
// installed catalog is the glyph-level apple_phrase_v2, i.e. exactly the
// animator stack this lane rejects — so every generated text item states its
// motion explicitly. A new job fingerprint can select another treatment while
// retries of the same job stay bit-identical.
func SelectTextMotion(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "text_motion", generatedTextMotions)
}

func containsMotion(pool []string, id string) bool {
	for _, candidate := range pool {
		if candidate == id {
			return true
		}
	}
	return false
}

func selectWordPreset(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "important_word", wordPresetCandidates)
}

func selectImagePreset(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "entity_image", imagePresetCandidates)
}

// SelectEntityImagePreset chooses the image motion independently from the
// entity name treatment. Both choices are made by the shared sampler so the
// renderer never has to infer or invent a preset.
func SelectEntityImagePreset(jobID, sceneID, itemID string) string {
	return selectImagePreset(jobID, sceneID, itemID)
}

// SelectEntityImageAnimation chooses a stable-but-varied Chronon entry
// animation for an entity image. Retries of the same job remain bit-identical.
func SelectEntityImageAnimation(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "entity_image_animation", imageAnimationCandidates)
}

// SelectImageAnimation is the generic image/product/logo planner selector.
func SelectImageAnimation(jobID, sceneID, itemID string) string {
	return SelectEntityImageAnimation(jobID, sceneID, itemID)
}

// CertifiedImageMotions returns the image 2.5D motions admitted by the
// catalog parity contract. Callers receive a copy.
func CertifiedImageMotions() []string {
	return append([]string(nil), imageMotionCandidates...)
}

// SelectImageMotion chooses a stable catalog image motion for one image
// overlay. Retries of the same job, scene and item resolve identically.
func SelectImageMotion(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "image_motion", imageMotionCandidates)
}

func selectImageMotion(jobID, sceneID string, ordinal int, pool []string) string {
	candidates := imageMotionCandidates
	if len(pool) > 0 {
		candidates = pool
	}
	if len(candidates) == 0 {
		return ""
	}
	seeded := selectPreset(jobID, sceneID, sceneID, "image_motion", candidates)
	start := 0
	for i, candidate := range candidates {
		if candidate == seeded {
			start = i
			break
		}
	}
	return candidates[(start+ordinal)%len(candidates)]
}
