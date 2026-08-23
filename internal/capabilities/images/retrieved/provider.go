// Package retrieved (application/images/retrieved) — provider.go
// declares the RetrievalProvider interface and the minimal HTTP
// client interface (httpDoer) used by all concrete providers.
// Per PR-IMG-SPLIT-3 (July 2026), interfaces live in their own
// file, separate from concrete implementations and the registry.
//
// FASE 8 (July 2026): the per-call DTOs (RetrievalSearchOptions +
// RetrievalSearchResult) moved to internal/application/images/routing
// to break the routing↔retrieved import cycle.
package retrieved

import (
	"context"
	nethttp "net/http"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// RetrievalProvider is one named retrieval source. Implementations
// live in this package (WikipediaProvider, SearXNGProvider,
// DuckDuckGoProvider, DriveImageProvider) and are wired via
// NewDefaultProviderRegistry at composition time.
//
// FASE 8: Search takes routing.RetrievalSearchOptions + returns
// []routing.RetrievalSearchResult (the canonical home of those types
// after the routing↔retrieved cycle break).
type RetrievalProvider interface {
	// Search runs the provider-specific query and returns the
	// candidates for ingestion. Returns nil + nil when the source
	// is unconfigured or produces no hits (NOT an error).
	Search(ctx context.Context, query string, opts routing.RetrievalSearchOptions) ([]routing.RetrievalSearchResult, error)
	// Name returns the ImageProvider taxonomy constant for this provider.
	Name() asset.ImageProvider
	// Healthy reports whether the provider is reachable in the current
	// environment (config presence + reachable probe). Used by the
	// diagnostics surface to surface "SearXNG unavailable" state.
	Healthy(ctx context.Context) error
}

// httpDoer is the minimal interface over *http.Client that providers
// need. Splitting it out keeps tests focusable.
type httpDoer interface {
	Do(req *nethttp.Request) (*nethttp.Response, error)
}
