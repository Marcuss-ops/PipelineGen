package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// normalizeVidRushCandidate stamps the canonical segment envelope onto a
// provider candidate. Existing legacy candidates may omit the envelope and
// are upgraded from their owning segment; an already-stamped candidate with
// a conflicting identity is rejected rather than rebound to another segment.
func normalizeVidRushCandidate(candidate scriptpkg.SegmentAssetCandidate, segment scriptpkg.VidRushSegmentResult) (scriptpkg.SegmentAssetCandidate, bool) {
	segmentID := strings.TrimSpace(segment.SegmentID)
	if segmentID == "" {
		return candidate, false
	}
	if stamped := strings.TrimSpace(candidate.SegmentID); stamped != "" && stamped != segmentID {
		return scriptpkg.SegmentAssetCandidate{}, false
	}
	if stamped := strings.TrimSpace(candidate.TextHash); stamped != "" && strings.TrimSpace(segment.TextHash) != "" && stamped != strings.TrimSpace(segment.TextHash) {
		return scriptpkg.SegmentAssetCandidate{}, false
	}
	if strings.TrimSpace(candidate.SegmentID) != "" && candidate.Position != segment.Position {
		return scriptpkg.SegmentAssetCandidate{}, false
	}
	candidate.SegmentID = segmentID
	candidate.Position = segment.Position
	candidate.TextHash = strings.TrimSpace(candidate.TextHash)
	if candidate.TextHash == "" {
		candidate.TextHash = strings.TrimSpace(segment.TextHash)
	}
	if candidate.TextHash == "" {
		identityText := segment.Text
		if strings.TrimSpace(identityText) == "" {
			identityText = segment.SegmentID
		}
		candidate.TextHash = scriptpkg.ComputeCanonicalSegmentTextHash(identityText)
	}
	candidate.Query = strings.TrimSpace(candidate.Query)
	if candidate.Query == "" {
		candidate.Query = firstSegmentAssetQuery(segment)
	}
	candidate.Provider = strings.TrimSpace(candidate.Provider)
	candidate.AssetID = strings.TrimSpace(candidate.AssetID)
	candidate.Entity = strings.TrimSpace(candidate.Entity)
	if strings.TrimSpace(candidate.EntityID) == "" {
		identity := candidate.Entity
		if identity == "" {
			identity = candidate.Query
		}
		if identity == "" {
			identity = segmentID
		}
		candidate.EntityID = "entity:" + provenanceSlug(identity)
	}
	return candidate, candidate.AssetID != "" && candidate.Provider != ""
}

func firstSegmentAssetQuery(segment scriptpkg.VidRushSegmentResult) string {
	for _, query := range [][]string{segment.Insights.ImageQueries, segment.Insights.ArtlistQueries, segment.Insights.YouTubeQueries} {
		for _, value := range query {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	if text := strings.TrimSpace(segment.Text); text != "" {
		return text
	}
	return strings.TrimSpace(segment.SegmentID)
}

func provenanceSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func normalizeVidRushCandidateList(candidates []scriptpkg.SegmentAssetCandidate, segment scriptpkg.VidRushSegmentResult) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if normalized, ok := normalizeVidRushCandidate(candidate, segment); ok {
			out = append(out, normalized)
		}
	}
	return out
}
