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
	namePresetRenderSafeCandidates = []string{string(PresetModernName)}
	phrasePresetCandidates         = []string{string(PresetModernPhrase)}
	wordPresetCandidates           = []string{string(PresetModernWord)}
	imagePresetCandidates          = []string{
		// Auto-selected images use an entrance long enough to remain visible.
		// image_fast_fade is an explicit short variant (<1.5 s), so it stays
		// available for editorial plans but is not picked for generated overlays.
		"image_focus_in", "image_fade_in", "image_scale_in",
		"image_slide_left", "image_slide_right",
		// The strict Vulkan/NVENC image path now applies image radius in its
		// native source node; keep a rounded treatment in normal rotation.
		"modern_rounded_pop", "bottom_card_rise",
	}
	// RenderingGen catalog inventories: all 32 certified layer-only image motions
	// and the surviving phrase families (42 classic Apple and 60 modern Apple;
	// the retired typewriter/highlight families were removed with the catalog).
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
	imageAnimationCandidates    = automaticSingleImageMotionCandidates
	imageMotionCandidates       = renderSafeImageMotions
	// Automatic video images stay on the simple 2D entrance set. Depth/camera
	// image motions are still certified and may be requested explicitly, but on
	// the installed Vulkan debug worker they make later image-chain segments
	// take seconds per frame instead of milliseconds.
	automaticSingleImageMotionCandidates = []string{
		"image_fade_reveal",
		"image_focus_reveal",
		"image_scale_reveal",
		"image_slide_left_reveal",
		"image_slide_right_reveal",
	}
	// centeredImageMotionCandidates is the certified georeferenced-map pool:
	// every recipe changes only opacity/scale/blur around the plate center, so
	// projected pins and labels remain attached to the map. Keep this list
	// catalog-backed by the admission test; the old country-name recipes are
	// deprecated and must not be selected for operator-supplied rasters.
	centeredImageMotionCandidates = []string{
		"image_fade_reveal",
		"image_focus_reveal",
		"image_scale_reveal",
		"image_soft_focus_reveal",
	}
	// Entity portraits use restrained single-photo moves. Keep collage,
	// perspective, depth-stack and 2.5D recipes out of this pool: they can
	// introduce stray fragments or visibly distort ordinary source portraits.
	// This pool still gives repeated entity cards more variety than the former
	// five-reveal cap without weakening photo quality.
	generatedEntityImageMotionCandidates = []string{
		"image_fade_reveal",
		"image_focus_reveal",
		"image_scale_reveal",
		"image_slide_left_reveal",
		"image_slide_right_reveal",
		"image_diagonal_sweep",
		"image_soft_focus_reveal",
		"image_float_settle",
		"image_focus_push",
	}
	// Image cards with captions share the same full certified image pool as
	// plain entity portraits; caption motion is selected independently.
	generatedImageWithTextMotionCandidates = generatedEntityImageMotionCandidates
	// Entity caption entrances combine the authored caption, text-treatment
	// and entity-card presentation catalogs (the retired typewriter families
	// were removed here with the catalog). Runtime certification exercises
	// each selected ID through the caption compiler and GPU lane.
	generatedEntityCaptionMotionCandidates = []string{
		// One motion per distinct visual behaviour. The 18 near-duplicates that
		// were retired from this pool (same property set, same direction, values
		// within a few pixels or hundredths of a scale) remain in the catalog so
		// saved plans that name them still compile; see
		// retiredEntityCaptionMotions in RenderingGen's entity_card_layout.go.
		"entity_caption_rise",  // vertical rise
		"entity_depth_caption", // push forward in Z
		"entity_split_side",    // horizontal slide
		"text_scale_punch",     // overshoot scale
		"text_yaw_in",          // yaw turn from depth
		"text_word_rise",       // per-word rise
		"trump_entity_text_04", // blur resolve
		"trump_entity_text_08", // subtle yaw + depth
		"trump_entity_text_09", // horizontal squeeze open
		"trump_entity_text_10", // diagonal slide
		"trump_entity_text_11", // pitch tilt
		"trump_entity_text_12", // roll + scale
		"trump_entity_text_13", // drop from above with depth
	}
	// Automatic date selection is a curated premium subset of ChrononTemplate's
	// date_v1 catalog. The remaining authored motions stay available in the
	// catalog for explicit plans, but generated dates rotate only through these
	// eight restrained, legible treatments.
	datePresentationMotionCandidates = []string{
		"date_fade_rise", "date_calendar_flip", "date_timeline_tick", "date_chronology_focus",
		"date_calendar_drop", "date_month_wipe", "date_era_zoom",
	}
	// Metrics use the authored metric_v1 treatment family rather than generic
	// typewriter effects. This curated pool favors meaningful count, chart,
	// comparison and reveal motions for automatic statistic overlays.
	metricPresentationMotionCandidates = []string{
		"metric_counter_scale_settle", "metric_odometer_vertical", "metric_digits_stagger",
		"metric_delta_reveal", "metric_focus_punch", "metric_before_after",
		"metric_count_flip", "metric_split_odometer",
	}
	// mapImageMotionCandidates is the producer's safe selector pool, not the
	// full runtime map catalog: country/renderer-bound map_image_v1 entries are
	// not appropriate for arbitrary operator-supplied georeferenced rasters.
	mapImageMotionCandidates = centeredImageMotionCandidates
	// The documentary_clean_v1 family was retired 2026-10-15: its four entrances
	// remain callable through the modern_apple pool and the long-phrase lane,
	// but the family key itself is no longer selectable (see
	// removedGeneratedPhraseMotionIDs and phraseMotionFamilyCandidates).
	classicAppleMotionCandidates = []string{
		"air_rise_type_on",
		"apple_phrase_v2",
		"aurora_gradient_sweep",
		"cinematic_credits_drift",
		"counter_scroll_reveal",
		"crisp_mask_center_open",
		"depth_of_field_rack_focus",
		"duotone_block_rise",
		"dynamic_island_expansion",
		"fluid_gradient_text_flow",
		"glassmorphism_card_tilt",
		"glitch_slice_band",
		"hologram_scanline_build",
		"isometric_3d_fold",
		"kinetic_split_word",
		"kinetic_stamp_impact",
		"letterbox_wipe",
		"masked_upward_reveal",
		"neon_flicker_ignite",
		"parallax_depth_stack",
		"pulse_emphasis_beat",
		"risograph_offset_print",
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
		"apple_line_cascade",
		"apple_precision_type",
		"apple_scale_settle",
		"apple_soft_scale",
		"apple_spread_rise",
		"apple_tracking_reveal",
		"apple_vertical_glyph_lift",
		"apple_word_cascade",
		"cinematic_camera_push",
		"depth_parallax_reveal",
		"editorial_line_build",
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
		"premium_soft_reveal",
		"quiet_hero_settle",
		"soft_kinetic_rise",
	}
	// Generated short phrases rotate the surviving exact-cardinality
	// ChrononTemplate recipes, grouped by the word_count each recipe declares in
	// its own definition (short_phrase_word_groups.go). The previous
	// implementation pointed every bucket at the same four documentary-clean
	// entrances, which is why every short phrase in a run looked alike.
	// Typewriter glyph animators stay out of the automatic lane: they rendered
	// blank clips on the installed Vulkan runtime, and the 2026-10-13 review
	// retired 14 more recipes outright (removedGeneratedPhraseMotions);
	// short_phrase_product_scroll_reveal was retired the same day after it
	// crashed the Vulkan daemon.
	shortPhraseMotionCandidates = copyShortPhraseWordGroups()
	brushPhraseMotionCandidates = []string{
		"brush_phrase_gold_marker",
		"brush_phrase_white_light_sweep",
	}
	documentaryHighlightMotionCandidates = []string{
		"phrase_highlight_soft_marker",
		"phrase_highlight_archive_yellow",
		"phrase_highlight_evidence_red",
		"phrase_highlight_phrase_focus",
		"phrase_highlight_document_scan",
		"doc_01_yellow_marker_sweep",
		"doc_02_multiline_stagger_highlight",
		"doc_03_documentary_quote_marker",
		"doc_04_archival_source_highlight",
		"doc_05_selective_phrase_tracking",
		"doc_06_red_keyword_punch",
		"doc_07_word_impact_reveal",
		"doc_08_mixed_font_emphasis",
		"doc_09_cinematic_underline_draw",
		"doc_10_selective_contrast_focus",
		"doc_11_headline_cinematic_zoom",
		"doc_12_quote_line_by_line_reveal",
		"doc_13_analog_scanner_highlight",
		"doc_14_dual_highlight_sequence",
		"doc_15_documentary_source_focus",
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
	// Long grounded phrases combine readable Apple entrances, certified Brush
	// accents and spatial 3D treatments. Caller families may narrow this list,
	// while the long-lane intersection keeps short-only recipes out.
	longPhraseMotionCandidates = []string{
		"apple_expand_from_center",
		"apple_scale_settle",
		"apple_soft_scale",
		"cinematic_camera_push",
		"hero_scale_focus",
		"phrase_apple_clean_01_blur_soft_reveal",
		"phrase_apple_clean_07_slide_up_soft",
		"phrase_apple_clean_10_slide_from_right_apple",
		"phrase_apple_clean_25_opacity_soft_reveal",
		"phrase_apple_clean_28_opacity_cinematic",
		"phrase_apple_clean_29_opacity_hero_settle",
		"phrase_apple_clean_30_opacity_clean_apple",
		// Full phrase Apple-clean inventory: every ID resolves in the runtime
		// catalog and lowers through its native text-layer motion compiler.
		"phrase_apple_clean_03_blur_scale_clean",
		"phrase_apple_clean_04_blur_tracking_drift",
		"phrase_apple_clean_05_blur_apple_fade",
		"phrase_apple_clean_06_blur_gravity",
		"phrase_apple_clean_08_slide_up_spring",
		"phrase_apple_clean_09_slide_down_catch",
		"phrase_apple_clean_11_slide_left_ease",
		"phrase_apple_clean_12_slide_diagonal_pop",
		"phrase_apple_clean_13_scale_soft_pop",
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
		"phrase_apple_clean_26_opacity_depth_push",
		"phrase_apple_clean_27_opacity_parallax",
		"phrase_apple_clean_02_blur_focus_snap",
		// Keep only the restrained marker and light sweep from the Brush set.
		// The underline, circle, arrow and flourish variants drew conspicuous
		// strokes that overwhelmed the long-form typography.
		"brush_phrase_gold_marker",
		"brush_phrase_white_light_sweep",
		"phrase_highlight_soft_marker",
		"phrase_highlight_archive_yellow",
		"phrase_highlight_evidence_red",
		"phrase_highlight_phrase_focus",
		"phrase_highlight_document_scan",
		"doc_01_yellow_marker_sweep",
		"doc_02_multiline_stagger_highlight",
		"doc_03_documentary_quote_marker",
		"doc_04_archival_source_highlight",
		"doc_05_selective_phrase_tracking",
		"doc_06_red_keyword_punch",
		"doc_07_word_impact_reveal",
		"doc_08_mixed_font_emphasis",
		"doc_09_cinematic_underline_draw",
		"doc_10_selective_contrast_focus",
		"doc_11_headline_cinematic_zoom",
		"doc_12_quote_line_by_line_reveal",
		"doc_13_analog_scanner_highlight",
		"doc_14_dual_highlight_sequence",
		"doc_15_documentary_source_focus",
		// Spatial text is safe again now that semantic backgrounds stay in
		// screen space instead of entering the camera/depth bin with the text.
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
		// NOTE: typewriter, highlight and burst are retired from the callable
		// catalog.
	}
	phraseMotionCandidates      = combineMotionPools(classicAppleMotionCandidates, modernAppleMotionCandidates, text3DMotionCandidates, brushPhraseMotionCandidates, documentaryHighlightMotionCandidates)
	renderSafeTextMotions       = phraseAppleCleanMotionCandidates
	generatedPhraseMotions      = phraseMotionCandidates
	generatedTextMotions        = phraseAppleCleanMotionCandidates
	defaultPhraseMotionFamilies = []string{"classic_apple", "modern_apple", "text_3d_v1", "brush_v1", "documentary_highlighting_v1"}
)

