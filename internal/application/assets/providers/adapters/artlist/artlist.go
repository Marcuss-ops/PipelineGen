// Package artlist adapts internal/sources/artlist.Service to the
// canonical providers.Provider in internal/application/assets/providers.
//
// Wave 12 scope: this adapter is transitional. The contract will not
// change when artlist migrates to a real domain/application layer,
// but the import path will. Adapter owners update the import path;
// downstream consumers keep using providers.Provider unchanged.
//
// CapabilityFetch is intentionally NOT declared in Capabilities():
// the artlist service has no public fetch binary path, and the
// download pipeline (videomuscles + drive upload) is out of scope
// for the Provider contract. Fetch returns ErrFetchNotImplemented
// only to satisfy the interface; callers should NOT reach it
// (ByCapability(CapabilityFetch) will not return this adapter).
package artlist

import (
	"context"
	"errors"
	"fmt"

	artlistsrc "github.com/Marcuss-ops/PipelineGen/internal/sources/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
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
// CapabilityFetch is intentionally omitted: artlist's download
// pipeline (videomuscles + drive upload) is not a Provider concern.
func (a *Adapter) Capabilities() []providers.Capability {
	return []providers.Capability{
		providers.CapabilitySearch,
		providers.CapabilityVideo,
		providers.CapabilityMusic,
	}
}

// Search implements providers.Provider.
//
// Mapping rules:
//
//   - req.Query               -> artlist SearchRequest.Term
//   - req.Limit               -> artlist SearchRequest.Limit
//   - req.PageToken           -> not supported (artlist has no cursor)
//   - req.TopicOnly           -> not supported (artlist is term-based)
//   - req.Filters.PublishedAfter/Sort/MinDuration/MaxDuration
//     -> not supported on artlist (single global order via the scraper)
//   - req.Filters.MediaTypes  -> not honoured; artlist always returns
//     video/music regardless.
//
// Why the response is already close to canonical: artlist's
// SearchResponse uses []assets.Asset directly (see
// internal/sources/artlist/dto_search.go), so the loop maps each
// Asset into a typed Candidate without inventing new fields. Asset
// canonicalization is the downstream ingest use case's
// responsibility — this adapter returns raw findings only.
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
			SourceName:   a.Name(),
			SourceRef:    asset.ID,
			Title:        asset.Name,
			PreviewURL:   asset.ClipPageURL,
			ThumbnailURL: asset.ThumbnailURL,
			MediaType:    asset.MediaType,
			Duration:     asset.Duration,
			PublishedAt:  nil, // artlist DB record may carry CreatedAt but not publish time
			Score:        0,
		})
	}
	return out, nil
}

// Fetch implements providers.Provider.
//
// Per PR 3E, this adapter does NOT advertise CapabilityFetch in
// Capabilities(): the proper route is Registry.ByCapability, which
// will never return this adapter. If a direct interface call
// reaches here anyway, the method returns a plain unrecoverable
// error - no sentinel is exported, callers MUST not switch on it.
func (a *Adapter) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchedAsset, error) {
	_ = ctx
	_ = req
	return nil, errors.New("artlist: fetch not supported (CapabilityFetch not declared)")
}
