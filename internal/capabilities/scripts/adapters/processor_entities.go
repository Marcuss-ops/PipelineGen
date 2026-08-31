package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	capabilityimagesearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/imagesearch"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// EntitiesProcessor extracts entities and visual search terms for each
// canonical VidRush segment. Extraction is required when registered. It is
// the non-streaming batch consumer of VidRushSegmentEnricher: it materializes
// canonical segments from the whole document, then reuses the single-segment
// enricher so extraction/query/cache/metrics logic is implemented exactly once.
type EntitiesProcessor struct {
	*VidRushSegmentEnricher
}

// VidRushSegmentEnricher is the reusable single-segment VidRush owner. Its
// Enrich method implements the SegmentEnricher port consumed by the
// incremental coordinator (asserted at the composition root, since the
// migration-zone adapters package must not import the capability package); it
// is embedded by the batch EntitiesProcessor for non-streaming workflows.
type VidRushSegmentEnricher struct {
	extractor     EntityExtractor
	understanding SegmentUnderstandingModel
	metrics       VidRushMetrics
	cache         scriptports.VidRushCachePort
	// imageSearch is the optional deterministic Image Search Intent resolver
	// (capabilities/imagesearch — the same one the golden battery certifies).
	// When wired, its decision drives the segment's image fan-out: ordered
	// queries (primary first, negated excluded, MONEY/DATE excluded) when an
	// imageable entity exists, and an EMPTY query set (provider disabled) on
	// abstract sentences — the no-image decision the battery certifies. Nil
	// keeps the legacy ad-hoc query builder.
	imageSearch *capabilityimagesearch.Resolver
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
	return &EntitiesProcessor{VidRushSegmentEnricher: NewVidRushSegmentEnricher(extractor, cache, metrics...)}
}

// NewVidRushSegmentEnricher constructs the single-segment enrichment owner.
func NewVidRushSegmentEnricher(extractor EntityExtractor, cache scriptports.VidRushCachePort, metrics ...VidRushMetrics) *VidRushSegmentEnricher {
	var m VidRushMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &VidRushSegmentEnricher{extractor: extractor, metrics: m, cache: cache, extractionGate: make(chan struct{}, 1)}
}

// WithImageSearchResolver attaches the deterministic Image Search Intent
// resolver (the same one the golden battery certifies) to the enricher. It
// is chainable and nil-safe; it is also available on EntitiesProcessor via
// the embedded enricher. Nil keeps the legacy ad-hoc query builder.
func (e *VidRushSegmentEnricher) WithSegmentUnderstandingModel(model SegmentUnderstandingModel) *VidRushSegmentEnricher {
	if e != nil {
		e.understanding = model
	}
	return e
}

func (e *VidRushSegmentEnricher) WithImageSearchResolver(resolver *capabilityimagesearch.Resolver) *VidRushSegmentEnricher {
	if e != nil {
		e.imageSearch = resolver
	}
	return e
}

func (p *EntitiesProcessor) Name() ProcessorName { return ProcessorEntities }

func (p *EntitiesProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorRequired
}

