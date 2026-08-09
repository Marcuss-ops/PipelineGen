package resolver

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
)

type fetchProvider struct{}

func (fetchProvider) Name() string { return "stock" }
func (fetchProvider) Capabilities() []providers.Capability {
	return []providers.Capability{providers.CapabilityFetch}
}
func (fetchProvider) Fetch(context.Context, providers.FetchRequest) (*providers.FetchedAsset, error) {
	return &providers.FetchedAsset{LocalPath: "/tmp/selected.mp4", Bytes: 42}, nil
}

func TestResolveDelegatesOnlyAfterSelection(t *testing.T) {
	registry := providers.NewRegistry()
	if err := registry.RegisterFetch(fetchProvider{}); err != nil {
		t.Fatal(err)
	}
	service := NewService(registry)
	result, err := service.Resolve(context.Background(), Request{Source: "stock", AssetID: "a1", SourceRef: "https://cdn.example/a1.mp4"})
	if err != nil || result == nil || result.LocalPath == "" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestResolveFailsClosedForSearchOnlyProvider(t *testing.T) {
	registry := providers.NewRegistry()
	searchOnly := testSearchProvider{}
	if err := registry.RegisterSearch(searchOnly); err != nil {
		t.Fatal(err)
	}
	_, err := NewService(registry).Resolve(context.Background(), Request{Source: "artlist", AssetID: "a1", SourceRef: "https://artlist.io/a1"})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err=%v, want ErrUnsupported", err)
	}
}

type testSearchProvider struct{}

func (testSearchProvider) Name() string { return "artlist" }
func (testSearchProvider) Capabilities() []providers.Capability {
	return []providers.Capability{providers.CapabilitySearch}
}
func (testSearchProvider) Search(context.Context, providers.SearchRequest) (providers.SearchResult, error) {
	return providers.SearchResult{}, nil
}
