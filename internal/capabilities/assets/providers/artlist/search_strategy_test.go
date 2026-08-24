package assets

import (
	"context"
	"testing"
)

type fakeStrategySearcher struct{}

func (*fakeStrategySearcher) Search(context.Context, SearchRequest) ([]Candidate, error) {
	return nil, nil
}

func TestArtlistSearchStrategyNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   ArtlistSearchStrategy
		want ArtlistSearchStrategy
	}{
		{name: "empty defaults", in: "", want: StrategyArtlistOnly},
		{name: "trim lowercase", in: " artlist_then_public_fallback ", want: StrategyArtlistThenPublicFallback},
		{name: "case fold", in: "PUBLIC_ONLY_FOR_DEV", want: StrategyPublicOnlyForDev},
		{name: "invalid preserved", in: "something_else", want: "something_else"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.Normalize(); got != tt.want {
				t.Fatalf("Normalize()=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveSearcherChainStrategies(t *testing.T) {
	scraper := &fakeStrategySearcher{}
	pixabay := &fakeStrategySearcher{}
	pexels := &fakeStrategySearcher{}

	assertChain(t, ResolveSearcherChain(StrategyArtlistOnly, scraper, pixabay, pexels), scraper)
	assertChain(t, ResolveSearcherChain(StrategyArtlistThenPublicFallback, scraper, pixabay, pexels), scraper, pixabay, pexels)
	assertChain(t, ResolveSearcherChain(StrategyPublicOnlyForDev, scraper, pixabay, pexels), pixabay, pexels)
	assertChain(t, ResolveSearcherChain("", scraper, pixabay, pexels), scraper)
	assertChain(t, ResolveSearcherChain("invalid", scraper, pixabay, pexels), scraper)
}

func assertChain(t *testing.T, got []Searcher, want ...Searcher) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(got)=%d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%p, want %p", i, got[i], want[i])
		}
	}
}
