package images

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/retrieved"
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

type RetrievalProviderSearchBackend interface {
	SearchProvider(ctx context.Context, provider, query string, opts retrieved.RetrievalSearchOptions) ([]retrieved.RetrievalSearchResult, error)
}

type ImageListRepository interface {
	ListImages(ctx context.Context, filter ImageFilter) ([]ImageSearchResult, error)
}

type SubService interface {
	Search(ctx context.Context, req SearchRequest) (SearchResponse, error)
}
