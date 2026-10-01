package images

import (
	"context"

	retrieved "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/search"
)

type ImageSearcher interface {
	Search(ctx context.Context, filter ImageFilter) ([]ImageSearchResult, error)
}

type ImageSearchResolver interface {
	Resolve(territory ImageSearchTerritory) (ImageSearcher, error)
}

type RetrievalSearchBackend interface {
	SearchAll(ctx context.Context, query string, opts retrieved.RetrievalSearchOptions) ([]retrieved.RetrievalSearchResult, error)
}

// RetrievalBestSearchBackend is the optional capability a retrieval backend
// exposes when it can rank across providers instead of returning the first
// non-empty hit. Searcher bridges prefer it when available so the sequential
// first-hit-wins cascade is never the production path.
type RetrievalBestSearchBackend interface {
	SearchBest(ctx context.Context, query string, opts retrieved.RetrievalSearchOptions) ([]retrieved.RetrievalSearchResult, error)
}

type RetrievalProviderSearchBackend interface {
	SearchProvider(ctx context.Context, provider, query string, opts retrieved.RetrievalSearchOptions) ([]retrieved.RetrievalSearchResult, error)
}

type ImageListRepository interface {
	ListImages(ctx context.Context, filter ImageFilter) ([]ImageSearchResult, error)
}

type SubService interface {
	Search(ctx context.Context, req SearchRequest) (SearchResponse, error)
}
