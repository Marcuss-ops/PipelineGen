// vidrush_internet_image_candidates.go — internet image candidate resolution, split out of vidrush_registry_searchers.go (600-line file gate).
package adapters

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

func (f *VidRushProviderFanout) resolveInternetImageCandidates(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, fanoutPlan vidRushFanoutPlan, workerInput scriptpkg.VidRushSegmentResult, outcomes chan<- vidRushProviderOutcome) {
	perQueryLimit := fanoutPlan.perQueryLimit
	segmentID := fanoutPlan.segmentID
	textHash := fanoutPlan.textHash
	imageQueries := fanoutPlan.imageQueries
	// Segment-level cache is keyed on segment identity (SegmentID +
	// TextHash + prompt/model/limit), matching the canonical resolver cache.
	// On the text path the TextHash is deterministic across a warm
	// replay (memory gate), so this cache produces HIT_EXACT without
	// re-calling the provider.
	segmentCacheKeyStr := vidRushFanoutImageCacheKey(&fanoutPlan, plan)
	if !fanoutPlan.parallelQueryChild && !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh {
		if cached, ok := cacheLoad(vidrushImageCache, segmentCacheKeyStr); ok {
			// An empty payload was cached under a plan that did not require an
			// entity-image surface. It is a negative observation, not a hit: a
			// plan that now enables entity images must search instead of
			// inheriting the empty result.
			if payload, ok := cached.(internetImageCachePayload); ok {
				// ELIM-01: a completed-but-empty search is remembered for 15
				// minutes so a repeated entity does not pay the same provider
				// wait twice. It is only a reason not to wait: the outcome is an
				// empty candidate set, exactly like the live search produced.
				if internetImageNegativeFresh(payload, time.Now()) {
					if f.log.Core() != nil {
						f.log.Info("VidRush image negative cache hit", zap.String("segment_id", segmentID), zap.String("reason", payload.NegativeReason))
					}
					outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: nil, allCacheHits: true}
					return
				}
				if len(payload.Candidates) > 0 || !plan.MediaPlan.Extraction.EntityImageSurfaceEnabled() {
					candidates, err := f.verifyPersonImageCandidates(ctx, payload.Candidates)
					outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: candidates, err: err, allCacheHits: true}
					return
				}
			}
		}
		var persisted internetImageCachePayload
		if hit, err := loadVidRushPersistentJSON(ctx, f.cache, "internet_images", segmentCacheKeyStr, &persisted); err != nil {
			outcomes <- vidRushProviderOutcome{provider: "internet_images", err: err}
			return
		} else if hit {
			persisted = sanitizeInternetImageCachePayload(persisted)
			if len(persisted.Candidates) > 0 || persisted.Negative {
				cacheStore(vidrushImageCache, segmentCacheKeyStr, persisted)
			}
			if internetImageNegativeFresh(persisted, time.Now()) {
				if f.log.Core() != nil {
					f.log.Info("VidRush image negative cache hit (durable)", zap.String("segment_id", segmentID), zap.String("reason", persisted.NegativeReason))
				}
				outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: nil, allCacheHits: true}
				return
			}
			if len(persisted.Candidates) > 0 || !plan.MediaPlan.Extraction.EntityImageSurfaceEnabled() {
				candidates, scoreErr := f.verifyPersonImageCandidates(ctx, persisted.Candidates)
				outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: candidates, err: scoreErr, allCacheHits: true}
				return
			}
		}
	}
	if len(imageQueries) > 1 && !fanoutPlan.parallelQueryChild {
		candidates, allCacheHits, queryErr := f.resolveInternetImageQueryBatch(ctx, plan, fanoutPlan, workerInput)
		f.finishInternetImageCandidates(ctx, plan, fanoutPlan, workerInput, segmentCacheKeyStr, candidates, allCacheHits, queryErr, outcomes)
		return
	}
	candidates := make([]scriptpkg.SegmentAssetCandidate, 0)
	seen := make(map[string]struct{})
	allCacheHits := len(imageQueries) > 0
	for _, query := range imageQueries {
		// Discovery cache identity follows the canonical entity and verifier,
		// so language and surrounding scene prose cannot fragment reuse.
		queryClass := searchClassForEntityQuery(workerInput.Insights.Entities, query)
		entityCacheKey := entityImageDiscoveryCacheKey(newEntityImageCacheIdentity(workerInput, query), queryClass)
		catalogIdentity, catalogEligible, catalogErr := personCatalogIdentityForSegmentQuery(workerInput, query)
		if catalogErr != nil {
			outcomes <- vidRushProviderOutcome{provider: "internet_images", err: catalogErr}
			return
		}
		results, catalogFallback, catalogRefreshRequired, fromCache, releaseDiscovery, resolutionErr := f.resolveEntityImageCatalogAndCache(ctx, plan, query, entityCacheKey, catalogIdentity, catalogEligible)
		if releaseDiscovery != nil {
			defer func() {
				if releaseDiscovery != nil {
					releaseDiscovery()
				}
			}()
		}
		if resolutionErr != nil {
			outcomes <- vidRushProviderOutcome{provider: "internet_images", err: resolutionErr}
			return
		}
		catalogMetrics := entityImageCatalogMetricsFor(f.metrics)
		if !fromCache {
			allCacheHits = false
			if catalogMetrics != nil {
				if catalogEligible && (catalogRefreshRequired || plan.MediaPlan.ForceRefreshAssets || plan.ForceRefresh) {
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
				entityName = catalogIdentity.CanonicalName
			}
			imageLimit := perQueryLimit
			if catalogEligible && len(imageQueries) == 1 && imageLimit < entityImageCatalogDiscoveryLimit {
				imageLimit = entityImageCatalogDiscoveryLimit
			}
			searched, err := f.images.SearchImages(ctx, InternetImageSearchRequest{
				// The immutable intent is the provider query and entity identity.
				SegmentID: segmentID, Position: workerInput.Position, Query: entityName, Entity: entityName,
				TextHash: textHash, Language: plan.Language, Limit: imageLimit,
				QueryClass: searchClassForEntityQuery(workerInput.Insights.Entities, query),
				Provider:   "internet_images",
				// A refresh must not resurrect recorded hashes from the
				// durable image index: re-downloaded bytes are re-hashed
				// in-run, keeping overlay refs content-addressed.
				ForceRefresh: plan.MediaPlan.ForceRefreshAssets || plan.ForceRefresh,
			})
			if err != nil {
				if catalogEligible {
					incEntityImageCatalogLifecycle(f.metrics, scriptmetrics.EntityImageStageDiscovery, scriptmetrics.EntityImageOutcomeFailure)
				}
				if f.metrics != nil {
					f.metrics.IncProviderFailure("internet_images")
				}
				outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: catalogFallback, err: err}
				return
			}
			if f.log.Core() != nil {
				f.log.Info("VidRush image candidates received", zap.String("segment_id", segmentID), zap.String("query", query), zap.Int("count", len(searched)), zap.String("first_asset_id", func() string {
					if len(searched) > 0 {
						return searched[0].AssetID
					}
					return ""
				}()), zap.String("first_source_url", func() string {
					if len(searched) > 0 {
						return searched[0].SourceURL
					}
					return ""
				}()))
			}
			providerResults := normalizeInternetImageCatalogResults(searched, query)
			if catalogEligible {
				addEntityImageCatalogCandidates(f.metrics, scriptmetrics.EntityImageStageDiscovery, scriptmetrics.EntityImageOutcomeFound, len(providerResults))
				incEntityImageCatalogLifecycle(f.metrics, scriptmetrics.EntityImageStageDiscovery, scriptmetrics.EntityImageOutcomeSuccess)
			}
			if catalogEligible && f.catalog != nil {
				providerCount := len(providerResults)
				rawProviderResults := providerResults
				rejected := make(map[string]int)
				for _, candidate := range providerResults {
					ok, _, _, reason := evaluatePersonImageCandidate(catalogIdentity, candidate)
					if !ok {
						rejected[reason]++
					}
				}
				providerResults = filterPersonEntityImageCandidatesMeasured(catalogIdentity, providerResults, f.metrics)
				if len(rejected) > 0 && f.log.Core() != nil {
					f.log.Info("VidRush person image catalog gate", zap.String("segment_id", segmentID),
						zap.String("query", query), zap.Int("provider_candidates", providerCount),
						zap.Int("catalog_eligible", len(providerResults)), zap.Any("catalog_rejections", rejected))
				}
				if catalogErr := persistEntityImageCatalogCandidatesMeasured(ctx, f.catalog, catalogIdentity, rawProviderResults, f.metrics); catalogErr != nil {
					outcomes <- vidRushProviderOutcome{provider: "internet_images", err: catalogErr}
					return
				}
				filteredResults, filterErr := excludeUnavailableEntityImageCatalogCandidates(ctx, f.catalog, catalogIdentity, providerResults)
				if filterErr != nil {
					outcomes <- vidRushProviderOutcome{provider: "internet_images", err: filterErr}
					return
				}
				providerResults = filteredResults
			}
			results = AppendProviderCandidatesUnique(catalogFallback, providerResults)
			if len(results) > 0 {
				cacheStore(entityImageCache, entityCacheKey, append([]scriptpkg.SegmentAssetCandidate(nil), results...))
			}
			if results == nil {
				results = []scriptpkg.SegmentAssetCandidate{}
			}
			// Empty entity-image results are temporary provider observations,
			// not stable catalog facts. Avoid poisoning later scenes/jobs with a
			// 48h negative cache entry when the provider's index changes.
			if len(results) > 0 || !plan.MediaPlan.Extraction.EntityImageSurfaceEnabled() {
				// L1 keeps the run-scoped materialized candidate for binding; the
				// durable L2 cache is a discovery projection and must never carry
				// a Drive/content-address that can outlive catalog invalidation.
				cacheResults := sanitizeVidRushDiscoveryCandidates(results)
				if cacheErr := storeVidRushPersistentJSON(ctx, f.cache, "entity_images", entityCacheKey, cacheResults); cacheErr != nil {
					// The per-query cache is only a replay projection. Keep the
					// live provider results and continue fan-out when that
					// projection is unavailable.
					if f.log.Core() != nil {
						f.log.Warn("VidRush image query cache write failed; retaining live candidates", zap.String("query", query), zap.Error(cacheErr))
					}
				}
			}
		}
		for _, cand := range results {
			provider := strings.ToLower(strings.TrimSpace(cand.Provider))
			if provider == "" {
				provider = "internet_images"
			}
			// Search adapters preserve the concrete source (for example,
			// duckduckgo or wikimedia_commons), while the resolver contract
			// routes all approved web-image sources through the internet_images
			// lifecycle. Keep the source in the reason for auditability.
			if !isInternetImageSourceProvider(provider) {
				continue
			}
			if provider != "internet_images" {
				if cand.SelectionReason == "" {
					cand.SelectionReason = "web image retrieved from " + provider
				}
				if strings.TrimSpace(cand.RetrievalProvider) == "" {
					cand.RetrievalProvider = provider
				}
				cand.Provider = "internet_images"
			}
			if strings.TrimSpace(cand.Query) == "" {
				cand.Query = query
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
		if releaseDiscovery != nil {
			releaseDiscovery()
			releaseDiscovery = nil
		}
	}
	if fanoutPlan.parallelQueryChild {
		outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: candidates, allCacheHits: allCacheHits}
		return
	}
	f.finishInternetImageCandidates(ctx, plan, fanoutPlan, workerInput, segmentCacheKeyStr, candidates, allCacheHits, nil, outcomes)
}

