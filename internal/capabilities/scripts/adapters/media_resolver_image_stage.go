// Package adapters — media_resolver_image_stage.go: the image-discovery
// stage of the canonical media resolver (provider candidates only; it never
// selects a winner).
//
// The entity-card half — which candidate may bind as a scene entity's image —
// lives in media_resolver_entity_binding.go (split 2026-09-12 to keep both
// halves under max_lines_per_file_strict=600, godlike/08).
package adapters

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// internetImageCachePayload stores only the image-provider delta so a cache
// hit cannot replace Artlist candidates or other upstream segment state.
type internetImageCachePayload struct {
	Candidates []scriptpkg.SegmentAssetCandidate
}

// entityImageL1Capacity bounds the canonical entity-key L1 (see
// vidrush_helpers.go for the VidRush L1 cache contract).
const entityImageL1Capacity = 4096

var (
	// entityImageCache is the bounded L1 for canonical entity key ->
	// []SegmentAssetCandidate (see vidrush_helpers.go for the L1 contract).
	entityImageCache = cacheutil.NewLRU(entityImageL1Capacity)
	// entityImageLocks is the canonical reference-counted per-key locker: it
	// serialises concurrent provider/catalog lookups for the same entity key
	// while leaving different keys fully parallel. Unlike the retired
	// sync.Map-of-mutexes registry it never leaks entries — the key is removed
	// when the last holder releases (pkg/concurrent.KeyedLocker).
	entityImageLocks = concurrent.NewKeyedLocker()
)

// MediaResolverImageStage handles image discovery for the canonical media
// resolver path. It attaches provider candidates but never selects a winner.
// attaches every unique result returned for the segment queries.
type MediaResolverImageStage struct {
	searcher InternetImageSearcher
	metrics  VidRushMetrics
	cache    scriptports.VidRushCachePort
	catalog  entitycatalog.Repository
}

func NewMediaResolverImageStage(searcher InternetImageSearcher, metrics ...VidRushMetrics) *MediaResolverImageStage {
	return NewMediaResolverImageStageWithCatalog(searcher, nil, nil, metrics...)
}

func NewMediaResolverImageStageWithCache(searcher InternetImageSearcher, cache scriptports.VidRushCachePort, metrics ...VidRushMetrics) *MediaResolverImageStage {
	return NewMediaResolverImageStageWithCatalog(searcher, cache, nil, metrics...)
}

func NewMediaResolverImageStageWithCatalog(searcher InternetImageSearcher, cache scriptports.VidRushCachePort, catalog entitycatalog.Repository, metrics ...VidRushMetrics) *MediaResolverImageStage {
	var m VidRushMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &MediaResolverImageStage{searcher: searcher, metrics: m, cache: cache, catalog: catalog}
}

func (p *MediaResolverImageStage) Name() ProcessorName { return ProcessorInternetImages }

func (p *MediaResolverImageStage) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

func (p *MediaResolverImageStage) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if plan == nil {
		return &PostProcessResult{}, nil
	}
	options := internetImageOptions(plan)
	if result, handled := p.handleBypassOrUnavailable(plan, input, options); handled {
		return result, nil
	}
	if len(input.VidRushSegments) == 0 {
		return &PostProcessResult{}, nil
	}
	return p.processInternetImageSegments(ctx, plan, input, options)
}

type internetImageProcessOptions struct {
	cacheOnly           bool
	entityImagesEnabled bool
}

func (p *MediaResolverImageStage) handleBypassOrUnavailable(plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput, options internetImageProcessOptions) (*PostProcessResult, bool) {
	if !plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() && !options.entityImagesEnabled {
		segments := markInternetImagesBypassed(input.VidRushSegments)
		if len(segments) == 0 {
			return &PostProcessResult{}, true
		}
		return &PostProcessResult{VidRushSegments: segments, Changed: true}, true
	}
	if !hasMediaSearchSegments(input) {
		return &PostProcessResult{VidRushSegments: markInternetImagesBypassed(input.VidRushSegments), Changed: true}, true
	}
	if p.searcher == nil && !options.cacheOnly {
		return &PostProcessResult{Changed: true, Warnings: []string{"internet_images: InternetImageSearcher not configured"}}, true
	}
	return nil, false
}

func markInternetImagesBypassed(input []scriptpkg.VidRushSegmentResult) []scriptpkg.VidRushSegmentResult {
	segments := make([]scriptpkg.VidRushSegmentResult, 0, len(input))
	for _, segment := range input {
		cloned := CloneVidRushSegmentResult(segment)
		cloned.Cache.InternetImages = "BYPASSED"
		segments = append(segments, cloned)
	}
	return segments
}

