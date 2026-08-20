package adapters

import (
	"context"
	"fmt"
	"golang.org/x/text/unicode/norm"
	"strings"
	"sync"
	"unicode"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// internetImageCachePayload stores only the image-provider delta so a cache
// hit cannot replace Artlist candidates or other upstream segment state.
type internetImageCachePayload struct {
	Candidates []scriptpkg.SegmentAssetCandidate
}

var (
	entityImageCache sync.Map // canonical entity key -> []SegmentAssetCandidate
	entityImageLocks sync.Map // canonical entity key -> *sync.Mutex
)

// InternetImagesProcessor searches web images per canonical segment and
// attaches every unique result returned for the segment queries.
type InternetImagesProcessor struct {
	searcher InternetImageSearcher
	metrics  VidRushMetrics
	cache    scriptports.VidRushCachePort
}

func NewInternetImagesProcessor(searcher InternetImageSearcher, metrics ...VidRushMetrics) *InternetImagesProcessor {
	return NewInternetImagesProcessorWithCache(searcher, nil, metrics...)
}

func NewInternetImagesProcessorWithCache(searcher InternetImageSearcher, cache scriptports.VidRushCachePort, metrics ...VidRushMetrics) *InternetImagesProcessor {
	var m VidRushMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &InternetImagesProcessor{searcher: searcher, metrics: m, cache: cache}
}

func (p *InternetImagesProcessor) Name() ProcessorName { return ProcessorInternetImages }

func (p *InternetImagesProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

func (p *InternetImagesProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if plan == nil {
		return &PostProcessResult{}, nil
	}
	cacheOnly := plan.MediaPlan.Mode == mediadomain.MediaPlanModeCacheOnly
	entityImagesEnabled := plan.MediaPlan.Extraction.EntityImages.Enabled
	if !plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() && !entityImagesEnabled {
		segments := make([]scriptpkg.VidRushSegmentResult, 0, len(input.VidRushSegments))
		for _, segment := range input.VidRushSegments {
			cloned := cloneVidRushSegmentResult(segment)
			cloned.Cache.InternetImages = "BYPASSED"
			segments = append(segments, cloned)
		}
		if len(segments) == 0 {
			return &PostProcessResult{}, nil
		}
		return &PostProcessResult{VidRushSegments: segments, Changed: true}, nil
	}
	if p.searcher == nil && !cacheOnly {
		return &PostProcessResult{
			Changed:  true,
			Warnings: []string{"internet_images: InternetImageSearcher not configured"},
		}, nil
	}
	if len(input.VidRushSegments) == 0 {
		return &PostProcessResult{}, nil
	}
	perQueryLimit := 10
	if plan.MediaPlan.Planner.CandidateLimit > 0 {
		perQueryLimit = plan.MediaPlan.Planner.CandidateLimit
	}
	if perQueryLimit > 50 {
		perQueryLimit = 50
	}

	// Pre-index scenes by identity so each segment's entity-image query
	// lookup is O(1) instead of a full O(scenes) scan per segment (the
	// same index shape used by projectEntityImageBindings).
	var sceneIdx sceneIdentityIndex
	if entityImagesEnabled {
		sceneIdx = buildSceneIdentityIndex(input.SpecScene)
	}

	updatedSegments := make([]scriptpkg.VidRushSegmentResult, 0, len(input.VidRushSegments))
	var warnings []string
	for _, seg := range input.VidRushSegments {
		updated := cloneVidRushSegmentResult(seg)
		imageQueries := updated.Insights.ImageQueries
		// Explicit media_plan.searches are the caller's retrieval intent and
		// take precedence over entity-image expansion for the image slot.
		manualImageQueries := ResolveManualSegmentQueries(plan, scriptpkg.CanonicalSegment{ID: updated.SegmentID}, scriptpkg.VidRushProviderInternetImages, mediadomain.SlotSecondaryImage)
		if len(manualImageQueries) > 0 {
			imageQueries = manualImageQueries
		} else if entityImagesEnabled {
			if entityQueries := scenePrimaryEntityQueries(input.SpecScene, sceneIdx, updated); len(entityQueries) > 0 {
				imageQueries = entityQueries
			}
		}
		if len(imageQueries) == 0 {
			updated.Cache.InternetImages = "BYPASSED"
			updatedSegments = append(updatedSegments, updated)
			continue
		}

		cacheKey := segmentCacheKey(
			// Query text is an LLM-derived retrieval hint, not scene
			// identity. Keep it out of the durable key so a warm replay
			// reuses the same scene assets even if extraction wording
			// changes between runs.
			"internet-images-assets-v3",
			updated.SegmentID,
			updated.TextHash,
			plan.Language,
			plan.Model,
			plan.PromptVersion,
			fmt.Sprintf("%d", perQueryLimit),
		)
		// cache_only is an absolute no-provider contract. A forced refresh flag
		// must not turn it into an external search; it may only replay a warm
		// materialized result.
		if cacheOnly || (!plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh) {
			if cached, ok := cacheLoad(&vidrushImageCache, cacheKey); ok {
				if payload, ok := cached.(internetImageCachePayload); ok {
					candidates := append([]scriptpkg.SegmentAssetCandidate(nil), payload.Candidates...)
					updated.Assets.Candidates = appendProviderCandidatesUnique(updated.Assets.Candidates, candidates)
					updated.Assets.SecondaryImages = appendProviderCandidatesUnique(updated.Assets.SecondaryImages, candidates)
					updated.Cache.InternetImages = "HIT_EXACT"
					updatedSegments = append(updatedSegments, updated)
					if p.metrics != nil {
						p.metrics.IncAssetCache("internet_images", true)
					}
					continue
				}
			}
			var persisted internetImageCachePayload
			if hit, cacheErr := loadVidRushPersistentJSON(ctx, p.cache, "internet_images", cacheKey, &persisted); cacheErr != nil {
				return nil, cacheErr
			} else if hit {
				persisted.Candidates = append([]scriptpkg.SegmentAssetCandidate(nil), persisted.Candidates...)
				updated.Assets.Candidates = appendProviderCandidatesUnique(updated.Assets.Candidates, persisted.Candidates)
				updated.Assets.SecondaryImages = appendProviderCandidatesUnique(updated.Assets.SecondaryImages, persisted.Candidates)
				updated.Cache.InternetImages = "HIT_EXACT"
				if len(persisted.Candidates) > 0 {
					cacheStore(&vidrushImageCache, cacheKey, persisted)
				}
				updatedSegments = append(updatedSegments, updated)
				if p.metrics != nil {
					p.metrics.IncAssetCache("internet_images", true)
				}
				continue
			}
		}
		if cacheOnly {
			updated.Cache.InternetImages = "CACHE_MISS"
			warnings = append(warnings, fmt.Sprintf("internet_images: cache-only miss for segment %s", updated.SegmentID))
			updatedSegments = append(updatedSegments, updated)
			continue
		}

		if p.metrics != nil {
			p.metrics.IncAssetCache("internet_images", false)
		}

		candidates := make([]scriptpkg.SegmentAssetCandidate, 0, perQueryLimit*len(imageQueries))
		seen := make(map[string]struct{}, cap(candidates))
		firstEntity := ""
		if len(updated.Insights.Entities) > 0 {
			firstEntity = strings.TrimSpace(updated.Insights.Entities[0].Value)
		}
		type queryResult struct {
			candidates []scriptpkg.SegmentAssetCandidate
			query      string
			fromCache  bool
			err        error
		}
		queryResults, mapErr := concurrent.Map(ctx, imageQueries, 4, func(ctx context.Context, _ int, query string) (queryResult, error) {
			// The per-query cache is keyed on (topic, query, language), NOT
			// on the segment TextHash. On the research path the generated
			// scene text (and therefore its TextHash) is non-deterministic
			// across runs, but the topic and the derived entity/image query
			// are stable when the research source is stable. Keying on them
			// lets a warm replay reuse the same assets without re-calling
			// the provider, even though entity_images binding is disabled.
			entityCacheKey := segmentCacheKey("entity-image-v1", strings.ToLower(strings.TrimSpace(plan.Topic)), strings.ToLower(strings.TrimSpace(query)), plan.Language)
			if !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh {
				if cached, ok := cacheLoad(&entityImageCache, entityCacheKey); ok {
					if cachedCandidates, ok := cached.([]scriptpkg.SegmentAssetCandidate); ok {
						return queryResult{candidates: append([]scriptpkg.SegmentAssetCandidate(nil), cachedCandidates...), query: query, fromCache: true}, nil
					}
				}
				var persisted []scriptpkg.SegmentAssetCandidate
				if hit, err := loadVidRushPersistentJSON(ctx, p.cache, "entity_images", entityCacheKey, &persisted); err != nil {
					return queryResult{}, err
				} else if hit {
					// Never promote an empty result into the no-TTL L1 map: a
					// persistent empty hit is re-read from L2 on each warm replay.
					if len(persisted) > 0 {
						cacheStore(&entityImageCache, entityCacheKey, persisted)
					}
					return queryResult{candidates: persisted, query: query, fromCache: true}, nil
				}
			}
			var entityLock *sync.Mutex
			if !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh {
				actual, _ := entityImageLocks.LoadOrStore(entityCacheKey, &sync.Mutex{})
				entityLock = actual.(*sync.Mutex)
				entityLock.Lock()
				defer entityLock.Unlock()
				if cached, ok := cacheLoad(&entityImageCache, entityCacheKey); ok {
					if cachedCandidates, ok := cached.([]scriptpkg.SegmentAssetCandidate); ok {
						return queryResult{candidates: append([]scriptpkg.SegmentAssetCandidate(nil), cachedCandidates...), query: query, fromCache: true}, nil
					}
				}
			}
			if p.metrics != nil {
				// Count an actual provider invocation, not a segment-level
				// cache miss. Entity-image L2/L1 hits below may satisfy the
				// query without calling the external searcher.
				p.metrics.IncProviderRequest("internet_images")
			}
			var results []scriptpkg.SegmentAssetCandidate
			err := measureVidRushProvider(ctx, p.metrics, kernobs.OperationInfo{
				Stage: kernobs.StageAcquire, Component: "vidrush", Operation: "search", Provider: "internet_images",
			}, func(callCtx context.Context) error {
				var searchErr error
				results, searchErr = p.searcher.SearchImages(callCtx, InternetImageSearchRequest{
					SegmentID: updated.SegmentID, Query: query, Entity: firstEntity,
					TextHash: updated.TextHash, Language: plan.Language, Limit: perQueryLimit,
					Provider: "internet_images",
				})
				return searchErr
			})
			if err == nil {
				// Empty results are durable-cached in L2 (TTL 48h) so a warm
				// replay of the same query does not re-call the provider, but
				// they are kept out of the no-TTL L1 map to avoid unbounded
				// growth of empty in-memory entries.
				if len(results) > 0 {
					cacheStore(&entityImageCache, entityCacheKey, append([]scriptpkg.SegmentAssetCandidate(nil), results...))
				}
				if results == nil {
					results = []scriptpkg.SegmentAssetCandidate{}
				}
				if cacheErr := storeVidRushPersistentJSON(ctx, p.cache, "entity_images", entityCacheKey, results); cacheErr != nil {
					return queryResult{}, cacheErr
				}
			}
			return queryResult{candidates: results, query: query, err: err}, nil
		})
		if mapErr != nil {
			warnings = append(warnings, fmt.Sprintf("internet_images: bounded query fan-out failed for segment %s: %v", updated.SegmentID, mapErr))
		}
		for _, queryResult := range queryResults {
			if queryResult.err != nil {
				if p.metrics != nil {
					p.metrics.IncProviderFailure("internet_images")
				}
				warnings = append(warnings, fmt.Sprintf("internet_images: search failed for segment %s: %v", updated.SegmentID, queryResult.err))
				continue
			}
			for _, cand := range queryResult.candidates {
				if cand.Provider == "" {
					cand.Provider = "internet_images"
				}
				if cand.Query == "" {
					cand.Query = queryResult.query
				}
				// Defense-in-depth: reject candidates from forbidden providers.
				// The binding gate (validVidRushCandidate) also rejects these,
				// but filtering at ingest time prevents forbidden candidates
				// from polluting cache entries.
				if strings.ToLower(strings.TrimSpace(cand.Provider)) != "internet_images" {
					continue
				}
				if cand.RightsStatus == "" {
					cand.RightsStatus = "unknown"
				}
				if cand.SelectionReason == "" {
					cand.SelectionReason = "retrieved image candidate matching a segment entity/query"
				}
				key := vidRushCandidateIdentity(cand)
				if key == "" {
					continue
				}
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				candidates = append(candidates, cand)
			}
		}

		allCacheHits := mapErr == nil && len(queryResults) > 0
		for _, qr := range queryResults {
			if !qr.fromCache {
				allCacheHits = false
				break
			}
		}
		updated.Cache.InternetImages = "MISS"
		if plan.MediaPlan.ForceRefreshAssets {
			updated.Cache.InternetImages = "REFRESHED"
		} else if allCacheHits {
			updated.Cache.InternetImages = "HIT_EXACT"
		}
		if len(candidates) > 0 {
			updated.Assets.Candidates = appendProviderCandidatesUnique(updated.Assets.Candidates, candidates)
			updated.Assets.SecondaryImages = appendProviderCandidatesUnique(updated.Assets.SecondaryImages, candidates)
		}
		payload := internetImageCachePayload{
			Candidates: append([]scriptpkg.SegmentAssetCandidate(nil), candidates...),
		}
		if len(payload.Candidates) > 0 {
			cacheStore(&vidrushImageCache, cacheKey, payload)
		}
		// Empty provider results are durable-cached in L2 (TTL 48h) so a warm
		// replay of the same segment is deterministic and does not re-call the
		// provider, but they stay out of the no-TTL L1 map to avoid unbounded
		// growth of empty in-memory entries.
		if cacheErr := storeVidRushPersistentJSON(ctx, p.cache, "internet_images", cacheKey, payload); cacheErr != nil {
			return nil, cacheErr
		}
		updatedSegments = append(updatedSegments, updated)
	}

	updatedSpecScene := projectEntityImageBindings(input.SpecScene, updatedSegments, plan.MediaPlan.Extraction.EntityImages)
	return &PostProcessResult{
		VidRushSegments:  updatedSegments,
		UpdatedSpecScene: updatedSpecScene,
		SpecSceneChanged: len(updatedSpecScene.Scenes) > 0,
		Warnings:         warnings,
		Changed:          len(updatedSegments) > 0,
	}, nil
}

// projectEntityImageBindings attaches only provider candidates that are
// explicitly relevant to the primary entity. Generic scene candidates are
// never promoted to an entity image, preventing unrelated images from being
// presented as a person/org/place match.
func projectEntityImageBindings(spec scriptpkg.SpecSceneOutput, segments []scriptpkg.VidRushSegmentResult, policy mediadomain.EntityImagePolicy) scriptpkg.SpecSceneOutput {
	if !policy.Enabled || len(spec.Scenes) == 0 {
		return spec
	}
	out := cloneSpecSceneOutput(spec)
	allowed := map[string]bool{"PERSON": true, "ORG": true, "GPE": true}
	if len(policy.EntityTypes) > 0 {
		allowed = make(map[string]bool, len(policy.EntityTypes))
		for _, raw := range policy.EntityTypes {
			allowed[normalizeAnnotationType(raw)] = true
		}
	}
	// Pre-index segments by SegmentID and SceneID (first occurrence wins, so
	// the min-index semantics of the old linear scan are preserved). The
	// per-scene lookup then becomes O(1) instead of a full O(segments) scan
	// for every scene.
	bySegmentID := make(map[string]int, len(segments))
	bySceneID := make(map[string]int, len(segments))
	for i := range segments {
		if segments[i].SegmentID != "" {
			if _, ok := bySegmentID[segments[i].SegmentID]; !ok {
				bySegmentID[segments[i].SegmentID] = i
			}
		}
		if segments[i].SceneID != "" {
			if _, ok := bySceneID[segments[i].SceneID]; !ok {
				bySceneID[segments[i].SceneID] = i
			}
		}
	}
	for i := range out.Scenes {
		if out.Scenes[i].Annotations == nil {
			continue
		}
		seg := findSegmentForScene(out.Scenes[i], segments, bySegmentID, bySceneID)
		for entityIndex := range out.Scenes[i].Annotations.PrimaryEntities {
			entity := &out.Scenes[i].Annotations.PrimaryEntities[entityIndex]
			if !allowed[normalizeAnnotationType(entity.Type)] {
				continue
			}
			entity.Image = &scriptpkg.EntityImageBinding{Status: "not_found"}
			if seg == nil {
				continue
			}
			if candidate, ok := findEntityImageCandidate(*entity, *seg); ok {
				entity.Image = &scriptpkg.EntityImageBinding{
					Status: "resolved", AssetID: candidate.AssetID,
					DriveLink: candidate.DriveLink, Source: candidate.Provider,
					License:    candidate.RightsBasis,
					PreviewURL: entityImagePreviewURL(candidate),
					// The verified content address is what lets the binding be
					// promoted into the content-addressed EntityMediaIndex for
					// the entity card asset (bindings without it stay plain
					// references).
					SHA256: candidate.FileHash,
				}
			}
		}
	}
	return out
}

// ProjectEntityImageBindings reapplies the canonical identity-image
// projection to a final scene envelope. It is intentionally exported for the
// persistence boundary: later postprocessors may rebuild annotations while
// retaining the already materialized segment candidates.
func ProjectEntityImageBindings(spec scriptpkg.SpecSceneOutput, segments []scriptpkg.VidRushSegmentResult, policy mediadomain.EntityImagePolicy) scriptpkg.SpecSceneOutput {
	return projectEntityImageBindings(spec, segments, policy)
}

// sceneIdentityIndex pre-indexes spec.Scenes by SegmentID and ID so the
// per-segment scene lookup is O(1) instead of a full O(scenes) scan for
// every segment. firstNoID records the first scene carrying neither
// identity key (the "matches any segment" fallback).
type sceneIdentityIndex struct {
	bySegmentID map[string]int
	bySceneID   map[string]int
	firstNoID   int
}

// buildSceneIdentityIndex builds the scene identity index. First-occurrence
// wins per key so min-index semantics match the old linear scan exactly.
func buildSceneIdentityIndex(spec scriptpkg.SpecSceneOutput) sceneIdentityIndex {
	idx := sceneIdentityIndex{
		bySegmentID: make(map[string]int, len(spec.Scenes)),
		bySceneID:   make(map[string]int, len(spec.Scenes)),
		firstNoID:   -1,
	}
	for i := range spec.Scenes {
		s := spec.Scenes[i]
		switch {
		case s.SegmentID != "":
			if _, ok := idx.bySegmentID[s.SegmentID]; !ok {
				idx.bySegmentID[s.SegmentID] = i
			}
		case s.ID != "":
			if _, ok := idx.bySceneID[s.ID]; !ok {
				idx.bySceneID[s.ID] = i
			}
		default:
			if idx.firstNoID == -1 {
				idx.firstNoID = i
			}
		}
	}
	return idx
}

// sceneFor returns the index of the first scene matching the segment,
// mirroring the original linear-scan precedence: SegmentID match, then
// ID match, then the first identity-less scene — whichever is earliest.
func (idx sceneIdentityIndex) sceneFor(segment scriptpkg.VidRushSegmentResult) int {
	best := -1
	if segment.SegmentID != "" {
		if i, ok := idx.bySegmentID[segment.SegmentID]; ok {
			best = i
		}
	}
	if segment.SceneID != "" {
		if i, ok := idx.bySceneID[segment.SceneID]; ok && (best == -1 || i < best) {
			best = i
		}
	}
	if idx.firstNoID != -1 && (best == -1 || idx.firstNoID < best) {
		best = idx.firstNoID
	}
	return best
}

func scenePrimaryEntityQueries(spec scriptpkg.SpecSceneOutput, idx sceneIdentityIndex, segment scriptpkg.VidRushSegmentResult) []string {
	best := idx.sceneFor(segment)
	if best == -1 {
		return nil
	}
	scene := spec.Scenes[best]
	if scene.Annotations == nil {
		return nil
	}
	queries := make([]string, 0, len(scene.Annotations.PrimaryEntities))
	seen := make(map[string]struct{}, len(queries))
	for _, entity := range scene.Annotations.PrimaryEntities {
		if entity.Type != "PERSON" && entity.Type != "ORG" && entity.Type != "GPE" {
			continue
		}
		query := strings.TrimSpace(entity.CanonicalName)
		if query == "" {
			query = strings.TrimSpace(entity.Text)
		}
		key := strings.ToLower(query)
		if query == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		queries = append(queries, query)
	}
	return queries
}

func findSegmentForScene(scene scriptpkg.SpecScene, segments []scriptpkg.VidRushSegmentResult, bySegmentID, bySceneID map[string]int) *scriptpkg.VidRushSegmentResult {
	if scene.SegmentID == "" && scene.ID == "" {
		// Positional fallback for scenes that carry neither identity key.
		for i := range segments {
			if scene.Index == segments[i].Position {
				return &segments[i]
			}
		}
		return nil
	}
	// The old single-pass scan returned the first index where EITHER key
	// matched; taking the min of the two first-occurrence indices preserves
	// that exactly.
	best := -1
	if scene.SegmentID != "" {
		if i, ok := bySegmentID[scene.SegmentID]; ok {
			best = i
		}
	}
	if scene.ID != "" {
		if i, ok := bySceneID[scene.ID]; ok && (best == -1 || i < best) {
			best = i
		}
	}
	if best == -1 {
		return nil
	}
	return &segments[best]
}

func findEntityImageCandidate(entity scriptpkg.AnnotatedEntity, seg scriptpkg.VidRushSegmentResult) (scriptpkg.SegmentAssetCandidate, bool) {
	want := normalizeEntityMatch(entity.CanonicalName)
	if want == "" {
		want = normalizeEntityMatch(entity.Text)
	}
	// Small local models sometimes turn prompt instructions into the
	// extracted entity text (for example, "Describe John Cena").  The
	// provider query and the public person's canonical name are still
	// "John Cena", so remove that non-semantic instruction prefix before
	// comparing the entity with a retrieved candidate.
	want = strings.TrimSpace(strings.TrimPrefix(want, "describe "))
	all := append(append([]scriptpkg.SegmentAssetCandidate(nil), seg.Assets.Candidates...), seg.Assets.SecondaryImages...)
	for _, candidate := range all {
		if !validVidRushCandidate(candidate) || strings.TrimSpace(candidate.AssetID) == "" {
			continue
		}
		// Entity images are internet-images-only by contract: a candidate from
		// any other provider (even a fully materialized video whose query
		// matches the person) must never bind as a person/org/place image.
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) {
			continue
		}
		entityText := normalizeEntityMatch(candidate.Entity)
		query := normalizeEntityMatch(candidate.Query)
		candidateQuery := strings.TrimSpace(strings.TrimPrefix(query, "describe "))
		candidateEntity := strings.TrimSpace(strings.TrimPrefix(entityText, "describe "))
		if candidateQuery == want || candidateEntity == want || strings.Contains(candidateQuery, want) || strings.Contains(candidateEntity, want) {
			// Search results are projected once before acquisition and again
			// after Drive/SQLite/Qdrant materialization. Prefer the durable
			// candidate on the second pass; otherwise an early discovered hit
			// can leave the document with an asset_id but no drive_link.
			if readyVidRushCandidate(candidate) {
				return candidate, true
			}
		}
	}
	return scriptpkg.SegmentAssetCandidate{}, false
}

// entityImagePreviewURL returns the direct image URL used for inline rendering:
// the candidate's source image first, then its preview URL. It never falls back
// to the Drive view-page link, which is not a renderable image.
func entityImagePreviewURL(candidate scriptpkg.SegmentAssetCandidate) string {
	if url := strings.TrimSpace(candidate.SourceURL); url != "" {
		return url
	}
	return strings.TrimSpace(candidate.PreviewURL)
}

func normalizeEntityMatch(value string) string {
	decomposed := norm.NFD.String(strings.ToLower(strings.TrimSpace(value)))
	var b strings.Builder
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	// NER commonly emits an English possessive when the name is the subject
	// of a sentence ("Dwayne Johnson's journey"), while image candidates are
	// keyed by the canonical identity ("Dwayne Johnson").
	return strings.TrimSuffix(strings.TrimSuffix(b.String(), "'s"), "’s")
}
