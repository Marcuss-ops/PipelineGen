// vidrush_internet_image_candidates.go — internet image candidate resolution, split out of vidrush_registry_searchers.go (600-line file gate).
package adapters

import (
	"context"
	"strings"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
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
	if !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh {
		if cached, ok := cacheLoad(vidrushImageCache, segmentCacheKeyStr); ok {
			if payload, ok := cached.(internetImageCachePayload); ok {
				candidates, err := f.verifyPersonImageCandidates(ctx, payload.Candidates)
				outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: candidates, err: err, allCacheHits: true}
				return
			}
		}
		var persisted internetImageCachePayload
		if hit, err := loadVidRushPersistentJSON(ctx, f.cache, "internet_images", segmentCacheKeyStr, &persisted); err != nil {
			outcomes <- vidRushProviderOutcome{provider: "internet_images", err: err}
			return
		} else if hit {
			if len(persisted.Candidates) > 0 {
				cacheStore(vidrushImageCache, segmentCacheKeyStr, persisted)
			}
			candidates, scoreErr := f.verifyPersonImageCandidates(ctx, persisted.Candidates)
			outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: candidates, err: scoreErr, allCacheHits: true}
			return
		}
	}
	candidates := make([]scriptpkg.SegmentAssetCandidate, 0)
	seen := make(map[string]struct{})
	allCacheHits := len(imageQueries) > 0
	for _, query := range imageQueries {
		// Per-query cache keyed on (topic, query, language), stable on
		// the research path where the generated scene text (and thus
		// TextHash) is non-deterministic across runs.
		entityCacheKey := versionedSegmentCacheKey("entity-image-discovery-v2", scriptports.DiscoveryCacheVersion, strings.ToLower(strings.TrimSpace(plan.Topic)), strings.ToLower(strings.TrimSpace(query)), plan.Language)
		catalogIdentity, catalogEligible, catalogErr := personCatalogIdentityForSegmentQuery(workerInput, query)
		if catalogErr != nil {
			outcomes <- vidRushProviderOutcome{provider: "internet_images", err: catalogErr}
			return
		}
		var results []scriptpkg.SegmentAssetCandidate
		catalogFallback := []scriptpkg.SegmentAssetCandidate(nil)
		catalogRefreshRequired := false
		fromCache := false
		var releaseCatalog func()
		catalogMetrics := entityImageCatalogMetricsFor(f.metrics)
		if catalogEligible && f.catalog != nil {
			// Canonical reference-counted per-key locker: the entry is
			// removed when the last holder releases, so the registry
			// never grows with the set of entity IDs seen over the
			// process lifetime (retired sync.Map-of-mutexes pattern).
			releaseCatalog = entityImageLocks.Lock("entity-catalog:" + catalogIdentity.CanonicalEntityID)
			if !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh {
				lookupStarted := time.Now()
				pool, err := entityImageCatalogCandidates(ctx, f.catalog, catalogIdentity, entityImageCatalogPoolSize)
				observeEntityImageCatalogLookup(f.metrics, lookupStarted)
				if err != nil {
					if releaseCatalog != nil {
						releaseCatalog()
					}
					outcomes <- vidRushProviderOutcome{provider: "internet_images", err: err}
					return
				}
				if pool.Sufficient {
					if catalogMetrics != nil {
						catalogMetrics.IncEntityImageCatalogLookup(true)
					}
					results = pool.Candidates
					fromCache = true
				} else {
					if catalogMetrics != nil {
						catalogMetrics.IncEntityImageCatalogLookup(false)
					}
					catalogFallback = pool.Candidates
					catalogRefreshRequired = true
				}
			}
		}
		if !catalogRefreshRequired && !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh && !fromCache {
			if cached, ok := cacheLoad(entityImageCache, entityCacheKey); ok {
				if cachedCandidates, ok := cached.([]scriptpkg.SegmentAssetCandidate); ok {
					results = append([]scriptpkg.SegmentAssetCandidate(nil), cachedCandidates...)
					fromCache = true
				}
			}
			if !fromCache {
				var persisted []scriptpkg.SegmentAssetCandidate
				if hit, err := loadVidRushPersistentJSON(ctx, f.cache, "entity_images", entityCacheKey, &persisted); err != nil {
					if releaseCatalog != nil {
						releaseCatalog()
					}
					outcomes <- vidRushProviderOutcome{provider: "internet_images", err: err}
					return
				} else if hit {
					if len(persisted) > 0 {
						cacheStore(entityImageCache, entityCacheKey, persisted)
					}
					results = persisted
					fromCache = true
				}
			}
		}
		if fromCache {
			results = normalizeInternetImageCatalogResults(results, query)
			if catalogEligible && f.catalog != nil {
				results = filterPersonEntityImageCandidates(catalogIdentity, results)
				if catalogErr := persistEntityImageCatalogCandidates(ctx, f.catalog, catalogIdentity, results); catalogErr != nil {
					if releaseCatalog != nil {
						releaseCatalog()
					}
					outcomes <- vidRushProviderOutcome{provider: "internet_images", err: catalogErr}
					return
				}
				filteredResults, filterErr := excludeUnavailableEntityImageCatalogCandidates(ctx, f.catalog, catalogIdentity, results)
				if filterErr != nil {
					if releaseCatalog != nil {
						releaseCatalog()
					}
					outcomes <- vidRushProviderOutcome{provider: "internet_images", err: filterErr}
					return
				}
				results = filteredResults
			}
		}
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
			searchQuery := personImageSearchQuery(query, catalogEligible)
			imageLimit := perQueryLimit
			if catalogEligible && imageLimit < entityImageCatalogPoolSize {
				imageLimit = entityImageCatalogPoolSize
			}
			searched, err := f.images.SearchImages(ctx, InternetImageSearchRequest{
				// Entity remains canonical; the provider query may include a
				// retrieval hint but candidates are normalized to canonical identity.
				SegmentID: segmentID, Position: workerInput.Position, Query: searchQuery, Entity: query,
				TextHash: textHash, Language: plan.Language, Limit: imageLimit,
				Provider: "internet_images",
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
				if releaseCatalog != nil {
					releaseCatalog()
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
				if catalogErr := persistEntityImageCatalogCandidatesMeasured(ctx, f.catalog, catalogIdentity, providerResults, f.metrics); catalogErr != nil {
					if releaseCatalog != nil {
						releaseCatalog()
					}
					outcomes <- vidRushProviderOutcome{provider: "internet_images", err: catalogErr}
					return
				}
				filteredResults, filterErr := excludeUnavailableEntityImageCatalogCandidates(ctx, f.catalog, catalogIdentity, providerResults)
				if filterErr != nil {
					if releaseCatalog != nil {
						releaseCatalog()
					}
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
				if cacheErr := storeVidRushPersistentJSON(ctx, f.cache, "entity_images", entityCacheKey, results); cacheErr != nil {
					// The per-query cache is only a replay projection. Keep the
					// live provider results and continue fan-out when that
					// projection is unavailable.
					if f.log.Core() != nil {
						f.log.Warn("VidRush image query cache write failed; retaining live candidates", zap.String("query", query), zap.Error(cacheErr))
					}
				}
			}
		}
		if releaseCatalog != nil {
			releaseCatalog()
		}
		for _, cand := range results {
			if strings.TrimSpace(cand.Provider) == "" {
				cand.Provider = "internet_images"
			}
			if strings.TrimSpace(cand.Query) == "" {
				cand.Query = query
			}
			if strings.ToLower(strings.TrimSpace(cand.Provider)) != "internet_images" {
				continue
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
	if len(payload.Candidates) > 0 {
		cacheStore(vidrushImageCache, segmentCacheKeyStr, payload)
	}
	if len(payload.Candidates) > 0 || !plan.MediaPlan.Extraction.EntityImageSurfaceEnabled() {
		if cacheErr := storeVidRushPersistentJSON(ctx, f.cache, "internet_images", segmentCacheKeyStr, payload); cacheErr != nil {
			// Cache persistence is a projection, not the source of truth for
			// provider discovery. Preserve the live candidates so a cache
			// outage cannot erase usable media before materialization.
			outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: candidates, err: cacheErr}
			return
		}
	}
	if f.log.Core() != nil {
		f.log.Info("VidRush image fanout merged candidates", zap.String("segment_id", segmentID), zap.Int("count", len(candidates)))
	}
	outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: candidates, allCacheHits: allCacheHits}
}
