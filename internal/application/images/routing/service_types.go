// Package routing — service_types.go declares the cross-territory
// search request/response DTOs that the ImageSearcher contract
// (FASE 6 routing layer) returns to its callers.
//
// These types used to live alongside the canonical routing
// ImageFilter/ImageSearchResult in routing/dto.go (pre-FASE-8
// design). The FASE 8 cycle-break moved RetrievalSearchOptions /
// RetrievalSearchResult into retrieval_types.go (separate file to
// make the cycle-break scope explicit). This file is the
// third-party of the routing DTO trio and hosts the per-call
// request/response envelopes that every routing-level searcher
// (retrieved.SearchServiceAdapter, generated.GeneratedSearchServiceAdapter)
// returns to the Router dispatcher.
//
// FASE 8 (July 2026): SearchRequest + SearchResponse are the
// canonical envelopes; the subpackage adapters wrap their concrete
// search results into a SearchResponse (with a SubService tag for
// the dispatch log) and convert the inbound SearchRequest into the
// concrete port call.
package routing

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// SearchRequest is the per-call request envelope the Router
// dispatcher hands to every ImageSearcher.Search implementation.
// Carries the query, language tag, tag filters, origin hint, and
// pagination knobs in a structural shape that the retrieved/ and
// generated/ subpackages adapt to their concrete port calls.
type SearchRequest struct {
	Query  string
	Lang   string
	Tags   []string
	Origin asset.ImageOrigin
	Limit  int
}

// SearchResponse is the per-call response envelope the ImageSearcher
// implementations return. Assets is the concrete asset slice
// (zero or one for retrieved; one for generated); SubService is
// the territory identifier ("retrieved" | "generated") the Router
// dispatcher uses for log lines + downstream dispatch.
type SearchResponse struct {
	Assets     []asset.ImageAsset
	SubService string
}
