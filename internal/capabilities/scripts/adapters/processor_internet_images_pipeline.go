package adapters

import (
	"sort"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// internetImagePipelineInput is the provider-independent candidate set for a
// single segment. Each stage returns a fresh slice so callers cannot mutate
// the provider result or an earlier stage accidentally.
type internetImagePipelineInput struct {
	Candidates []scriptpkg.SegmentAssetCandidate
	Query      string
}

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
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) {
			continue
		}
		if !validVidRushCandidate(candidate) {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

func rankInternetImageCandidates(in []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := append([]scriptpkg.SegmentAssetCandidate(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return vidRushCandidateIdentity(out[i]) < vidRushCandidateIdentity(out[j])
	})
	return out
}

func materializeInternetImageCandidates(in []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	// Acquisition is owned by VidRushMaterializationProcessor. This stage
	// intentionally performs only the canonical immutable projection needed by
	// downstream materialization and commit.
	return append([]scriptpkg.SegmentAssetCandidate(nil), in...)
}

func commitInternetImageCandidates(
	base []scriptpkg.SegmentAssetCandidate,
	materialized []scriptpkg.SegmentAssetCandidate,
) []scriptpkg.SegmentAssetCandidate {
	return appendProviderCandidatesUnique(base, materialized)
}

func runInternetImageCandidatePipeline(in internetImagePipelineInput) []scriptpkg.SegmentAssetCandidate {
	candidates := normalizeInternetImageCandidates(in.Candidates, in.Query)
	candidates = deduplicateInternetImageCandidates(candidates)
	candidates = filterInternetImageCandidates(candidates)
	candidates = rankInternetImageCandidates(candidates)
	candidates = materializeInternetImageCandidates(candidates)
	return commitInternetImageCandidates(nil, candidates)
}
