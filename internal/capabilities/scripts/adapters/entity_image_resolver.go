package adapters

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// entityImageResolverDeps is the complete dependency set of the durable
// entity-image resolution stage. Keeping it explicit is what makes the stage
// shared: neither the fan-out nor the media postprocessor owns a private copy
// of catalog/cache/filter/persist behavior.
type entityImageResolverDeps struct {
	catalog entitycatalog.Repository
	cache   scriptports.VidRushCachePort
	metrics VidRushMetrics
}

// entityImageCatalogCacheResolution is the result of the shared resolver.
// FromCache means the candidates came from the durable catalog or the entity
// cache and the caller must not perform a provider search. CatalogFallback is
// the usable subset of an insufficient catalog pool; it is appended to a
// provider result so a refresh never discards already verified assets.
type entityImageCatalogCacheResolution struct {
	Candidates             []scriptpkg.SegmentAssetCandidate
	CatalogFallback        []scriptpkg.SegmentAssetCandidate
	CatalogRefreshRequired bool
	FromCache              bool
	// ReleaseDiscovery is non-nil only when the resolver kept the per-entity
	// discovery lock to serialize the first provider population. The caller
	// must defer it until provider I/O completes; the helper owns releasing it
	// on every cache/error path.
	ReleaseDiscovery func()
}

// resolveEntityImageCatalogCache owns the one catalog → cache → filter →
// persist stage. Every lock is released with defer, including lookup,
// persistence and cache-error paths. Provider I/O is deliberately outside this
// function so no catalog/discovery lock is held across the network.
func resolveEntityImageCatalogCache(
	ctx context.Context,
	deps entityImageResolverDeps,
	plan *scriptpkg.ResolvedGenerationPlan,
	query, entityCacheKey string,
	catalogIdentity entitycatalog.PersonIdentity,
	catalogEligible, cacheOnly bool,
) (entityImageCatalogCacheResolution, error) {
	resolution := entityImageCatalogCacheResolution{}
	force := plan != nil && (plan.MediaPlan.ForceRefreshAssets || plan.ForceRefresh)
	useCatalog := catalogEligible && deps.catalog != nil
	catalogMetrics := entityImageCatalogMetricsFor(deps.metrics)

	var releaseCatalog func()
	if useCatalog {
		releaseCatalog = entityImageLocks.Lock("entity-catalog:" + catalogIdentity.CanonicalEntityID)
	}
	defer func() {
		if releaseCatalog != nil {
			releaseCatalog()
		}
	}()

	if useCatalog && !force {
		lookupStarted := time.Now()
		pool, err := entityImageCatalogCandidates(ctx, deps.catalog, catalogIdentity, entityImageCatalogPoolSize)
		observeEntityImageCatalogLookup(deps.metrics, lookupStarted)
		if err != nil {
			return resolution, err
		}
		if entityImageCatalogPoolMeetsDemand(pool, plan.ImagesPerScene) {
			if catalogMetrics != nil {
				catalogMetrics.IncEntityImageCatalogLookup(true)
			}
			resolution.Candidates = pool.Candidates
			resolution.FromCache = true
			return resolution, nil
		}
		if catalogMetrics != nil {
			catalogMetrics.IncEntityImageCatalogLookup(false)
		}
		resolution.CatalogFallback = pool.Candidates
		resolution.CatalogRefreshRequired = true
		if cacheOnly {
			resolution.Candidates = resolution.CatalogFallback
			resolution.FromCache = true
			return resolution, nil
		}
	}

	// The catalog lock protects local durable state only. Release it before
	// the discovery lock, cache I/O and provider work.
	if releaseCatalog != nil {
		releaseCatalog()
		releaseCatalog = nil
	}

	if useCatalog && !force {
		releaseDiscovery := entityImageDiscoveryLocks.Lock("entity-discovery:" + catalogIdentity.CanonicalEntityID)
		defer func() {
			if releaseDiscovery != nil {
				releaseDiscovery()
			}
		}()
		// A concurrent first population may have filled the catalog while this
		// request waited. Re-check before spending a provider call.
		pool, err := entityImageCatalogCandidates(ctx, deps.catalog, catalogIdentity, entityImageCatalogPoolSize)
		if err != nil {
			return resolution, err
		}
		if entityImageCatalogPoolMeetsDemand(pool, plan.ImagesPerScene) {
			if catalogMetrics != nil {
				catalogMetrics.IncEntityImageCatalogLookup(true)
			}
			resolution.Candidates = pool.Candidates
			resolution.FromCache = true
			return resolution, nil
		}
		resolution.CatalogFallback = AppendProviderCandidatesUnique(resolution.CatalogFallback, pool.Candidates)
		resolution.CatalogRefreshRequired = true
		// Keep the lock for the caller's first provider population; the
		// deferred release covers every error/cache path.
		resolution.ReleaseDiscovery = releaseDiscovery
		releaseDiscovery = nil
	}

	if !force && !resolution.CatalogRefreshRequired {
		if cached, ok := cacheLoad(entityImageCache, entityCacheKey); ok {
			if cachedCandidates, ok := cached.([]scriptpkg.SegmentAssetCandidate); ok &&
				(len(cachedCandidates) > 0 || !plan.MediaPlan.Extraction.EntityImageSurfaceEnabled()) {
				resolution.Candidates = append([]scriptpkg.SegmentAssetCandidate(nil), cachedCandidates...)
				resolution.FromCache = true
			}
		}
		if !resolution.FromCache {
			var persisted []scriptpkg.SegmentAssetCandidate
			if hit, err := loadVidRushPersistentJSON(ctx, deps.cache, "entity_images", entityCacheKey, &persisted); err != nil {
				return resolution, err
			} else if hit {
				persisted = sanitizeVidRushDiscoveryCandidates(persisted)
				if len(persisted) > 0 {
					cacheStore(entityImageCache, entityCacheKey, persisted)
				}
				if len(persisted) > 0 || !plan.MediaPlan.Extraction.EntityImageSurfaceEnabled() {
					resolution.Candidates = persisted
					resolution.FromCache = true
				}
			}
		}
	}

	if resolution.FromCache {
		resolution.Candidates = normalizeInternetImageCatalogResults(resolution.Candidates, query)
		if useCatalog {
			resolution.Candidates = filterPersonEntityImageCandidates(catalogIdentity, resolution.Candidates)
			if err := persistEntityImageCatalogCandidates(ctx, deps.catalog, catalogIdentity, resolution.Candidates); err != nil {
				return resolution, err
			}
			filtered, err := excludeUnavailableEntityImageCatalogCandidates(ctx, deps.catalog, catalogIdentity, resolution.Candidates)
			if err != nil {
				return resolution, err
			}
			resolution.Candidates = filtered
		}
	}
	return resolution, nil
}
