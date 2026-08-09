// Package app — search_backend_provider.go is the provider-side backend
// extracted from search_backends.go (LONG-FILES-DECOMPOSITION-2026-07-06 Band B #2).
//
// Owns: providerSearchBackend struct + Name/Capabilities/Search methods.
package app

import (
	"context"
	"strings"

	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
)

// providerSearchBackend wraps a single providers.SearchProvider so
// the Aggregator can coordinate fanout. Capabilities are translated
// from the provider-native enum (CapabilitySearch + CapabilityVideo
// + ...) into the search.Capability enum used by Aggregator.Eligible.
//
// SourceRef carries the provider-native identifier (YouTube VideoID,
// artlist item id, etc). The Aggregator's 4-key dedup uses
// Source+"|"+SourceRef as the canonical-provider-identity key.
type providerSearchBackend struct {
	provider providers.SearchProvider
	caps     []search.Capability
	srcName  string
}

func (b *providerSearchBackend) Name() string {
	if b.srcName != "" {
		return b.srcName
	}
	return b.provider.Name()
}

func (b *providerSearchBackend) Capabilities() []search.Capability {
	if b.caps != nil {
		return b.caps
	}
	return []search.Capability{search.CapVideo}
}

func (b *providerSearchBackend) Search(ctx context.Context, q search.Query) ([]search.Candidate, error) {
	// PR-2 (June 2026): provider backends (artlist, youtube, stock)
	// do not support hash-match lookups; the canonical hash path
	// is in domain of the local catalog. A non-empty Query.Hash
	// is intentionally a no-op (return nil, nil) so the fanout
	// continues without cancel.
	if q.Hash != "" {
		return nil, nil
	}
	// PR-AGGREGATE-FILTER-UNIFORM (July 2026): q.Filters is now
	// the canonical forwarding channel for filter semantics
	// (architecture/current.yaml#id-30, PR-1 of VERDICT §6). The
	// provider backend reads the FILTERS DTO's MediaType field
	// (single value) — NOT q.MediaTypes (legacy capability-shape).
	// q.Filters.MediaType is already a single string (NOT a slice)
	// so it forwards as a 1-element slice to provider.SearchFilters.
	//
	// Category / Language / Tags populate the new SearchFilters
	// fields. Provider adapters silently drop what their native
	// APIs don't support (the documented SearchFilters contract):
	// the Aggregator fan-out never aborts on a per-backend filter
	// mismatch so partial results remain preferred.
	provReq := providers.SearchRequest{
		Query: q.Text,
		Limit: q.Limit,
		Filters: providers.SearchFilters{
			MediaTypes: mediaTypesSingleFromString(q.Filters.MediaType),
			Category:   strings.TrimSpace(q.Filters.Category),
			Language:   strings.TrimSpace(q.Filters.Language),
			Tags:       append([]string(nil), q.Filters.Tags...),
		},
	}
	res, err := b.provider.Search(ctx, provReq)
	if err != nil {
		return nil, err
	}
	out := make([]search.Candidate, 0, len(res.Candidates))
	for _, c := range res.Candidates {
		out = append(out, search.Candidate{
			AssetID: firstNonEmptyProvider(c.ID, c.ExternalID),
			Source:  b.Name(),
			// Provider-native ID is the stable identity. URLs belong in
			// SourceURL/PreviewURL and may expire independently.
			SourceRef:    firstNonEmptyProvider(c.ExternalID, c.ID, c.SourceRef),
			MediaType:    string(c.MediaType),
			Title:        c.Title,
			Name:         c.Title,
			SourceURL:    c.PageURL,
			ThumbnailURL: c.ThumbnailURL,
			PreviewURL:   c.PreviewURL,
			DurationMs:   c.DurationMs,
			Width:        c.Width,
			Height:       c.Height,
			Tags:         append([]string(nil), c.Keywords...),
			Hash:         "", // providers don't emit content hash
			Score:        c.Score,
		})
	}
	return out, nil
}

func firstNonEmptyProvider(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var _ search.SearchBackend = (*providerSearchBackend)(nil)
