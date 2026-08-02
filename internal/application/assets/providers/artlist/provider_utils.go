package artlist

import (
	"context"
	"errors"
	"strings"
)

// SearcherFallbackChain chains multiple Searcher implementations and tries them
// in order until one returns results. This makes the fallback strategy
// configurable and testable.
type SearcherFallbackChain struct {
	searchers []Searcher
}

// NewSearcherFallbackChain creates a fallback chain from the given searchers.
func NewSearcherFallbackChain(searchers ...Searcher) *SearcherFallbackChain {
	return &SearcherFallbackChain{searchers: searchers}
}

// Search tries each searcher in order. Returns the first non-empty result set.
// If all searchers fail, returns the last searcher's error.
func (fc *SearcherFallbackChain) Search(ctx context.Context, req SearchRequest) ([]Candidate, error) {
	var lastErr error
	for _, s := range fc.searchers {
		candidates, err := s.Search(ctx, req)
		if err == nil && len(candidates) > 0 {
			return candidates, nil
		}
		if err != nil {
			// Rate limiting is a provider-wide condition, not a candidate
			// miss. Do not fan the same query into fallback providers or
			// overwrite the typed signal with a later empty result.
			if errors.Is(err, ErrRateLimited) {
				return nil, err
			}
			lastErr = err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

// getIntFromResult extracts an int from a result map, handling both int and float64 types
func getIntFromResult(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	default:
		return 0
	}
}

// bestPexelsVideoURL selects the best-quality video URL from Pexels video files.
func bestPexelsVideoURL(files []struct {
	ID       int     `json:"id"`
	Quality  string  `json:"quality"`
	FileType string  `json:"file_type"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	FPS      float64 `json:"fps"`
	Link     string  `json:"link"`
}) string {
	var bestURL string
	bestScore := -1
	for _, f := range files {
		if strings.TrimSpace(f.Link) == "" {
			continue
		}
		score := f.Width * f.Height
		if strings.EqualFold(f.Quality, "hd") {
			score += 1_000_000
		}
		if score > bestScore {
			bestScore = score
			bestURL = f.Link
		}
	}
	return bestURL
}
