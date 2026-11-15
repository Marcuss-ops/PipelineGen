// Package adapters — media_resolver_image_segments.go: the segment loop of
// the image-discovery stage. Split from media_resolver_image_stage.go to keep
// both files under max_lines_per_file_strict=600 (same package, no behavior
// change).
package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptmetrics "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports/metrics"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

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
		stockBound := scriptpkg.SceneHasDirectStockBinding(input.SpecScene, input.StockBindings, seg.SceneID, seg.SegmentID, seg.Position)
		if (stockBound && !stockBoundEntityImageMaterializationEnabled(plan)) ||
			(!stockBound && !sceneAllowsMediaSearch(input.SpecScene, seg.SceneID, seg.SegmentID, seg.Position)) {
			updated.Cache.InternetImages = "BYPASSED"
			updatedSegments = append(updatedSegments, updated)
			continue
		}
		entityPoolLookup := entityImagesEnabled && plan != nil && plan.MediaPlan.Extraction.EntityImages.PerScene()
		// Which queries this segment resolves and whether discovery already
		// happened is one rule (identity beats topic, manual queries are always
		// kept, a fan-out hit only covers contextual images). It lives in
		// planInternetImageDiscovery so it can be tested without running the
		// stage — the suppression bug this replaces was invisible here.
		var entityQueries []string
		if entityImagesEnabled {
			entityQueries = scenePrimaryEntityQueries(input.SpecScene, sceneIdx, updated)
		}
		// Pinned-media fast path (opt-in, see media_manifest_shim.go): when
		// the manifest pins this scene/segment, discovery is complete by
		// construction — offline-verified bytes, durable verdicts, zero
		// provider calls. It wins over caches, the fan-out short circuit and
		// force refresh alike (a pin is the operator's authoritative pick),
		// and it never violates cache_only (no network in either path).
		//
		// A pin is a CONTEXTUAL discovery verdict: it never covers the
		// entity-image lane. When the scene asks for per-scene entity
		// portraits, the pinned candidate is preserved as the contextual
		// selection and the entity queries still run; otherwise the pin would
		// satisfy discovery while every person overlay remained imageless.
		if pinned := p.pinnedInternetImageState(plan, updated); len(pinned) > 0 {
			updated.Assets.Candidates = AppendProviderCandidatesUnique(updated.Assets.Candidates, pinned)
			updated.Assets.SecondaryImages = AppendProviderCandidatesUnique(updated.Assets.SecondaryImages, pinned)
			updated.Cache.InternetImages = MediaManifestCacheState
			updated.Cache.InternetImagesProviderSearches = 0
			updated.Cache.InternetImagesNewUploads = 0
			if !entityPoolLookup || len(entityQueries) == 0 {
				updatedSegments = append(updatedSegments, updated)
				if p.metrics != nil {
					p.metrics.IncAssetCache("internet_images", true)
				}
				continue
			}
		}
		manualImageQueries := ResolveManualSegmentQueries(plan, scriptpkg.CanonicalSegment{ID: updated.SegmentID}, scriptpkg.VidRushProviderInternetImages, mediadomain.SlotSecondaryImage)
		discovery := planInternetImageDiscovery(
			updated.Insights.ImageQueries, entityQueries, manualImageQueries, entityImagesEnabled,
			fanoutAlreadyOwnsInternetImageDiscovery(plan, &updated),
			func(query string) bool { return len(entityImageMatches(normalizeEntityMatch(query), "", updated)) > 0 },
		)
		if discovery.Mode == internetImageDiscoveryBypass {
			updated.Cache.InternetImages = "BYPASSED"
			updatedSegments = append(updatedSegments, updated)
			continue
		}
		imageQueries := discovery.Queries
		if discovery.Mode == internetImageDiscoveryReuse {
			// A segment-level fan-out hit is sufficient for contextual images.
			// Entity-image mode has a stricter identity contract: a warm generic
			// scene image cannot satisfy a requested person/org/place portrait.
			// Reuse fan-out only when every requested identity has a ready match;
			// otherwise use the query-keyed entity cache/provider path below.
			reuseFanoutInternetImageCandidates(&updated)
			updated.Cache.InternetImagesProviderSearches = 0
			updatedSegments = append(updatedSegments, updated)
			continue
		}

		cacheKey := segmentCacheKey(
			// Query text is an LLM-derived retrieval hint, not scene
			// identity. Keep it out of the durable key so a warm replay
			// reuses the same scene assets even if extraction wording
			// changes between runs.
			"internet-images-assets-v5",
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
		// A segment cache can contain the one portrait selected by an earlier
		// job. In per-scene entity mode the canonical entity catalog is the
		// reusable cache; bypass the segment winner so the full pool can be
		// bound and randomized again for this run.
		if internetImageCacheReadable(plan, options) && !entityPoolLookup {
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
				persisted = sanitizeInternetImageCachePayload(persisted)
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
		if cacheOnly && !entityPoolLookup {
			updated.Cache.InternetImages = "CACHE_MISS"
			warnings = append(warnings, internetImageCacheMissWarning(updated.SegmentID))
			updatedSegments = append(updatedSegments, updated)
			continue
		}

		if p.metrics != nil {
			p.metrics.IncAssetCache("internet_images", false)
		}

		queryCandidateLimit := perQueryLimit
		if entityImagesEnabled && len(imageQueries) > 1 {
			queryCandidateLimit = internetImagePerQueryLimit(perQueryLimit, len(imageQueries))
		}
		candidates := make([]scriptpkg.SegmentAssetCandidate, 0, queryCandidateLimit*len(imageQueries))
		seen := make(map[string]struct{}, cap(candidates))
		type queryResult struct {
			candidates []scriptpkg.SegmentAssetCandidate
			query      string
			fromCache  bool
			err        error
		}
		queryResults, mapErr := concurrent.Map(ctx, imageQueries, 4, func(ctx context.Context, _ int, query string) (queryResult, error) {
			// Reuse is independent of scene wording and source language. The
			// canonical identity, intent class, provider and verifier policy
			// determine this cache entry.
			searchQuery := query
			queryClass := searchClassForEntityQuery(updated.Insights.Entities, query)
			cacheIdentity := newEntityImageCacheIdentity(updated, query)
			canonicalID := cacheIdentity.CanonicalEntityID
			entityCacheKey := ""
			if canonicalID != "" {
				entityCacheKey = entityImageDiscoveryCacheKey(cacheIdentity, queryClass)
			} else {
				// Contextual queries have no canonical identity. Key their
				// per-query cache on the research-path identity instead of
				// collapsing every query to the empty key.
				entityCacheKey = contextualImageDiscoveryCacheKey(plan, query, queryClass)
			}
			catalogIdentity, personIdentityQuery, identityErr := personCatalogIdentityForQuery(input.SpecScene, sceneIdx, updated, query)
			if identityErr != nil {
				return queryResult{}, identityErr
			}
			catalogEligible := p.catalog != nil && entityImagesEnabled && personIdentityQuery && canonicalID != "" && !strings.HasPrefix(canonicalID, "local:")
			catalogRefreshRequired := false
			catalogFallback := []scriptpkg.SegmentAssetCandidate(nil)
			catalogMetrics := entityImageCatalogMetricsFor(p.metrics)
			resolution, resolutionErr := resolveEntityImageCatalogCache(ctx, entityImageResolverDeps{
				catalog: p.catalog, cache: p.cache, metrics: p.metrics,
			}, plan, query, entityCacheKey, catalogIdentity, catalogEligible, cacheOnly)
			if resolution.ReleaseDiscovery != nil {
				defer resolution.ReleaseDiscovery()
			}
			if resolutionErr != nil {
				return queryResult{}, resolutionErr
			}
			catalogFallback = resolution.CatalogFallback
			catalogRefreshRequired = resolution.CatalogRefreshRequired
			if resolution.FromCache {
				return queryResult{candidates: resolution.Candidates, query: query, fromCache: true}, nil
			}
			imageLimit := queryCandidateLimit
			if catalogEligible && len(imageQueries) == 1 && imageLimit < entityImageCatalogDiscoveryLimit {
				imageLimit = entityImageCatalogDiscoveryLimit
			}
			var results []scriptpkg.SegmentAssetCandidate
			var err error
			providerCalled := true
			err = measureVidRushProvider(ctx, p.metrics, kernobs.OperationInfo{
				Stage: kernobs.StageAcquire, Component: "vidrush", Operation: "search", Provider: "internet_images",
			}, func(callCtx context.Context) error {
				var searchErr error
				// The query is the identity surface for entity-image searches.
				// Labelling every result with the first entity in a multi-person
				// segment makes the semantic catalog gate discard later people.
				searchEntity := query
				if personIdentityQuery {
					searchEntity = catalogIdentity.CanonicalName
				}
				results, searchErr = p.searcher.SearchImages(callCtx, InternetImageSearchRequest{SegmentID: updated.SegmentID, Position: updated.Position, Query: searchQuery, Entity: searchEntity,
					TextHash: updated.TextHash, Language: plan.Language, Limit: imageLimit,
					QueryClass:   searchClassForEntityQuery(updated.Insights.Entities, query),
					Provider:     "internet_images",
					ForceRefresh: plan.MediaPlan.ForceRefreshAssets || plan.ForceRefresh || catalogRefreshRequired,
				})
				return searchErr
			})
			if catalogMetrics != nil {
				// A force refresh bypasses the pool lookup; an insufficient pool
				// reaches this same provider call with catalogRefreshRequired set.
				if catalogEligible && (catalogRefreshRequired || plan.MediaPlan.ForceRefreshAssets || plan.ForceRefresh) {
					catalogMetrics.IncEntityImageCatalogRefresh()
				}
				if catalogEligible && providerCalled {
					catalogMetrics.IncEntityImageCatalogProviderCall()
				}
			}
			if p.metrics != nil && providerCalled {
				// Count an actual provider invocation, not a segment-level
				// cache miss. Entity-image L2/L1 hits below may satisfy the
				// query without calling the external searcher.
				p.metrics.IncProviderRequest("internet_images")
			}
			if err == nil {
				results = normalizeInternetImageCatalogResults(results, query)
				if catalogEligible {
					addEntityImageCatalogCandidates(p.metrics, scriptmetrics.EntityImageStageDiscovery, scriptmetrics.EntityImageOutcomeFound, len(results))
					incEntityImageCatalogLifecycle(p.metrics, scriptmetrics.EntityImageStageDiscovery, scriptmetrics.EntityImageOutcomeSuccess)
					rawResults := results
					results = filterPersonEntityImageCandidatesMeasured(catalogIdentity, results, p.metrics)
					if catalogErr := persistEntityImageCatalogCandidatesMeasured(ctx, p.catalog, catalogIdentity, rawResults, p.metrics); catalogErr != nil {
						return queryResult{}, catalogErr
					}
					filteredResults, filterErr := excludeUnavailableEntityImageCatalogCandidates(ctx, p.catalog, catalogIdentity, results)
					if filterErr != nil {
						return queryResult{}, filterErr
					}
					results = filteredResults
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
				// A no-result response is transient for person image searches: a
				// provider may be temporarily sparse, rate-limited, or return a
				// different page on the next query. Persisting that miss for the
				// normal 48h cache TTL poisons every later scene for this entity.
				// Keep negative caching for ordinary editorial image queries, but
				// retry empty entity-image searches on the next run.
				if len(results) > 0 || !entityImagesEnabled {
					// L1 keeps the run-scoped materialized candidate for binding; L2
					// persists discovery only, never a stale Drive/content-address.
					cacheResults := sanitizeVidRushDiscoveryCandidates(results)
					if cacheErr := storeVidRushPersistentJSON(ctx, p.cache, "entity_images", entityCacheKey, cacheResults); cacheErr != nil {
						return queryResult{}, cacheErr
					}
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
			identity, personIdentityQuery, identityErr := personCatalogIdentityForQuery(input.SpecScene, sceneIdx, updated, queryResult.query)
			if identityErr != nil {
				return nil, identityErr
			}
			if personIdentityQuery {
				for i := range queryResult.candidates {
					queryResult.candidates[i].Entity = identity.CanonicalName
				}
			}
			// Cached/provider candidates arrive PRE-verification: stamp the
			// durable catalog verdicts first so a warm reuse skips the download
			// + VLM call (the allCandidatesHaveReusableVerdict fast path in
			// scoreInternetImageCandidates then serves the whole batch).
			hydratePersonImageVisualVerdicts(ctx, p.catalog, queryResult.candidates)
			visualClass := searchClassForEntityQuery(updated.Insights.Entities, queryResult.query)
			if visualClass == "" && personIdentityQuery {
				visualClass = "person"
			}
			scored, scoreErr := resolveEntityVisualCandidates(ctx, p.semanticScorer, visualClass, updated.Text, queryResult.candidates)
			if scoreErr != nil {
				warnings = append(warnings, fmt.Sprintf("internet_images: semantic scoring unavailable for segment %s: %v", updated.SegmentID, scoreErr))
			} else {
				queryResult.candidates = scored
			}
			if p.semanticScorer != nil {
				if err := persistPersonImageVisualVerdicts(ctx, p.catalog, queryResult.candidates); err != nil {
					return nil, fmt.Errorf("internet_images: persist visual person-image verdicts for segment %s: %w", updated.SegmentID, err)
				}
			}
			if personIdentityQuery && p.semanticScorer != nil {
				// With a scorer wired this is the fail-closed boundary before
				// candidate binding: only a definitive pixel acceptance may reach
				// the selection plan. Without a scorer the stage keeps its
				// documented fail-open discovery contract and leaves the rows
				// pending for downstream verification; the catalog still refuses
				// to reuse them until a real verdict exists.
				queryResult.candidates = filterVerifiedPersonImageCandidates(queryResult.candidates)
			}
			recordEntityImageVisualOutcomes(p.metrics, queryResult.candidates, scoreErr)
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
		if len(payload.Candidates) > 0 && !entityPoolLookup {
			cacheStore(vidrushImageCache, cacheKey, payload)
		}
		// Empty provider results are durable-cached in L2 (TTL 48h) so a warm
		// replay of the same segment is deterministic and does not re-call the
		// provider, but they stay out of the no-TTL L1 map to avoid unbounded
		// growth of empty in-memory entries. L2 carries discovery only.
		if !entityPoolLookup {
			cachePayload := sanitizeInternetImageCachePayload(payload)
			if cacheErr := storeVidRushPersistentJSON(ctx, p.cache, "internet_images", cacheKey, cachePayload); cacheErr != nil {
				return nil, cacheErr
			}
		}
		updatedSegments = append(updatedSegments, updated)
	}
	for i := range updatedSegments {
		normalizeVidRushSegmentAssets(&updatedSegments[i])
	}
	entityImagePolicy := plan.MediaPlan.Extraction.EntityImages
	entityImagePolicy.Enabled = plan.MediaPlan.Extraction.EntityImageSurfaceEnabled()
	// Entity-image overlays are authored per scene, but their candidate pool is
	// shared by the entity catalog. Keep repeated catalog candidates attached
	// here: the overlay binder prefers an unused variant and falls back to a
	// previously shown image when the pool is exhausted. Removing duplicates at
	// this stage can leave later scenes with no image at all.
	if entityImagePolicy.PerScene() && !entityImagePolicy.Enabled {
		updatedSegments = scriptpkg.RemoveCrossSceneImageCandidateReuse(updatedSegments)
	}
	updatedSpecScene := projectEntityImageBindings(input.SpecScene, updatedSegments, entityImagePolicy)
	return &PostProcessResult{
		VidRushSegments:  updatedSegments,
		UpdatedSpecScene: updatedSpecScene,
		SpecSceneChanged: len(updatedSpecScene.Scenes) > 0,
		Warnings:         warnings,
		Changed:          len(updatedSegments) > 0,
	}, nil
}
