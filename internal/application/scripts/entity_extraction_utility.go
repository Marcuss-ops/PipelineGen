package scripts

import (
	"context"
	"strings"

	"go.uber.org/zap"
)

// EntityExtractionOutput is the canonical reusable result for entity
// extraction across clip, catalog, and curation script flows.
type EntityExtractionOutput struct {
	EntitiesJSON string
	Insights     ScriptInsights
}

// ApplyToMap appends the stable entity/insight response contract to a job
// result. Callers keep transport-specific fields while sharing one entity
// serialization shape.
func (o EntityExtractionOutput) ApplyToMap(out map[string]any) {
	if out == nil {
		return
	}
	out["entities_json"] = o.EntitiesJSON
	out["important_words"] = o.Insights.ImportantWords
	out["important_phrases"] = o.Insights.ImportantPhrases
	out["special_names"] = o.Insights.SpecialNames
	out["artlist_phrases"] = o.Insights.ArtlistPhrases
	out["artlist_clip_suggestions"] = o.Insights.ArtlistClipSuggestions
	out["recommended_drive_folder"] = o.Insights.RecommendedDriveFolder
	out["phrase_clip_suggestions"] = o.Insights.PhraseClipSuggestions
	out["intro_clips"] = o.Insights.IntroClips
	out["entity_images"] = o.Insights.EntityImages
}

// EntityExtractionUtility is the single application utility used by every
// script-producing workflow. It owns extraction, JSON serialization and the
// optional insight-enrichment pass; endpoint/job handlers only opt in through
// their extract_entities flag.
type EntityExtractionUtility struct {
	extractor      EntityScriptExtractor
	insightBuilder InsightBuilder
	defaultModel   string
	log            *zap.Logger
}

func NewEntityExtractionUtility(
	extractor EntityScriptExtractor,
	insightBuilder InsightBuilder,
	defaultModel string,
	log *zap.Logger,
) *EntityExtractionUtility {
	return &EntityExtractionUtility{
		extractor:      extractor,
		insightBuilder: insightBuilder,
		defaultModel:   strings.TrimSpace(defaultModel),
		log:            log,
	}
}

// Run extracts entities from a completed script. A missing extractor or empty
// script is a supported no-op, allowing the same utility to be wired in
// deployments where post-generation AI capabilities are disabled.
func (u *EntityExtractionUtility) Run(ctx context.Context, title, script, model string) (EntityExtractionOutput, error) {
	var out EntityExtractionOutput
	if u == nil || u.extractor == nil || strings.TrimSpace(script) == "" {
		return out, nil
	}
	if strings.TrimSpace(model) == "" {
		model = u.defaultModel
	}

	entitiesJSON, err := ExtractScriptEntities(ctx, u.extractor, script, model)
	if err != nil {
		if u.log != nil {
			u.log.Warn("entity extraction utility failed", zap.Error(err))
		}
		return out, err
	}
	out.EntitiesJSON = entitiesJSON
	if u.insightBuilder != nil {
		out.Insights = u.insightBuilder.Build(ctx, title, script, entitiesJSON)
	}
	return out, nil
}

// EntityExtractionUtilityFromClipBuilder builds the common utility from the
// canonical ClipSourceBuilder dependency. Catalog and curation services already
// own this builder, so they can share extraction without reaching into HTTP or
// composition packages.
func EntityExtractionUtilityFromClipBuilder(builder *ClipSourceBuilder, log *zap.Logger) *EntityExtractionUtility {
	if builder == nil {
		return NewEntityExtractionUtility(nil, nil, "", log)
	}
	extractor, _ := builder.ollamaClient.(EntityScriptExtractor)
	return NewEntityExtractionUtility(extractor, nil, "", log)
}
