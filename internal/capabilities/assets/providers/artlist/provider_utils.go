package artlist

import (
	"context"
	"errors"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func cloneMetadata(metadata asset.Metadata) map[string]any {
	if metadata == nil {
		return nil
	}
	out := make(map[string]any, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}
