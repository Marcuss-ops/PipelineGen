// Package routing — searcher_retrieved.go bridges the canonical
// RetrievalSearchBackend into a territory-scoped ImageSearcher.
//
// FASE 8 (July 2026): no longer imports the retrieved subpackage —
// the shared DTOs (RetrievalSearchOptions, RetrievalSearchResult) live
// in the routing package itself, breaking the routing→retrieved
// import edge that completed the pre-FASE-8 import cycle.
package images

import (
	"context"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

type retrievedSearcher struct {
	backend RetrievalSearchBackend
}

func newRetrievedSearcher(b RetrievalSearchBackend) *retrievedSearcher {
	return &retrievedSearcher{backend: b}
}

var _ ImageSearcher = (*retrievedSearcher)(nil)

func (s *retrievedSearcher) Search(ctx context.Context, filter ImageFilter) ([]ImageSearchResult, error) {
	if s == nil || s.backend == nil {
		return nil, nil
	}
	limit := ResolvedLimit(filter.Limit)
	opts := RetrievalSearchOptions{Limit: limit}
	hits, err := s.backend.SearchAll(ctx, filter.SubjectID, opts)
	if err != nil {
		return nil, err
	}
	out := make([]ImageSearchResult, 0, len(hits))
	for _, h := range hits {
		// Retrieved rows: StyleVersion has no upstream source (provider
		// doesn't carry style versioning); hard-set Score=1.0 to signal
		// the row is exact-match of the upstream-sourced candidate.
		out = append(out, ImageSearchResult{
			AssetID:       "",
			Origin:        string(detail.ImageOriginRetrieved),
			Provider:      string(h.Provider),
			Name:          h.Title,
			PreviewURL:    h.PreviewURL,
			SourcePageURL: h.PageURL,
			Width:         h.Width,
			Height:        h.Height,
			Score:         1.0,
			StyleID:       h.StyleID,
			StyleVersion:  "",
			License:       h.License,
			Author:        h.Author,
		})
	}
	return out, nil
}
