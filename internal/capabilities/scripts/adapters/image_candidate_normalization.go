package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// normalizeInternetImageCandidates performs discovery normalization only.
// It intentionally does not assign scores or choose a candidate.
func normalizeInternetImageCandidates(in []scriptpkg.SegmentAssetCandidate, query string) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(in))
	for _, candidate := range in {
		if strings.TrimSpace(candidate.Provider) == "" {
			candidate.Provider = scriptpkg.VidRushProviderInternetImages
		}
		if strings.TrimSpace(candidate.Query) == "" {
			candidate.Query = query
		}
		if candidate.RightsStatus == "" {
			candidate.RightsStatus = "unknown"
		}
		if candidate.SelectionReason == "" {
			candidate.SelectionReason = "retrieved image candidate matching a segment entity/query"
		}
		out = append(out, candidate)
	}
	return out
}

func deduplicateInternetImageCandidates(in []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, candidate := range in {
		key := vidRushCandidateIdentity(candidate)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func filterInternetImageCandidates(in []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(in))
	for _, candidate := range in {
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) || !validVidRushCandidate(candidate) {
			continue
		}
		out = append(out, candidate)
	}
	return out
}
