package vidrush

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/imagegate"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptmetrics "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports/metrics"
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
	// contentStore resolves a materialized candidate's local path from its
	// content address. Optional: without it the catalog still reuses the
	// Drive artifact, it just cannot name a local copy.
	contentStore EntityImageContentStore
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
		pool, err := entityImageCatalogCandidates(ctx, deps.catalog, catalogIdentity, entityImageCatalogPoolSize, deps.contentStore)
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

	// A forced refresh replaces the discovery projection; it must not destroy
	// the portraits the catalog already verified and copied to Drive. Load
	// them as a fallback so a refresh that yields nothing usable (VLM offline,
	// provider 429/timeout) still renders the already verified assets instead
	// of leaving the scene with zero images.
	if useCatalog && force {
		release := entityImageLocks.Lock("entity-catalog:" + catalogIdentity.CanonicalEntityID)
		pool, err := entityImageCatalogCandidates(ctx, deps.catalog, catalogIdentity, entityImageCatalogPoolSize, deps.contentStore)
		release()
		if err != nil {
			return resolution, err
		}
		resolution.CatalogFallback = pool.Candidates
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
		pool, err := entityImageCatalogCandidates(ctx, deps.catalog, catalogIdentity, entityImageCatalogPoolSize, deps.contentStore)
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

const (
	// v6 adds the intent and the entity-index version to the discovery key.
	// v5 reused one row for every intent of a canonical identity, which made
	// "Nike sportswear" and "Nike statue" share a cache entry.
	entityImageDiscoveryCacheSchema = "entity-image-discovery-v6"
	// entityImageIndexVersionFallback is used only for segments produced
	// before analyzer provenance was propagated. New segments carry
	// bundle.Analyzers.EntityIndex.
	entityImageIndexVersionFallback = "entity_index.v1"
)

// entityImageCacheIdentity is the complete discovery-cache identity of one
// entity-image intent. CanonicalID is empty for unlinked entities; in that
// case the adapter already emits a document-scoped "local:<fingerprint>:<id>"
// value, so the cache remains document-scoped instead of becoming a global
// surface cache.
type entityImageCacheIdentity struct {
	CanonicalEntityID string
	IntentKey         string
	EntityIndex       string
}

func newEntityImageCacheIdentity(segment scriptpkg.VidRushSegmentResult, query string) entityImageCacheIdentity {
	entityIndex := strings.TrimSpace(segment.Insights.EntityIndex)
	if entityIndex == "" {
		entityIndex = entityImageIndexVersionFallback
	}
	return entityImageCacheIdentity{
		CanonicalEntityID: canonicalImageSearchEntityID(segment, query),
		IntentKey:         normalizeEntityMatch(query),
		EntityIndex:       entityIndex,
	}
}

// entityImageDiscoveryCacheKey is identity-scoped discovery cache identity.
// Provider identity, verifier policy, the exact intent and the entity-index
// version are part of the key. Replaying a row produced by another intent or
// an older index is not a cache hit: it is evidence about different rules.
func entityImageDiscoveryCacheKey(identity entityImageCacheIdentity, queryClass string) string {
	return entityImageDiscoveryCacheKeyFor(identity, queryClass, scriptpkg.VidRushProviderInternetImages, entitycatalog.CandidateVerifierVersion)
}

func entityImageDiscoveryCacheKeyFor(identity entityImageCacheIdentity, queryClass, provider, verifierVersion string) string {
	return segmentCacheKey(
		entityImageDiscoveryCacheSchema,
		strings.ToLower(strings.TrimSpace(identity.CanonicalEntityID)),
		strings.ToLower(strings.TrimSpace(queryClass)),
		normalizeEntityMatch(identity.IntentKey),
		strings.ToLower(strings.TrimSpace(provider)),
		strings.TrimSpace(verifierVersion),
		strings.TrimSpace(identity.EntityIndex),
	)
}

// contextualImageDiscoveryCacheKey is the query-keyed cache identity for a
// scene query that has no canonical entity. Reuse is determined by the query,
// its class and the language — NOT by the plan topic. Including the topic
// fragmented the cache per video: two runs looking for the same place or
// organization ("RAI", "Via Teulada") missed each other because their titles
// differed, so the provider was queried again for identical bytes.
func contextualImageDiscoveryCacheKey(plan *scriptpkg.ResolvedGenerationPlan, query, queryClass string) string {
	language := ""
	if plan != nil {
		language = plan.Language
	}
	return segmentCacheKey(
		"internet-images-query-v2",
		strings.TrimSpace(language),
		strings.ToLower(strings.TrimSpace(queryClass)),
		normalizeEntityMatch(query),
		strings.ToLower(strings.TrimSpace(scriptpkg.VidRushProviderInternetImages)),
		strings.TrimSpace(entitycatalog.CandidateVerifierVersion),
	)
}

// canonicalImageSearchEntityID consumes only an identity ID emitted by
// BrainRust. Linked entities carry the canonical KB id; unlinked entities
// carry the document-scoped local id the adapter already prefixed with
// "local:<fingerprint>:". Unlinked strings do not become global identities.
func canonicalImageSearchEntityID(segment scriptpkg.VidRushSegmentResult, query string) string {
	key := normalizeEntityMatch(query)
	for _, entity := range segment.Insights.Entities {
		for _, intent := range entity.SearchIntents {
			if normalizeEntityMatch(intent) == key && strings.TrimSpace(entity.CanonicalID) != "" {
				return entity.CanonicalID
			}
		}
	}
	return ""
}

func entityImageCatalogMetricsFor(metrics any) scriptmetrics.EntityImageCatalogMetrics {
	if metrics == nil {
		return nil
	}
	catalogMetrics, _ := metrics.(scriptmetrics.EntityImageCatalogMetrics)
	return catalogMetrics
}

func observeEntityImageCatalogLookup(metrics any, started time.Time) {
	if catalogMetrics := entityImageCatalogMetricsFor(metrics); catalogMetrics != nil {
		catalogMetrics.ObserveEntityImageCatalogLookup(time.Since(started).Seconds())
	}
}

func addEntityImageCatalogCandidates(metrics any, stage, outcome string, count int) {
	if catalogMetrics := entityImageCatalogMetricsFor(metrics); catalogMetrics != nil {
		catalogMetrics.AddEntityImageCatalogCandidates(stage, outcome, count)
	}
}

func incEntityImageCatalogLifecycle(metrics any, stage, outcome string) {
	if catalogMetrics := entityImageCatalogMetricsFor(metrics); catalogMetrics != nil {
		catalogMetrics.IncEntityImageCatalogLifecycle(stage, outcome)
	}
}

func filterPersonEntityImageCandidatesMeasured(identity entitycatalog.PersonIdentity, results []scriptpkg.SegmentAssetCandidate, metrics any) []scriptpkg.SegmentAssetCandidate {
	accepted, rejected := 0, 0
	for _, result := range results {
		ok, _, _, _ := evaluatePersonImageCandidate(identity, result)
		if ok {
			accepted++
		} else {
			rejected++
		}
	}
	addEntityImageCatalogCandidates(metrics, scriptmetrics.EntityImageStageIdentity, scriptmetrics.EntityImageOutcomeAccepted, accepted)
	addEntityImageCatalogCandidates(metrics, scriptmetrics.EntityImageStageIdentity, scriptmetrics.EntityImageOutcomeRejected, rejected)
	return filterPersonEntityImageCandidates(identity, results)
}

func persistEntityImageCatalogCandidatesMeasured(ctx context.Context, repo entitycatalog.Repository, identity entitycatalog.PersonIdentity, results []scriptpkg.SegmentAssetCandidate, metrics any) error {
	accepted := filterPersonEntityImageCandidates(identity, results)
	err := persistEntityImageCatalogCandidates(ctx, repo, identity, results)
	outcome := scriptmetrics.EntityImageOutcomeSuccess
	if err != nil {
		outcome = scriptmetrics.EntityImageOutcomeFailure
	} else {
		addEntityImageCatalogCandidates(metrics, scriptmetrics.EntityImageStageCatalogPersist, scriptmetrics.EntityImageOutcomeAccepted, len(accepted))
	}
	incEntityImageCatalogLifecycle(metrics, scriptmetrics.EntityImageStageCatalogPersist, outcome)
	return err
}

func recordEntityImageVisualOutcomes(metrics any, candidates []scriptpkg.SegmentAssetCandidate, scoringErr error) {
	if scoringErr != nil {
		incEntityImageCatalogLifecycle(metrics, scriptmetrics.EntityImageStageVisual, scriptmetrics.EntityImageOutcomeFailure)
		return
	}
	if len(candidates) == 0 {
		incEntityImageCatalogLifecycle(metrics, scriptmetrics.EntityImageStageVisual, scriptmetrics.EntityImageOutcomeSkipped)
		return
	}
	accepted, rejected := 0, 0
	for _, candidate := range candidates {
		if candidate.SemanticStatus == entitycatalog.CandidateSemanticRejected ||
			(candidate.SemanticScore > 0 && candidate.SemanticScore < MinInternetImageSemanticScore) {
			rejected++
		} else {
			accepted++
		}
	}
	addEntityImageCatalogCandidates(metrics, scriptmetrics.EntityImageStageVisual, scriptmetrics.EntityImageOutcomeAccepted, accepted)
	addEntityImageCatalogCandidates(metrics, scriptmetrics.EntityImageStageVisual, scriptmetrics.EntityImageOutcomeRejected, rejected)
	incEntityImageCatalogLifecycle(metrics, scriptmetrics.EntityImageStageVisual, scriptmetrics.EntityImageOutcomeSuccess)
}

func observeEntityImageCatalogMaterialization(metrics any, started time.Time) {
	if started.IsZero() || metrics == nil {
		return
	}
	if catalogMetrics, ok := metrics.(scriptmetrics.EntityImageCatalogMetrics); ok {
		catalogMetrics.ObserveEntityImageCatalogMaterialization(time.Since(started).Seconds())
	}
}

const (
	EntityImageVLMOptional = "optional"
	EntityImageVLMOff      = "off"
	EntityImageVLMRequired = "required"
)

// OptionalEntityImageScorer wraps a VLM SemanticCandidateScorer so visual
// verification can be configured as "optional" (default), "off", or "required".
type OptionalEntityImageScorer struct {
	mode string
	vlm  SemanticCandidateScorer
}

// NewOptionalEntityImageScorer creates a scorer honoring mode ("optional",
// "off", or "required"). Unknown/empty modes default to "optional".
func NewOptionalEntityImageScorer(mode string, vlm SemanticCandidateScorer) SemanticCandidateScorer {
	normalized := normalizeEntityImageVLMMode(mode)
	if normalized == EntityImageVLMRequired {
		return vlm
	}
	return &OptionalEntityImageScorer{
		mode: normalized,
		vlm:  vlm,
	}
}

func normalizeEntityImageVLMMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case EntityImageVLMOff, "disabled", "false", "0", "none":
		return EntityImageVLMOff
	case EntityImageVLMRequired, "strict", "true", "1":
		return EntityImageVLMRequired
	default:
		return EntityImageVLMOptional
	}
}

