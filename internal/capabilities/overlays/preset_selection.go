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
var (
	namePresetRenderSafeCandidates = []string{string(PresetModernName)}
	phrasePresetCandidates         = []string{string(PresetModernPhrase)}
	wordPresetCandidates           = []string{string(PresetModernWord)}
	imagePresetCandidates          = AnimationCandidates("preset.image")
	renderSafeImageMotions         = AnimationCandidates("image.all")
	imageRecipeMotionCandidates    = AnimationCandidates("image.recipe")
	// The automatic single-image lane rotates the five safest entrances plus
	// the certified single-image recipe families. Multi-image recipes and
	// caption-dependent recipes stay explicit-only (see animreg exclusions).
	automaticSingleImageMotionCandidates = uniqueMotionPool(append(
		append([]string(nil), AnimationCandidates("image.automatic")...),
		imageRecipeMotionCandidates...,
	))
	centeredImageMotionCandidates          = AnimationCandidates("image.map_centered")
	generatedEntityImageMotionCandidates   = AnimationCandidates("image.entity")
	generatedEntityCaptionMotionCandidates = AnimationCandidates("entity.caption")
	// Presentation pools are the union of every certified family for the
	// target: the registry derives entity.date/entity.metric from each entry's
	// chronontemplate_family, so no caller keeps a second family list.
	// date_calendar_flip rotates around the canvas origin and clips in the
	// deployed compositor; date_oil_crisis_stamp is a content-specific authored
	// stamp, not a generic date-card treatment.
	datePresentationMotionCandidates     = withoutAnimationCandidates(AnimationCandidates("entity.date"), "date_calendar_flip", "date_oil_crisis_stamp")
	metricPresentationMotionCandidates   = AnimationCandidates("entity.metric")
	classicAppleMotionCandidates         = AnimationCandidates("phrase.classic_apple")
	modernAppleMotionCandidates          = AnimationCandidates("phrase.modern_apple")
	shortPhraseMotionCandidates          = registryShortPhraseGroups()
	documentaryHighlightMotionCandidates = AnimationCandidates("phrase.documentary")
	text3DMotionCandidates               = AnimationCandidates("phrase.3d")
	phraseAppleCleanMotionCandidates     = AnimationCandidates("phrase.apple_clean")
	longPhraseMotionCandidates           = AnimationCandidates("phrase.long")
	phraseMotionCandidates               = AnimationCandidates("phrase.all")
	renderSafeTextMotions                = phraseAppleCleanMotionCandidates
	generatedPhraseMotions               = phraseMotionCandidates
	generatedTextMotions                 = phraseAppleCleanMotionCandidates
	defaultPhraseMotionFamilies          = []string{"classic_apple", "modern_apple", "text_3d_v1", "documentary_highlighting_v1"}
)

// withoutAnimationCandidates removes motions that are registered but fail
// visual QA in the actual composed video. date_calendar_flip rotates around
// the canvas origin in the deployed compositor, so the card is clipped into
// the upper-left corner instead of remaining centered.
func withoutAnimationCandidates(candidates []string, excluded ...string) []string {
	blocked := make(map[string]struct{}, len(excluded))
	for _, id := range excluded {
		blocked[id] = struct{}{}
	}
	out := make([]string, 0, len(candidates))
	for _, id := range candidates {
		if _, skip := blocked[id]; !skip {
			out = append(out, id)
		}
	}
	return out
}

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
