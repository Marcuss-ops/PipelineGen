// vidrush_internet_image_candidates.go — internet image candidate resolution, split out of vidrush_registry_searchers.go (600-line file gate).
package vidrush

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptmetrics "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports/metrics"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// resolveInternetImageCandidates owns the internet-images phase of the
// provider fan-out. The setup/cache and query loop live in the phase helper so
// the entrypoint stays a small orchestration seam.
func (f *VidRushProviderFanout) resolveInternetImageCandidates(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, fanoutPlan vidRushFanoutPlan, workerInput scriptpkg.VidRushSegmentResult, outcomes chan<- vidRushProviderOutcome) {
	f.resolveInternetImageCandidatesPhase(ctx, plan, fanoutPlan, workerInput, outcomes)
}

func (f *VidRushProviderFanout) resolveInternetImageCandidatesPhase(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, fanoutPlan vidRushFanoutPlan, workerInput scriptpkg.VidRushSegmentResult, outcomes chan<- vidRushProviderOutcome) {
	cacheKey := vidRushFanoutImageCacheKey(&fanoutPlan, plan)
	if f.replayInternetImageFanout(ctx, plan, fanoutPlan, workerInput, cacheKey, outcomes) {
		return
	}
	if len(fanoutPlan.imageQueries) > 1 && !fanoutPlan.parallelQueryChild {
		candidates, allCacheHits, err := f.resolveInternetImageQueryBatch(ctx, plan, fanoutPlan, workerInput)
		f.finishInternetImageCandidates(ctx, plan, fanoutPlan, workerInput, cacheKey, candidates, allCacheHits, err, outcomes)
		return
	}
	candidates, allCacheHits, err := f.resolveInternetImageQuerySet(ctx, plan, fanoutPlan, workerInput)
	if fanoutPlan.parallelQueryChild {
		outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: candidates, allCacheHits: allCacheHits, err: err}
		return
	}
	f.finishInternetImageCandidates(ctx, plan, fanoutPlan, workerInput, cacheKey, candidates, allCacheHits, err, outcomes)
}

func (f *VidRushProviderFanout) replayInternetImageFanout(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, fanoutPlan vidRushFanoutPlan, workerInput scriptpkg.VidRushSegmentResult, cacheKey string, outcomes chan<- vidRushProviderOutcome) bool {
	if fanoutPlan.parallelQueryChild || plan.MediaPlan.ForceRefreshAssets || plan.ForceRefresh {
		return false
	}
	cached, hit, err := newInternetImageSegmentCache(vidrushImageCache, f.cache).Load(ctx, cacheKey, internetImageObservationPolicy, time.Now())
	if err != nil {
		outcomes <- vidRushProviderOutcome{provider: "internet_images", err: err}
		return true
	}
	if !hit {
		return false
	}
	if cached.Negative {
		if f.log.Core() != nil {
			f.log.Info("VidRush image negative cache hit", zap.String("segment_id", fanoutPlan.segmentID), zap.String("reason", cached.NegativeReason))
		}
		outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: nil, allCacheHits: true}
		return true
	}
	if len(cached.Candidates) == 0 && plan.MediaPlan.Extraction.EntityImageSurfaceEnabled() {
		return false
	}
	candidates, verifyErr := f.verifyPersonImageCandidatesForSegment(ctx, workerInput, cached.Candidates)
	outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: candidates, err: verifyErr, allCacheHits: true}
	return true
}

type vidRushImageQueryOutcome struct {
	candidates       []scriptpkg.SegmentAssetCandidate
	fromCache        bool
	releaseDiscovery func()
}

func (f *VidRushProviderFanout) resolveInternetImageQuerySet(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, fanoutPlan vidRushFanoutPlan, workerInput scriptpkg.VidRushSegmentResult) ([]scriptpkg.SegmentAssetCandidate, bool, error) {
	queries := fanoutPlan.imageQueries
	candidates := make([]scriptpkg.SegmentAssetCandidate, 0)
	seen := make(map[string]struct{})
	allCacheHits := len(queries) > 0
	var releaseDiscovery func()
	defer func() {
		if releaseDiscovery != nil {
			releaseDiscovery()
		}
	}()
	for _, query := range queries {
		outcome, err := f.resolveInternetImageQueryForFanout(ctx, plan, fanoutPlan, workerInput, query, len(queries))
		releaseDiscovery = outcome.releaseDiscovery
		if err != nil {
			return outcome.candidates, false, err
		}
		if !outcome.fromCache {
			allCacheHits = false
		}
		appendVidRushInternetImageCandidates(&candidates, seen, outcome.candidates, query)
		if releaseDiscovery != nil {
			releaseDiscovery()
			releaseDiscovery = nil
		}
	}
	return candidates, allCacheHits, nil
}

