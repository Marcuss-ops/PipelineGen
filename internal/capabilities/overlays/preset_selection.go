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
	namePresetRenderSafeCandidates = []string{
		"name_glow_slide", "name_glow_pop",
	}
	phrasePresetCandidates = []string{
		"fast_fade_through", "clean_slide_up", "slide_lateral",
		"phrase_word_reveal", "undertext_pop",
	}
	wordPresetCandidates = []string{
		"snap_scale", "fast_fade_through", "phrase_word_reveal",
	}
	imagePresetCandidates = []string{
		"image_fast_fade", "image_slide_left", "image_slide_right",
		"modern_rounded_pop", "bottom_card_rise",
	}
	imageAnimationCandidates = []string{
		"fade_in", "reveal_from_bottom", "scale_drop", "fade_shift_vertical",
	}
)

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
