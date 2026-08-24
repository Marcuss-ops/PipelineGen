// Package adapters provides ProviderAdapter implementations that bridge
// the existing provider/searcher surfaces (providers.SearchProvider and
// artlist.Searcher) into the unified providerassets.ProviderAdapter
// contract. Each adapter is a thin, stateless translation layer so the
// canonical ProviderAsset model can be consumed without changing the
// underlying source integrations.
package assets

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providerassets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
)

// ErrAdapterNotWired is returned when Search is called on an adapter
// whose underlying provider was never supplied.
var ErrAdapterNotWired = errors.New("providerassets: adapter source not wired")

// SearchProviderAdapter adapts any providers.SearchProvider to the
// providerassets.ProviderAdapter contract. It is the canonical bridge
// between the existing provider registry and the unified providerassets
// registry.
type SearchProviderAdapter struct {
	name string
	src  providers.SearchProvider
}

// NewSearchProviderAdapter returns an adapter wrapping src. The name
// argument overrides src.Name(); when empty the adapter uses src.Name().
func NewSearchProviderAdapter(name string, src providers.SearchProvider) *SearchProviderAdapter {
	return &SearchProviderAdapter{name: name, src: src}
}

// Name implements providerassets.ProviderAdapter.
func (a *SearchProviderAdapter) Name() string {
	if a.name != "" {
		return a.name
	}
	if a.src != nil {
		return a.src.Name()
	}
	return ""
}

// Search implements providerassets.ProviderAdapter.
func (a *SearchProviderAdapter) Search(ctx context.Context, req providerassets.SearchRequest) (providerassets.SearchResult, error) {
	if a.src == nil {
		return providerassets.SearchResult{}, ErrAdapterNotWired
	}
	res, err := a.src.Search(ctx, providers.SearchRequest{
		Query: req.Query,
		Limit: req.Limit,
	})
	if err != nil {
		return providerassets.SearchResult{}, err
	}
	return providerassets.SearchResult{
		Assets:        res.Candidates,
		NextPageToken: res.NextPageToken,
	}, nil
}