func (p *MediaResolverImageStage) processInternetImageSegments(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput, options internetImageProcessOptions) (*PostProcessResult, error) {
	cacheOnly := options.cacheOnly
	entityImagesEnabled := options.entityImagesEnabled
	perQueryLimit := internetImageCandidateLimit(plan)

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
		updated := CloneVidRushSegmentResult(seg)
		if !sceneAllowsMediaSearch(input.SpecScene, seg.SceneID, seg.SegmentID, seg.Position) {
			updated.Cache.InternetImages = "BYPASSED"
			updatedSegments = append(updatedSegments, updated)
			continue
		}
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
		if internetImageCacheReadable(plan, options) {
			if cached, ok := cacheLoad(vidrushImageCache, cacheKey); ok {
				if payload, ok := cached.(internetImageCachePayload); ok {
					candidates := append([]scriptpkg.SegmentAssetCandidate(nil), payload.Candidates...)
					updated.Assets.Candidates = AppendProviderCandidatesUnique(updated.Assets.Candidates, candidates)
					updated.Assets.SecondaryImages = AppendProviderCandidatesUnique(updated.Assets.SecondaryImages, candidates)
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
				updated.Assets.Candidates = AppendProviderCandidatesUnique(updated.Assets.Candidates, persisted.Candidates)
				updated.Assets.SecondaryImages = AppendProviderCandidatesUnique(updated.Assets.SecondaryImages, persisted.Candidates)
				updated.Cache.InternetImages = "HIT_EXACT"
				if len(persisted.Candidates) > 0 {
					cacheStore(vidrushImageCache, cacheKey, persisted)
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
			warnings = append(warnings, internetImageCacheMissWarning(updated.SegmentID))
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
			var catalogIdentity entitycatalog.PersonIdentity
			catalogEligible := false
			catalogRefreshRequired := false
			catalogFallback := []scriptpkg.SegmentAssetCandidate(nil)
			catalogMetrics := entityImageCatalogMetricsFor(p.metrics)
			if p.catalog != nil && entityImagesEnabled {
				var catalogErr error
				catalogIdentity, catalogEligible, catalogErr = personCatalogIdentityForQuery(input.SpecScene, sceneIdx, updated, query)
				if catalogErr != nil {
					return queryResult{}, catalogErr
				}
			}
			if catalogEligible {
				releaseCatalogLock := entityImageLocks.Lock("entity-catalog:" + catalogIdentity.CanonicalEntityID)
				defer releaseCatalogLock()
				if !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh {
					lookupStarted := time.Now()
					pool, err := entityImageCatalogCandidates(ctx, p.catalog, catalogIdentity, perQueryLimit)
					observeEntityImageCatalogLookup(p.metrics, lookupStarted)
					if err != nil {
						return queryResult{}, err
					}
					if pool.Sufficient {
						if catalogMetrics != nil {
							catalogMetrics.IncEntityImageCatalogLookup(true)
						}
						return queryResult{candidates: pool.Candidates, query: query, fromCache: true}, nil
					}
					if catalogMetrics != nil {
						catalogMetrics.IncEntityImageCatalogLookup(false)
					}
					catalogFallback = pool.Candidates
					catalogRefreshRequired = true
				}
			}
			if !catalogRefreshRequired && !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh {
				if cached, ok := cacheLoad(entityImageCache, entityCacheKey); ok {
					if cachedCandidates, ok := cached.([]scriptpkg.SegmentAssetCandidate); ok {
						cachedCandidates = normalizeInternetImageCatalogResults(cachedCandidates, query)
						if catalogEligible {
							cachedCandidates = filterPersonEntityImageCandidates(catalogIdentity, cachedCandidates)
							if err := persistEntityImageCatalogCandidates(ctx, p.catalog, catalogIdentity, cachedCandidates); err != nil {
								return queryResult{}, err
							}
						}
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
						cacheStore(entityImageCache, entityCacheKey, persisted)
					}
					persisted = normalizeInternetImageCatalogResults(persisted, query)
					if catalogEligible {
						persisted = filterPersonEntityImageCandidates(catalogIdentity, persisted)
						if err := persistEntityImageCatalogCandidates(ctx, p.catalog, catalogIdentity, persisted); err != nil {
							return queryResult{}, err
						}
					}
					return queryResult{candidates: persisted, query: query, fromCache: true}, nil
				}
			}
			if !catalogEligible && !catalogRefreshRequired && !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh {
				releaseEntityLock := entityImageLocks.Lock(entityCacheKey)
				defer releaseEntityLock()
				if cached, ok := cacheLoad(entityImageCache, entityCacheKey); ok {
					if cachedCandidates, ok := cached.([]scriptpkg.SegmentAssetCandidate); ok {
						cachedCandidates = normalizeInternetImageCatalogResults(cachedCandidates, query)
						if catalogEligible {
							cachedCandidates = filterPersonEntityImageCandidates(catalogIdentity, cachedCandidates)
							if err := persistEntityImageCatalogCandidates(ctx, p.catalog, catalogIdentity, cachedCandidates); err != nil {
								return queryResult{}, err
							}
						}
						return queryResult{candidates: append([]scriptpkg.SegmentAssetCandidate(nil), cachedCandidates...), query: query, fromCache: true}, nil
					}
				}
			}
			if catalogMetrics != nil {
				// A force refresh bypasses the pool lookup; an insufficient pool
				// reaches this same provider call with catalogRefreshRequired set.
				if catalogEligible && (catalogRefreshRequired || plan.MediaPlan.ForceRefreshAssets || plan.ForceRefresh) {
					catalogMetrics.IncEntityImageCatalogRefresh()
				}
				if catalogEligible {
					catalogMetrics.IncEntityImageCatalogProviderCall()
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
				results, searchErr = p.searcher.SearchImages(callCtx, InternetImageSearchRequest{SegmentID: updated.SegmentID, Position: updated.Position, Query: query, Entity: firstEntity,
					TextHash: updated.TextHash, Language: plan.Language, Limit: perQueryLimit,
					Provider: "internet_images",
				})
				return searchErr
			})
			if err == nil {
				results = normalizeInternetImageCatalogResults(results, query)
				if catalogEligible {
					results = filterPersonEntityImageCandidates(catalogIdentity, results)
					if catalogErr := persistEntityImageCatalogCandidates(ctx, p.catalog, catalogIdentity, results); catalogErr != nil {
						return queryResult{}, catalogErr
					}
				}
				results = AppendProviderCandidatesUnique(catalogFallback, results)
				// Empty results are durable-cached in L2 (TTL 48h) so a warm
				// replay of the same query does not re-call the provider, but
				// they are kept out of the no-TTL L1 map to avoid unbounded
				// growth of empty in-memory entries.
				if len(results) > 0 {
					cacheStore(entityImageCache, entityCacheKey, append([]scriptpkg.SegmentAssetCandidate(nil), results...))
				}
				if results == nil {
					results = []scriptpkg.SegmentAssetCandidate{}
				}
				if cacheErr := storeVidRushPersistentJSON(ctx, p.cache, "entity_images", entityCacheKey, results); cacheErr != nil {
					return queryResult{}, cacheErr
				}
			}
			if err != nil && len(catalogFallback) > 0 {
				return queryResult{candidates: catalogFallback, query: query, err: err}, nil
			}
			return queryResult{candidates: results, query: query, err: err}, nil
		})
		if mapErr != nil {
			warnings = append(warnings, fmt.Sprintf("internet_images: bounded query fan-out failed for segment %s: %v", updated.SegmentID, mapErr))
		}
		for _, queryResult := range queryResults {
			queryResult.candidates = filterInternetImageCandidates(deduplicateInternetImageCandidates(normalizeInternetImageCandidates(queryResult.candidates, queryResult.query)))
			if queryResult.err != nil {
				if p.metrics != nil {
					p.metrics.IncProviderFailure("internet_images")
				}
				warnings = append(warnings, fmt.Sprintf("internet_images: search failed for segment %s: %v", updated.SegmentID, queryResult.err))
			}
			for _, cand := range queryResult.candidates {
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
		providerSearches := 0
		for _, qr := range queryResults {
			if !qr.fromCache {
				allCacheHits = false
				providerSearches++
			}
		}
		updated.Cache.InternetImages = "MISS"
		if plan.MediaPlan.ForceRefreshAssets {
			updated.Cache.InternetImages = "REFRESHED"
		} else if allCacheHits {
			updated.Cache.InternetImages = "HIT_EXACT"
		}
		// Numeric provider-search counter: 0 on a warm catalog/cache replay,
		// the number of provider invocations otherwise. This is the observable
		// "provider search = N" proof the certification consumes.
		updated.Cache.InternetImagesProviderSearches = providerSearches
		if len(candidates) > 0 {
			updated.Assets.Candidates = AppendProviderCandidatesUnique(updated.Assets.Candidates, candidates)
			updated.Assets.SecondaryImages = AppendProviderCandidatesUnique(updated.Assets.SecondaryImages, candidates)
		}
		payload := internetImageCachePayload{
			Candidates: append([]scriptpkg.SegmentAssetCandidate(nil), candidates...),
		}
		if len(payload.Candidates) > 0 {
			cacheStore(vidrushImageCache, cacheKey, payload)
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
	for i := range updatedSegments {
		normalizeVidRushSegmentAssets(&updatedSegments[i])
	}
	entityImagePolicy := plan.MediaPlan.Extraction.EntityImages
	entityImagePolicy.Enabled = plan.MediaPlan.Extraction.EntityImageSurfaceEnabled()
	updatedSpecScene := projectEntityImageBindings(input.SpecScene, updatedSegments, entityImagePolicy)
	return &PostProcessResult{
		VidRushSegments:  updatedSegments,
		UpdatedSpecScene: updatedSpecScene,
		SpecSceneChanged: len(updatedSpecScene.Scenes) > 0,
		Warnings:         warnings,
		Changed:          len(updatedSegments) > 0,
	}, nil
}
