package overlays

import (
	"strings"
)

const (
	// SharedTextFontSizePX is phrase_default's canonical 1080p text size.
	SharedTextFontSizePX = 112
	// PresentationPhraseFontMinimumPX lifts runtime documentary phrases above
	// the undersized 70px legacy request while leaving date/stat callouts on
	// their dedicated size path.
	PresentationPhraseFontMinimumPX = 92
	// PresentationTextFontIncreasePX keeps Date and Metric callouts readable
	// while retaining the shared text font and appearance family.
	PresentationTextFontIncreasePX = 28
)

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
	// RenderingGen catalog inventories: all 32 certified layer-only image motions
	// and the full phrase families (42 classic Apple, 60 modern Apple and 10 typewriter).
	renderSafeImageMotions = []string{
		"image_fade_reveal",
		"image_focus_reveal",
		"image_scale_reveal",
		"image_slide_left_reveal",
		"image_slide_right_reveal",
		"image_parallax_depth_reveal",
		"image_tilt_settle",
		"image_card_push",
		"image_diagonal_sweep",
		"image_soft_focus_reveal",
		"image_25d_depth_float_in",
		"image_25d_yaw_flip_in",
		"image_25d_pitch_lift",
		"image_25d_pop_z_bounce",
		"image_25d_swipe_3d",
		"image_25d_card_swing",
		"image_25d_blur_focus_in",
		"image_25d_blur_scale_in",
		// Editorial Image V1 uses the same independently compiled image-layer
		// motion path; recipe/stack/premium families are deliberately excluded.
		"image_collage_scatter",
		"image_card_flip",
		"image_depth_cascade",
		"image_depth_dolly",
		"image_document_push",
		"image_evidence_focus",
		"image_float_settle",
		"image_focus_push",
		"image_orbit_enter",
		"image_perspective_stack",
		"image_photo_drop",
		"image_roll_in",
		"image_tilt_parallax",
		"image_yaw_reveal",
	}
	// Single-image overlays can use the complete 32-motion ChrononTemplate
	// layer-only image catalog. The renderer owns these IDs and its runtime
	// catalog contract verifies all 32; this prevents automatic scene images
	// from repeatedly falling back to a handful of legacy presets.
	singleImageMotionCandidates = renderSafeImageMotions
	imageAnimationCandidates    = singleImageMotionCandidates
	imageMotionCandidates       = renderSafeImageMotions
	// centeredImageMotionCandidates is the certified CENTERED image-motion
	// pool: the subset of motions that keep the raster pinned to the canvas
	// center, so a map's geography never drifts away from the pins projected
	// over it. MapOverlay.Validate accepts exactly these three ids.
	centeredImageMotionCandidates = []string{
		"image_fade_reveal",
		"image_focus_reveal",
		"image_scale_reveal",
	}
	// Entity portraits use the same complete certified image pool as other
	// generated images; RenderingGen has explicit lowering for the 2.5D and
	// editorial families, and the full set is covered by its image canary.
	generatedEntityImageMotionCandidates = renderSafeImageMotions
	// Entity captions can use every authored entity-name treatment: six shared
	// caption motions plus fifteen premium documentary treatments. All target
	// text layers and are registered in RenderingGen's runtime motion catalog.
	generatedEntityCaptionMotionCandidates = []string{
		"text_depth_in",
		"text_fade_up",
		"text_scale_punch",
		"text_word_rise",
		"text_word_stagger",
		"text_yaw_in",
		"trump_entity_text_01", "trump_entity_text_02", "trump_entity_text_03",
		"trump_entity_text_04", "trump_entity_text_05", "trump_entity_text_06",
		"trump_entity_text_07", "trump_entity_text_08", "trump_entity_text_09",
		"trump_entity_text_10", "trump_entity_text_11", "trump_entity_text_12",
		"trump_entity_text_13", "trump_entity_text_14", "trump_entity_text_15",
	}
	// Dates and metrics are text overlays: use the renderer-certified typewriter
	// motions instead of the image-like date-card and stat-card motion families.
	// Keeping both pools on the same typography treatment also prevents numeric
	// values from inheriting image transitions.
	datePresentationMotionCandidates = []string{
		"typewriter_clean", "typewriter_slide_in", "typewriter_soft_lift",
	}
	metricPresentationMotionCandidates = []string{
		"typewriter_clean", "typewriter_pop", "typewriter_tracking",
		"typewriter_glitch", "typewriter_neon",
		"typewriter_lift", "typewriter_slide_in", "typewriter_scale_up",
		"typewriter_blur_focus", "typewriter_soft_lift",
	}
	// Map image recipes authored in ChrononTemplate's map_image_v1 family.
	// They are transported through MapOverlay.motion_id and lowered onto the
	// certified basemap layer by RenderingGen.
	mapImageMotionCandidates = []string{
		"map_image_australia_sunset_drift",
		"map_image_brazil_glow_reveal",
		"map_image_china_slow_reveal",
		"map_image_gujarat_detail_push",
		"map_image_india_contour_draw",
		"map_image_iran_gold_focus",
		"map_image_italy_beacon_arrival",
		"map_image_korea_pin_focus",
		"map_image_nigeria_neon_bloom",
		"map_image_usa_sweep_in",
	}
	// Documentary phrase rotation uses only continuous opacity/slide entrances.
	// Typewriter and 3D families intermittently shimmer/flicker on long runtime
	// text, so keep them available for explicit editorial plans but out of this
	// automatic path.
	documentaryCleanPhraseMotionCandidates = []string{
		"phrase_apple_clean_07_slide_up_soft",
		"phrase_apple_clean_10_slide_from_right_apple",
		"phrase_apple_clean_25_opacity_soft_reveal",
		"phrase_apple_clean_28_opacity_cinematic",
	}
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
		"apple_spread_rise",
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
		"typewriter_lift",
		"typewriter_slide_in",
		"typewriter_scale_up",
		"typewriter_blur_focus",
		"typewriter_soft_lift",
	}
	text3DMotionCandidates = []string{
		"text_3d_camera_push",
		"text_3d_perspective_drop",
		"text_3d_roll_depth",
		"text_3d_tilt_rise",
		"text_3d_word_cascade",
		"text_3d_yaw_flip_in",
		"text_3d_pitch_lift",
		"text_3d_yaw_sweep",
		"text_3d_double_axis_reveal",
		"text_3d_orbit_lock",
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
	// Long grounded phrases need a single readable entrance for the full text
	// block. Avoid per-word, per-glyph and typewriter motions for these items;
	// the planner intersects this list with the caller's selected family so an
	// explicit family remains authoritative.
	longPhraseMotionCandidates = []string{
		"apple_expand_from_center",
		"apple_focus_rise",
		"apple_hero_statement",
		"apple_scale_settle",
		"apple_soft_scale",
		"cinematic_camera_push",
		"glass_morphism_fade",
		"hero_scale_focus",
		"phrase_apple_clean_01_blur_soft_reveal",
		"phrase_apple_clean_07_slide_up_soft",
		"phrase_apple_clean_10_slide_from_right_apple",
		"phrase_apple_clean_25_opacity_soft_reveal",
		"phrase_apple_clean_28_opacity_cinematic",
		"phrase_apple_clean_29_opacity_hero_settle",
		"phrase_apple_clean_30_opacity_clean_apple",
		// Long phrases are still allowed to rotate through clearly different
		// text families; limiting them to Apple fades made every documentary
		// sentence look identical and hid typewriter / 3D treatments entirely.
		"typewriter_clean",
		"typewriter_lift",
		"typewriter_tracking",
		"text_3d_camera_push",
		"text_3d_tilt_rise",
		"phrase_apple_clean_02_blur_focus_snap",
	}
	phraseMotionCandidates      = combineMotionPools(classicAppleMotionCandidates, modernAppleMotionCandidates, typewriterMotionCandidates, text3DMotionCandidates)
	renderSafeTextMotions       = phraseAppleCleanMotionCandidates
	generatedPhraseMotions      = phraseMotionCandidates
	generatedTextMotions        = phraseAppleCleanMotionCandidates
	defaultPhraseMotionFamilies = []string{"classic_apple", "modern_apple", "typewriter", "text_3d_v1"}
)

