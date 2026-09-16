package overlays

// These candidate ids are owned by Chronon's VisualPresetRegistry. PipelineGen
// only selects one deterministically and transports the opaque id downstream.
var (
	// The current REQUIRE_GPU_NATIVE fused text path supports line-level name
	// motion. character_cascade is glyph-level and deliberately stays in the
	// official catalog for explicit/certification plans, but must not be chosen
	// by the generated entity-card planner until Chronon can prepare that
	// selector on the native path. name_glow_typewriter is retired from this
	// candidate set for the same reason: there is no second name-candidate
	// list, so the render-safe set is the ONLY owner of the name surface.
	// The installed RenderingGen release exposes only apple_v2 for the text
	// family. Keep generated plans inside that runtime registry; the
	// typewriter motion is selected by the preset itself (apple_phrase_v2).
	namePresetRenderSafeCandidates = []string{"apple_v2"}
	phrasePresetCandidates         = []string{"apple_v2"}
	wordPresetCandidates           = []string{"apple_v2"}
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
	// These are text-native phrase motions. They animate selector/text
	// properties only (no layer scale/rotation and no glyph scale/blur), which
	// keeps REQUIRE_GPU_NATIVE on Chronon's zero-readback path. The planner
	// assigns them by deterministic phrase order so the first three phrases in
	// a scene cannot collapse to the same animation.
	phraseMotionCandidates = []string{
		"word_reveal", "character_cascade", "char_wave", "opacity_wave", "center_expansion",
	}
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

func selectPhraseMotion(jobID, sceneID string, ordinal int) string {
	if len(phraseMotionCandidates) == 0 {
		return ""
	}
	// The ordinal is part of the stable semantic identity for this bounded
	// phrase surface. Hash selection still varies the starting point per
	// render, while the rotation guarantees distinct motions within a scene.
	seeded := DefaultDeterministicPresetSampler.Sample(PresetSampleInput{
		JobFingerprint: jobID,
		SceneID:        sceneID,
		// Use the scene as the stable rotation seed. Including itemID here would
		// choose a different starting point per phrase and could reintroduce a
		// collision after the ordinal is applied.
		SemanticID:   sceneID,
		PresetFamily: "important_phrase_motion",
		Presets:      phraseMotionCandidates,
	}).Preset
	start := 0
	for i, candidate := range phraseMotionCandidates {
		if candidate == seeded {
			start = i
			break
		}
	}
	return phraseMotionCandidates[(start+ordinal)%len(phraseMotionCandidates)]
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
