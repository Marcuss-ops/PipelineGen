package artlist

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// searchLiveWithFallbacks orchestrates the fallback chain:
//  1. DB search (fast, indexed terms)
//  2. Cached scraper results (in-memory with background refresh)
//  3. Pixabay API (free fallback)
//  4. Pexels API (free fallback)
func (ss *SearchService) searchLiveWithFallbacks(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	normalizedTerm := normalizeSearchTerm(term)
	if normalizedTerm == "" {
		return nil, fmt.Errorf("term is required")
	}
	// Single-character terms are too short for meaningful search and would be
	// silently dropped by the LIKE fallback (tokens < 2 chars are skipped).
	if len(normalizedTerm) < 2 {
		return nil, fmt.Errorf("term must be at least 2 characters, got %q", normalizedTerm)
	}
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}

	// Build the fallback chain lazily (cheap — no allocations for providers)
	chain := ss.buildFallbackChain()
	if chain == nil {
		return nil, fmt.Errorf("no search providers configured")
	}

	clips, err := chain.Search(ctx, normalizedTerm, limit)
	if err != nil {
		ss.service.log.Warn("all search providers failed",
			zap.String("term", term),
			zap.Error(err),
		)
		return nil, err
	}
	if len(clips) == 0 {
		return nil, fmt.Errorf("no results from any search provider for %q", normalizedTerm)
	}
	return clips, nil
}

// buildFallbackChain constructs the provider fallback chain from the service configuration.
func (ss *SearchService) buildFallbackChain() *FallbackChain {
	s := ss.service

	var providers []SourceProvider

	// Level 1: DB search (fast, indexed). PR2.5: AssetStore port
	// (was *assets.ClipsRepository concrete). DBProvider.NewDBProvider
	// signature was extended to take the AssetStore port.
	if s.assetStore != nil {
		providers = append(providers, NewDBProvider(s.assetStore))
	}

	// Level 2: Cached scraper (in-memory with background refresh)
	scraperProvider := NewScraperProvider(
		s.cfg.External.ArtlistScraperServerURL,
		s.cfg.External.NodeScraperDir,
		s.log,
	)
	ttlHours := 24
	if s.cfg != nil && s.cfg.External.ArtlistLiveSearchCacheTTLHours > 0 {
		ttlHours = s.cfg.External.ArtlistLiveSearchCacheTTLHours
	}
	cachedScraper := NewCachedScraperProvider(scraperProvider, s.liveCache, ttlHours, s.log)
	providers = append(providers, cachedScraper)

	// Level 3: Pixabay API (free fallback)
	if s.cfg != nil && strings.TrimSpace(s.cfg.External.PixabayAPIKey) != "" {
		providers = append(providers, NewPixabayProvider(
			s.cfg.External.PixabayAPIKey,
			s.cfg.External.PixabayBaseURL,
		))
	}

	// Level 4: Pexels API (free fallback)
	if s.cfg != nil && strings.TrimSpace(s.cfg.External.PexelsAPIKey) != "" {
		providers = append(providers, NewPexelsProvider(
			s.cfg.External.PexelsAPIKey,
			s.cfg.External.PexelsBaseURL,
		))
	}

	if len(providers) == 0 {
		return nil
	}
	return NewFallbackChain(providers...)
}
