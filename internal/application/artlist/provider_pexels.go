package artlist

import (
	"context"
	"fmt"

	artfallback "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/fallback"
)

// PR2: LEGACY — Provider migrated to internal/infrastructure/artlist/
// fallback.Pexels in PR2.4. This wrapper bridges the legacy
// SourceProvider shape (Search(ctx, term, limit) -> []ScraperClip)
// used by the chain in search_fallback.go to the new artlist.Searcher
// port implemented by artfallback.Pexels.
//
// Plan: rewrite the chain on the port shape (PR2.5) and delete this
// wrapper along with provider_pixabay.go, provider.go::FallbackChain,
// and the SourceProvider interface.

type PexelsProvider struct {
	inner *artfallback.Pexels
}

func NewPexelsProvider(apiKey, baseURL string) *PexelsProvider {
	if apiKey == "" {
		// Mirror the previous behaviour: an empty key produced a
		// Provider that fails at Search time. Same error semantics
		// so callers don't observe a behaviour drift.
		return &PexelsProvider{}
	}
	return &PexelsProvider{
		inner: artfallback.NewPexels(artfallback.Config{
			APIKey:  apiKey,
			BaseURL: baseURL,
		}),
	}
}

func (p *PexelsProvider) Name() string { return "pexels" }

func (p *PexelsProvider) Search(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	if p.inner == nil {
		return nil, fmt.Errorf("pexels api key not configured")
	}
	candidates, err := p.inner.Search(ctx, SearchRequest{Term: term, Limit: limit})
	if err != nil {
		return nil, err
	}
	return candidatesToScraperClips(candidates), nil
}
