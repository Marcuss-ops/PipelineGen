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

// LocalImageAssetHydrator is an optional repository capability used by the
// semantic index to hydrate Qdrant asset IDs from canonical local metadata.
type LocalImageAssetHydrator interface {
	ListImagesByAssetIDs(ctx context.Context, assetIDs []string) ([]ImageSearchResult, error)
}

// LocalResolvedImageIndex searches metadata for images already ingested by
// this service. Search returns canonical asset IDs; callers hydrate bytes and
// rights metadata from the asset repository instead of treating Qdrant
// payloads as an asset store.
type LocalResolvedImageIndex interface {
	Search(ctx context.Context, query string, limit int) ([]string, error)
	Index(ctx context.Context, assetID, document string) error
}

type SubService interface {
	Search(ctx context.Context, req SearchRequest) (SearchResponse, error)
}
