// Package stockintelligence — resolver.go is the LOCAL FIRST PROVIDER
// SECOND resolver. It consults the local Qdrant search first, hydrates
// the candidates from SQLite (the truth), and falls back to the provider
// live browser ONLY when local_candidates < threshold OR best_score <
// minimum_quality.
package stockintelligence

import (
	"context"
	"errors"
	"fmt"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"golang.org/x/sync/singleflight"
)

// Default thresholds. When a ResolveRequest does not set them, the
// resolver uses these. The values are deliberately conservative so the
// provider live path is the exception, not the rule.
const (
	// defaultLocalCandidateThreshold is the minimum local candidate count
	// below which the provider live path is consulted. 10 keeps the
	// provider live path off the hot path for any well-stocked local
	// catalog (e.g. 50 relevant hummus videos).
	defaultLocalCandidateThreshold = 10
	// defaultMinimumQuality is the minimum best local score below which
	// the provider live path is consulted.
	defaultMinimumQuality = 0.6
	// defaultSearchLimit is the candidate cap for both local and provider
	// searches.
	defaultSearchLimit = 20
)

// Resolver is the LOCAL FIRST PROVIDER SECOND resolver. It is constructed
// with the three ports (local search, SQLite hydrator, provider client)
// and applies the fallback policy. The Sampler field is the function that
// picks the winner from the merged candidate set (the Rust mediasampler
// is invoked across the boundary; for unit tests a Go stub is used).
type Resolver struct {
	local    LocalSearchPort
	hydrate  AssetHydratorPort
	provider ProviderClientPort // Sampler picks the winner asset id from the candidate set. It is
	//	injected so the resolver does not depend on the Rust boundary at
	//	construction time. Returns "" when no candidate reaches minimum
	//	quality. Sampling ALWAYS runs per request — never cached — so owner,
	//	subject and terms stay scene-correct on cache hits.
	Sampler func(candidates []Candidate, segmentID, subject string, visualTerms []string) (string, error)
	// cache is the optional content-addressed replay projection for the
	// discovery I/O (see cache.go). Nil disables caching entirely.
	cache ResolveCache
	// flight coalesces concurrent identical discovery passes so N scenes
	// resolving the same entity in parallel issue ONE local search / provider
	// request instead of N.
	flight singleflight.Group
}

// NewResolver constructs a Resolver. Every port must be non-nil; a nil
// port returns ErrResolverNotWired so a misconfigured composition root
// fails closed instead of silently degrading to a provider-only path.
func NewResolver(local LocalSearchPort, hydrate AssetHydratorPort, provider ProviderClientPort, sampler func([]Candidate, string, string, []string) (string, error)) (*Resolver, error) {
	if local == nil || hydrate == nil || provider == nil || sampler == nil {
		return nil, ErrResolverNotWired
	}
	return &Resolver{local: local, hydrate: hydrate, provider: provider, Sampler: sampler}, nil
}

// WithCache attaches the content-addressed resolve cache. The cache is a
// replay projection: with it (or without it) Resolve returns the same
// candidates for the same request, only faster on reuse.
func (r *Resolver) WithCache(cache ResolveCache) *Resolver {
	r.cache = cache
	return r
}

// Resolve runs the LOCAL FIRST PROVIDER SECOND pipeline for one request.
//
//  1. Search the local Qdrant index.
//  2. Hydrate the candidate labels from SQLite (the truth).
//  3. If local_candidates >= threshold AND best_score >= minimum_quality,
//     pick the winner locally → 0 provider requests.
//  4. Otherwise, make ONE provider live request, merge the candidates,
//     and pick the winner from the merged set → 1 provider request.
//
// Steps 1-4's I/O is the request-independent work product and goes through
// the content-addressed replay cache (cache.go); the winner is ALWAYS
// sampled fresh against this request's segment/subject/terms.
func (r *Resolver) Resolve(ctx context.Context, req ResolveRequest) (ResolveResult, error) {
	entry, cacheHit, err := r.resolveEntry(ctx, req)
	if err != nil {
		return ResolveResult{}, err
	}
	// Per-request finalize: re-stamp provider ownership onto the sampling
	// segment (identical to the live merge) and sample the winner fresh.
	candidates := cloneCandidates(entry.Candidates)
	stampProviderCandidateOwners(candidates, req.SegmentID)
	samplerLabel := "local sampler"
	if entry.MergedProvider {
		samplerLabel = "fallback sampler"
	}
	winner, err := r.Sampler(candidates, req.SegmentID, req.Subject, req.VisualTerms)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("stockintelligence: %s: %w", samplerLabel, err)
	}
	liveRequests := entry.ProviderLiveRequests
	if cacheHit {
		// A cached serve contacts no provider for this request. The entry's
		// original fallback provenance stays visible in FallbackReason.
		liveRequests = 0
	}
	return ResolveResult{
		SegmentID:            req.SegmentID,
		Candidates:           candidates,
		WinnerAssetID:        winner,
		ProviderLiveRequests: liveRequests,
		LocalCandidateCount:  entry.LocalCandidateCount,
		FallbackReason:       entry.FallbackReason,
		CacheHit:             cacheHit,
	}, nil
}

// resolveEntryResult carries one discovery pass plus its provenance through
// the coalescer.
type resolveEntryResult struct {
	entry    resolveCacheEntry
	cacheHit bool
}

