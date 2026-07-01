// Package routing — searcher_generated.go bridges the canonical
// ImageListRepository into a territory-scoped ImageSearcher.
package routing

import (
	"context"
)

type generatedSearcher struct {
	repo ImageListRepository
}

func newGeneratedSearcher(r ImageListRepository) *generatedSearcher {
	return &generatedSearcher{repo: r}
}

var _ ImageSearcher = (*generatedSearcher)(nil)

func (s *generatedSearcher) Search(ctx context.Context, filter ImageFilter) ([]ImageSearchResult, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	narrowed := filter
	if len(narrowed.Origins) == 0 {
		narrowed.Origins = []ImageOrigin{OriginGenerated}
	} else {
		narrowed.Origins = intersectOrigins(narrowed.Origins, []ImageOrigin{OriginGenerated})
	}
	rows, err := s.repo.ListImages(ctx, narrowed)
	if err != nil {
		return nil, err
	}
	out := make([]ImageSearchResult, 0, len(rows))
	for _, r := range rows {
		r.Origin = OriginGenerated // territory invariant
		out = append(out, r)
	}
	return out, nil
}

func intersectOrigins(a, b []ImageOrigin) []ImageOrigin {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	set := make(map[ImageOrigin]struct{}, len(b))
	for _, x := range b {
		set[x] = struct{}{}
	}
	out := make([]ImageOrigin, 0, len(a))
	for _, x := range a {
		if _, ok := set[x]; ok {
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
