package overlays

// These candidate ids are owned by Chronon's VisualPresetRegistry. PipelineGen
// only selects one deterministically and transports the opaque id downstream.
var (
	namePresetCandidates = []string{
		"name_glow_typewriter", "name_glow_slide", "name_glow_pop",
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
	return selectPreset(jobID, sceneID, itemID, "entity_name:"+entityType, namePresetCandidates)
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
