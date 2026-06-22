// Package artlist adapts internal/sources/artlist.Service to the
// canonical providers.SearchProvider contract in
// internal/application/assets/providers.
//
// Wave 12 scope: this adapter is transitional. The contract will not
// change when artlist migrates to a real domain/application layer,
// but the import path will. Adapter owners update the import path;
// downstream consumers keep using providers.SearchProvider
// unchanged.
//
// Layout note (post-Agent-3 cleanup): artlist lives at
// providers/artlist/adapter.go, parallel to youtube/adapter.go. The
// historical nesting under providers/adapters/<src>/ is removed.
package artlist

import (
	"context"
	"errors"
	"fmt"
	artlistsrc "github.com/Marcuss-ops/PipelineGen/internal/application/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"reflect"
)

// Compile-time assertion: *Adapter satisfies providers.SearchProvider.
// Catches interface drift at build time. The Adapter intentionally
// does NOT implement FetchProvider (artlist has no public fetch
// binary path; the download pipeline lives in stockpipeline +
// drive upload, out of scope for the Provider contract).
var _ providers.SearchProvider = (*Adapter)(nil)

// ErrSourceNotWired is returned when an Adapter is constructed
// without a non-nil underlying artlist.Service.
var ErrSourceNotWired = errors.New("artlist adapter: source not wired")

// searcher is the minimal internal interface the adapter depends on
// for Search. Defining it private to this package lets the unit tests
// inject a stub without constructing a full *artlistsrc.Service.
//
// *artlistsrc.Service satisfies searcher via its public Search method.
type searcher interface {
	Search(ctx context.Context, req *artlistsrc.SearchRequest) (*artlistsrc.SearchResponse, error)
}

// Adapter wraps an artlist searcher (production: *artlistsrc.Service)
// and presents it as a SearchProvider. It does NOT introduce new
// search semantics: it translates the canonical
// providers.SearchRequest into the service's native request at the
// boundary, and translates the SearchResponse back into canonical
// providers.Candidate values.
type Adapter struct {
	src searcher
}

// NewAdapter returns an Adapter wrapping the given service. The
// service must be fully wired (composition root responsibility).
func NewAdapter(src *artlistsrc.Service) *Adapter { return &Adapter{src: src} }

// Name implements providers.Provider.
func (a *Adapter) Name() string { return "artlist" }

// Capabilities implements providers.Provider.
// CapabilityFetch is intentionally omitted: artlist's download
// pipeline (videomuscles + drive upload) is not a Provider
// concern. The Search/Fetch split means this adapter ONLY
// satisfies SearchProvider and the registry must not return it
// for ByCapability(CapabilityFetch).
func (a *Adapter) Capabilities() []providers.Capability {
	return []providers.Capability{
		providers.CapabilitySearch,
		providers.CapabilityVideo,
		providers.CapabilityMusic,
	}
}

// Search implements providers.SearchProvider.
//
// Mapping rules:
//
//   - req.Query               -> artlist SearchRequest.Term
//   - req.Limit               -> artlist SearchRequest.Limit
//   - req.TopicOnly           -> not supported (artlist is term-based)
//   - req.Filters.PublishedAfter/Sort/MinDuration/MaxDuration
//     -> not supported on artlist (single global order via the scraper)
//   - req.Filters.MediaTypes  -> not honoured; artlist always returns
//     video/music regardless.
//
// NextPageToken is always empty in the returned SearchResult:
// artlist has no cursor. Callers treat empty as "no more pages".
//
// Why the response is already close to canonical: artlist's
// SearchResponse uses the Asset struct directly (see
// internal/sources/artlist/dto_search.go), so the loop maps each
// Asset into a typed Candidate without inventing new fields. Asset
// canonicalization is the downstream ingest use case's
// responsibility — this adapter returns raw findings only.
func (a *Adapter) Search(ctx context.Context, req providers.SearchRequest) (providers.SearchResult, error) {
	if err := a.checkWired(); err != nil {
		return providers.SearchResult{}, err
	}

	native := &artlistsrc.SearchRequest{
		Term:     req.Query,
		Limit:    req.Limit,
		PreferDB: false,
	}

	resp, err := a.src.Search(ctx, native)
	if err != nil {
		return providers.SearchResult{}, fmt.Errorf("artlist search: %w", err)
	}
	if resp == nil {
		return providers.SearchResult{}, nil
	}

	candidates := make([]providers.Candidate, 0, len(resp.Clips))
	for i := range resp.Clips {
		asset := &resp.Clips[i]
		candidates = append(candidates, providers.Candidate{
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
	return providers.SearchResult{Candidates: candidates}, nil
}

// checkWired returns ErrSourceNotWired when the adapter has no
// usable searcher. Guards against both nil interface and typed-nil
// pointer (matches the registry's typed-nil convention, consistent
// with youtube/adapter.go).
func (a *Adapter) checkWired() error {
	if a.src == nil {
		return ErrSourceNotWired
	}
	rv := reflect.ValueOf(a.src)
	if rv.Kind() == reflect.Ptr && rv.IsNil() {
		return ErrSourceNotWired
	}
	return nil
}
