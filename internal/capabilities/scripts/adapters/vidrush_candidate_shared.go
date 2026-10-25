// Package adapters — vidrush_candidate_shared.go: the provider-agnostic
// candidate merge helpers shared by the image and YouTube discovery paths.
package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// AppendProviderCandidatesUnique appends provider candidates while preserving
// the caller's order and dropping duplicates by provider identity.
func AppendProviderCandidatesUnique(base, additions []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := append([]scriptpkg.SegmentAssetCandidate(nil), base...)
	seen := make(map[string]struct{}, len(out)+len(additions))
	for _, candidate := range out {
		if key := vidRushCandidateIdentity(candidate); key != "" {
			seen[key] = struct{}{}
		}
	}
	for _, candidate := range additions {
		key := vidRushCandidateIdentity(candidate)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

// vidRushCandidateIdentity is the provider-scoped identity of a candidate. The
// first durable/remote locator wins; an empty identity means the candidate is
// not mergeable and is skipped.
func vidRushCandidateIdentity(candidate scriptpkg.SegmentAssetCandidate) string {
	provider := strings.ToLower(strings.TrimSpace(candidate.Provider))
	for _, value := range []string{candidate.DriveLink, candidate.SourceURL, candidate.PreviewURL, candidate.AssetID} {
		if value = strings.TrimSpace(value); value != "" {
			return provider + "\x00" + strings.ToLower(value)
		}
	}
	return ""
}
