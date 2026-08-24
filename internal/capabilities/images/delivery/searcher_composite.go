// Package routing — searcher_composite.go is the territory=all
// searcher: serial composition of retrieved + generated.
package delivery

import (
	"context"
)

type compositeSearcher struct {
	retrieved RetrievalSearchBackend
	repo      ImageListRepository
}

func newCompositeSearcher(r RetrievalSearchBackend, repo ImageListRepository) *compositeSearcher {
	return &compositeSearcher{retrieved: r, repo: repo}
}

var _ ImageSearcher = (*compositeSearcher)(nil)

func (s *compositeSearcher) Search(ctx context.Context, filter ImageFilter) ([]ImageSearchResult, error) {
	if s == nil {
		return nil, nil
	}
	limit := ResolvedLimit(filter.Limit)
	retrS, err := newRetrievedSearcher(s.retrieved).Search(ctx, filter)
	if err != nil {
		return nil, err
	}
	genS, err := newGeneratedSearcher(s.repo).Search(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := make([]ImageSearchResult, 0, len(retrS)+len(genS))
	out = append(out, retrS...)
	out = append(out, genS...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
