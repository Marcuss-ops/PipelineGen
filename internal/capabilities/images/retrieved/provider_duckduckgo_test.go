package images

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/workflow/routing"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

type duckDuckGoBridgeStub struct {
	url   string
	calls int
}

func (b *duckDuckGoBridgeStub) SearchWikipedia(context.Context, string, string) (string, string) {
	return "", ""
}

func (b *duckDuckGoBridgeStub) SearchWikimediaCommons(context.Context, string) routing.RetrievalSearchResult {
	return routing.RetrievalSearchResult{}
}

func (b *duckDuckGoBridgeStub) SearchSearXNGImages(context.Context, string) string { return "" }

func (b *duckDuckGoBridgeStub) SearchSearXNGImagesMany(context.Context, string, int) []routing.RetrievalSearchResult {
	return nil
}

func (b *duckDuckGoBridgeStub) SearchDDGWide(context.Context, string) string {
	b.calls++
	return b.url
}

func (b *duckDuckGoBridgeStub) SearchBySlug(context.Context, string, int) []string { return nil }

func TestDuckDuckGoProviderEmptyQueryDoesNotCallBridge(t *testing.T) {
	bridge := &duckDuckGoBridgeStub{url: "https://images.example/fox.jpg"}
	provider := NewDuckDuckGoProvider(bridge, nil, zap.NewNop())

	results, err := provider.Search(context.Background(), "  ", routing.RetrievalSearchOptions{Lang: "en"})
	if err != nil {
		t.Fatalf("Search(empty): %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("Search(empty) returned %d results, want 0", len(results))
	}
	if bridge.calls != 0 {
		t.Fatalf("empty query called bridge %d times", bridge.calls)
	}
}

func TestDuckDuckGoProviderReturnsRetrievedCandidate(t *testing.T) {
	bridge := &duckDuckGoBridgeStub{url: "https://images.example/fox.jpg"}
	provider := NewDuckDuckGoProvider(bridge, nil, zap.NewNop())

	results, err := provider.Search(context.Background(), "red fox in snow", routing.RetrievalSearchOptions{Lang: "en"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Search returned %d results, want 1", len(results))
	}
	got := results[0]
	if got.Provider != asset.ProviderDuckDuckGo {
		t.Fatalf("provider = %q, want %q", got.Provider, asset.ProviderDuckDuckGo)
	}
	if got.Origin != asset.ImageOriginRetrieved {
		t.Fatalf("origin = %q, want %q", got.Origin, asset.ImageOriginRetrieved)
	}
	if got.PreviewURL != bridge.url || got.PageURL != bridge.url {
		t.Fatalf("candidate URLs = (%q, %q), want %q", got.PreviewURL, got.PageURL, bridge.url)
	}
	if got.Provider == asset.ProviderGoogleSlides || got.Origin == asset.ImageOriginGenerated {
		t.Fatal("DuckDuckGo candidate crossed into generated territory")
	}
}

func TestDuckDuckGoProviderEmptyBackendResponseIsControlled(t *testing.T) {
	provider := NewDuckDuckGoProvider(&duckDuckGoBridgeStub{}, nil, zap.NewNop())
	results, err := provider.Search(context.Background(), "valid query", routing.RetrievalSearchOptions{})
	if err != nil {
		t.Fatalf("Search(empty backend response): %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("Search returned %d results, want 0", len(results))
	}
}
