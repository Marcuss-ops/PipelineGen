package images

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type resolverSearchFake struct{ searcher *resolverSearcherFake }

func (f *resolverSearchFake) Resolve(routing.ImageSearchTerritory) (routing.ImageSearcher, error) {
	return f.searcher, nil
}

type resolverSearcherFake struct {
	calls atomic.Int32
	block <-chan struct{}
}

func (f *resolverSearcherFake) Search(ctx context.Context, _ routing.ImageFilter) ([]routing.ImageSearchResult, error) {
	f.calls.Add(1)
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return []routing.ImageSearchResult{{
		AssetID: "img-1", Origin: asset.ImageOriginRetrieved, Provider: "wikipedia",
		Name: "Elon Musk", PreviewURL: "https://img.example/elon.jpg", SourcePageURL: "https://source.example/elon",
		Width: 1200, Height: 800, Score: 1,
	}}, nil
}

func TestResolverSearchProviderUsesNormalizedSingleflightKey(t *testing.T) {
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

func TestImageSearchKeySeparatesDelimiterContainingTags(t *testing.T) {
	first := providers.SearchRequest{Query: "subject", Filters: providers.SearchFilters{Tags: []string{"a,b"}}, Limit: 2}
	second := providers.SearchRequest{Query: "subject", Filters: providers.SearchFilters{Tags: []string{"a", "b"}}, Limit: 2}
	if got, want := imageSearchKey(first), imageSearchKey(second); got == want {
		t.Fatalf("delimiter-containing tags collided: %q", got)
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

func TestResolverSearchProviderCoalescesConcurrentCalls(t *testing.T) {
	block := make(chan struct{})
	searcher := &resolverSearcherFake{block: block}
	provider := NewResolverSearchProvider(&resolverSearchFake{searcher: searcher})
	req := providers.SearchRequest{Query: "Elon Musk Tesla", Limit: 20}

	const callers = 12
	results := make([]providers.SearchResult, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = provider.Search(context.Background(), req)
		}(i)
	}
	started := make(chan struct{})
	go func() {
		for searcher.calls.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		close(started)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the coalesced search to start")
	}
	close(block)
	wg.Wait()

	if got := searcher.calls.Load(); got != 1 {
		t.Fatalf("concurrent retrieval calls=%d, want 1", got)
	}
	for i := range results {
		if errs[i] != nil || len(results[i].Candidates) != 1 {
			t.Fatalf("caller %d result=%#v err=%v", i, results[i], errs[i])
		}
	}
}

func TestResolverSearchProviderDoesNotRetainResultsBetweenCalls(t *testing.T) {
	searcher := &resolverSearcherFake{}
	provider := NewResolverSearchProvider(&resolverSearchFake{searcher: searcher})
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
