// Package adapters — media_resolver_image_segments.go: the segment loop of
// the image-discovery stage. Split from media_resolver_image_stage.go to keep
// both files under max_lines_per_file_strict=600 (same package, no behavior
// change).
package vidrush

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/imagegate"

	scriptmetrics "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports/metrics"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// processInternetImageSegments owns the segment image-discovery walk. The
// phase helper holds the per-segment cache/provider/finalization work.
func (p *MediaResolverImageStage) processInternetImageSegments(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput, options internetImageProcessOptions) (*PostProcessResult, error) {
	return p.processInternetImageSegmentsPhase(ctx, plan, input, options)
}

// entityImageLaneEnabled is the policy boundary shared by the resolver and its
// tests. `entity_images.enabled` is a direct operator request for portraits;
// explicit_only restricts inferred CONTEXTUAL queries, not this lane. Only an
// explicit `disabled` image-search mode turns the identity lane off.
func entityImageLaneEnabled(requested bool, imageSearchMode string) bool {
	return requested && imageSearchMode != mediadomain.ImageSearchModeDisabled
}

func (p *MediaResolverImageStage) processInternetImageSegmentsPhase(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput, options internetImageProcessOptions) (*PostProcessResult, error) {
	cacheOnly := options.cacheOnly
	imageSearchMode := ""
	if plan != nil {
		imageSearchMode = strings.TrimSpace(plan.MediaPlan.Extraction.ImageSearchMode)
	}
	// entity_images.enabled is itself the operator's explicit authorization for
	// identity portraits, so explicit_only must not switch the lane off. The
	// mode still governs contextual queries; only "disabled" suppresses both.
	entityImagesEnabled := entityImageLaneEnabled(options.entityImagesEnabled, imageSearchMode)
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
		if imageSearchMode == mediadomain.ImageSearchModeDisabled {
			updated.Cache.InternetImages = "BYPASSED"
			updatedSegments = append(updatedSegments, updated)
			continue
		}
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
		imageQueries, discoveryOutcome := p.planSegmentInternetImageDiscovery(plan, &updated, entityQueries, entityImagesEnabled, entityPoolLookup)
		switch discoveryOutcome {
		case internetImageSegmentSearch:
		case internetImageSegmentPinned, internetImageSegmentBypassed, internetImageSegmentReused:
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
		updated, cacheDone, cacheWarnings, cacheErr := p.replayInternetImageSegment(ctx, plan, updated, cacheKey, options, entityPoolLookup)
		if cacheErr != nil {
			return nil, cacheErr
		}
		if cacheDone {
			warnings = append(warnings, cacheWarnings...)
			updatedSegments = append(updatedSegments, updated)
			continue
		}

		updated, queryWarnings, queryErr := p.resolveInternetImageSegmentQueries(ctx, plan, input, sceneIdx, updated, imageQueries, perQueryLimit, entityImagesEnabled, cacheOnly, entityPoolLookup, cacheKey)
		if queryErr != nil {
			return nil, queryErr
		}
		warnings = append(warnings, queryWarnings...)
		updatedSegments = append(updatedSegments, updated)
	}
	for i := range updatedSegments {
		normalizeVidRushSegmentAssets(&updatedSegments[i])
	}
	entityImagePolicy := plan.MediaPlan.Extraction.EntityImages
	entityImagePolicy.Enabled = entityImagesEnabled
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

func (p *MediaResolverImageStage) replayInternetImageSegment(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, updated scriptpkg.VidRushSegmentResult, cacheKey string, options internetImageProcessOptions, entityPoolLookup bool) (scriptpkg.VidRushSegmentResult, bool, []string, error) {
	if internetImageCacheReadable(plan, options) && !entityPoolLookup {
		cached, hit, err := newInternetImageSegmentCache(vidrushImageCache, p.cache).Load(ctx, cacheKey, internetImageReplayPolicy, time.Now())
		if err != nil {
			return updated, false, nil, err
		}
		if hit {
			candidates := append([]scriptpkg.SegmentAssetCandidate(nil), cached.Candidates...)
			updated.Assets.Candidates = AppendProviderCandidatesUnique(updated.Assets.Candidates, candidates)
			updated.Assets.SecondaryImages = AppendProviderCandidatesUnique(updated.Assets.SecondaryImages, candidates)
			updated.Cache.InternetImages = "HIT_EXACT"
			if p.metrics != nil {
				p.metrics.IncAssetCache("internet_images", true)
			}
			return updated, true, nil, nil
		}
	}
	if options.cacheOnly && !entityPoolLookup {
		updated.Cache.InternetImages = "CACHE_MISS"
		return updated, true, []string{internetImageCacheMissWarning(updated.SegmentID)}, nil
	}
	if p.metrics != nil {
		p.metrics.IncAssetCache("internet_images", false)
	}
	return updated, false, nil, nil
}

func (p *MediaResolverImageStage) resolveInternetImageSegmentQueries(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput, sceneIdx sceneIdentityIndex, updated scriptpkg.VidRushSegmentResult, imageQueries []string, perQueryLimit int, entityImagesEnabled, cacheOnly, entityPoolLookup bool, cacheKey string) (scriptpkg.VidRushSegmentResult, []string, error) {
	queryCandidateLimit := perQueryLimit
	if entityImagesEnabled && len(imageQueries) > 1 {
		queryCandidateLimit = internetImagePerQueryLimit(perQueryLimit, len(imageQueries))
	}
	queryResults, mapErr := concurrent.Map(ctx, imageQueries, 4, func(ctx context.Context, _ int, query string) (internetImageQueryResult, error) {
		return p.resolveInternetImageQuery(ctx, plan, input, sceneIdx, updated, query, len(imageQueries), queryCandidateLimit, entityImagesEnabled, cacheOnly)
	})
	var warnings []string
	if mapErr != nil {
		warnings = append(warnings, fmt.Sprintf("internet_images: bounded query fan-out failed for segment %s: %v", updated.SegmentID, mapErr))
	}
	candidates, queryWarnings, err := p.finalizeInternetImageSegmentQueries(ctx, input, sceneIdx, updated, queryResults, queryCandidateLimit*len(imageQueries))
	if err != nil {
		return updated, warnings, err
	}
	warnings = append(warnings, queryWarnings...)
	allCacheHits, providerSearches := internetImageQueryCacheSummary(queryResults, mapErr == nil)
	updated.Cache.InternetImages = "MISS"
	if plan.MediaPlan.ForceRefreshAssets {
		updated.Cache.InternetImages = "REFRESHED"
	} else if allCacheHits {
		updated.Cache.InternetImages = "HIT_EXACT"
	}
	updated.Cache.InternetImagesProviderSearches = providerSearches
	if len(candidates) > 0 {
		updated.Assets.Candidates = AppendProviderCandidatesUnique(updated.Assets.Candidates, candidates)
		updated.Assets.SecondaryImages = AppendProviderCandidatesUnique(updated.Assets.SecondaryImages, candidates)
	}
	if !entityPoolLookup {
		payload := internetImageCachePayload{Candidates: append([]scriptpkg.SegmentAssetCandidate(nil), candidates...)}
		if err := newInternetImageSegmentCache(vidrushImageCache, p.cache).Store(ctx, cacheKey, payload, internetImageReplayPolicy); err != nil {
			return updated, warnings, err
		}
	}
	return updated, warnings, nil
}

// internetImageFinalizeConcurrency bounds the per-segment finalization pool.
// Scoring/hydration is an independent per-query operation; the serial loop made
// a segment pay the sum of its queries' VLM/catalog latency instead of the max.
const internetImageFinalizeConcurrency = 4

func (p *MediaResolverImageStage) finalizeInternetImageSegmentQueries(ctx context.Context, input ProcessInput, sceneIdx sceneIdentityIndex, updated scriptpkg.VidRushSegmentResult, queryResults []internetImageQueryResult, capacity int) ([]scriptpkg.SegmentAssetCandidate, []string, error) {
	if len(queryResults) == 0 {
		return make([]scriptpkg.SegmentAssetCandidate, 0, capacity), nil, nil
	}
	type finalizedQuery struct {
		candidates []scriptpkg.SegmentAssetCandidate
		warnings   []string
		err        error
	}
	// Finalize the queries concurrently, but keep the result slice indexed by
	// declaration order: candidate and warning order must not depend on which
	// query's scorer replied first. The only shared state left is the dedupe
	// below, which runs on this goroutine after the join.
	finalized, mapErr := concurrent.Map(ctx, queryResults, internetImageFinalizeConcurrency,
		func(ctx context.Context, _ int, queryResult internetImageQueryResult) (finalizedQuery, error) {
			cands, queryWarnings, err := p.finalizeInternetImageQuery(ctx, input, sceneIdx, updated, queryResult)
			return finalizedQuery{candidates: cands, warnings: queryWarnings, err: err}, err
		})
	var warnings []string
	if mapErr != nil {
		// concurrent.Map reports the error that cancelled the pool; scan in
		// declaration order so the caller sees the same first failure the
		// serial loop would have returned.
		for _, outcome := range finalized {
			warnings = append(warnings, outcome.warnings...)
			if outcome.err != nil {
				return nil, warnings, outcome.err
			}
		}
		return nil, warnings, mapErr
	}
	candidates := make([]scriptpkg.SegmentAssetCandidate, 0, capacity)
	seen := make(map[string]struct{})
	for _, outcome := range finalized {
		warnings = append(warnings, outcome.warnings...)
		for _, candidate := range outcome.candidates {
			key := vidRushCandidateIdentity(candidate)
			if key == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			candidates = append(candidates, candidate)
		}
	}
	return candidates, warnings, nil
}

func internetImageQueryCacheSummary(results []internetImageQueryResult, complete bool) (bool, int) {
	allCacheHits := complete && len(results) > 0
	providerSearches := 0
	for _, result := range results {
		if result.fromCache {
			continue
		}
		allCacheHits = false
		providerSearches++
	}
	return allCacheHits, providerSearches
}

// internetImageQueryResult is one query's fan-out outcome. It is package-level
// so the resolution and finalization helpers can share it.
type internetImageQueryResult struct {
	candidates []scriptpkg.SegmentAssetCandidate
	query      string
	fromCache  bool
	err        error
}

// resolveInternetImageQuery resolves one query of a segment's image fan-out:
// identity and cache key, catalog cache, provider call, discovery cache write.
// It is the body of the bounded fan-out callback, extracted unchanged.
// resolveInternetImageQuery resolves one query of a segment's image fan-out.
func (p *MediaResolverImageStage) resolveInternetImageQuery(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
	sceneIdx sceneIdentityIndex,
	updated scriptpkg.VidRushSegmentResult,
	query string,
	queryCount int,
	queryCandidateLimit int,
	entityImagesEnabled bool,
	cacheOnly bool,
) (internetImageQueryResult, error) {
	return p.resolveInternetImageQueryPhase(ctx, plan, input, sceneIdx, updated, query, queryCount, queryCandidateLimit, entityImagesEnabled, cacheOnly)
}

func (p *MediaResolverImageStage) resolveInternetImageQueryPhase(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
	sceneIdx sceneIdentityIndex,
	updated scriptpkg.VidRushSegmentResult,
	query string,
	queryCount int,
	queryCandidateLimit int,
	entityImagesEnabled bool,
	cacheOnly bool,
) (internetImageQueryResult, error) {
	// Reuse is independent of scene wording and source language. The
	// canonical identity, intent class, provider and verifier policy
	// determine this cache entry.
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
		return internetImageQueryResult{}, identityErr
	}
	if personIdentityQuery {
		// The topic now participates in person disambiguation, so fence old
		// first-name-only discovery entries. Otherwise a cached result for
		// "Natalia" could bypass the corrected "Natalia Augias + Corrado
		// Augias" provider query.
		cacheIdentity.IntentKey = normalizeEntityMatch(catalogIdentity.CanonicalName + " " + plan.Topic)
	}
	if personIdentityQuery && catalogIdentity.CanonicalEntityID != "" &&
		(canonicalID == "" || strings.HasPrefix(canonicalID, "local:") || cacheIdentity.IntentKey != normalizeEntityMatch(query)) {
		canonicalID = catalogIdentity.CanonicalEntityID
		cacheIdentity.CanonicalEntityID = canonicalID
		entityCacheKey = entityImageDiscoveryCacheKey(cacheIdentity, queryClass)
	}
	catalogEligible := p.catalog != nil && entityImagesEnabled && personIdentityQuery && canonicalID != "" && !strings.HasPrefix(canonicalID, "local:")
	catalogRefreshRequired := false
	catalogFallback := []scriptpkg.SegmentAssetCandidate(nil)
	catalogMetrics := entityImageCatalogMetricsFor(p.metrics)
	resolution, resolutionErr := resolveEntityImageCatalogCache(ctx, entityImageResolverDeps{
		catalog: p.catalog, cache: p.cache, metrics: p.metrics, contentStore: p.contentStore,
	}, plan, query, entityCacheKey, catalogIdentity, catalogEligible, cacheOnly)
	if resolution.ReleaseDiscovery != nil {
		defer resolution.ReleaseDiscovery()
	}
	if resolutionErr != nil {
		return internetImageQueryResult{}, resolutionErr
	}
	catalogFallback = resolution.CatalogFallback
	catalogRefreshRequired = resolution.CatalogRefreshRequired
	if resolution.FromCache {
		return internetImageQueryResult{candidates: resolution.Candidates, query: query, fromCache: true}, nil
	}
	return p.searchAndCacheInternetImageQuery(ctx, plan, updated, query, queryCount, queryCandidateLimit, entityImagesEnabled, entityCacheKey, catalogIdentity, catalogEligible, catalogRefreshRequired, catalogFallback, catalogMetrics, personIdentityQuery)
}

