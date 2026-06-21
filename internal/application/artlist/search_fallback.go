package artlist

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// searchLiveWithFallbacks orchestrates the fallback chain using the
// Searcher port. Implementations come from infrastructure:
//   - DB: in-memory indexed terms (fast)
//   - CachedSearcher: wraps infrastructure/scraper with L1/L2 cache
//   - Pixabay HTTP (free fallback)
//   - Pexels HTTP (free fallback)
func (ss *SearchService) searchLiveWithFallbacks(ctx context.Context, term string, limit int) ([]Candidate, error) {
	normalizedTerm := normalizeSearchTerm(term)
	if normalizedTerm == "" {
		return nil, fmt.Errorf("term is required")
	}
	if len(normalizedTerm) < 2 {
		return nil, fmt.Errorf("term must be at least 2 characters, got %q", normalizedTerm)
	}
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}

	chain := ss.buildSearcherChain()
	if chain == nil {
		return nil, fmt.Errorf("no search providers configured")
	}

	candidates, err := chain.Search(ctx, SearchRequest{Term: normalizedTerm, Limit: limit})
	if err != nil {
		ss.service.log.Warn("all search providers failed",
			zap.String("term", term),
			zap.Error(err),
		)
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no results from any search provider for %q", normalizedTerm)
	}
	return candidates, nil
}

// buildSearcherChain constructs the Searcher fallback chain from the service
// configuration. Infrastructure searchers are injected here so the application
// layer stays decoupled from concrete implementations.
func (ss *SearchService) buildSearcherChain() *SearcherFallbackChain {
	s := ss.service

	var searchers []Searcher

	// Level 1: DB search (fast, indexed).
	if s.assetStore != nil {
		searchers = append(searchers, NewDBSearcher(s.assetStore))
	}

	// Level 2: Cached scraper (in-memory with background refresh).
	// The infrastructure scraper.Provider satisfies Searcher directly.
	// We wrap it with CachedSearcher for L1 in-memory caching.
	if s.scraperSearcher != nil {
		ttlHours := 24
		if s.cfg != nil && s.cfg.External.ArtlistLiveSearchCacheTTLHours > 0 {
			ttlHours = s.cfg.External.ArtlistLiveSearchCacheTTLHours
		}
		cached := NewCachedSearcher(s.scraperSearcher, s.liveCache, ttlHours, s.log)
		searchers = append(searchers, cached)
	}

	// Level 3: Pixabay API (free fallback).
	if s.pixabaySearcher != nil {
		searchers = append(searchers, s.pixabaySearcher)
	}

	// Level 4: Pexels API (free fallback).
	if s.pexelsSearcher != nil {
		searchers = append(searchers, s.pexelsSearcher)
	}

	if len(searchers) == 0 {
		return nil
	}
	return NewSearcherFallbackChain(searchers...)
}