// ImagePresetCandidates returns a copy of the render-safe generated-image
// preset ids in selection order. It is the read-only projection of the SINGLE
// owner of that list: a consumer (including a structural test) must never keep
// a second copy that can drift from the ids the planner can actually emit.
func ImagePresetCandidates() []string {
	return append([]string(nil), imagePresetCandidates...)
}

// MapImageMotionCandidates projects ChrononTemplate's map_image_v1 styles to
// the map planner without exposing the mutable package slice.
func MapImageMotionCandidates() []string {
	return append([]string(nil), mapImageMotionCandidates...)
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

// NumberPresentationTemplateForEntityType maps an extracted date/time or
// numeric value type to its registered ChrononTemplate presentation template.
// Unrecognized types return empty so callers can retain their existing
// fallback mapping.
func NumberPresentationTemplateForEntityType(entityType string) string {
	switch strings.ToUpper(strings.TrimSpace(entityType)) {
	case "DATE", "TIME":
		return "TIMELINE_DATE_CARD"
	case "NUMBER", "NUM", "CARDINAL", "ORDINAL", "MONEY", "PERCENT", "PERCENTAGE", "METRIC", "METRICS", "STATISTIC", "STATISTICS", "QUANTITY":
		return "METRIC_STAT_CARD"
	default:
		return ""
	}
}

// NumberPresentationForEntityType maps extracted date/time and numeric-value
// entity types to the registered ChrononTemplate presentation family. The
// catalog-owned motion ids are selected deterministically; callers never
// synthesize a preset or timing window.
func NumberPresentationForEntityType(jobID, sceneID, itemID, entityType string) (templateID, motionID string) {
	switch NumberPresentationTemplateForEntityType(entityType) {
	case "TIMELINE_DATE_CARD":
		return "TIMELINE_DATE_CARD", selectPreset(jobID, sceneID, itemID, "date_presentation", datePresentationMotionCandidates)
	case "METRIC_STAT_CARD":
		return "METRIC_STAT_CARD", selectPreset(jobID, sceneID, itemID, "metric_presentation", metricPresentationMotionCandidates)
	default:
		return "", ""
	}
}

// DatePresentationMotionCandidates and MetricPresentationMotionCandidates
// expose read-only catalog-id projections for contract tests and diagnostics.
func DatePresentationMotionCandidates() []string {
	return append([]string(nil), datePresentationMotionCandidates...)
}

func MetricPresentationMotionCandidates() []string {
	return append([]string(nil), metricPresentationMotionCandidates...)
}
