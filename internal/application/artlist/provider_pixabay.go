package artlist

import (
	"context"
	"fmt"

	artfallback "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/fallback"
)

// PR2: LEGACY — Provider migrated to internal/infrastructure/artlist/
// fallback.Pixabay in PR2.4. This wrapper bridges the legacy
// SourceProvider shape (Search(ctx, term, limit) -> []ScraperClip)
// used by the chain in search_fallback.go to the new artlist.Searcher
// port implemented by artfallback.Pixabay.
//
// Plan: rewrite the chain on the port shape (PR2.5) and delete this
// wrapper along with provider_pixabay_test, provider_pexels.go,
// provider.go::FallbackChain, and the SourceProvider interface.

type PixabayProvider struct {
	inner *artfallback.Pixabay
}

func NewPixabayProvider(apiKey, baseURL string) *PixabayProvider {
	if apiKey == "" {
		// Mirror the previous behaviour: an empty key produced a
		// Provider that fails at Search time. We keep the same
		// error semantics so callers don't observe a behaviour drift.
		return &PixabayProvider{}
	}
	return &PixabayProvider{
		inner: artfallback.NewPixabay(artfallback.Config{
			APIKey:  apiKey,
			BaseURL: baseURL,
		}),
	}
}

func (p *PixabayProvider) Name() string { return "pixabay" }

func (p *PixabayProvider) Search(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	if p.inner == nil {
		return nil, fmt.Errorf("pixabay api key not configured")
	}
	candidates, err := p.inner.Search(ctx, SearchRequest{Term: term, Limit: limit})
	if err != nil {
		return nil, err
	}
	return candidatesToScraperClips(candidates), nil
}

// candidatesToScraperClips maps the new infra-port Candidate shape
// to the legacy ScraperClip shape the chain still consumes. Kept
// here (not in the infra pkg) because ScraperClip is an
// application type that the infra layer cannot import.
func candidatesToScraperClips(cs []Candidate) []ScraperClip {
	out := make([]ScraperClip, 0, len(cs))
	for _, c := range cs {
		out = append(out, ScraperClip{
			ClipID:      c.ID,
			ID:          c.ID,
			Title:       c.Title,
			Name:        c.Title,
			PrimaryURL:  c.SourceRef,
			ClipPageURL: c.PageURL,
			StreamURLs:  []string{c.SourceRef},
		})
	}
	return out
}
