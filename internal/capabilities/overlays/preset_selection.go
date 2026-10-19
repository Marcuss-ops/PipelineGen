package overlays

import (
	"fmt"
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

// Generated phrases select their motion metadata through the canonical
// animation registry. Preset IDs below are style contracts, not motion IDs.
var (
	namePresetRenderSafeCandidates         = []string{string(PresetModernName)}
	phrasePresetCandidates                 = []string{string(PresetModernPhrase)}
	wordPresetCandidates                   = []string{string(PresetModernWord)}
	imagePresetCandidates                  = AnimationCandidates("preset.image")
	renderSafeImageMotions                 = AnimationCandidates("image.all")
	automaticSingleImageMotionCandidates   = AnimationCandidates("image.automatic")
	centeredImageMotionCandidates          = AnimationCandidates("image.map_centered")
	generatedEntityImageMotionCandidates   = AnimationCandidates("image.entity")
	generatedEntityCaptionMotionCandidates = AnimationCandidates("entity.caption")
	entitySequenceCameraMotion             = firstAnimationCandidate("entity.sequence_camera")
	datePresentationMotionCandidates       = AnimationCandidates("entity.date")
	metricPresentationMotionCandidates     = AnimationCandidates("entity.metric")
	classicAppleMotionCandidates           = AnimationCandidates("phrase.classic_apple")
	modernAppleMotionCandidates            = AnimationCandidates("phrase.modern_apple")
	shortPhraseMotionCandidates            = registryShortPhraseGroups()
	brushPhraseMotionCandidates            = AnimationCandidates("phrase.brush")
	documentaryHighlightMotionCandidates   = AnimationCandidates("phrase.documentary")
	text3DMotionCandidates                 = AnimationCandidates("phrase.3d")
	phraseAppleCleanMotionCandidates       = AnimationCandidates("phrase.apple_clean")
	longPhraseMotionCandidates             = AnimationCandidates("phrase.long")
	phraseMotionCandidates                 = AnimationCandidates("phrase.all")
	renderSafeTextMotions                  = phraseAppleCleanMotionCandidates
	generatedPhraseMotions                 = phraseMotionCandidates
	generatedTextMotions                   = phraseAppleCleanMotionCandidates
	defaultPhraseMotionFamilies            = []string{"classic_apple", "modern_apple", "text_3d_v1", "brush_v1", "documentary_highlighting_v1"}
)

func firstAnimationCandidate(group string) string {
	ids := AnimationCandidates(group)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func registryShortPhraseGroups() map[int][]string {
	groups := make(map[int][]string, 5)
	for wordCount := 1; wordCount <= 5; wordCount++ {
		groups[wordCount] = AnimationCandidates(fmt.Sprintf("phrase.short.words_%d", wordCount))
	}
	return groups
}

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
