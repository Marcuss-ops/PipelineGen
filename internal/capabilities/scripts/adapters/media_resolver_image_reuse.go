package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// fanoutAlreadyOwnsInternetImageDiscovery reports whether the incremental
// VidRush fan-out already produced the internet-image candidate set for this
// segment. HIT_EXACT and REFRESHED are authoritative: a second provider search
// in the postprocessor would duplicate the exact work the fan-out just did.
//
// MISS is deliberately excluded: an empty/negative fan-out answer stays
// retryable by the postprocessor, matching the existing no-negative-cache
// contract. REFRESHED is also authoritative on a force-refresh plan because
// the fan-out performed that refresh itself.
func fanoutAlreadyOwnsInternetImageDiscovery(plan *scriptpkg.ResolvedGenerationPlan, segment *scriptpkg.VidRushSegmentResult) bool {
	if plan == nil || segment == nil {
		return false
	}
	state := strings.ToUpper(strings.TrimSpace(segment.Cache.InternetImages))
	if state != "HIT_EXACT" && state != "REFRESHED" {
		return false
	}
	if (plan.ForceRefresh || plan.MediaPlan.ForceRefreshAssets) && state != "REFRESHED" {
		return false
	}
	return len(internetImageCandidatesFromSelection(segment.Assets)) > 0
}

// reuseFanoutInternetImageCandidates projects the candidates already attached
// by the fan-out onto both asset surfaces the materializer reads. It never
// invents a candidate and never calls a provider.
func reuseFanoutInternetImageCandidates(segment *scriptpkg.VidRushSegmentResult) {
	if segment == nil {
		return
	}
	candidates := internetImageCandidatesFromSelection(segment.Assets)
	if len(candidates) == 0 {
		return
	}
	segment.Assets.Candidates = AppendProviderCandidatesUnique(segment.Assets.Candidates, candidates)
	segment.Assets.SecondaryImages = AppendProviderCandidatesUnique(segment.Assets.SecondaryImages, candidates)
}

func internetImageCandidatesFromSelection(assets scriptpkg.SegmentAssetSelection) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(assets.Candidates)+len(assets.SecondaryImages))
	seen := make(map[string]struct{}, cap(out))
	for _, candidate := range append(append([]scriptpkg.SegmentAssetCandidate(nil), assets.Candidates...), assets.SecondaryImages...) {
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) {
			continue
		}
		key := vidRushCandidateIdentity(candidate)
		if key == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}
