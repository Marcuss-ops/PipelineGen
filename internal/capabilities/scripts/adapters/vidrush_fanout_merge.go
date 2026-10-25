package adapters

import (
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func mergeVidRushProviderOutcome(updated *scriptpkg.VidRushSegmentResult, outcome vidRushProviderOutcome, plan *scriptpkg.ResolvedGenerationPlan, profile scriptpkg.SegmentSemanticProfile, segmentID string) error {
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
	updated.Assets.Candidates = AppendProviderCandidatesUnique(updated.Assets.Candidates, outcome.candidates)
	if outcome.provider == scriptpkg.VidRushProviderInternetImages {
		updated.Assets.SecondaryImages = AppendProviderCandidatesUnique(updated.Assets.SecondaryImages, outcome.candidates)
	}
	if outcome.err != nil {
		if outcome.provider == scriptpkg.VidRushProviderYouTube && youtubeSourceRequired(plan, segmentID) {
			return fmt.Errorf("vidrush provider fanout: required youtube source failed for segment %s: %w", updated.SegmentID, outcome.err)
		}
		return nil
	}
	switch outcome.provider {
	case scriptpkg.VidRushProviderYouTube:
		updated.Cache.YouTube = fanoutCacheState(plan, outcome.allCacheHits)
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
