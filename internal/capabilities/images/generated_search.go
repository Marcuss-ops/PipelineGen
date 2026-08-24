// Package generated — generated_search.go declares the
// read-only search surface for the Generated territory.
//
// Per the July 2026 image-restructuring plan, generated assets
// (GoogleSlides / Flux / NVIDIA outputs) are persisted into
// media_assets with `origin = 'generated'`. Read-only lookup
// of those assets is the canonical "what did I generate for
// this subject/style" question — answered by ListImagesByOrigin
// or ListImagesBySubject on the storage layer, filtered to
// generation providers.
//
// This file declares:
//
//   - GeneratedSearchServicePort — structural interface that
//     any generated-territory DB search implementation must
//     satisfy. The parent's *ImageStorageService satisfies
//     this via its ListImagesByOrigin method (canonical entry).
//   - GeneratedSearchServiceAdapter — wraps a port so the
//     Router can dispatch Generated-territory queries through
//     a uniform routing.Service interface.
package images

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// GeneratedSearchServicePort is the structural port for
// generated-territory DB search. Parent *ImageStorageService
// satisfies this interface via ListImagesByOrigin.
type GeneratedSearchServicePort interface {
	// ListImagesByOrigin returns all media_assets rows where
	// origin matches the supplied asset.ImageOrigin. Used as
	// the canonical read-only entry for generated-territory
	// search. Mirrors the parent images.service.go façade.
	ListImagesByOrigin(ctx context.Context, origin asset.ImageOrigin, limit int) ([]asset.ImageAsset, error)
}

// GeneratedSearchServiceAdapter wraps a port for callers that
// consume routing.Service (Router dispatch).
type GeneratedSearchServiceAdapter struct {
	port GeneratedSearchServicePort
}

// NewGeneratedSearchServiceAdapter constructs an adapter
// around the supplied port. nil port yields an adapter that
// returns ErrGeneratedPortNotWired on Search.
func NewGeneratedSearchServiceAdapter(port GeneratedSearchServicePort) *GeneratedSearchServiceAdapter {
	return &GeneratedSearchServiceAdapter{port: port}
}

// Search delegates to ListImagesByOrigin with the supplied
// request Origin (or AssetOriginRetrieved as fallback). Limit
// comes from SearchRequest.Limit. Empty Origin → returns an
// empty result (defensive: Router already knows which
// territory it's dispatching to).
func (a *GeneratedSearchServiceAdapter) Search(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	if a == nil || a.port == nil {
		return SearchResponse{}, ErrGeneratedPortNotWired
	}
	if req.Origin == "" {
		return SearchResponse{SubService: a.Name()}, nil
	}
	assets, err := a.port.ListImagesByOrigin(ctx, req.Origin, req.Limit)
	if err != nil {
		return SearchResponse{SubService: a.Name()}, err
	}
	return SearchResponse{
		Assets:     assets,
		SubService: a.Name(),
	}, nil
}

// Name returns the territory identifier for the generated
// search service.
func (a *GeneratedSearchServiceAdapter) Name() string {
	return "generated"
}

// ── Sentinel errors ──

// ErrGeneratedPortNotWired is returned when the adapter is
// invoked without a backing port.
var ErrGeneratedPortNotWired = errGeneratedSearch("generated.GeneratedSearchServiceAdapter: port not wired")

type errGeneratedSearch string

func (e errGeneratedSearch) Error() string { return string(e) }
