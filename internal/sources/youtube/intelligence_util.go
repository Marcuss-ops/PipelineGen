package youtube

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/similarity"
)

// ── Text normalization ────────────────────────────────────────────────

func normalizeSemanticText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.NewReplacer(
		"&gt;", " ", "&nbsp;", " ", "https://", " ", "http://", " ",
		",", " ", ".", " ", "!", " ", "?", " ", ";", " ", ":", " ",
		"(", " ", ")", " ", "[", " ", "]", " ", "-", " ", "_", " ",
		"\"", " ", "'", " ", "/", " ", "\\", " ", "|", " ", "#", " ",
	).Replace(text)
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	filtered := make([]string, 0, len(words))
	for _, w := range words {
		if len(w) < 3 || isGenericToken(w) {
			continue
		}
		filtered = append(filtered, w)
	}
	return strings.Join(filtered, " ")
}

func isGenericToken(token string) bool {
	switch token {
	case "the", "and", "for", "with", "that", "this", "from", "you", "your", "are", "was", "were", "has", "have", "had",
		"his", "her", "him", "she", "they", "them", "their", "there", "here", "what", "when", "where", "why", "how",
		"who", "into", "onto", "like", "just", "really", "very", "could", "would", "should", "about", "after",
		"before", "because", "then", "than", "also", "been", "being", "our", "out", "over", "under", "some", "more",
		"most", "much", "many", "way", "one", "two", "three", "all", "not", "can", "will", "able", "if", "or", "so",
		"um", "uh", "https", "http", "www", "com", "nbsp", "code", "watch", "listen", "subscribe", "channel", "official",
		"new", "tour", "dates", "go", "check", "find", "submit", "merch", "music", "producer", "facebook", "instagram",
		"twitter", "spotify", "live", "video", "videos", "clip", "clips":
		return true
	}
	return false
}

// ── Jaccard similarity ────────────────────────────────────────────────

func textJaccardScore(a, b string) float64 {
	return similarity.Jaccard(tokenSetForText(a), tokenSetForText(b))
}

func sliceJaccardScore(a, b []string) float64 {
	return similarity.Jaccard(tokenSetFromStrings(a), tokenSetFromStrings(b))
}

// ── Slice merging ─────────────────────────────────────────────────────

func mergeStringSlices(values ...[]string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, list := range values {
		for _, item := range list {
			norm := normalizeSemanticText(item)
			if norm == "" {
				continue
			}
			if _, ok := seen[norm]; ok {
				continue
			}
			seen[norm] = struct{}{}
			out = append(out, norm)
		}
	}
	return out
}
