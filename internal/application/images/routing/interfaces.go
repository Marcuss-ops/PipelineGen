// Package routing — public interfaces for the routing layer.
package routing

import "context"

type ImageSearcher interface {
	Search(ctx context.Context, filter ImageFilter) ([]ImageSearchResult, error)
}

type ImageSearchResolver interface {
	Resolve(territory ImageSearchTerritory) (ImageSearcher, error)
}

// RetrievalSearchBackend is the structural port over the
// canonical retrieval provider list (Wikipedia → SearXNG →
// DuckDuckGo → Drive). The `retrieved` subpackage supplies the
// concrete *RetrievalProviderRegistry; the routing package owns
// the shared DTO types (RetrievalSearchOptions, RetrievalSearchResult)
// to avoid a routing → retrieved import edge that completes the
// pre-FASE-8 import cycle.
type RetrievalSearchBackend interface {
	SearchAll(ctx context.Context, query string, opts RetrievalSearchOptions) ([]RetrievalSearchResult, error)
}

// RetrievalProviderSearchBackend is the optional explicit-provider seam used
// by canaries and provider-enabled workflows. Implementations must resolve
// the provider through their existing registry; callers do not get a second
// search implementation.
type RetrievalProviderSearchBackend interface {
	SearchProvider(ctx context.Context, provider, query string, opts RetrievalSearchOptions) ([]RetrievalSearchResult, error)
}

type ImageListRepository interface {
	ListImages(ctx context.Context, filter ImageFilter) ([]ImageSearchResult, error)
}

// Service is the unified router-dispatch contract that the
// per-territory adapters (retrieved.SearchServiceAdapter,
// generated.GeneratedSearchServiceAdapter) implement. The Router
// dispatches Search calls to the Service returned by
// ImageSearchResolver.Resolve; the SearchResponse.SubService
// field is set by each adapter to its territory name for
// downstream dispatch logging.
//
// FASE 8 (July 2026): promoted from a compile-time assertion in
// images/service.go to a first-class interface in the routing
// package. Keeps the adapter↔router contract co-located with
// the other routing-layer interfaces (ImageSearcher,
// ImageSearchResolver, RetrievalSearchBackend, ImageListRepository).
type Service interface {
	Search(ctx context.Context, req SearchRequest) (SearchResponse, error)
	Name() string
}
