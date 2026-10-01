// Package images — searcher_retrieved.go bridges the canonical
// RetrievalSearchBackend into a territory-scoped ImageSearcher.
//
// FASE 8 (July 2026): the shared DTOs (RetrievalSearchOptions,
// RetrievalSearchResult) live in the images routing package itself,
// breaking the routing→retrieved import edge that completed the pre-FASE-8
// import cycle.
package images

import (
	"context"

	retrieved "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/search"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// maxRetrievalRecessionRungs bounds how many relaxed query variants a single
// retrieval attempt may try after the primary query returns empty. Three
// covers the useful cases (instruction prefix, clause strip, one truncation)
// without turning a genuine miss into an unbounded provider burst.
const maxRetrievalRecessionRungs = 3

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
	hits, err := s.searchOnce(ctx, filter.SubjectID, opts)
	if err != nil {
		return nil, err
	}
	// Query recession (fanout=0 fix): an empty primary search (flaky provider,
	// quota, or an over-narrow query) must not leave the caller imageless when
	// a relaxed variant of the same query can still satisfy it. The ladder is
	// bounded and only consulted on a miss, so the happy path never pays for
	// it.
	if len(hits) == 0 {
		hits = s.recede(ctx, filter.SubjectID, opts)
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

// searchOnce runs one query against the backend, preferring the concurrent,
// quality-ranked selection when the backend exposes it. A poor early provider
// must not hide a better later one, and latency is bounded by the slowest
// provider rather than the sum of the fallback chain. Legacy backends without
// SearchBest keep SearchAll.
func (s *retrievedSearcher) searchOnce(ctx context.Context, query string, opts RetrievalSearchOptions) ([]RetrievalSearchResult, error) {
	if best, ok := s.backend.(RetrievalBestSearchBackend); ok {
		return best.SearchBest(ctx, query, opts)
	}
	return s.backend.SearchAll(ctx, query, opts)
}

// recede walks the relaxed-query ladder (never the original) and returns the
// first rung that produces a hit. A backend error on a rung is reported as no
// hit rather than aborting the ladder: the primary search already succeeded
// empty, and one degraded rung must not mask a later usable one.
func (s *retrievedSearcher) recede(ctx context.Context, query string, opts RetrievalSearchOptions) []RetrievalSearchResult {
	ladder := retrieved.RelaxImageQuery(query)
	if len(ladder) <= 1 {
		return nil
	}
	ladder = ladder[1:]
	if len(ladder) > maxRetrievalRecessionRungs {
		ladder = ladder[:maxRetrievalRecessionRungs]
	}
	for _, rung := range ladder {
		hits, err := s.searchOnce(ctx, rung, opts)
		if err != nil {
			continue
		}
		if len(hits) > 0 {
			return hits
		}
	}
	return nil
}
