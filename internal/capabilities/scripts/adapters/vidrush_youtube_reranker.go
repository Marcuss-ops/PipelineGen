package adapters

import (
	"context"
	"fmt"
	"sort"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// YouTubeCandidateReranker may select only one candidate from the supplied
// closed candidate set. It cannot create timestamps or alter windows.
type YouTubeCandidateReranker interface {
	Rerank(context.Context, YouTubeRerankRequest) (int, error)
}

type YouTubeRerankRequest struct {
	Scene          string
	Query          string
	TargetDuration int64
	Candidates     []scriptpkg.SegmentAssetCandidate
}

func youtubeCandidateLimit(req scriptports.VidRushSearchRequest) int {
	limit := req.Limit
	if limit <= 0 || limit > 8 {
		return 8
	}
	return limit
}

func preRankYouTubeCandidates(candidates []scriptpkg.SegmentAssetCandidate, query, subject string, limit int) []scriptpkg.SegmentAssetCandidate {
	if limit <= 0 || limit > 8 {
		limit = 8
	}
	out := append([]scriptpkg.SegmentAssetCandidate(nil), candidates...)
	for i := range out {
		if out[i].RelevanceScore == 0 {
			out[i].RelevanceScore = out[i].Score
		}
		out[i].Score = deterministicYouTubeScore(out[i], query, subject)
		out[i].SemanticStatus = "pre_ranked"
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			if out[i].SourceURL == out[j].SourceURL {
				return out[i].SourceStartMs < out[j].SourceStartMs
			}
			return out[i].SourceURL < out[j].SourceURL
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func deterministicYouTubeScore(candidate scriptpkg.SegmentAssetCandidate, query, subject string) float64 {
	text := candidate.Query + " " + candidate.SelectionReason
	if candidate.Score > 0 {
		return candidate.Score
	}
	if query == "" && subject == "" {
		return 0.1
	}
	score := 0.1
	for _, token := range []string{query, subject} {
		if token != "" && containsFold(text, token) {
			score += 0.4
		}
	}
	return clampYouTubeScore(score)
}

func containsFold(value, needle string) bool {
	return len(needle) > 0 && len(value) > 0 && (value == needle || len(value) >= len(needle) && stringFoldContains(value, needle))
}

func stringFoldContains(value, needle string) bool {
	return len(value) >= len(needle) && fmt.Sprintf("%s", value) != "" && containsLower(value, needle)
}

func containsLower(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		match := true
		for j := range needle {
			if lowerASCII(value[i+j]) != lowerASCII(needle[j]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func lowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

func clampYouTubeScore(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