func appendVidRushInternetImageCandidates(candidates *[]scriptpkg.SegmentAssetCandidate, seen map[string]struct{}, results []scriptpkg.SegmentAssetCandidate, query string) {
	for _, candidate := range results {
		provider := strings.ToLower(strings.TrimSpace(candidate.Provider))
		if provider == "" {
			provider = "internet_images"
		}
		if !isInternetImageSourceProvider(provider) {
			continue
		}
		if provider != "internet_images" {
			if candidate.SelectionReason == "" {
				candidate.SelectionReason = "web image retrieved from " + provider
			}
			if strings.TrimSpace(candidate.RetrievalProvider) == "" {
				candidate.RetrievalProvider = provider
			}
			candidate.Provider = "internet_images"
		}
		if strings.TrimSpace(candidate.Query) == "" {
			candidate.Query = query
		}
		key := vidRushCandidateIdentity(candidate)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		*candidates = append(*candidates, candidate)
	}
}

func (f *VidRushProviderFanout) resolveInternetImageQueryForFanout(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, fanoutPlan vidRushFanoutPlan, workerInput scriptpkg.VidRushSegmentResult, query string, queryCount int) (vidRushImageQueryOutcome, error) {
	queryClass := searchClassForEntityQuery(workerInput.Insights.Entities, query)
	entityCacheKey := entityImageDiscoveryCacheKey(newEntityImageCacheIdentity(workerInput, query), queryClass)
	catalogIdentity, catalogEligible, err := personCatalogIdentityForSegmentQuery(workerInput, query)
	if err != nil {
		return vidRushImageQueryOutcome{}, err
	}
	results, fallback, refresh, fromCache, release, err := f.resolveEntityImageCatalogAndCache(ctx, plan, query, entityCacheKey, catalogIdentity, catalogEligible)
	outcome := vidRushImageQueryOutcome{candidates: results, fromCache: fromCache, releaseDiscovery: release}
	if err != nil || fromCache {
		return outcome, err
	}
	results, err = f.searchVidRushImageQuery(ctx, plan, fanoutPlan, workerInput, query, queryCount, catalogIdentity, catalogEligible, refresh, fallback)
	outcome.candidates = results
	return outcome, err
}

func (f *VidRushProviderFanout) searchVidRushImageQuery(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, fanoutPlan vidRushFanoutPlan, workerInput scriptpkg.VidRushSegmentResult, query string, queryCount int, identity entitycatalog.PersonIdentity, catalogEligible, refresh bool, fallback []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	catalogMetrics := entityImageCatalogMetricsFor(f.metrics)
	if catalogMetrics != nil {
		if catalogEligible && (refresh || plan.MediaPlan.ForceRefreshAssets || plan.ForceRefresh) {
			catalogMetrics.IncEntityImageCatalogRefresh()
		}
		if catalogEligible {
			catalogMetrics.IncEntityImageCatalogProviderCall()
		}
	}
	if f.metrics != nil {
		f.metrics.IncProviderRequest("internet_images")
	}
	entityName := query
	if catalogEligible {
		entityName = identity.CanonicalName
	}
	imageLimit := fanoutPlan.perQueryLimit
	if catalogEligible && queryCount == 1 && imageLimit < entityImageCatalogDiscoveryLimit {
		imageLimit = entityImageCatalogDiscoveryLimit
	}
	searched, err := f.images.SearchImages(ctx, InternetImageSearchRequest{
		SegmentID: fanoutPlan.segmentID, Position: workerInput.Position, Query: entityName, Entity: entityName,
		TextHash: fanoutPlan.textHash, Language: plan.Language, Limit: imageLimit,
		QueryClass: searchClassForEntityQuery(workerInput.Insights.Entities, query), Provider: "internet_images",
		ForceRefresh: plan.MediaPlan.ForceRefreshAssets || plan.ForceRefresh,
	})
	if err != nil {
		if catalogEligible {
			incEntityImageCatalogLifecycle(f.metrics, scriptmetrics.EntityImageStageDiscovery, scriptmetrics.EntityImageOutcomeFailure)
		}
		if f.metrics != nil {
			f.metrics.IncProviderFailure("internet_images")
		}
		return fallback, err
	}
	if f.log.Core() != nil {
		f.log.Info("VidRush image candidates received", zap.String("segment_id", fanoutPlan.segmentID), zap.String("query", query), zap.Int("count", len(searched)), zap.String("first_asset_id", firstInternetImageAssetID(searched)), zap.String("first_source_url", firstInternetImageSourceURL(searched)))
	}
	results := normalizeInternetImageCatalogResults(searched, query)
	if catalogEligible {
		results, err = f.filterAndPersistVidRushCatalogCandidates(ctx, fanoutPlan.segmentID, query, identity, results)
		if err != nil {
			return nil, err
		}
	}
	results = AppendProviderCandidatesUnique(fallback, results)
	if len(results) > 0 {
		cacheStore(entityImageCache, entityImageKey(workerInput, query), append([]scriptpkg.SegmentAssetCandidate(nil), results...))
	}
	if results == nil {
		results = []scriptpkg.SegmentAssetCandidate{}
	}
	if len(results) > 0 || !plan.MediaPlan.Extraction.EntityImageSurfaceEnabled() {
		cacheResults := sanitizeVidRushDiscoveryCandidates(results)
		if cacheErr := storeVidRushPersistentJSON(ctx, f.cache, "entity_images", entityImageKey(workerInput, query), cacheResults); cacheErr != nil && f.log.Core() != nil {
			f.log.Warn("VidRush image query cache write failed; retaining live candidates", zap.String("query", query), zap.Error(cacheErr))
		}
	}
	return results, nil
}

