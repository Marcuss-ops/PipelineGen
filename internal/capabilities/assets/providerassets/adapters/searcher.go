package adapters

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providerassets"
	artapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
)

// SearcherAdapter adapts any artlist.Searcher to the
// providerassets.ProviderAdapter contract. It maps the canonical
// providerassets.SearchRequest to the artlist-native SearchRequest and
// returns the results as providerassets.ProviderAsset values.
type SearcherAdapter struct {
	name string
	src  artapp.Searcher
}

// NewSearcherAdapter returns an adapter wrapping src.
func NewSearcherAdapter(name string, src artapp.Searcher) *SearcherAdapter {
	return &SearcherAdapter{name: name, src: src}
}

// Name implements providerassets.ProviderAdapter.
func (a *SearcherAdapter) Name() string { return a.name }

// Search implements providerassets.ProviderAdapter.
func (a *SearcherAdapter) Search(ctx context.Context, req providerassets.SearchRequest) (providerassets.SearchResult, error) {
	if a.src == nil {
		return providerassets.SearchResult{}, ErrAdapterNotWired
	}
	candidates, err := a.src.Search(ctx, artapp.SearchRequest{
		Term:  req.Query,
		Limit: req.Limit,
	})
	if err != nil {
		return providerassets.SearchResult{}, err
	}
	return providerassets.SearchResult{Assets: candidates}, nil
}
