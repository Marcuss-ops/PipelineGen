package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// EntitiesProcessor extracts entities and visual search terms for each
// canonical VidRush segment. Extraction is required when registered.
type EntitiesProcessor struct {
	extractor EntityExtractor
	metrics   VidRushMetrics
	cache     scriptports.VidRushCachePort
	// Ollama's configured local model is single-slot on this host. Keep the
	// segment workers bounded at four for ordering/CPU work, but serialize the
	// remote model call so requests do not queue indefinitely inside Ollama.
	extractionGate     chan struct{}
	extractionGateOnce sync.Once
}

func NewEntitiesProcessor(extractor EntityExtractor, metrics ...VidRushMetrics) *EntitiesProcessor {
	return NewEntitiesProcessorWithCache(extractor, nil, metrics...)
}

func NewEntitiesProcessorWithCache(extractor EntityExtractor, cache scriptports.VidRushCachePort, metrics ...VidRushMetrics) *EntitiesProcessor {
	var m VidRushMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &EntitiesProcessor{extractor: extractor, metrics: m, cache: cache, extractionGate: make(chan struct{}, 1)}
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
	p.extractionGateOnce.Do(func() {
		if p.extractionGate == nil {
			p.extractionGate = make(chan struct{}, 1)
		}
	})

	extractionLimit := 5
	if plan.MediaPlan.Extraction.MaxEntitiesPerSegment > 0 {
		extractionLimit = plan.MediaPlan.Extraction.MaxEntitiesPerSegment
	}
	phrasesLimit := 5
	if plan.MediaPlan.Extraction.MaxImportantPhrasesPerSegment > 0 {
		phrasesLimit = plan.MediaPlan.Extraction.MaxImportantPhrasesPerSegment
	}
	// The annotation contract is scene-level: retain at most one key
	// statement per scene, regardless of a wider legacy extraction limit.
	if phrasesLimit > 1 {
		phrasesLimit = 1
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
	type extractionOutcome struct {
		index       int
		segment     scriptpkg.VidRushSegmentResult
		cached      bool
		unavailable error
		err         error
	}

	// Extraction is provider/LLM I/O, not CPU-bound work. Keep the worker
	// count bounded and preserve canonical segment order by storing outcomes
	// by input index before aggregating them below.
	workerCount := 4
	if len(canonical) < workerCount {
		workerCount = len(canonical)
	}
	jobs := make(chan int)
	outcomes := make(chan extractionOutcome, len(canonical))
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for worker := 0; worker < workerCount; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				canonicalSeg := canonical[index]
				device := extractionDevice(plan)
				if p.metrics != nil {
					p.metrics.IncSegments()
				}
				cacheKey := segmentCacheKey(
					"extraction-v4-local-v1",
					canonicalSeg.TextHash,
					plan.Language,
					plan.Model,
					plan.PromptVersion,
					fmt.Sprintf("%d", extractionLimit),
					fmt.Sprintf("%d", phrasesLimit),
					fmt.Sprintf("%d", wordsLimit),
					fmt.Sprintf("%d", artlistLimit),
					fmt.Sprintf("%d", imageLimit),
					device,
				)
				if !plan.MediaPlan.ForceRefreshExtraction {
					if cached, ok := cacheLoad(&vidrushExtractionCache, cacheKey); ok {
						if seg, ok := cached.(scriptpkg.VidRushSegmentResult); ok {
							seg = cloneVidRushSegmentResult(seg)
							seg.Cache.Extraction = "HIT_EXACT"
							if p.metrics != nil {
								p.metrics.IncExtractionCache(true)
							}
							outcomes <- extractionOutcome{index: index, segment: seg, cached: true}
							continue
						}
					}
					var persisted scriptpkg.VidRushSegmentResult
					if hit, cacheErr := loadVidRushPersistentJSON(workerCtx, p.cache, "extraction", cacheKey, &persisted); cacheErr != nil {
						outcomes <- extractionOutcome{index: index, err: cacheErr}
						continue
					} else if hit {
						persisted = cloneVidRushSegmentResult(persisted)
						persisted.Cache.Extraction = "HIT_EXACT"
						cacheStore(&vidrushExtractionCache, cacheKey, persisted)
						if p.metrics != nil {
							p.metrics.IncExtractionCache(true)
						}
						outcomes <- extractionOutcome{index: index, segment: persisted, cached: true}
						continue
					}
				}

				select {
				case p.extractionGate <- struct{}{}:
				case <-workerCtx.Done():
					outcomes <- extractionOutcome{index: index, err: workerCtx.Err()}
					continue
				}
				res, err := p.extractor.ExtractEntities(workerCtx, scriptpkg.EntityExtractionRequest{
					Text:        canonicalSeg.Text,
					Title:       plan.Title,
					Language:    plan.Language,
					Device:      device,
					Model:       plan.Model,
					EntityCount: extractionLimit,
					SpecScene:   segmentSpecSceneContext(input.SpecScene, canonicalSeg),
				})
				<-p.extractionGate
				if err != nil {
					if errors.Is(err, ErrEntityExtractorUnavailable) {
						outcomes <- extractionOutcome{index: index, unavailable: err}
					} else {
						outcomes <- extractionOutcome{index: index, err: err}
					}
					continue
				}
				if res == nil {
					res = &scriptpkg.EntityResult{}
				}
				seg := buildVidRushSegmentResult(plan, canonicalSeg, res, extractionLimit, phrasesLimit, wordsLimit, artlistLimit, imageLimit, segmentQueryContext(plan, canonicalSeg))
				seg.Cache.Extraction = "MISS"
				if plan.MediaPlan.ForceRefreshExtraction {
					seg.Cache.Extraction = "REFRESHED"
				}
				if p.metrics != nil {
					p.metrics.IncExtractionCache(false)
				}
				cacheStore(&vidrushExtractionCache, cacheKey, seg)
				if cacheErr := storeVidRushPersistentJSON(workerCtx, p.cache, "extraction", cacheKey, seg); cacheErr != nil {
					outcomes <- extractionOutcome{index: index, err: cacheErr}
					continue
				}
				outcomes <- extractionOutcome{index: index, segment: seg}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := range canonical {
			select {
			case jobs <- index:
			case <-workerCtx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(outcomes)
	}()

	outcomeByIndex := make(map[int]extractionOutcome, len(canonical))
	for outcome := range outcomes {
		outcomeByIndex[outcome.index] = outcome
	}
	segments := make([]scriptpkg.VidRushSegmentResult, 0, len(canonical))
	for index := range canonical {
		outcome := outcomeByIndex[index]
		if outcome.unavailable != nil {
			cancel()
			return &PostProcessResult{Changed: true, Warnings: []string{outcome.unavailable.Error()}}, nil
		}
		if outcome.err != nil {
			cancel()
			return nil, outcome.err
		}
		segments = append(segments, outcome.segment)
		mergeVidRushAggregate(agg, outcome.segment)
	}

	// Text generation can reach semantic extraction before the clip-binding
	// processor materializes its final scenes. Build a provisional scene
	// envelope from the canonical segments so annotations are not discarded;
	// the composite merge later carries them onto synthesized scenes.
	sceneInput := input.SpecScene
	if len(sceneInput.Scenes) == 0 && len(canonical) > 0 {
		sceneInput.Version = 1
		sceneInput.Scenes = make([]scriptpkg.SpecScene, 0, len(canonical))
		for i, segment := range canonical {
			sceneID := segment.SceneID
			if sceneID == "" {
				sceneID = fmt.Sprintf("scene-%d", i)
			}
			sceneInput.Scenes = append(sceneInput.Scenes, scriptpkg.SpecScene{
				ID:        sceneID,
				Index:     i,
				SegmentID: segment.ID,
				Text:      segment.Text,
				Kind:      scriptpkg.SceneClip,
			})
		}
	}
	updated := cloneSpecSceneOutput(sceneInput)
	language := strings.TrimSpace(input.EffectiveLanguage)
	if language == "" {
		language = strings.TrimSpace(plan.Language)
	}
	for i := range updated.Scenes {
		for _, seg := range segments {
			if (updated.Scenes[i].SegmentID != "" && updated.Scenes[i].SegmentID == seg.SegmentID) ||
				(updated.Scenes[i].ID != "" && updated.Scenes[i].ID == seg.SceneID) ||
				(updated.Scenes[i].SegmentID == "" && updated.Scenes[i].ID == "" && updated.Scenes[i].Index == seg.Position) {
				updated.Scenes[i].Annotations = sceneAnnotations(updated.Scenes[i].Text, language, seg)
				break
			}
		}
	}

	return &PostProcessResult{
		Entities:         agg,
		VidRushSegments:  segments,
		UpdatedSpecScene: updated,
		SpecSceneChanged: len(updated.Scenes) > 0,
		Changed:          true,
	}, nil
}

// segmentSpecSceneContext prevents cross-segment contamination when an
// extractor starts using structured scene context. The selected scene is
// re-indexed to zero because it becomes a valid one-scene local envelope.
func segmentSpecSceneContext(input scriptpkg.SpecSceneOutput, segment scriptpkg.CanonicalSegment) scriptpkg.SpecSceneOutput {
	version := input.Version
	if version == 0 {
		version = 1
	}
	for _, scene := range input.Scenes {
		matchesScene := segment.SceneID != "" && strings.TrimSpace(scene.ID) == strings.TrimSpace(segment.SceneID)
		matchesSegment := strings.TrimSpace(scene.SegmentID) != "" && strings.TrimSpace(scene.SegmentID) == strings.TrimSpace(segment.ID)
		matchesPosition := segment.SceneID == "" && strings.TrimSpace(scene.SegmentID) == "" && scene.Index == segment.Position
		if !matchesScene && !matchesSegment && !matchesPosition {
			continue
		}
		localScene := scene
		localScene.Index = 0
		return scriptpkg.SpecSceneOutput{Version: version, Scenes: []scriptpkg.SpecScene{localScene}}
	}
	return scriptpkg.SpecSceneOutput{Version: version, Scenes: []scriptpkg.SpecScene{}}
}

func extractionDevice(plan *scriptpkg.ResolvedGenerationPlan) string {
	if plan == nil {
		return "auto"
	}
	device := strings.ToLower(strings.TrimSpace(plan.MediaPlan.Extraction.Device))
	if device == "" {
		// "local" is the existing semantic strategy and means automatic
		// local hardware selection, not a separate device backend.
		device = strings.ToLower(strings.TrimSpace(plan.MediaPlan.Extraction.Strategy))
	}
	switch device {
	case "cpu", "gpu", "auto":
		return device
	default:
		return "auto"
	}
}

func mergeVidRushAggregate(dst *scriptpkg.EntityResult, seg scriptpkg.VidRushSegmentResult) {
	if dst == nil {
		return
	}
	for _, ent := range seg.Insights.Entities {
		entity := scriptpkg.Entity{Value: ent.Value, Type: ent.Type, Score: float32(ent.Confidence)}
		switch strings.ToUpper(strings.TrimSpace(ent.Type)) {
		case "PERSON":
			dst.Persons = append(dst.Persons, entity)
		case "LOCATION", "PLACE", "COUNTRY", "CITY":
			dst.Places = append(dst.Places, entity)
		default:
			dst.Concepts = append(dst.Concepts, entity)
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
	queryText ...string,
) scriptpkg.VidRushSegmentResult {
	insights := scriptpkg.SegmentInsights{
		SegmentID: canonicalSeg.ID,
		TextHash:  canonicalSeg.TextHash,
	}
	entities := make([]scriptpkg.ExtractedEntity, 0, entitiesLimit)
	for _, person := range res.Persons {
		if v := strings.TrimSpace(person.Value); v != "" {
			kind := strings.ToUpper(strings.TrimSpace(person.Type))
			if kind == "" {
				kind = "PERSON"
			}
			entities = append(entities, scriptpkg.ExtractedEntity{Value: v, Type: kind, Confidence: float64(person.Score)})
		}
	}
	for _, place := range res.Places {
		if v := strings.TrimSpace(place.Value); v != "" {
			kind := strings.ToUpper(strings.TrimSpace(place.Type))
			if kind == "" {
				kind = "LOCATION"
			}
			entities = append(entities, scriptpkg.ExtractedEntity{Value: v, Type: kind, Confidence: float64(place.Score)})
		}
	}
	for _, concept := range res.Concepts {
		if v := strings.TrimSpace(concept.Value); v != "" {
			kind := strings.ToUpper(strings.TrimSpace(concept.Type))
			if kind == "" {
				kind = "CONCEPT"
			}
			entities = append(entities, scriptpkg.ExtractedEntity{Value: v, Type: kind, Confidence: float64(concept.Score)})
		}
	}
	insights.Entities = uniqueLimitedEntities(entities, entitiesLimit)
	insights.ImportantPhrases = uniqueLimitedStrings(res.ImportantPhrases, phrasesLimit)
	// Keep the per-segment insight contract total for short canonical
	// segments (for example a one-word section heading). The fallback is
	// the segment text itself, never a model-generated or hardcoded value.
	if len(insights.ImportantPhrases) == 0 && strings.TrimSpace(canonicalSeg.Text) != "" && phrasesLimit > 0 {
		insights.ImportantPhrases = []string{strings.TrimSpace(canonicalSeg.Text)}
	}
	insights.ImportantWords = uniqueLimitedStrings(res.ImportantWords, wordsLimit)

	// Deterministic source-grounded queries lead the bounded fan-out. Model
	// phrases enrich the set only after a retrieval-safe visual query is
	// present; this prevents poetic prose from consuming every provider slot.
	visualText := canonicalSeg.Text
	if len(queryText) > 0 && strings.TrimSpace(queryText[0]) != "" {
		visualText = strings.TrimSpace(queryText[0])
	}
	manualArtlistQueries := ResolveManualSegmentQueries(plan, canonicalSeg, scriptpkg.VidRushProviderArtlist, mediadomain.SlotPrimaryVideo)
	if len(manualArtlistQueries) > 0 {
		insights.ArtlistQueries = uniqueLimitedStrings(manualArtlistQueries, artlistLimit)
	} else if !hasLockedSegmentAssignment(plan.MediaPlan.Assignments, canonicalSeg.ID, mediadomain.SlotPrimaryVideo) {
		fallbackArtlistQueries := buildArtlistQueries(visualText, insights.Entities, insights.ImportantPhrases, insights.ImportantWords, plan.Topic)
		llmArtlistQueries := uniqueLimitedStrings(res.ArtlistPhrases, artlistLimit)
		insights.ArtlistQueries = uniqueLimitedStrings(append(fallbackArtlistQueries, llmArtlistQueries...), artlistLimit)
	}

	imagePhrases := append([]string(nil), res.ArtlistPhrases...)
	imagePhrases = append(imagePhrases, insights.ImportantPhrases...)
	manualImageQueries := ResolveManualSegmentQueries(plan, canonicalSeg, scriptpkg.VidRushProviderInternetImages, mediadomain.SlotSecondaryImage)
	if len(manualImageQueries) > 0 {
		insights.ImageQueries = uniqueLimitedStrings(manualImageQueries, imageLimit)
	} else if !hasLockedSegmentAssignment(plan.MediaPlan.Assignments, canonicalSeg.ID, mediadomain.SlotSecondaryImage) {
		insights.ImageQueries = buildImageQueries(visualText, insights.Entities, imagePhrases, insights.ImportantWords, plan.Topic)
		insights.ImageQueries = uniqueLimitedStrings(insights.ImageQueries, imageLimit)
	}

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

// segmentQueryContext keeps visual retrieval grounded in the source supplied
// by the caller when the model's prose is editorially embellished. Explicit
// ScriptSegments are authoritative; paragraph-aligned source text is the
// deterministic fallback used by segment_words plans. The emitted segment
// text and text_hash remain the generated narration contract.
func segmentQueryContext(plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.CanonicalSegment) string {
	if plan == nil {
		return segment.Text
	}
	if len(plan.Segments) > 0 {
		if segment.Position >= 0 && segment.Position < len(plan.Segments) {
			if text := strings.TrimSpace(plan.Segments[segment.Position].SourceText); text != "" {
				return text
			}
		}
		for _, sourceSegment := range plan.Segments {
			if strings.EqualFold(strings.TrimSpace(sourceSegment.ID), strings.TrimSpace(segment.ID)) {
				if text := strings.TrimSpace(sourceSegment.SourceText); text != "" {
					return text
				}
			}
		}
	}
	if len(plan.SegmentTopics) > 0 && segment.Position >= 0 && segment.Position < len(plan.SegmentTopics) {
		if topic := strings.TrimSpace(plan.SegmentTopics[segment.Position]); topic != "" {
			return topic
		}
	}
	paragraphs := splitParagraphSegments(plan.SourceText)
	if segment.Position >= 0 && segment.Position < len(paragraphs) {
		if text := strings.TrimSpace(paragraphs[segment.Position]); text != "" {
			return text
		}
	}
	return segment.Text
}