func (p *MediaResolverImageStage) searchAndCacheInternetImageQuery(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, updated scriptpkg.VidRushSegmentResult, query string, queryCount, queryCandidateLimit int, entityImagesEnabled bool, entityCacheKey string, catalogIdentity entitycatalog.PersonIdentity, catalogEligible, catalogRefreshRequired bool, catalogFallback []scriptpkg.SegmentAssetCandidate, catalogMetrics scriptmetrics.EntityImageCatalogMetrics, personIdentityQuery bool) (internetImageQueryResult, error) {
	imageLimit := queryCandidateLimit
	if catalogEligible && queryCount == 1 && imageLimit < entityImageCatalogDiscoveryLimit {
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
		searchQuery := query
		if personIdentityQuery {
			searchEntity = catalogIdentity.CanonicalName
			// Short first-name queries are ambiguous even when the catalog has
			// resolved a canonical identity. Search with the complete identity
			// and the generation topic so a story about Corrado Augias cannot
			// retrieve an unrelated Natalia (for example Natalia Oreiro).
			searchQuery = strings.TrimSpace(catalogIdentity.CanonicalName + " " + plan.Topic)
			if searchQuery == "" {
				searchQuery = catalogIdentity.CanonicalName
			}
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
				return internetImageQueryResult{}, catalogErr
			}
			filteredResults, filterErr := excludeUnavailableEntityImageCatalogCandidates(ctx, p.catalog, catalogIdentity, results)
			if filterErr != nil {
				return internetImageQueryResult{}, filterErr
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
				return internetImageQueryResult{}, cacheErr
			}
		}
	}
	if err != nil && len(catalogFallback) > 0 {
		return internetImageQueryResult{candidates: catalogFallback, query: query, err: err}, nil
	}
	return internetImageQueryResult{candidates: results, query: query, err: err}, nil
}

