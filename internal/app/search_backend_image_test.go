package app

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type imageResolverFake struct{ searcher *imageSearcherFake }

func (f *imageResolverFake) Resolve(routing.ImageSearchTerritory) (routing.ImageSearcher, error) {
	return f.searcher, nil
}

type imageSearcherFake struct{ calls atomic.Int32 }

func (f *imageSearcherFake) Search(context.Context, routing.ImageFilter) ([]routing.ImageSearchResult, error) {
	f.calls.Add(1)
	return []routing.ImageSearchResult{{
		AssetID: "img-1", Origin: asset.ImageOriginRetrieved, Provider: "wikipedia",
		Name: "Elon Musk", PreviewURL: "https://img.example/elon.jpg", SourcePageURL: "https://source.example/elon",
		Width: 1200, Height: 800, Score: 1,
	}}, nil
}

func TestImageSearchProviderUsesNormalizedSingleflightKey(t *testing.T) {
	base := providers.SearchRequest{Query: "Elon Musk Tesla", Limit: 20}
	for _, equivalent := range []providers.SearchRequest{
		{Query: " elon   musk tesla ", Limit: 20},
		{Query: "ELON MUSK TESLA", Limit: 20},
	} {
		if got, want := imageSearchKey(equivalent), imageSearchKey(base); got != want {
			t.Fatalf("equivalent query key=%q, want %q", got, want)
		}
	}
}

func TestImageSearchKeySeparatesDelimiterContainingFields(t *testing.T) {
	cases := []struct {
		name   string
		first  providers.SearchRequest
		second providers.SearchRequest
	}{
		{
			name:   "comma in tags",
			first:  providers.SearchRequest{Query: "subject", Filters: providers.SearchFilters{Tags: []string{"a,b"}}, Limit: 2},
			second: providers.SearchRequest{Query: "subject", Filters: providers.SearchFilters{Tags: []string{"a", "b"}}, Limit: 2},
		},
		{
			name:   "colon and pipe in query",
			first:  providers.SearchRequest{Query: "a:b|c", Limit: 2},
			second: providers.SearchRequest{Query: "a", Filters: providers.SearchFilters{Tags: []string{"b|c"}}, Limit: 2},
		},
		{
			name:   "delimiter in tag",
			first:  providers.SearchRequest{Query: "subject", Filters: providers.SearchFilters{Tags: []string{"a:b|c"}}, Limit: 2},
			second: providers.SearchRequest{Query: "subject", Filters: providers.SearchFilters{Tags: []string{"a:b", "c"}}, Limit: 2},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := imageSearchKey(tc.first), imageSearchKey(tc.second); got == want {
				t.Fatalf("delimiter-containing fields collided: %q", got)
			}
		})
	}
}

func TestImageSearchKeySeparatesLimits(t *testing.T) {
	first := providers.SearchRequest{Query: "subject", Filters: providers.SearchFilters{Tags: []string{"tag"}}, Limit: 2}
	second := providers.SearchRequest{Query: "subject", Filters: providers.SearchFilters{Tags: []string{"tag"}}, Limit: 20}
	if got, want := imageSearchKey(first), imageSearchKey(second); got == want {
		t.Fatalf("different limits collided: %q", got)
	}
}

func TestImageSearchKeyIncludesQueryTagsAndLimitDeterministically(t *testing.T) {
	base := providers.SearchRequest{Query: "A subject", Filters: providers.SearchFilters{Tags: []string{"z", "a"}}, Limit: 7}
	equivalent := providers.SearchRequest{Query: " a   subject ", Filters: providers.SearchFilters{Tags: []string{"a", "z"}}, Limit: 7}
	if got, want := imageSearchKey(base), imageSearchKey(equivalent); got != want {
		t.Fatalf("equivalent requests produced different keys: %q vs %q", got, want)
	}
	for _, changed := range []providers.SearchRequest{
		{Query: "different subject", Filters: providers.SearchFilters{Tags: []string{"a", "z"}}, Limit: 7},
		{Query: "A subject", Filters: providers.SearchFilters{Tags: []string{"different"}}, Limit: 7},
		{Query: "A subject", Filters: providers.SearchFilters{Tags: []string{"a", "z"}}, Limit: 8},
	} {
		if imageSearchKey(base) == imageSearchKey(changed) {
			t.Fatalf("changed query/tag/limit collided: base=%q changed=%q", imageSearchKey(base), imageSearchKey(changed))
		}
	}
}

func TestImageSearchKeySortsTagsWithoutMutatingRequest(t *testing.T) {
	tags := []string{"z", "a"}
	req := providers.SearchRequest{Query: "subject", Filters: providers.SearchFilters{Tags: tags}, Limit: 2}
	_ = imageSearchKey(req)
	if tags[0] != "z" || tags[1] != "a" {
		t.Fatalf("imageSearchKey mutated caller tags: %v", tags)
	}
	if imageSearchKey(req) != imageSearchKey(providers.SearchRequest{Query: "subject", Filters: providers.SearchFilters{Tags: []string{"a", "z"}}, Limit: 2}) {
		t.Fatal("equivalent tag order must produce the same key")
	}
}

func TestImageSearchProviderDoesNotRetainResultsBetweenCalls(t *testing.T) {
	searcher := &imageSearcherFake{}
	provider := newImageSearchProvider(&imageResolverFake{searcher: searcher})
	req := providers.SearchRequest{Query: "Elon Musk Tesla", Limit: 20}

	for i := 0; i < 2; i++ {
		if _, err := provider.Search(context.Background(), req); err != nil {
			t.Fatalf("search %d: %v", i+1, err)
		}
	}
	if got := searcher.calls.Load(); got != 2 {
		t.Fatalf("retrieval calls=%d, want 2 after sequential searches", got)
	}
}
