package assets

import (
	"context"
	"errors"
	"testing"
)

type rateLimitedSearcher struct{}

func (rateLimitedSearcher) Search(context.Context, SearchRequest) ([]Candidate, error) {
	return nil, ErrRateLimited
}

type unexpectedFallbackSearcher struct{}

func (unexpectedFallbackSearcher) Search(context.Context, SearchRequest) ([]Candidate, error) {
	return nil, errors.New("fallback must not run after rate limit")
}

func TestSearcherFallbackChainStopsOnRateLimit(t *testing.T) {
	chain := NewSearcherFallbackChain(rateLimitedSearcher{}, unexpectedFallbackSearcher{})
	_, err := chain.Search(context.Background(), SearchRequest{Term: "mountain sunrise", Limit: 1})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("chain error = %v, want ErrRateLimited", err)
	}
}