// finalizeInternetImageQuery scores, hydrates and filters one query's
// candidates, returning the surviving set plus the warnings it produced.
func (p *MediaResolverImageStage) finalizeInternetImageQuery(
	ctx context.Context,
	input ProcessInput,
	sceneIdx sceneIdentityIndex,
	updated scriptpkg.VidRushSegmentResult,
	qr internetImageQueryResult,
) ([]scriptpkg.SegmentAssetCandidate, []string, error) {
	var warns []string
	identity, personIdentityQuery, identityErr := personCatalogIdentityForQuery(input.SpecScene, sceneIdx, updated, qr.query)
	if identityErr != nil {
		return nil, nil, identityErr
	}
	if personIdentityQuery {
		for i := range qr.candidates {
			qr.candidates[i].Entity = identity.CanonicalName
		}
	}
	// Cached/provider candidates arrive PRE-verification: stamp the
	// durable catalog verdicts first so a warm reuse skips the download
	// + VLM call (the imagegate.AllCandidatesHaveReusableVerdict fast path in
	// scoreInternetImageCandidates then serves the whole batch).
	aliasGraph := segmentPersonAliasGraph(updated)
	if personIdentityQuery {
		// Only a PERSON query may inherit a durable PERSON verdict. A
		// contextual/typed-entity query that happens to share a surface must
		// not be stamped with a verdict about different pixels.
		hydratePersonImageVisualVerdicts(ctx, p.catalog, aliasGraph, qr.candidates)
	}
	visualClass := searchClassForEntityQuery(updated.Insights.Entities, qr.query)
	if visualClass == "" && personIdentityQuery {
		visualClass = "person"
	}
	scored, scoreErr := resolveEntityVisualCandidates(ctx, p.semanticScorer, visualClass, updated.Text, qr.candidates)
	if scoreErr != nil {
		warns = append(warns, fmt.Sprintf("internet_images: semantic scoring unavailable for segment %s: %v", updated.SegmentID, scoreErr))
	} else {
		qr.candidates = scored
	}
	// The durable entity catalog is PERSON-only. Persisting here regardless of
	// personIdentityQuery was the write that turned contextual scene queries
	// (places, newsroom phrases, "...-portrait" strings) into person:* pools.
	if personIdentityQuery && p.semanticScorer != nil {
		if err := persistPersonImageVisualVerdicts(ctx, p.catalog, identity, aliasGraph, qr.candidates); err != nil {
			return nil, nil, fmt.Errorf("internet_images: persist visual person-image verdicts for segment %s: %w", updated.SegmentID, err)
		}
	}
	if personIdentityQuery && p.semanticScorer != nil {
		// With a scorer wired this is the fail-closed boundary before
		// candidate binding: only a definitive pixel acceptance may reach
		// the selection plan. Without a scorer the stage keeps its
		// documented fail-open discovery contract and leaves the rows
		// pending for downstream verification; the catalog still refuses
		// to reuse them until a real verdict exists.
		qr.candidates = imagegate.FilterVerifiedPersonImageCandidates(qr.candidates)
	}
	recordEntityImageVisualOutcomes(p.metrics, qr.candidates, scoreErr)
	qr.candidates = filterInternetImageCandidates(deduplicateInternetImageCandidates(normalizeInternetImageCandidates(qr.candidates, qr.query)))
	if qr.err != nil {
		if p.metrics != nil {
			p.metrics.IncProviderFailure("internet_images")
		}
		warns = append(warns, fmt.Sprintf("internet_images: search failed for segment %s: %v", updated.SegmentID, qr.err))
	}
	return qr.candidates, warns, nil
}

