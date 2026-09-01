// Package stockintelligence — resolver.go is the LOCAL FIRST PROVIDER
// SECOND resolver. It consults the local Qdrant search first, hydrates
// the candidates from SQLite (the truth), and falls back to the provider
// live browser ONLY when local_candidates < threshold OR best_score <
// minimum_quality.
package stockintelligence

import (
	"context"
	"fmt"
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
	provider ProviderClientPort
	// Sampler picks the winner asset id from the candidate set. It is
	// injected so the resolver does not depend on the Rust boundary at
	// construction time. Returns "" when no candidate reaches minimum
	// quality.
	Sampler func(candidates []Candidate, segmentID, subject string, visualTerms []string) (string, error)
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

// Resolve runs the LOCAL FIRST PROVIDER SECOND pipeline for one request.
//
//  1. Search the local Qdrant index.
//  2. Hydrate the candidate labels from SQLite (the truth).
//  3. If local_candidates >= threshold AND best_score >= minimum_quality,
//     pick the winner locally → 0 provider requests.
//  4. Otherwise, make ONE provider live request, merge the candidates,
//     and pick the winner from the merged set → 1 provider request.
func (r *Resolver) Resolve(ctx context.Context, req ResolveRequest) (ResolveResult, error) {
	threshold := req.LocalCandidateThreshold
	if threshold == 0 {
		threshold = defaultLocalCandidateThreshold
	}
	minQuality := req.MinimumQuality
	if minQuality == 0 {
		minQuality = defaultMinimumQuality
	}
	limit := defaultSearchLimit

	// 1. LOCAL FIRST: Qdrant local search.
	localCands, err := r.local.SearchLocal(ctx, req.Query, limit)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("stockintelligence: local search: %w", err)
	}

	// 2. SQLite hydrate (truth). Hydrate labels for the local asset ids.
	if len(localCands) > 0 {
		ids := make([]string, 0, len(localCands))
		for _, c := range localCands {
			ids = append(ids, c.AssetID)
		}
		labels, err := r.hydrate.Hydrate(ctx, ids)
		if err != nil {
			return ResolveResult{}, fmt.Errorf("stockintelligence: hydrate: %w", err)
		}
		verified := localCands[:0]
		for i := range localCands {
			if label, ok := labels[localCands[i].AssetID]; ok && localCands[i].Label == "" {
				localCands[i].Label = label
			}
			// Qdrant is only a search projection. A hit with no SQLite truth
			// is an orphan and must not influence thresholds or become a
			// selected asset.
			if localCands[i].Label == "" {
				continue
			}
			localCands[i].Source = "local"
			verified = append(verified, localCands[i])
		}
		localCands = verified
	}

	result := ResolveResult{
		SegmentID:           req.SegmentID,
		Candidates:          localCands,
		LocalCandidateCount: len(localCands),
	}

	// 3. Decide whether the provider live path is needed.
	bestLocal := bestScore(localCands)
	needProvider := len(localCands) < threshold || bestLocal < minQuality

	if !needProvider && len(localCands) > 0 {
		// Local-first success: pick the winner locally, 0 provider requests.
		winner, err := r.Sampler(localCands, req.SegmentID, req.Subject, req.VisualTerms)
		if err != nil {
			return ResolveResult{}, fmt.Errorf("stockintelligence: local sampler: %w", err)
		}
		result.WinnerAssetID = winner
		result.ProviderLiveRequests = 0
		return result, nil
	}

	// 4. PROVIDER SECOND: exactly one live provider request.
	reason := fmt.Sprintf("local_candidates=%d (threshold=%d), best_score=%.2f (min=%.2f)", len(localCands), threshold, bestLocal, minQuality)
	providerCands, err := r.provider.SearchProvider(ctx, req.Query, limit)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("stockintelligence: provider search: %w", err)
	}
	for i := range providerCands {
		providerCands[i].Source = "provider"
		if providerCands[i].OwnerSegmentID == "" {
			providerCands[i].OwnerSegmentID = req.SegmentID
		}
	}
	merged := append([]Candidate{}, localCands...)
	merged = append(merged, providerCands...)
	result.Candidates = merged
	result.ProviderLiveRequests = 1
	result.FallbackReason = reason

	winner, err := r.Sampler(merged, req.SegmentID, req.Subject, req.VisualTerms)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("stockintelligence: fallback sampler: %w", err)
	}
	result.WinnerAssetID = winner
	return result, nil
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