// resolveEntityImageCatalogAndCache delegates to the single shared resolver.
// The fan-out owns provider discovery, not a private catalog/cache policy.
func (f *VidRushProviderFanout) resolveEntityImageCatalogAndCache(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, query, entityCacheKey string, catalogIdentity entitycatalog.PersonIdentity, catalogEligible bool) ([]scriptpkg.SegmentAssetCandidate, []scriptpkg.SegmentAssetCandidate, bool, bool, func(), error) {
	resolution, err := resolveEntityImageCatalogCache(ctx, entityImageResolverDeps{
		catalog: f.catalog, cache: f.cache, metrics: f.metrics,
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
	if len(payload.Candidates) > 0 && queryErr == nil {
		cacheStore(vidrushImageCache, segmentCacheKeyStr, payload)
	}
	if negative {
		cacheStore(vidrushImageCache, segmentCacheKeyStr, payload)
		if cacheErr := storeVidRushPersistentJSON(ctx, f.cache, "internet_images", segmentCacheKeyStr, sanitizeInternetImageCachePayload(payload)); cacheErr != nil {
			// A negative projection that cannot be written is only a lost
			// optimisation: the live (empty) result is still authoritative.
			if f.log.Core() != nil {
				f.log.Warn("VidRush negative image cache write failed; continuing with the live empty result", zap.String("segment_id", fanoutPlan.segmentID), zap.Error(cacheErr))
			}
		}
	} else if queryErr == nil && (len(payload.Candidates) > 0 || !plan.MediaPlan.Extraction.EntityImageSurfaceEnabled()) {
		cachePayload := sanitizeInternetImageCachePayload(payload)
		if cacheErr := storeVidRushPersistentJSON(ctx, f.cache, "internet_images", segmentCacheKeyStr, cachePayload); cacheErr != nil {
			// Cache persistence is a projection, not the source of truth for
			// provider discovery. Preserve the live candidates so a cache
			// outage cannot erase usable media before materialization.
			outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: candidates, err: cacheErr}
			return
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
