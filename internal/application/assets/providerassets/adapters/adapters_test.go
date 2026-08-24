package adapters

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providerassets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	artapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
)

type fakeSearchProvider struct {
	name string
	res  providers.SearchResult
	err  error
}

func (f *fakeSearchProvider) Name() string { return f.name }

func (f *fakeSearchProvider) Capabilities() []providers.Capability { return nil }

func (f *fakeSearchProvider) Search(ctx context.Context, req providers.SearchRequest) (providers.SearchResult, error) {
	if f.err != nil {
		return providers.SearchResult{}, f.err
	}
	return f.res, nil
}

type fakeSearcher struct {
	cands []artapp.Candidate
	err   error
}

func (f *fakeSearcher) Search(ctx context.Context, req artapp.SearchRequest) ([]artapp.Candidate, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.cands, nil
}

func TestSearchProviderAdapter_NameOverride(t *testing.T) {
	adapter := NewSearchProviderAdapter("override", &fakeSearchProvider{name: "src"})
	if adapter.Name() != "override" {
		t.Fatalf("expected override, got %s", adapter.Name())
	}
}

func TestSearchProviderAdapter_NameFallback(t *testing.T) {
	adapter := NewSearchProviderAdapter("", &fakeSearchProvider{name: "src"})
	if adapter.Name() != "src" {
		t.Fatalf("expected src, got %s", adapter.Name())
	}
}

func TestSearchProviderAdapter_Search(t *testing.T) {
	provider := &fakeSearchProvider{
		name: "artlist",
		res: providers.SearchResult{
			Candidates: []providers.Candidate{{ID: "1", Title: "Clip"}},
		},
	}
	adapter := NewSearchProviderAdapter("", provider)
	res, err := adapter.Search(context.Background(), providerassets.SearchRequest{Query: "test", Limit: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(res.Assets) != 1 || res.Assets[0].ID != "1" {
		t.Fatalf("unexpected assets: %v", res.Assets)
	}
}

func TestSearchProviderAdapter_NotWired(t *testing.T) {
	adapter := NewSearchProviderAdapter("artlist", nil)
	_, err := adapter.Search(context.Background(), providerassets.SearchRequest{})
	if !errors.Is(err, ErrAdapterNotWired) {
		t.Fatalf("expected ErrAdapterNotWired, got %v", err)
	}
}

func TestSearcherAdapter_Search(t *testing.T) {
	searcher := &fakeSearcher{cands: []artapp.Candidate{{ID: "2", Title: "Pix"}}}
	adapter := NewSearcherAdapter("pixabay", searcher)
	res, err := adapter.Search(context.Background(), providerassets.SearchRequest{Query: "test", Limit: 5})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(res.Assets) != 1 || res.Assets[0].ID != "2" {
		t.Fatalf("unexpected assets: %v", res.Assets)
	}
}

func TestSearcherAdapter_NotWired(t *testing.T) {
	adapter := NewSearcherAdapter("pexels", nil)
	_, err := adapter.Search(context.Background(), providerassets.SearchRequest{})
	if !errors.Is(err, ErrAdapterNotWired) {
		t.Fatalf("expected ErrAdapterNotWired, got %v", err)
	}
}
