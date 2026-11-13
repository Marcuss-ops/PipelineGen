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
		return "TIMELINE_DATE_CARD", selectPresentationMotion(jobID, sceneID, itemID, "date_presentation", datePresentationMotionCandidates, limit)
	case "METRIC_STAT_CARD":
		return "METRIC_STAT_CARD", selectPresentationMotion(jobID, sceneID, itemID, "metric_presentation", metricPresentationMotionCandidates, limit)
	default:
		return "", ""
	}
}

// selectPresentationMotion samples the complete certified family. A positive
// animation-count limit is applied to a deterministically shuffled window, not
// to the catalog prefix: a five-style request must sample five styles across
// every family, not always the first five ids. Retries of the same job stay
// bit-identical because the shuffle is seeded by the job id.
func selectPresentationMotion(jobID, sceneID, itemID, family string, candidates []string, limit int) string {
	if limit > 0 {
		candidates = limitedShuffledMotionPool(candidates, limit, jobID+"\x00"+family)
	}
	return selectPreset(jobID, sceneID, itemID, family, candidates)
}

// DatePresentationMotionCandidates and MetricPresentationMotionCandidates
// expose read-only catalog-id projections for contract tests and diagnostics.
func DatePresentationMotionCandidates() []string {
	return append([]string(nil), datePresentationMotionCandidates...)
}

func MetricPresentationMotionCandidates() []string {
	return append([]string(nil), metricPresentationMotionCandidates...)
}
