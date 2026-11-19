package overlays

import (
	"fmt"
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

// Generated phrases select their motion metadata through the canonical
// animation registry. Preset IDs below are style contracts, not motion IDs.
//
// Only the pools that a live selection path reads are declared here. The
// retired short-lane selectors left behind a second set of phrase/date/style
// pools that no caller read; keeping them made dead lanes look wired.
var (
	imagePresetCandidates                  = AnimationCandidates("preset.image")
	renderSafeImageMotions                 = AnimationCandidates("image.all")
	imageRecipeMotionCandidates            = AnimationCandidates("image.recipe")
	centeredImageMotionCandidates          = AnimationCandidates("image.map_centered")
	generatedEntityImageMotionCandidates   = AnimationCandidates("image.entity")
	generatedEntityCaptionMotionCandidates = AnimationCandidates("entity.caption")
	shortPhraseMotionCandidates            = registryShortPhraseGroups()
	longPhraseMotionCandidates             = AnimationCandidates("phrase.long")
)

func registryShortPhraseGroups() map[int][]string {
	groups := make(map[int][]string, 5)
	for wordCount := 1; wordCount <= 5; wordCount++ {
		groups[wordCount] = AnimationCandidates(fmt.Sprintf("phrase.short.words_%d", wordCount))
	}
	return groups
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
