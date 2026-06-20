// Package artlist adapts the existing artlist.Service to the
// canonical providers.Provider interface defined in
// internal/application/providers.
//
// This file lives under internal/application/ to be reachable from
// both the composition root (internal/app) and the public HTTP
// handlers (internal/api). It imports internal/sources/artlist
// directly because the source package has not yet been migrated to
// a domain/application layer (Wave 12 scope). When that migration
// lands, this file will be re-pointed at the new home WITHOUT an API
// change — the providers.Provider contract makes that transparent.
package artlist

import (
	"context"
	"errors"
	"fmt"

	artlistsrc "github.com/Marcuss-ops/PipelineGen/internal/sources/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/application/providers"
)

// Compile-time assertion: *Adapter satisfies providers.Provider.
// Catches interface drift at build time.
var _ providers.Provider = (*Adapter)(nil)

// ErrSourceNotWired is returned when an Adapter is constructed
// without a non-nil underlying artlist.Service.
var ErrSourceNotWired = errors.New("artlist adapter: source not wired")

// Adapter wraps artlist.Service and presents it as a Provider.
// It does NOT introduce new search semantics: it translates the
// canonical providers.SearchRequest into the service's native
// request at the boundary, and translates the SearchResponse back
// into canonical providers.Candidate values.
type Adapter struct {
	src *artlistsrc.Service
}

// NewAdapter returns an Adapter wrapping the given service. The
// service must be fully wired (composition root responsibility).
func NewAdapter(src *artlistsrc.Service) *Adapter { return &Adapter{src: src} }

// Name implements providers.Provider.
func (a *Adapter) Name() string { return "artlist" }

// Capabilities implements providers.Provider.
func (a *Adapter) Capabilities() []providers.Capability {
	return []providers.Capability{
		providers.CapabilitySearch,
		providers.CapabilityFetch,
		providers.CapabilityVideo,
		providers.CapabilityMusic,
	}
}

// Search implements providers.Provider.
//
// Mapping rules:
//
//   - req.Query  -> artlist SearchRequest.Term
//   - req.Limit  -> artlist SearchRequest.Limit
//   - PageToken / TopicOnly / Filters -> not supported by artlist.
//     The native scraper ignores these keys; downstream consumers
//     that need them must use the youtube adapter instead.
//
// Why the response is already canonical: artlist's SearchResponse
// uses []assets.Asset directly (see internal/sources/artlist/dto_search.go),
// so the loop is a structural copy rather than a field-by-field
// conversion.
func (a *Adapter) Search(ctx context.Context, req providers.SearchRequest) ([]providers.Candidate, error) {
	if a.src == nil {
		return nil, ErrSourceNotWired
	}

	native := &artlistsrc.SearchRequest{
		Term:     req.Query,
		Limit:    req.Limit,
		PreferDB: false,
	}

	resp, err := a.src.Search(ctx, native)
	if err != nil {
		return nil, fmt.Errorf("artlist search: %w", err)
	}
	if resp == nil {
		return nil, nil
	}

	out := make([]providers.Candidate, 0, len(resp.Clips))
	for i := range resp.Clips {
		asset := &resp.Clips[i]
		out = append(out, providers.Candidate{
			AssetID:          asset.ID,
			Title:            asset.Name,
			SourceName:       a.Name(),
			ProvisionalAsset: asset,
			ProviderMetadata: map[string]any{
				"native_source": resp.Source,
				"ok":            resp.OK,
			},
		})
	}
	return out, nil
}

// Fetch implements providers.Provider.
//
// The artlist service does not expose a public fetch binary path:
// actual binary download is handled by the existing pipeline
// (videomuscles + drive upload) and is out of scope for this
// adapter. We return providers.ErrFetchNotImplemented so callers
// can detect the gap with errors.Is and fall back to the legacy
// pipeline without coupling.
func (a *Adapter) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchedAsset, error) {
	_ = ctx
	_ = req
	return nil, providers.ErrFetchNotImplemented
}