// internetImageDiscoveryOutcome says how one segment's discovery resolved.
type internetImageDiscoveryOutcome int

const (
	// internetImageSegmentSearch means the query fan-out must run.
	internetImageSegmentSearch internetImageDiscoveryOutcome = iota
	// internetImageSegmentPinned means the manifest pin satisfied discovery.
	internetImageSegmentPinned
	// internetImageSegmentBypassed means the segment may not search at all.
	internetImageSegmentBypassed
	// internetImageSegmentReused means a warm fan-out hit already covers it.
	internetImageSegmentReused
)

// planSegmentInternetImageDiscovery decides, for one segment, whether discovery
// is already satisfied (manifest pin or fan-out reuse), suppressed, or must run,
// and returns the queries the fan-out should resolve. It mutates the segment's
// assets and cache fields exactly where the inline version did.
//
// Pinned-media fast path (opt-in, see media_manifest_shim.go): when the manifest
// pins this scene/segment, discovery is complete by construction — offline
// verified bytes, durable verdicts, zero provider calls. It wins over caches,
// the fan-out short circuit and force refresh alike (a pin is the operator's
// authoritative pick), and it never violates cache_only (no network in either
// path).
//
// A pin is a CONTEXTUAL discovery verdict: it never covers the entity-image
// lane. When the scene asks for per-scene entity portraits, the pinned candidate
// is preserved as the contextual selection and the entity queries still run;
// otherwise the pin would satisfy discovery while every person overlay remained
// imageless.
func (p *MediaResolverImageStage) planSegmentInternetImageDiscovery(
	plan *scriptpkg.ResolvedGenerationPlan,
	updated *scriptpkg.VidRushSegmentResult,
	entityQueries []string,
	entityImagesEnabled bool,
	entityPoolLookup bool,
) ([]string, internetImageDiscoveryOutcome) {
	if pinned := p.pinnedInternetImageState(plan, *updated); len(pinned) > 0 {
		updated.Assets.Candidates = AppendProviderCandidatesUnique(updated.Assets.Candidates, pinned)
		updated.Assets.SecondaryImages = AppendProviderCandidatesUnique(updated.Assets.SecondaryImages, pinned)
		updated.Cache.InternetImages = MediaManifestCacheState
		updated.Cache.InternetImagesProviderSearches = 0
		updated.Cache.InternetImagesNewUploads = 0
		if !entityPoolLookup || len(entityQueries) == 0 {
			if p.metrics != nil {
				p.metrics.IncAssetCache("internet_images", true)
			}
			return nil, internetImageSegmentPinned
		}
	}
	manualImageQueries := ResolveManualSegmentQueries(plan, scriptpkg.CanonicalSegment{ID: updated.SegmentID}, scriptpkg.VidRushProviderInternetImages, mediadomain.SlotSecondaryImage)
	discovery := planInternetImageDiscovery(
		updated.Insights.ImageQueries, entityQueries, manualImageQueries, entityImagesEnabled,
		fanoutAlreadyOwnsInternetImageDiscovery(plan, updated),
		func(query string) bool { return len(entityImageMatches(normalizeEntityMatch(query), "", *updated)) > 0 },
	)
	if discovery.Mode == internetImageDiscoveryBypass {
		updated.Cache.InternetImages = "BYPASSED"
		return nil, internetImageSegmentBypassed
	}
	// A segment-level fan-out hit is sufficient for contextual images.
	// Entity-image mode has a stricter identity contract: a warm generic scene
	// image cannot satisfy a requested person/org/place portrait. Reuse fan-out
	// only when every requested identity has a ready match; otherwise use the
	// query-keyed entity cache/provider path.
	if discovery.Mode == internetImageDiscoveryReuse {
		reuseFanoutInternetImageCandidates(updated)
		updated.Cache.InternetImagesProviderSearches = 0
		return nil, internetImageSegmentReused
	}
	return discovery.Queries, internetImageSegmentSearch
}
