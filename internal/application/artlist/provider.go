package artlist

import "context"

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
			lastErr = err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}