func entityImageKey(workerInput scriptpkg.VidRushSegmentResult, query string) string {
	queryClass := searchClassForEntityQuery(workerInput.Insights.Entities, query)
	return entityImageDiscoveryCacheKey(newEntityImageCacheIdentity(workerInput, query), queryClass)
}

func (f *VidRushProviderFanout) filterAndPersistVidRushCatalogCandidates(ctx context.Context, segmentID, query string, identity entitycatalog.PersonIdentity, results []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	addEntityImageCatalogCandidates(f.metrics, scriptmetrics.EntityImageStageDiscovery, scriptmetrics.EntityImageOutcomeFound, len(results))
	incEntityImageCatalogLifecycle(f.metrics, scriptmetrics.EntityImageStageDiscovery, scriptmetrics.EntityImageOutcomeSuccess)
	if f.catalog == nil {
		return results, nil
	}
	providerCount := len(results)
	rawResults := results
	rejected := make(map[string]int)
	for _, candidate := range results {
		ok, _, _, reason := evaluatePersonImageCandidate(identity, candidate)
		if !ok {
			rejected[reason]++
		}
	}
	results = filterPersonEntityImageCandidatesMeasured(identity, results, f.metrics)
	if len(rejected) > 0 && f.log.Core() != nil {
		f.log.Info("VidRush person image catalog gate", zap.String("segment_id", segmentID), zap.String("query", query), zap.Int("provider_candidates", providerCount), zap.Int("catalog_eligible", len(results)), zap.Any("catalog_rejections", rejected))
	}
	if err := persistEntityImageCatalogCandidatesMeasured(ctx, f.catalog, identity, rawResults, f.metrics); err != nil {
		return nil, err
	}
	return excludeUnavailableEntityImageCatalogCandidates(ctx, f.catalog, identity, results)
}

func firstInternetImageAssetID(candidates []scriptpkg.SegmentAssetCandidate) string {
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].AssetID
}

func firstInternetImageSourceURL(candidates []scriptpkg.SegmentAssetCandidate) string {
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].SourceURL
}

// resolveEntityImageCatalogAndCache delegates to the single shared resolver.
// The fan-out owns provider discovery, not a private catalog/cache policy.
func (f *VidRushProviderFanout) resolveEntityImageCatalogAndCache(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, query, entityCacheKey string, catalogIdentity entitycatalog.PersonIdentity, catalogEligible bool) ([]scriptpkg.SegmentAssetCandidate, []scriptpkg.SegmentAssetCandidate, bool, bool, func(), error) {
	resolution, err := resolveEntityImageCatalogCache(ctx, entityImageResolverDeps{
		catalog: f.catalog, cache: f.cache, metrics: f.metrics, contentStore: f.contentStore,
	}, plan, query, entityCacheKey, catalogIdentity, catalogEligible, false)
	if err != nil {
		return nil, nil, false, false, nil, err
	}
	return resolution.Candidates, resolution.CatalogFallback, resolution.CatalogRefreshRequired, resolution.FromCache, resolution.ReleaseDiscovery, nil
}

