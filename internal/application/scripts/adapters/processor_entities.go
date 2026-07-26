// Package scripts — processor_entities.go (PR 3, June 2026).
//
// Rewritten to drop the legacy PostGenFunc callback + GenerationSpec
// bridge. The processor now consumes the typed EntityExtractor port
// from ports_entity_metadata.go, building a typed
// `scriptpkg.EntityExtractionRequest` from `ProcessInput.Text` (the
// canonical V1 `output.text`) plus the ResolvedGenerationPlan identity
// fields.
//
// Policy is ProcessorRequired per the PR 3 spec — composition must
// wire a backend extractor and the runtime preflight rejects plans
// that request "entities" without one.
package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// EntitiesProcessor extracts named entities (Persons / Places /
// Concepts) from the generated script via the typed EntityExtractor
// port. Enabled as "entities" in the plan's Postprocessors list.
//
// PR 3 (June 2026): promoted to ProcessorRequired (was BestEffort
// in PR 2). Composition root fails closed without a wired backend;
// the runtime preflight rejects plans that request "entities"
// without a registered adapter.
type EntitiesProcessor struct {
	extractor EntityExtractor
	metrics   VidRushMetrics
}

// NewEntitiesProcessor creates an EntitiesProcessor. extractor must
// be non-nil at composition time (composition-side validation
// enforces this via validateRequiredProcessors).
func NewEntitiesProcessor(extractor EntityExtractor, metrics ...VidRushMetrics) *EntitiesProcessor {
	var m VidRushMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &EntitiesProcessor{extractor: extractor, metrics: m}
}

func (p *EntitiesProcessor) Name() ProcessorName { return ProcessorEntities }

// Policy classifies entities as ProcessorRequired. The plan arg is
// accepted for interface uniformity but ignored for now — a future
// PR can read plan.OutputSpec.ExtractEntities (or similar payload)
// and conditionally resolve. Until then, the static Required
// classification is the canonical source.
func (p *EntitiesProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorRequired
}

// Process executes entity extraction via the typed port. The
// processor does NOT depend on GenerationSpec or share state
// with the metadata path; the EntityExtractor port encapsulates
// the backend (production adapter wraps EntityScriptExtractor;
// tests inject a fake extractor returning a hand-crafted
// EntityResult).
//
// Returns (*PostProcessResult{Entities: result}, nil) on success.
// Returns an empty PostProcessResult (no error) when the input Text
// is empty — defensive short-circuit so the processor does not
// waste a backend call.
//
// Returns a typed error wrapping scriptpkg.ErrPostprocessFailed on
// backend failure.
func (p *EntitiesProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p.extractor == nil {
		return nil, fmt.Errorf("%w: entities processor: EntityExtractor not configured", scriptpkg.ErrPostprocessFailed)
	}
	if strings.TrimSpace(input.Text) == "" {
		return &PostProcessResult{}, nil
	}
	if plan == nil {
		return nil, fmt.Errorf("%w: entities processor: nil ResolvedGenerationPlan", scriptpkg.ErrPostprocessFailed)
	}

	canonical := buildCanonicalSegments(plan, input.SpecScene.Scenes, input.Text)
	if len(canonical) == 0 {
		return &PostProcessResult{}, nil
	}

	extractionLimit := 5
	if plan.MediaPlan.Extraction.MaxEntitiesPerSegment > 0 {
		extractionLimit = plan.MediaPlan.Extraction.MaxEntitiesPerSegment
	}
	phrasesLimit := 5
	if plan.MediaPlan.Extraction.MaxImportantPhrasesPerSegment > 0 {
		phrasesLimit = plan.MediaPlan.Extraction.MaxImportantPhrasesPerSegment
	}
	wordsLimit := 5
	if plan.MediaPlan.Extraction.MaxImportantWordsPerSegment > 0 {
		wordsLimit = plan.MediaPlan.Extraction.MaxImportantWordsPerSegment
	}
	artlistLimit := 5
	if plan.MediaPlan.Extraction.MaxArtlistQueriesPerSegment > 0 {
		artlistLimit = plan.MediaPlan.Extraction.MaxArtlistQueriesPerSegment
	}
	imageLimit := 5
	if plan.MediaPlan.Extraction.MaxImageQueriesPerSegment > 0 {
		imageLimit = plan.MediaPlan.Extraction.MaxImageQueriesPerSegment
	}

	agg := &scriptpkg.EntityResult{
		Persons:          []scriptpkg.Entity{},
		Places:           []scriptpkg.Entity{},
		Concepts:         []scriptpkg.Entity{},
		ArtlistPhrases:   []string{},
		ImportantPhrases: []string{},
		ImportantWords:   []string{},
	}
	segments := make([]scriptpkg.VidRushSegmentResult, 0, len(canonical))

	for _, canonicalSeg := range canonical {
		if p.metrics != nil {
			p.metrics.IncSegments()
		}
		cacheKey := segmentCacheKey(
			"extraction",
			canonicalSeg.TextHash,
			plan.Language,
			plan.Model,
			plan.PromptVersion,
			fmt.Sprintf("%d", extractionLimit),
			fmt.Sprintf("%d", phrasesLimit),
			fmt.Sprintf("%d", wordsLimit),
			fmt.Sprintf("%d", artlistLimit),
			fmt.Sprintf("%d", imageLimit),
		)
		if !plan.MediaPlan.ForceRefreshExtraction {
			if cached, ok := cacheLoad(&vidrushExtractionCache, cacheKey); ok {
				if seg, ok := cached.(scriptpkg.VidRushSegmentResult); ok {
					seg = cloneVidRushSegmentResult(seg)
					seg.Cache.Extraction = "HIT_EXACT"
					segments = append(segments, seg)
					mergeVidRushAggregate(agg, seg)
					if p.metrics != nil {
						p.metrics.IncExtractionCache(true)
					}
					continue
				}
			}
		}

		req := scriptpkg.EntityExtractionRequest{
			Text:      canonicalSeg.Text,
			Title:     plan.Title,
			Language:  plan.Language,
			Model:     plan.Model,
			SpecScene: input.SpecScene,
		}
		res, err := p.extractor.ExtractEntities(ctx, req)
		if err != nil {
			if errors.Is(err, ErrEntityExtractorUnavailable) {
				return &PostProcessResult{
					Changed:  true,
					Warnings: []string{err.Error()},
				}, nil
			}
			return nil, err
		}
		if res == nil {
			res = &scriptpkg.EntityResult{}
		}

		seg := buildVidRushSegmentResult(plan, canonicalSeg, res, extractionLimit, phrasesLimit, wordsLimit, artlistLimit, imageLimit)
		seg.Cache.Extraction = "MISS"
		if p.metrics != nil {
			p.metrics.IncExtractionCache(false)
		}
		segments = append(segments, seg)
		mergeVidRushAggregate(agg, seg)
		cacheStore(&vidrushExtractionCache, cacheKey, seg)
	}

	return &PostProcessResult{
		Entities:        agg,
		VidRushSegments: segments,
		Changed:         true,
	}, nil
}

