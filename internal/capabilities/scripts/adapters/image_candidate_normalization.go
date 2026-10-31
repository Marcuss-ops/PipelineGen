package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// isInternetImageSourceProvider is the explicit allowlist of concrete search
// backends that may enter the generic internet_images media lifecycle.
func isInternetImageSourceProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "internet_images", "duckduckgo", "wikimedia_commons", "searxng":
		return true
	default:
		return false
	}
}

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
	// The semantic gate runs last: a candidate must first be a well-formed
	// internet image before its (optional) relevance verdict is consulted.
	return applyInternetImageSemanticGate(out)
}