// ImagePresetCandidates returns a copy of the render-safe generated-image
// preset ids in selection order. It is the read-only projection of the SINGLE
// owner of that list: a consumer (including a structural test) must never keep
// a second copy that can drift from the ids the planner can actually emit.
func ImagePresetCandidates() []string {
	return append([]string(nil), imagePresetCandidates...)
}

// MapImageMotionCandidates returns map-safe, centered image motions for
// operator-supplied georeferenced rasters. Renderer/country-specific map
// catalog entries remain discoverable through the runtime animation catalog.
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
// entity types to the registered ChrononTemplate presentation family. Date and
// metric motions are independently sampled from their curated premium pools;
// stable item identity keeps retries identical while different values vary.
// Callers never synthesize a preset or timing window.
func NumberPresentationForEntityType(jobID, sceneID, itemID, entityType string) (templateID, motionID string) {
	return NumberPresentationForEntityTypeWithLimit(jobID, sceneID, itemID, entityType, 0)
}

func NumberPresentationForEntityTypeWithLimit(jobID, sceneID, itemID, entityType string, limit int) (templateID, motionID string) {
	switch NumberPresentationTemplateForEntityType(entityType) {
	case "TIMELINE_DATE_CARD":
		return "TIMELINE_DATE_CARD", selectPreset(jobID, sceneID, itemID, "date_presentation", limitedMotionPool(datePresentationMotionCandidates, limit))
	case "METRIC_STAT_CARD":
		return "METRIC_STAT_CARD", selectPreset(jobID, sceneID, itemID, "metric_presentation", limitedMotionPool(metricPresentationMotionCandidates, limit))
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
