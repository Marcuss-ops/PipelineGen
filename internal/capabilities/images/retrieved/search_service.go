// Package retrieved — search_service.go declares the narrow
// search-surface for the Retrieved territory.
//
// Per the July 2026 image-restructuring plan, the retrieved
// subpackage owns the search-by-query surface (Wikipedia,
// SearXNG, DuckDuckGo, Drive — see provider_registry.go Step 8).
// This file declares:
//
//   - SearchServicePort — the structural interface that any
//     "search by query" implementation must satisfy. The parent
//     package's *ImageStorageService already satisfies this
//     surface (SearchAndDownload + SearchWebImage methods).
//   - SearchServiceAdapter — a struct that wraps any
//     SearchServicePort into a routing.Service so the parent
//     images/service.go wiring can plug the storage service into
//     the routing.Router.
//
// The structural-port pattern avoids the cycle that would arise
// if this subpackage imported the parent directly: retrieved/
// declares the interface, parent images/ already satisfies it
// at compile time (existing methods on ImageStorageService).
package retrieved

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/workflow/routing"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// SearchServicePort is the structural port that the parent
// package's *ImageStorageService satisfies. Methods are kept
// tight — only what's needed by the routing.Router's
// Retrieved-territory path.
type SearchServicePort interface {
	// SearchAndDownload searches the LOCAL DB first; on miss
	// falls back to the configured retrieval providers in
	// canonical order (Wikipedia → SearXNG → DuckDuckGo →
	// Drive). Mirrors the parent images.service.go façade.
	SearchAndDownload(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string) (*asset.ImageAsset, error)

	// SearchWebImage performs an EXTERNAL web search (today:
	// DuckDuckGo only). Single-result, no fallback chain.
	SearchWebImage(ctx context.Context, prompt, slug string, tags []string) (*asset.ImageAsset, error)
}

// SearchServiceAdapter wraps a SearchServicePort so callers
// that consume routing.Service (e.g. the parent Router) can
// treat the parent's *ImageStorageService as a first-class
// routing service.
//
// The adapter is structurally minimal: it converts a
// SearchRequest to the SearchAndDownload signature and packs
// the result into a routing.SearchResponse.
type SearchServiceAdapter struct {
	port SearchServicePort
}

// NewSearchServiceAdapter constructs a SearchServiceAdapter
// that delegates Search to the supplied port. Pass nil port
// to obtain an adapter that returns ErrPortNotWired on Search.
func NewSearchServiceAdapter(port SearchServicePort) *SearchServiceAdapter {
	return &SearchServiceAdapter{port: port}
}

// Search runs the wrapped SearchAndDownload port with the
// SearchRequest's Query + Lang + Tags. Errors are surfaced
// unchanged. Single-asset result → wrapping into SearchResponse.
func (a *SearchServiceAdapter) Search(ctx context.Context, req routing.SearchRequest) (routing.SearchResponse, error) {
	if a == nil || a.port == nil {
		return routing.SearchResponse{}, ErrPortNotWired
	}
	img, err := a.port.SearchAndDownload(ctx, req.Query, req.Query, req.Query, req.Lang, req.Tags)
	if err != nil {
		return routing.SearchResponse{
			SubService: a.Name(),
		}, err
	}
	if img == nil {
		return routing.SearchResponse{SubService: a.Name()}, nil
	}
	return routing.SearchResponse{
		Assets:     []asset.ImageAsset{*img},
		SubService: a.Name(),
	}, nil
}

// Name returns the territory identifier for the retrieved
// sub-service. Used by the parent Router dispatch logs.
func (a *SearchServiceAdapter) Name() string {
	return "retrieved"
}

// ── Sentinel errors (port-misconfig surface) ──

// ErrPortNotWired is returned when the adapter is invoked
// without a backing SearchServicePort. Composition-root
// misconfiguration.
var ErrPortNotWired = errRetrievedSearch("retrieved.SearchServiceAdapter: port not wired")

type errRetrievedSearch string

func (e errRetrievedSearch) Error() string { return string(e) }