func (s *OptionalEntityImageScorer) ScoreImageCandidates(ctx context.Context, query string, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}
	out := append([]scriptpkg.SegmentAssetCandidate(nil), candidates...)
	if s.mode == EntityImageVLMOff || s.vlm == nil {
		reason := "deterministic identity verification confirmed (vlm disabled)"
		if s.mode != EntityImageVLMOff {
			reason = "deterministic identity verification confirmed (vlm unconfigured)"
		}
		for i := range out {
			acceptDeterministically(&out[i], 0.75, reason)
		}
		return out, nil
	}

	var vlmIndices []int
	var vlmBatch []scriptpkg.SegmentAssetCandidate
	for i := range out {
		cand := &out[i]
		if imagegate.ReusablePersonImageVerdict(*cand) ||
			strings.EqualFold(strings.TrimSpace(cand.SemanticStatus), entitycatalog.CandidateSemanticRejected) {
			continue
		}
		if isTrustedEncyclopediaImageCandidate(*cand) {
			acceptDeterministically(cand, 0.85, "deterministic identity verification confirmed (trusted source)")
			continue
		}
		vlmIndices = append(vlmIndices, i)
		vlmBatch = append(vlmBatch, *cand)
	}
	if len(vlmBatch) == 0 {
		return out, nil
	}

	scored, err := s.vlm.ScoreImageCandidates(ctx, query, vlmBatch)
	if err != nil || len(scored) != len(vlmBatch) {
		for _, idx := range vlmIndices {
			acceptDeterministically(&out[idx], 0.70, "deterministic identity verification confirmed (vlm fallback)")
		}
		return out, nil
	}

	for pos, origIdx := range vlmIndices {
		cand := scored[pos]
		status := strings.ToLower(strings.TrimSpace(cand.SemanticStatus))
		if status == entitycatalog.CandidateSemanticAccepted || status == entitycatalog.CandidateSemanticRejected {
			out[origIdx] = cand
			continue
		}
		// VLM returned unknown (e.g. timeout/circuit-breaker); fall back to
		// deterministic acceptance instead of blocking the slot.
		acceptDeterministically(&cand, 0.70, "deterministic identity verification confirmed (vlm fallback)")
		out[origIdx] = cand
	}
	return out, nil
}

func acceptDeterministically(cand *scriptpkg.SegmentAssetCandidate, floorScore float64, reason string) {
	if cand == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(cand.SemanticStatus), entitycatalog.CandidateSemanticRejected) {
		return
	}
	cand.SemanticStatus = entitycatalog.CandidateSemanticAccepted
	if cand.SemanticScore < floorScore {
		cand.SemanticScore = floorScore
	}
	cand.QualityReason = reason
}

func isTrustedEncyclopediaImageCandidate(cand scriptpkg.SegmentAssetCandidate) bool {
	for _, prov := range []string{cand.RetrievalProvider, cand.Provider} {
		switch strings.ToLower(strings.TrimSpace(prov)) {
		case "wikipedia", "wikimedia", "wikimedia_commons", "wikidata":
			return true
		}
	}
	parsed, err := url.Parse(strings.TrimSpace(cand.SourceURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "wikipedia.org" || strings.HasSuffix(host, ".wikipedia.org") ||
		host == "wikimedia.org" || strings.HasSuffix(host, ".wikimedia.org") ||
		host == "wikidata.org" || strings.HasSuffix(host, ".wikidata.org")
}
