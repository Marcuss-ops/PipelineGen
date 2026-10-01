package overlays

import (
	"crypto/rand"
	"math/big"
	"strings"
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
	imageAnimationCandidates = []string{
		"fade_in", "reveal_from_bottom", "scale_drop", "fade_shift_vertical",
	}
	// RenderingGen catalog inventories: all 18 certified image motions and the full
	// phrase families (42 classic Apple, 60 modern Apple and 5 typewriter).
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
	}
	imageMotionCandidates = renderSafeImageMotions
	// centeredImageMotionCandidates is the certified CENTERED image-motion
	// pool: the subset of motions that keep the raster pinned to the canvas
	// center, so a map's geography never drifts away from the pins projected
	// over it. MapOverlay.Validate accepts exactly these three ids.
	centeredImageMotionCandidates = []string{
		"image_fade_reveal",
		"image_focus_reveal",
		"image_scale_reveal",
	}
	// Dedicated DATE and metric motions are emitted only on their registered
	// ChrononTemplate presentation templates. Keep this small production pool
	// intentionally explicit; RenderingGen's catalog wiring test certifies the
	// IDs against the canonical motion registry.
	datePresentationMotionCandidates = []string{
		"date_fade_rise", "date_year_count", "date_calendar_flip", "date_timeline_tick",
	}
	metricPresentationMotionCandidates = []string{
		"metric_counter_rise", "metric_counter_scale_settle", "metric_odometer_vertical", "metric_count_flip",
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

// selectLongPhraseMotion keeps longer cards on block-level entrances such as
// a soft reveal, line slide, or restrained scale. It intersects with pool so
// caller-selected motion families remain authoritative. When an explicit
// family has no compatible motion, retain its regular deterministic choice.
func selectLongPhraseMotion(jobID, sceneID string, ordinal int, pool []string) string {
	compatible := make([]string, 0, len(longPhraseMotionCandidates))
	allowed := make(map[string]struct{}, len(pool))
	for _, id := range pool {
		allowed[id] = struct{}{}
	}
	for _, id := range longPhraseMotionCandidates {
		if len(pool) == 0 {
			compatible = append(compatible, id)
			continue
		}
		if _, ok := allowed[id]; ok {
			compatible = append(compatible, id)
		}
	}
	if len(compatible) == 0 {
		return selectPhraseMotion(jobID, sceneID, ordinal, pool)
	}
	return selectPhraseMotion(jobID, sceneID, ordinal, compatible)
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

// CertifiedImageMotions returns the complete certified image motion pool.
// Callers receive a copy.
func CertifiedImageMotions() []string {
	return append([]string(nil), imageMotionCandidates...)
}

// SelectImageMotion chooses a stable catalog image motion for one image
// overlay. Retries of the same job, scene and item resolve identically.
// Generated overlays rotate ONLY the centered subset: a portrait or scene
// image must stay pinned to the canvas center while it reveals (the same
// editorial rule maps already follow); slide/25d motions that carry the card
// across the canvas remain certified for explicit editorial plans but are
// never selected here.
func SelectImageMotion(jobID, sceneID, itemID string) string {
	return selectPreset(jobID, sceneID, itemID, "image_motion", centeredImageMotionCandidates)
}

// SelectImageMotionAt rotates through the certified CENTERED image pool
// from a deterministic per-job starting point. This gives each image in a run
// a different motion while allowing later jobs to start at a different point.
func SelectImageMotionAt(jobID, sceneID string, ordinal int) string {
	return selectImageMotion(jobID, sceneID, ordinal, centeredImageMotionCandidates)
}

// RandomImageMotionOffset chooses a fresh cryptographically random starting
// offset for one render plan. The caller samples it once, then rotates through
// the centered pool so no two images in the same run repeat before all are used.
func RandomImageMotionOffset() (int, error) {
	if len(centeredImageMotionCandidates) == 0 {
		return 0, nil
	}
	start, err := rand.Int(rand.Reader, big.NewInt(int64(len(centeredImageMotionCandidates))))
	if err != nil {
		return 0, err
	}
	return int(start.Int64()), nil
}

// ImageMotionAtOffset returns the image motion at a position in the randomly
// rotated CENTERED pool — the pool generated image overlays rotate over.
func ImageMotionAtOffset(offset, ordinal int) string {
	if len(centeredImageMotionCandidates) == 0 {
		return ""
	}
	index := ((offset % len(centeredImageMotionCandidates)) + ordinal) % len(centeredImageMotionCandidates)
	return centeredImageMotionCandidates[index]
}

// EntityImageParams returns the entity-image card geometry, scaled to the
// output canvas while keeping enough margin for image motion. On 1920×1080 the
// card is 960×864 (50% width, capped at 80% height): a readable portrait, not
// a corner stamp.
func EntityImageParams(width, height int) map[string]any {
	if width <= 0 || height <= 0 {
		return map[string]any{"box_width": 480, "box_height": 480}
	}
	size := width * 50 / 100
	if maxHeight := height * 80 / 100; maxHeight < size {
		size = maxHeight
	}
	if size < 1 {
		size = 1
	}
	// "width"/"height" are the keys RenderingGen's imageLayer() reads;
	// "box_width"/"box_height" stay for consumers of the legacy spelling.
	return map[string]any{"box_width": size, "box_height": size, "width": size, "height": size}
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
