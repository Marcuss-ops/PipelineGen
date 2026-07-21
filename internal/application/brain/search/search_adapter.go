// Package search adapts the canonical search.SearchFanOut to the
// brain.CandidateSearcher port.
//
// This is the SOLE bridge between the brain capability and the
// canonical search aggregator. The brain does not know any concrete
// provider, Qdrant, SQLite, Drive, FFmpeg or yt-dlp; it only knows
// the CandidateSearcher port.
package search

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/brain"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
)

// CandidateSearcherAdapter implements brain.CandidateSearcher on top
// of the canonical search.SearchFanOut.
type CandidateSearcherAdapter struct {
	inner search.SearchFanOut
}

// NewCandidateSearcherAdapter constructs the adapter. A nil inner
// surface is accepted so tests can inject a stub later, but Search
// will fail fast with search.ErrAggregatorNil.
func NewCandidateSearcherAdapter(inner search.SearchFanOut) *CandidateSearcherAdapter {
	return &CandidateSearcherAdapter{inner: inner}
}

// Compile-time assertion: CandidateSearcherAdapter satisfies the port.
var _ brain.CandidateSearcher = (*CandidateSearcherAdapter)(nil)

// Search translates a brain.SearchQuery into a canonical search.Query,
// delegates to the SearchFanOut, and projects the result back into the
// brain envelope.
func (a *CandidateSearcherAdapter) Search(ctx context.Context, query brain.SearchQuery) (brain.SearchResult, error) {
	if a.inner == nil {
		return brain.SearchResult{}, search.ErrAggregatorNil
	}

	canonical := search.Query{
		Text:       query.Text,
		MediaTypes: append([]string(nil), query.MediaTypes...),
		Sources:    append([]string(nil), query.Sources...),
		Limit:      query.Limit,
		Mode:       search.SearchModeANN,
		Filters: search.Filters{
			Language: query.Language,
		},
	}

	res, err := a.inner.Search(ctx, canonical)
	if err != nil {
		return brain.SearchResult{}, err
	}
	if res == nil {
		return brain.SearchResult{}, search.ErrAggregatorNil
	}

	candidates := make([]brain.Candidate, 0, len(res.Items))
	for _, c := range res.Items {
		candidates = append(candidates, brain.Candidate{
			ID:           c.SourceRef,
			AssetID:      c.AssetID,
			Provider:     c.Source,
			SourceURL:    c.PreviewURL,
			ThumbnailURL: c.ThumbnailURL,
			Title:        c.Title,
			Description:  c.Name,
			MediaType:    c.MediaType,
			Score:        c.Score,
		})
	}

	backendErrors := make(map[string]string, len(res.ProviderErrors))
	for name, msg := range res.ProviderErrors {
		backendErrors[name] = msg
	}

	return brain.SearchResult{
		Candidates:    candidates,
		Partial:       res.Partial,
		BackendErrors: backendErrors,
	}, nil
}