func mergeVidRushAggregate(dst *scriptpkg.EntityResult, seg scriptpkg.VidRushSegmentResult) {
	if dst == nil {
		return
	}
	for _, ent := range seg.Insights.Entities {
		switch strings.ToUpper(strings.TrimSpace(ent.Type)) {
		case "PERSON":
			dst.Persons = append(dst.Persons, scriptpkg.Entity{Value: ent.Value, Score: float32(ent.Confidence)})
		case "LOCATION", "COUNTRY", "CITY":
			dst.Places = append(dst.Places, scriptpkg.Entity{Value: ent.Value, Score: float32(ent.Confidence)})
		default:
			dst.Concepts = append(dst.Concepts, scriptpkg.Entity{Value: ent.Value, Score: float32(ent.Confidence)})
		}
	}
	dst.ImportantPhrases = uniqueLimitedStrings(append(dst.ImportantPhrases, seg.Insights.ImportantPhrases...), 5)
	dst.ImportantWords = uniqueLimitedStrings(append(dst.ImportantWords, seg.Insights.ImportantWords...), 5)
	dst.ArtlistPhrases = uniqueLimitedStrings(append(dst.ArtlistPhrases, seg.Insights.ArtlistQueries...), 5)
}

func buildVidRushSegmentResult(
	plan *scriptpkg.ResolvedGenerationPlan,
	canonicalSeg scriptpkg.CanonicalSegment,
	res *scriptpkg.EntityResult,
	entitiesLimit int,
	phrasesLimit int,
	wordsLimit int,
	artlistLimit int,
	imageLimit int,
) scriptpkg.VidRushSegmentResult {
	insights := scriptpkg.SegmentInsights{
		SegmentID: canonicalSeg.ID,
		TextHash:  canonicalSeg.TextHash,
	}
	entities := make([]scriptpkg.ExtractedEntity, 0, entitiesLimit)
	for _, person := range res.Persons {
		if v := strings.TrimSpace(person.Value); v != "" {
			entities = append(entities, scriptpkg.ExtractedEntity{Value: v, Type: "PERSON", Confidence: float64(person.Score)})
		}
	}
	for _, place := range res.Places {
		if v := strings.TrimSpace(place.Value); v != "" {
			entities = append(entities, scriptpkg.ExtractedEntity{Value: v, Type: "LOCATION", Confidence: float64(place.Score)})
		}
	}
	for _, concept := range res.Concepts {
		if v := strings.TrimSpace(concept.Value); v != "" {
			entities = append(entities, scriptpkg.ExtractedEntity{Value: v, Type: "CONCEPT", Confidence: float64(concept.Score)})
		}
	}
	insights.Entities = uniqueLimitedEntities(entities, entitiesLimit)
	insights.ImportantPhrases = uniqueLimitedStrings(res.ImportantPhrases, phrasesLimit)
	insights.ImportantWords = uniqueLimitedStrings(res.ImportantWords, wordsLimit)
	insights.ArtlistQueries = buildArtlistQueries(canonicalSeg.Text, insights.Entities, insights.ImportantPhrases, insights.ImportantWords, plan.Topic)
	insights.ArtlistQueries = uniqueLimitedStrings(insights.ArtlistQueries, artlistLimit)
	insights.ImageQueries = buildImageQueries(canonicalSeg.Text, insights.Entities, insights.ImportantPhrases, insights.ImportantWords, plan.Topic)
	insights.ImageQueries = uniqueLimitedStrings(insights.ImageQueries, imageLimit)

	return scriptpkg.VidRushSegmentResult{
		SegmentID: canonicalSeg.ID,
		SceneID:   canonicalSeg.SceneID,
		Position:  canonicalSeg.Position,
		Text:      canonicalSeg.Text,
		TextHash:  canonicalSeg.TextHash,
		Insights:  insights,
		Assets:    scriptpkg.SegmentAssetSelection{},
		Cache:     scriptpkg.SegmentCacheState{},
	}
}