// resolveEntry serves the request-independent discovery work product from
// the cache when possible, otherwise runs (and coalesces) one live pass.
func (r *Resolver) resolveEntry(ctx context.Context, req ResolveRequest) (resolveCacheEntry, bool, error) {
	if r.cache == nil {
		entry, err := r.resolveUncached(ctx, req)
		return entry, false, err
	}
	key := resolveCacheKey(req)
	if req.ForceRefresh {
		// A refresh must execute live even while an identical resolve is in
		// flight, so it bypasses the coalescer as well as the cache read.
		// The refreshed result still rewrites the entry (image caches'
		// force-refresh contract).
		entry, err := r.resolveUncached(ctx, req)
		if err == nil {
			storeResolveCacheEntry(ctx, r.cache, key, entry)
		}
		return entry, false, err
	}
	if entry, hit := loadResolveCacheEntry(ctx, r.cache, key); hit {
		return entry, true, nil
	}
	value, err, _ := r.flight.Do(key, func() (any, error) {
		// Re-check inside the coalescer: a request whose outer lookup raced
		// a just-finished pass must be served by the cache, never by a
		// second live discovery.
		if entry, hit := loadResolveCacheEntry(ctx, r.cache, key); hit {
			return resolveEntryResult{entry: entry, cacheHit: true}, nil
		}
		entry, err := r.resolveUncached(ctx, req)
		if err != nil {
			return resolveEntryResult{}, err
		}
		// Empty candidate sets are temporary provider observations, not
		// stable catalog facts: never cache them (the entity-image caches
		// apply the same rule), so later runs retry discovery.
		storeResolveCacheEntry(ctx, r.cache, key, entry)
		return resolveEntryResult{entry: entry}, nil
	})
	if err != nil {
		return resolveCacheEntry{}, false, err
	}
	result, ok := value.(resolveEntryResult)
	if !ok {
		return resolveCacheEntry{}, false, fmt.Errorf("stockintelligence: invalid coalesced resolve entry")
	}
	return result.entry, result.cacheHit, nil
}

// resolveUncached is the live discovery pass: local search + hydrate + the
// provider fallback policy. It returns the entry PRE-stamp (provider owners
// are bound per request at finalize) so the same entry can serve any segment.
func (r *Resolver) resolveUncached(ctx context.Context, req ResolveRequest) (resolveCacheEntry, error) {
	threshold, minQuality, limit := effectiveResolveLimits(req)

	// 1. LOCAL FIRST: Qdrant local search.
	localCands, err := r.local.SearchLocal(ctx, req.Query, limit)
	if err != nil {
		return resolveCacheEntry{}, fmt.Errorf("stockintelligence: local search: %w", err)
	}

	// 2. SQLite hydrate (truth). Hydrate labels for the local asset ids.
	if len(localCands) > 0 {
		ids := make([]string, 0, len(localCands))
		for _, c := range localCands {
			ids = append(ids, c.AssetID)
		}
		labels, err := r.hydrate.Hydrate(ctx, ids)
		if err != nil {
			return resolveCacheEntry{}, fmt.Errorf("stockintelligence: hydrate: %w", err)
		}
		verified := make([]Candidate, 0, len(localCands))
		for _, candidate := range localCands {
			if label, ok := labels[candidate.AssetID]; ok && candidate.Label == "" {
				candidate.Label = label
			}
			// Qdrant is only a search projection. A hit with no SQLite truth
			// is an orphan and must not influence thresholds or become a
			// selected asset.
			if candidate.Label == "" {
				continue
			}
			candidate.Source = "local"
			verified = append(verified, candidate)
		}
		localCands = verified
	}

	entry := resolveCacheEntry{
		Candidates:          localCands,
		LocalCandidateCount: len(localCands),
	}

	// 3. Decide whether the provider live path is needed.
	bestLocal := bestScore(localCands)
	needProvider := len(localCands) < threshold || bestLocal < minQuality

	if !needProvider && len(localCands) > 0 {
		// Local-first success: 0 provider requests, winner sampled at finalize.
		return entry, nil
	}

	// 4. PROVIDER SECOND: exactly one live provider request.
	reason := fmt.Sprintf("local_candidates=%d (threshold=%d), best_score=%.2f (min=%.2f)", len(localCands), threshold, bestLocal, minQuality)
	providerCands, err := r.provider.SearchProvider(ctx, req.Query, limit)
	if providerUnavailable(err) {
		// registry path is not wired since the 2026-10-05 wiring removal).
		// Issuing the fallback query is pure waste and its failure would kill
		// an otherwise valid local resolve, so the fallback is skipped
		// EXPLICITLY: the result keeps the local truth and records the skip in
		// FallbackReason (never a silent empty success). Asset sufficiency
		// stays fail-closed at the downstream certification gate.
		entry.FallbackReason = reason + "; provider fallback skipped: provider unavailable"
		return entry, nil
	}
	if err != nil {
		return resolveCacheEntry{}, fmt.Errorf("stockintelligence: provider search: %w", err)
	}
	for i := range providerCands {
		providerCands[i].Source = "provider"
	}
	merged := append([]Candidate{}, localCands...)
	merged = append(merged, providerCands...)
	entry.Candidates = merged
	entry.ProviderLiveRequests = 1
	entry.FallbackReason = reason
	entry.MergedProvider = true
	return entry, nil
}

// providerUnavailable reports the structural unavailability of the provider
// fallback: the typed ErrProviderUnavailable from the registry client, or a
// not-registered dispatch error surfaced through any wrapping. Transient
// provider failures (network, rate limit) are NOT unavailable and keep the
// fail-closed behavior.
func providerUnavailable(err error) bool {
	return errors.Is(err, ErrProviderUnavailable) || errors.Is(err, scriptports.ErrVidRushProviderNotFound)
}

// bestScore returns the highest generic similarity among the candidates,
// or 0.0 when the slice is empty.
func bestScore(cands []Candidate) float32 {
	var best float32
	for _, c := range cands {
		if c.GenericSimilarity > best {
			best = c.GenericSimilarity
		}
	}
	return best
}
