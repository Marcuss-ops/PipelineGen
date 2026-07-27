package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// EntitiesProcessor extracts entities and visual search terms for each
// canonical VidRush segment. Extraction is required when registered.
type EntitiesProcessor struct {
	extractor EntityExtractor
	metrics   VidRushMetrics
}

func NewEntitiesProcessor(extractor EntityExtractor, metrics ...VidRushMetrics) *EntitiesProcessor {
	var m VidRushMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &EntitiesProcessor{extractor: extractor, metrics: m}
}

func (p *EntitiesProcessor) Name() ProcessorName { return ProcessorEntities }

func (p *EntitiesProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorRequired
}

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
			"extraction-v2",
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
			Text:        canonicalSeg.Text,
			Title:       plan.Title,
			Language:    plan.Language,
			Model:       plan.Model,
			EntityCount: extractionLimit,
			SpecScene:   input.SpecScene,
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
		if plan.MediaPlan.ForceRefreshExtraction {
			seg.Cache.Extraction = "REFRESHED"
		}
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
		case "LOCATION", "PLACE", "COUNTRY", "CITY":
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

	// The LLM-generated Artlist phrases are already segment-scoped and
	// visually validated by the extraction prompt, so they are the primary
	// search keywords. Deterministic fallbacks only fill missing slots.
	llmArtlistQueries := uniqueLimitedStrings(res.ArtlistPhrases, artlistLimit)
	fallbackArtlistQueries := buildArtlistQueries(canonicalSeg.Text, insights.Entities, insights.ImportantPhrases, insights.ImportantWords, plan.Topic)
	insights.ArtlistQueries = uniqueLimitedStrings(append(llmArtlistQueries, fallbackArtlistQueries...), artlistLimit)

	imagePhrases := append([]string(nil), res.ArtlistPhrases...)
	imagePhrases = append(imagePhrases, insights.ImportantPhrases...)
	insights.ImageQueries = buildImageQueries(canonicalSeg.Text, insights.Entities, imagePhrases, insights.ImportantWords, plan.Topic)
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