func (p *EntitiesProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p.extractor == nil {
		return nil, fmt.Errorf("%w: entities processor: EntityExtractor not configured", scriptpkg.ErrPostprocessFailed)
	}
	extractionText := strings.TrimSpace(input.Text)
	if extractionText == "" && len(input.SpecScene.Scenes) == 1 {
		extractionText = strings.TrimSpace(input.SpecScene.Scenes[0].Text)
	}
	if extractionText == "" {
		return &PostProcessResult{}, nil
	}
	if plan == nil {
		return nil, fmt.Errorf("%w: entities processor: nil ResolvedGenerationPlan", scriptpkg.ErrPostprocessFailed)
	}

	materializedScenes := materializeNarrativeScenes(plan, input.SpecScene.Scenes, extractionText)
	materializedScenes = filterNLPScenes(materializedScenes)
	canonical := buildCanonicalSegments(plan, materializedScenes, extractionText)
	if len(canonical) == 0 {
		return &PostProcessResult{}, nil
	}
	p.extractionGateOnce.Do(func() {
		if p.extractionGate == nil {
			p.extractionGate = make(chan struct{}, 1)
		}
	})

	limits := resolveExtractionLimits(plan)

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
	batchExtractor, supportsBatch := p.extractor.(batchEntityExtractor)
	outcomes := make(chan extractionOutcome, len(canonical))
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if supportsBatch {
		const batchSize = 5
		// Keep the configured local Ollama instance single-slot until a
		// concurrency benchmark proves that overlapping batches improve
		// throughput instead of merely queueing inside the model server.
		const batchParallelism = 1
		batchJobs := make(chan int)
		var batchWG sync.WaitGroup
		for worker := 0; worker < batchParallelism; worker++ {
			batchWG.Add(1)
			go func() {
				defer batchWG.Done()
				for start := range batchJobs {
					end := start + batchSize
					if end > len(canonical) {
						end = len(canonical)
					}
					reqs := make([]scriptpkg.EntityExtractionRequest, 0, end-start)
					for index := start; index < end; index++ {
						seg := canonical[index]
						reqs = append(reqs, scriptpkg.EntityExtractionRequest{
							SegmentID: seg.ID, Text: seg.Text, Title: plan.Title,
							Language: plan.Language, Device: extractionDevice(plan),
							Model: plan.Model, EntityCount: limits.entities,
							UnderstandingModelVersion: plan.Model,
							PromptVersion:             plan.PromptVersion,
							SpecScene:                 segmentSpecSceneContext(input.SpecScene, seg),
						})
					}
					results, err := batchExtractor.ExtractEntitiesBatch(workerCtx, reqs)
					if err != nil {
						outcomes <- extractionOutcome{index: start, err: err}
						continue
					}
					byID := make(map[string]*scriptpkg.EntityResult, len(results))
					for _, result := range results {
						byID[result.SegmentID] = result.Result
					}
					for index := start; index < end; index++ {
						canonicalSeg := canonical[index]
						res := byID[canonicalSeg.ID]
						if res == nil {
							outcomes <- extractionOutcome{index: index, err: fmt.Errorf("entity batch missing segment %q", canonicalSeg.ID)}
							continue
						}
						if p.metrics != nil {
							p.metrics.IncSegments()
							p.metrics.IncExtractionCache(false)
						}
						seg := buildVidRushSegmentResult(ctx, p.imageSearch, plan, canonicalSeg, res, limits.entities, limits.phrases, limits.words, limits.artlist, limits.images, segmentQueryContext(plan, canonicalSeg))
						seg.Cache.Extraction = "MISS"
						outcomes <- extractionOutcome{index: index, segment: seg}
					}
				}
			}()
		}
		go func() {
			defer close(batchJobs)
			for start := 0; start < len(canonical); start += batchSize {
				select {
				case batchJobs <- start:
				case <-workerCtx.Done():
					return
				}
			}
		}()
		go func() { batchWG.Wait(); close(outcomes) }()
	} else {

		// Extraction is provider/LLM I/O, not CPU-bound work. Keep the worker
		// count bounded and preserve canonical segment order by storing outcomes
		// by input index before aggregating them below.
		workerCount := 4
		if len(canonical) < workerCount {
			workerCount = len(canonical)
		}
		jobs := make(chan int)
		var wg sync.WaitGroup
		for worker := 0; worker < workerCount; worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for index := range jobs {
					outcome := p.enrichSegment(workerCtx, plan, input.SpecScene, canonical[index], limits)
					outcomes <- extractionOutcome{index: index, segment: outcome.segment, cached: outcome.cached, unavailable: outcome.unavailable, err: outcome.err}
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
	}

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
	if len(materializedScenes) > 0 && len(materializedScenes) != len(sceneInput.Scenes) {
		sceneInput.Version = 1
		sceneInput.Scenes = materializedScenes
	}
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
	dst.NounChunks = uniqueLimitedStrings(append(dst.NounChunks, seg.Insights.NounChunks...), 5)
}

func projectProfileToVidRushSegment(seg scriptpkg.VidRushSegmentResult, profile scriptpkg.SegmentSemanticProfile) scriptpkg.VidRushSegmentResult {
	seg.Insights.ImportantPhrases = append([]string(nil), profile.ImportantPhrases...)
	seg.Insights.ImportantWords = uniqueLimitedStrings(weightedKeywordValues(profile.Keywords), 5)
	seg.Insights.Entities = uniqueLimitedEntities(profile.Entities, 5)
	seg.Insights.NounChunks = append([]string(nil), profile.NounChunks...)
	if profile.Retrieval != nil {
		seg.Insights.ArtlistQueries = uniqueLimitedStrings(profile.Retrieval.Artlist, 5)
		seg.Insights.YouTubeQueries = uniqueLimitedStrings(profile.Retrieval.YouTube, 5)
		seg.Insights.ImageQueries = uniqueLimitedStrings(profile.Retrieval.Images, 5)
	}
	return seg
}

func buildVidRushSegmentResult(
	ctx context.Context,
	resolver *capabilityimagesearch.Resolver,
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
	if res == nil {
		res = &scriptpkg.EntityResult{}
	}
	cleanText, explicitArtlist := scriptpkg.ParseArtlistDirectives(canonicalSeg.Text)
	canonicalSeg.Text = cleanText
	canonicalSeg.TextHash = segmentTextHash(cleanText)
	canonicalSeg.ArtlistKeywords = explicitArtlist
	insights := scriptpkg.SegmentInsights{
		SegmentID:         canonicalSeg.ID,
		TextHash:          canonicalSeg.TextHash,
		ArtlistIntentHash: scriptpkg.ArtlistSearchIntentHash(append(append([]string(nil), plan.ArtlistKeywords...), explicitArtlist...)),
	}
	// SINGLE canonical point: the typed extraction result evolves into the
	// segment profile via kernel/script.BuildSegmentSemanticProfile, and every
	// legacy insight below is projected FROM that profile. No parallel
	// extraction-to-insight mapping may exist outside this builder.
	profile := scriptpkg.BuildSegmentSemanticProfile(canonicalSeg, *res, plan.Model, plan.PromptVersion)
	if err := profile.Validate(); err != nil {
		// Extraction data is an untrusted model boundary. Keep invalid
		// profiles out of query generation and persistence rather than
		// allowing malformed confidence/identity values downstream.
		return scriptpkg.VidRushSegmentResult{
			SegmentID: canonicalSeg.ID, SceneID: canonicalSeg.SceneID,
			Position: canonicalSeg.Position, Text: canonicalSeg.Text,
			TextHash: canonicalSeg.TextHash,
		}
	}
	insights.Entities = uniqueLimitedEntities(profile.Entities, entitiesLimit)
	insights.NounChunks = uniqueLimitedStrings(profile.NounChunks, phrasesLimit)
	insights.ImportantPhrases = uniqueLimitedStrings(profile.ImportantPhrases, phrasesLimit)
	// Keep the per-segment insight contract total for short canonical
	// segments (for example a one-word section heading). The fallback is
	// the segment text itself, never a model-generated or hardcoded value.
	if len(insights.ImportantPhrases) == 0 && strings.TrimSpace(canonicalSeg.Text) != "" && phrasesLimit > 0 {
		insights.ImportantPhrases = []string{strings.TrimSpace(canonicalSeg.Text)}
	}
	insights.ImportantWords = uniqueLimitedStrings(weightedKeywordValues(profile.Keywords), wordsLimit)

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
		// Artlist consumes the provider projection of the canonical profile.
		// Editorial phrases and words are deliberately not a retrieval fallback:
		// they describe importance, not necessarily visible footage. Explicit
		// plan directives remain an intentional provider override.
		builders := NewVidRushProviderQueryBuilders()
		profileArtlistQueries := builders.Artlist(profile)
		explicitQueries := append(append([]string(nil), plan.ArtlistKeywords...), explicitArtlist...)
		queries := append(explicitQueries, profileArtlistQueries...)
		// The source-text query is a bounded lexical supplement for degraded
		// local extraction (where the model returns neither noun chunks nor
		// entities). It never consumes editorial phrases or words and is
		// always placed after the canonical profile projection.
		queries = append(queries, buildArtlistQueries(visualText, nil, insights.Entities, nil, nil, "")...)
		insights.ArtlistQueries = uniqueLimitedStrings(queries, artlistLimit)
	}

	imagePhrases := append([]string(nil), weightedKeywordValues(profile.VisualTerms)...)
	manualImageQueries := ResolveManualSegmentQueries(plan, canonicalSeg, scriptpkg.VidRushProviderInternetImages, mediadomain.SlotSecondaryImage)
	if len(manualImageQueries) > 0 {
		insights.ImageQueries = uniqueLimitedStrings(manualImageQueries, imageLimit)
	} else if !hasLockedSegmentAssignment(plan.MediaPlan.Assignments, canonicalSeg.ID, mediadomain.SlotSecondaryImage) {
		outcome := resolveImageQueries(ctx, resolver, plan, visualText, insights, imagePhrases, imageLimit)
		insights.ImageQueries = outcome.queries
		insights.ImageSearchRequired = outcome.required
		insights.ImageSearchNoImageReason = outcome.noImageReason
		// The resolver's chosen canonical identities travel with the segment:
		// the scene-annotation projection stamps them onto the annotated
		// entities so the overlay media index joins on the SAME identity the
		// resolver chose (never a re-derivation from a different surface).
		insights.ImagePrimaryCanonicalID = outcome.primaryCanonicalID
		insights.ImageEntityCanonicalIDs = outcome.canonicalIDs
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

// imageSearchOutcome is the resolved image search decision for one segment:
// the ordered queries plus the resolver's chosen canonical identities (the
// primary entity's canonical id and the per-surface id map the annotation
// projection stamps).
type imageSearchOutcome struct {
	queries       []string
	required      bool
	noImageReason string
	// primaryCanonicalID is the resolver's chosen primary entity canonical
	// id (e.g. "person:floyd-mayweather"); empty when the resolver is not
	// wired or the decision is no-image.
	primaryCanonicalID string
	// canonicalIDs maps each decision entity's lowercased surface/canonical
	// text to its canonical id. Never nil for a resolver-wired outcome.
	canonicalIDs map[string]string
}

// resolveImageQueries produces the segment's image search queries. When the
// deterministic Image Search Intent resolver (capabilities/imagesearch) is
// wired, its decision drives the fan-out: ordered queries (primary first,
// negated entities excluded, value entities MONEY/DATE/EVENT routed to the
// visual system) on Required=true, and NO queries on Required=false. The
// empty set is what disables the internet-images provider (the fan-out gates
// on len(ImageQueries) > 0), so the battery-certified no-image decision
// (T24/T25/T26: abstract sentences must not force an image search) takes
// effect end-to-end; the Artlist video path stays independent and still
// covers anonymous-but-visual B-roll. Without a resolver the legacy ad-hoc
// builder is used unchanged (Required=true).
func resolveImageQueries(
	ctx context.Context,
	resolver *capabilityimagesearch.Resolver,
	plan *scriptpkg.ResolvedGenerationPlan,
	visualText string,
	insights scriptpkg.SegmentInsights,
	imagePhrases []string,
	imageLimit int,
) imageSearchOutcome {
	if resolver != nil {
		decision := resolver.Resolve(ctx, capabilityimagesearch.Request{
			Text: visualText, Language: plan.Language,
		})
		outcome := imageSearchOutcome{
			required:      decision.Required,
			noImageReason: decision.NoImageReason,
			canonicalIDs:  map[string]string{},
		}
		for _, entity := range decision.Entities {
			stampCanonicalID(outcome.canonicalIDs, entity)
		}
		if decision.Primary != nil {
			outcome.primaryCanonicalID = decision.Primary.CanonicalID
			stampCanonicalID(outcome.canonicalIDs, *decision.Primary)
		}
		if !decision.Required {
			return outcome
		}
		if len(decision.Queries) > 0 {
			outcome.queries = uniqueLimitedStrings(decision.Queries, imageLimit)
			return outcome
		}
		return outcome
	}
	return imageSearchOutcome{
		queries:      uniqueLimitedStrings(buildImageQueries(visualText, insights.Entities, imagePhrases, insights.ImportantWords, plan.Topic), imageLimit),
		required:     true,
		canonicalIDs: map[string]string{},
	}
}

// stampCanonicalID registers every retrievable spelling of a decision entity
// (canonical text, query surface, verbatim source surface) under its chosen
// canonical id, so the annotation projection can join on whichever spelling
// the extractor produced.
func stampCanonicalID(dst map[string]string, entity capabilityimagesearch.ResolvedEntity) {
	if entity.CanonicalID == "" {
		return
	}
	for _, surface := range []string{entity.Text, entity.QueryName, entity.Verbatim} {
		key := strings.ToLower(strings.TrimSpace(surface))
		if key == "" {
			continue
		}
		dst[key] = entity.CanonicalID
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
	if segment.Position >= 0 && segment.Position < len(plan.Segments) {
		if topic := strings.TrimSpace(plan.Segments[segment.Position].Topic); topic != "" {
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

// segmentExtractionLimits bundles the bounded per-segment extraction limits
// resolved from the generation plan. They are resolved once per Process call
// and shared by every segment so no segment can diverge from the plan policy.
type segmentExtractionLimits struct {
	entities int
	phrases  int
	words    int
	artlist  int
	images   int
}

// segmentEnrichmentOutcome is the immutable result of enriching one canonical
// segment. Exactly one of segment, unavailable, or err is populated.
type segmentEnrichmentOutcome struct {
	segment     scriptpkg.VidRushSegmentResult
	cached      bool
	unavailable error
	err         error
}

// enrichSegment runs the complete single-segment VidRush enrichment for one
// canonical segment: durable cache lookup, single-slot extraction, query
// construction and cache write. It is the reusable per-segment owner shared
// by the batch processor's worker path. It never mutates shared scene state;
// it returns an immutable segment result. The batch transport path shares the
// same query/result builders but skips per-segment caching for its serialized
// model call.
func (e *VidRushSegmentEnricher) enrichSegment(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, specScene scriptpkg.SpecSceneOutput, canonicalSeg scriptpkg.CanonicalSegment, limits segmentExtractionLimits) segmentEnrichmentOutcome {
	outcome := segmentEnrichmentOutcome{}
	device := extractionDevice(plan)
	if e.metrics != nil {
		e.metrics.IncSegments()
	}
	cacheKey := segmentCacheKey(
		"extraction-v4-local-v1",
		canonicalSeg.TextHash,
		plan.Language,
		plan.Model,
		plan.PromptVersion,
		fmt.Sprintf("%d", limits.entities),
		fmt.Sprintf("%d", limits.phrases),
		fmt.Sprintf("%d", limits.words),
		fmt.Sprintf("%d", limits.artlist),
		fmt.Sprintf("%d", limits.images),
		device,
	)
	if !plan.MediaPlan.ForceRefreshExtraction {
		if cached, ok := cacheLoad(&vidrushExtractionCache, cacheKey); ok {
			if seg, ok := cached.(scriptpkg.VidRushSegmentResult); ok {
				seg = cloneVidRushSegmentResult(seg)
				seg.Cache.Extraction = "HIT_EXACT"
				if e.metrics != nil {
					e.metrics.IncExtractionCache(true)
				}
				outcome.segment = seg
				outcome.cached = true
				return outcome
			}
		}
		var persisted scriptpkg.VidRushSegmentResult
		if hit, cacheErr := loadVidRushPersistentJSON(ctx, e.cache, "extraction", cacheKey, &persisted); cacheErr != nil {
			outcome.err = cacheErr
			return outcome
		} else if hit {
			persisted = cloneVidRushSegmentResult(persisted)
			persisted.Cache.Extraction = "HIT_EXACT"
			cacheStore(&vidrushExtractionCache, cacheKey, persisted)
			if e.metrics != nil {
				e.metrics.IncExtractionCache(true)
			}
			outcome.segment = persisted
			outcome.cached = true
			return outcome
		}
	}

	select {
	case e.extractionGate <- struct{}{}:
	case <-ctx.Done():
		outcome.err = ctx.Err()
		return outcome
	}
	request := scriptpkg.EntityExtractionRequest{
		SegmentID:                 canonicalSeg.ID,
		Text:                      canonicalSeg.Text,
		Title:                     plan.Title,
		Language:                  plan.Language,
		Device:                    device,
		Model:                     plan.Model,
		EntityCount:               limits.entities,
		UnderstandingModelVersion: plan.Model,
		PromptVersion:             plan.PromptVersion,
		SpecScene:                 segmentSpecSceneContext(specScene, canonicalSeg),
	}
	res, err := e.extractor.ExtractEntities(ctx, request)
	// A malformed/too-short generated scene must not erase the semantic
	// evidence already resolved by the research source. Retry the same typed
	// extractor only against this segment's canonical source context. Never
	// fall back to the complete document: that would cross-contaminate scenes.
	if err == nil && !entityResultHasValues(res) {
		sourceText := strings.TrimSpace(segmentQueryContext(plan, canonicalSeg))
		if sourceText != "" && sourceText != request.Text {
			request.Text = sourceText
			res, err = e.extractor.ExtractEntities(ctx, request)
		}
	}
	<-e.extractionGate
	if err != nil {
		if errors.Is(err, ErrEntityExtractorUnavailable) {
			outcome.unavailable = err
		} else {
			outcome.err = err
		}
		return outcome
	}
	if res == nil {
		res = &scriptpkg.EntityResult{}
	}
	seg := buildVidRushSegmentResult(ctx, e.imageSearch, plan, canonicalSeg, res, limits.entities, limits.phrases, limits.words, limits.artlist, limits.images, segmentQueryContext(plan, canonicalSeg))
	if e.understanding != nil {
		profile := profileFromVidRushSegment(seg)
		understood, understandErr := e.understanding.UnderstandProfile(ctx, canonicalSeg, profile, plan.Language, plan.Model, plan.PromptVersion)
		if understandErr != nil {
			outcome.err = understandErr
			return outcome
		}
		seg = projectProfileToVidRushSegment(seg, understood)
	}
	seg.Cache.Extraction = "MISS"
	if plan.MediaPlan.ForceRefreshExtraction {
		seg.Cache.Extraction = "REFRESHED"
	}
	if e.metrics != nil {
		e.metrics.IncExtractionCache(false)
	}
	cacheStore(&vidrushExtractionCache, cacheKey, seg)
	if cacheErr := storeVidRushPersistentJSON(ctx, e.cache, "extraction", cacheKey, seg); cacheErr != nil {
		outcome.err = cacheErr
		return outcome
	}
	outcome.segment = seg
	return outcome
}

// Enrich implements the SegmentEnricher contract for a single stable scene.
// It converts the scene into its canonical segment identity, then runs the
// shared single-segment enrichment. The returned result is immutable and
// keyed by the scene content hash.
func (e *VidRushSegmentEnricher) Enrich(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	if !scene.AllowsNLP() {
		return scriptpkg.VidRushSegmentResult{}, nil
	}
	if e.extractor == nil {
		return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("%w: entities enricher: EntityExtractor not configured", scriptpkg.ErrPostprocessFailed)
	}
	if plan == nil {
		return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("%w: entities enricher: nil ResolvedGenerationPlan", scriptpkg.ErrPostprocessFailed)
	}
	if strings.TrimSpace(scene.Text) == "" {
		return scriptpkg.VidRushSegmentResult{}, nil
	}
	canonicalSeg := canonicalSegmentFromScene(scene)
	envelope := scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{scene}}
	outcome := e.enrichSegment(ctx, plan, envelope, canonicalSeg, resolveExtractionLimits(plan))
	if outcome.unavailable != nil {
		return scriptpkg.VidRushSegmentResult{}, outcome.unavailable
	}
	if outcome.err != nil {
		return scriptpkg.VidRushSegmentResult{}, outcome.err
	}
	return outcome.segment, nil
}

// resolveExtractionLimits derives the bounded per-segment extraction limits
// from the plan. It is the single source of truth shared by the batch
// processor and the single-segment Enrich path.
func resolveExtractionLimits(plan *scriptpkg.ResolvedGenerationPlan) segmentExtractionLimits {
	limits := segmentExtractionLimits{entities: 5, phrases: 5, words: 5, artlist: 5, images: 5}
	if plan.MediaPlan.Extraction.MaxEntitiesPerSegment > 0 {
		limits.entities = plan.MediaPlan.Extraction.MaxEntitiesPerSegment
	}
	if plan.MediaPlan.Extraction.MaxImportantPhrasesPerSegment > 0 {
		limits.phrases = plan.MediaPlan.Extraction.MaxImportantPhrasesPerSegment
	}
	// The annotation contract is scene-level: retain at most one key
	// statement per scene, regardless of a wider legacy extraction limit.
	if limits.phrases > 1 {
		limits.phrases = 1
	}
	if plan.MediaPlan.Extraction.MaxImportantWordsPerSegment > 0 {
		limits.words = plan.MediaPlan.Extraction.MaxImportantWordsPerSegment
	}
	if plan.MediaPlan.Extraction.MaxArtlistQueriesPerSegment > 0 {
		limits.artlist = plan.MediaPlan.Extraction.MaxArtlistQueriesPerSegment
	}
	if plan.MediaPlan.Extraction.MaxImageQueriesPerSegment > 0 {
		limits.images = plan.MediaPlan.Extraction.MaxImageQueriesPerSegment
	}
	return limits
}

// canonicalSegmentFromScene builds the stable segment identity for one scene,
// preserving the scene's canonical index and computing its content hash.
func canonicalSegmentFromScene(scene scriptpkg.SpecScene) scriptpkg.CanonicalSegment {
	segText := strings.TrimSpace(scene.Text)
	id := strings.TrimSpace(scene.SegmentID)
	if id == "" {
		id = strings.TrimSpace(scene.ID)
	}
	return scriptpkg.CanonicalSegment{
		ID: id, SceneID: strings.TrimSpace(scene.ID), Position: scene.Index,
		Text: segText, SourceText: segText,
		TextHash: segmentTextHash(segText), SourceTextHash: segmentTextHash(segText),
	}
}
