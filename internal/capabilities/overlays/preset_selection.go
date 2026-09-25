package overlays

import "strings"

// Generated text uses RenderingGen's canonical phrase_default visual style
// and an independently selected phrase_apple_clean motion. The motion pool is
// curated for the native MTSDF lane; PipelineGen transports catalog ids without
// synthesizing visual properties.
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
	// renderSafeImageMotions is the catalog-certified, layer-only subset of
	// image_25d_clean_v1. Camera-driven recipes remain template-only because
	// they move the source composition rather than one overlay layer.
	renderSafeImageMotions = []string{
		"image_25d_depth_float_in", "image_25d_yaw_flip_in",
		"image_25d_pitch_lift", "image_25d_pop_z_bounce",
		"image_25d_swipe_3d", "image_25d_card_swing",
		"image_25d_blur_focus_in", "image_25d_blur_scale_in",
	}
	imageMotionCandidates = renderSafeImageMotions
	// renderSafeTextMotions is the GPU-native phrase vocabulary: 30 modern
	// Apple-clean motions (phrase_apple_clean_v1) plus the 6 legacy layer
	// motions that remain smoke-verified. All 36 satisfy
	// can_lower_gpu_text_animation (single glyph animator, forward
	// square/smooth, opacity/position/scale/tracking/blur) and the layer
	// vocabulary, so they render on require_gpu_native without readback.
	renderSafeTextMotions = []string{
		// 30 Apple-clean (2s @30fps = 60 enter + 12 exit, blur/tracking modern)
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
		// 6 legacy layer-only smoke motions
		"fade_in", "slide_up", "slide_from_right",
		"scale_in", "soft_scale_reveal", "precision_spring_up",
	}
	// phraseMotionCandidates is the run-wide rotation pool for IMPORTANT_PHRASE.
	// It is exactly the modern Apple-clean vocabulary (first 30 entries) so
	// every admitted phrase rotates over a distinct, visible Apple finish;
	// the 6 legacy entries remain available via direct MotionID but are not
	// part of the editorial rotation.
	phraseMotionCandidates = renderSafeTextMotions[:30]
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
	candidates := phraseMotionCandidates
	if len(pool) > 0 {
		candidates = pool
	}
	if len(candidates) == 0 {
		return ""
	}
	// The caller supplies one run-wide ordinal after editorial ranking and
	// dedupe. Hash selection varies the first effect by job; rotating the
	// certified catalog then guarantees distinct motions across the admitted
	// phrase set.
	seeded := DefaultDeterministicPresetSampler.Sample(PresetSampleInput{
		JobFingerprint: jobID,
		SceneID:        sceneID,
		// The caller passes the stable run sentinel here. Per-scene seeds would
		// choose different starting points and could reintroduce collisions.
		SemanticID:   sceneID,
		PresetFamily: "important_phrase_motion",
		Presets:      candidates,
	}).Preset
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
	ids := make([]string, 0)
	for _, id := range phraseMotionCandidates {
		matched := false
		switch family {
		case "modern_apple":
			matched = strings.HasPrefix(id, "phrase_apple_clean_")
		case "typewriter":
			matched = strings.HasPrefix(id, "typewriter_")
		case "classic_apple":
			matched = strings.HasPrefix(id, "apple_v2_")
		case "web":
			matched = strings.HasPrefix(id, "web_")
		case "3d":
			matched = strings.Contains(id, "_3d_") || strings.HasSuffix(id, "_3d")
		default:
			return nil
		}
		if matched {
			ids = append(ids, id)
		}
	}
	return ids
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
	return selectPreset(jobID, sceneID, itemID, "text_motion", renderSafeTextMotions)
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
