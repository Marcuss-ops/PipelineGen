package adapters

import (
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func mergeVidRushProviderOutcome(updated *scriptpkg.VidRushSegmentResult, outcome vidRushProviderOutcome, plan *scriptpkg.ResolvedGenerationPlan, profile scriptpkg.SegmentSemanticProfile, segmentID string) error {
	if outcome.provider == scriptpkg.VidRushProviderArtlist {
		// The fanout is also a trust boundary: cached or provider-returned
		// Artlist candidates must pass the segment-local query/provenance gate
		// before ranking or winner selection. Legacy provider payloads may not
		// carry the envelope yet, so normalize them from this owning segment
		// before applying the strict filter.
		outcome.candidates = normalizeVidRushCandidateList(outcome.candidates, *updated)
		outcome.candidates = filterArtlistCandidatesForSegment(outcome.candidates, *updated, nil)
	}
	for i := range outcome.candidates {
		if normalized, ok := normalizeVidRushCandidate(outcome.candidates[i], *updated); ok {
			outcome.candidates[i] = normalized
		} else {
			outcome.candidates[i] = scriptpkg.SegmentAssetCandidate{}
		}
	}
	filtered := outcome.candidates[:0]
	for _, candidate := range outcome.candidates {
		if strings.TrimSpace(candidate.AssetID) != "" && strings.TrimSpace(candidate.Provider) != "" {
			filtered = append(filtered, candidate)
		}
	}
	outcome.candidates = filtered
	// Providers contribute discovery metadata only. Semantic scoring and
	// winner selection belong to MediaSampler during materialization.
	updated.Assets.Candidates = appendProviderCandidatesUnique(updated.Assets.Candidates, outcome.candidates)
	if outcome.provider == scriptpkg.VidRushProviderInternetImages {
		updated.Assets.SecondaryImages = appendProviderCandidatesUnique(updated.Assets.SecondaryImages, outcome.candidates)
	}
	if outcome.err != nil {
		if outcome.provider == scriptpkg.VidRushProviderArtlist && vidRushArtlistOnlyPlan(plan) {
			return fmt.Errorf("vidrush provider fanout: required artlist search failed for segment %s: %w", updated.SegmentID, outcome.err)
		}
		if outcome.provider == scriptpkg.VidRushProviderYouTube && youtubeSourceRequired(plan, segmentID) {
			return fmt.Errorf("vidrush provider fanout: required youtube source failed for segment %s: %w", updated.SegmentID, outcome.err)
		}
		return nil
	}
	switch outcome.provider {
	case scriptpkg.VidRushProviderYouTube:
		updated.Cache.YouTube = fanoutCacheState(plan, outcome.allCacheHits)
	case scriptpkg.VidRushProviderArtlist:
		// Discovery never selects a winner. PrimaryVideo is assigned only by
		// MediaSampler during materialization.
		updated.Cache.Artlist = fanoutCacheState(plan, outcome.allCacheHits)
	case scriptpkg.VidRushProviderInternetImages:
		updated.Cache.InternetImages = fanoutCacheState(plan, outcome.allCacheHits)
	}
	return nil
}

func fanoutCacheState(plan *scriptpkg.ResolvedGenerationPlan, cacheHit bool) string {
	if plan.MediaPlan.ForceRefreshAssets {
		return "REFRESHED"
	}
	if cacheHit {
		return "HIT_EXACT"
	}
	return "MISS"
}

func mergeVidRushProviderOutcomes(updated *scriptpkg.VidRushSegmentResult, outcomes []vidRushProviderOutcome, plan *scriptpkg.ResolvedGenerationPlan, profile scriptpkg.SegmentSemanticProfile, segmentID string) error {
	for _, outcome := range outcomes {
		if err := mergeVidRushProviderOutcome(updated, outcome, plan, profile, segmentID); err != nil {
			return err
		}
	}
	return nil
}

func providerNames(candidates []scriptpkg.SegmentAssetCandidate) string {
	seen := map[string]bool{}
	var names []string
	for _, candidate := range candidates {
		name := strings.TrimSpace(candidate.Provider)
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return strings.Join(names, ",")
}
