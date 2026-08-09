package app

import (
	"context"
	"sync"
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

func TestImageSearchProviderCoalescesNormalizedQueries(t *testing.T) {
	searcher := &imageSearcherFake{}
	provider := newImageSearchProvider(&imageResolverFake{searcher: searcher})

	var wg sync.WaitGroup
	for _, query := range []string{"Elon Musk Tesla", " elon   musk tesla ", "ELON MUSK TESLA"} {
		wg.Add(1)
		go func(query string) {
			defer wg.Done()
			result, err := provider.Search(context.Background(), providers.SearchRequest{Query: query, Limit: 20})
			if err != nil || len(result.Candidates) != 1 {
				t.Errorf("query=%q result=%#v err=%v", query, result, err)
			}
		}(query)
	}
	wg.Wait()
	if got := searcher.calls.Load(); got != 1 {
		t.Fatalf("retrieval calls=%d, want 1", got)
	}
}