func (f *VidRushProviderFanout) finishInternetImageCandidates(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, fanoutPlan vidRushFanoutPlan, workerInput scriptpkg.VidRushSegmentResult, segmentCacheKeyStr string, candidates []scriptpkg.SegmentAssetCandidate, allCacheHits bool, queryErr error, outcomes chan<- vidRushProviderOutcome) {
	// Empty entity-image results must be searched again on later runs; caching
	// them at segment level would bypass the per-person discovery retry above.
	addEntityImageCatalogCandidates(f.metrics, scriptmetrics.EntityImageStageDiscovery, scriptmetrics.EntityImageOutcomeAccepted, len(candidates))
	// Only canonical PERSON queries belong in the portrait verifier. Running
	// every retrieved image through a "person entity image" scorer discarded
	// valid contextual photos (courthouses, archives, streets) before they
	// could reach the scene, which made distinct non-person searches look empty.
	verified, verifyErr := f.verifySegmentImageCandidates(ctx, workerInput, candidates)
	if verifyErr != nil {
		outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: candidates, err: verifyErr}
		return
	}
	candidates = verified
	payload := internetImageCachePayload{Candidates: append([]scriptpkg.SegmentAssetCandidate(nil), candidates...)}
	// ELIM-01: a completed search with zero usable candidates is cached as a
	// NEGATIVE observation (15 min) instead of being forgotten. Repeating the
	// same entity no longer pays a 20-117 s provider wait for a result the run
	// cannot use. This is never read as proof of absence.
	negative := queryErr == nil && len(payload.Candidates) == 0
	if negative {
		payload.Negative = true
		payload.NegativeReason = "no_results"
		payload.ObservedAt = time.Now()
	}
	if queryErr == nil {
		cacheErr := newInternetImageSegmentCache(vidrushImageCache, f.cache).Store(ctx, segmentCacheKeyStr, payload, internetImageObservationPolicy)
		if cacheErr != nil {
			if negative {
				// A negative projection that cannot be written is only a lost
				// optimisation: the live (empty) result is still authoritative.
				if f.log.Core() != nil {
					f.log.Warn("VidRush negative image cache write failed; continuing with the live empty result", zap.String("segment_id", fanoutPlan.segmentID), zap.Error(cacheErr))
				}
			} else {
				// Cache persistence is a projection, not the source of truth for
				// provider discovery. Preserve the live candidates so a cache
				// outage cannot erase usable media before materialization.
				outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: candidates, err: cacheErr}
				return
			}
		}
	}
	if f.log.Core() != nil {
		f.log.Info("VidRush image fanout merged candidates", zap.String("segment_id", fanoutPlan.segmentID), zap.Int("count", len(candidates)))
	}
	outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: candidates, allCacheHits: allCacheHits, err: queryErr}
}

func (f *VidRushProviderFanout) resolveInternetImageQueryBatch(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, fanoutPlan vidRushFanoutPlan, workerInput scriptpkg.VidRushSegmentResult) ([]scriptpkg.SegmentAssetCandidate, bool, error) {
	type queryResult struct {
		outcome vidRushProviderOutcome
	}
	results := make([]queryResult, len(fanoutPlan.imageQueries))
	var wg sync.WaitGroup
	for i, query := range fanoutPlan.imageQueries {
		i, query := i, query
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case vidRushImageQuerySlots <- struct{}{}:
			case <-ctx.Done():
				results[i].outcome.err = ctx.Err()
				return
			}
			defer func() { <-vidRushImageQuerySlots }()
			childPlan := fanoutPlan
			childPlan.imageQueries = []string{query}
			childPlan.parallelQueryChild = true
			childOutcomes := make(chan vidRushProviderOutcome, 1)
			f.resolveInternetImageCandidates(ctx, plan, childPlan, workerInput, childOutcomes)
			results[i].outcome = <-childOutcomes
		}()
	}
	wg.Wait()

	candidates := make([]scriptpkg.SegmentAssetCandidate, 0)
	seen := make(map[string]struct{})
	allCacheHits := true
	var lastErr error
	for _, result := range results {
		outcome := result.outcome
		allCacheHits = allCacheHits && outcome.allCacheHits
		if outcome.err != nil {
			lastErr = outcome.err
		}
		for _, candidate := range outcome.candidates {
			key := vidRushCandidateIdentity(candidate)
			if key == "" {
				continue
			}
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			candidates = append(candidates, candidate)
		}
	}
	return candidates, allCacheHits, lastErr
}
