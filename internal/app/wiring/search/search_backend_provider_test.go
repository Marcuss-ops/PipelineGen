// Package app — search_backend_provider_test.go pins the
// PR-SEARCH-UNIVERSE invariant: the provider backend MUST NOT fabricate a
// canonical AssetID from the provider-native ID. It delegates
// source_type|source_ref → canonical-asset resolution to the injected
// CanonicalIdentityResolver and leaves AssetID empty when the source is
// unknown or the resolver is not wired.
package search

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	providers "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
)

// canonicalIdentityStub maps "source|ref" → canonical asset id.
type canonicalIdentityStub struct {
	known map[string]string
}

func (s *canonicalIdentityStub) ResolveSource(_ context.Context, sourceType, sourceRef string) (search.CanonicalIdentity, error) {
	if s != nil {
		if id, ok := s.known[sourceType+"|"+sourceRef]; ok {
			return search.CanonicalIdentity{AssetID: id, SourceType: sourceType, SourceRef: sourceRef, Resolved: true}, nil
		}
	}
	return search.CanonicalIdentity{SourceType: sourceType, SourceRef: sourceRef}, nil
}

func (s *canonicalIdentityStub) ResolveContent(_ context.Context, _ string) (search.CanonicalIdentity, error) {
	return search.CanonicalIdentity{}, nil
}

// fakeSearchProvider is a minimal providers.SearchProvider.
type fakeSearchProvider struct {
	name    string
	res     providers.SearchResult
	err     error
	request providers.SearchRequest
}

func (f *fakeSearchProvider) Name() string { return f.name }
func (f *fakeSearchProvider) Capabilities() []providers.Capability {
	return []providers.Capability{providers.CapabilitySearch, providers.CapabilityVideo}
}
func (f *fakeSearchProvider) Search(_ context.Context, req providers.SearchRequest) (providers.SearchResult, error) {
	f.request = req
	return f.res, f.err
}

func TestProviderBackendForwardsYouTubeDiscoveryControls(t *testing.T) {
	publishedAfter := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	provider := &fakeSearchProvider{name: "youtube"}
	backend := &providerSearchBackend{provider: provider}
	_, err := backend.Search(context.Background(), search.Query{
		Text: "boxing", Limit: 7, Filters: search.Filters{
			MediaType: "video", Sort: string(providers.SortByViews), PublishedAfter: &publishedAfter,
		},
	})
	if err != nil {
		t.Fatalf("Search err = %v", err)
	}
	if provider.request.Filters.Sort != providers.SortByViews {
		t.Fatalf("sort = %q, want %q", provider.request.Filters.Sort, providers.SortByViews)
	}
	if provider.request.Filters.PublishedAfter == nil || !provider.request.Filters.PublishedAfter.Equal(publishedAfter) {
		t.Fatalf("published_after = %v, want %v", provider.request.Filters.PublishedAfter, publishedAfter)
	}
}

func TestProviderBackendPreservesYouTubeSortMetadata(t *testing.T) {
	publishedAt := time.Date(2025, 2, 3, 0, 0, 0, 0, time.UTC)
	provider := &fakeSearchProvider{
		name: "youtube",
		res: providers.SearchResult{Candidates: []providers.Candidate{{
			ID: "video-1", ExternalID: "video-1", Title: "Latest",
			Duration: 42 * time.Second, PublishedAt: &publishedAt, ViewCount: 9876,
		}}},
	}
	backend := &providerSearchBackend{provider: provider}
	items, err := backend.Search(context.Background(), search.Query{Text: "boxing", Limit: 5})
	if err != nil {
		t.Fatalf("Search err = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%+v, want one item", items)
	}
	got := items[0]
	if got.DurationMs != 42_000 || got.PublishedAt == nil || !got.PublishedAt.Equal(publishedAt) || got.ViewCount != 9876 {
		t.Fatalf("sort metadata was lost: %+v", got)
	}
}

func TestBuildSearchBackendsMountsYouTubeDiscoveryProvider(t *testing.T) {
	providerReg := providers.NewRegistry()
	provider := &fakeSearchProvider{name: "youtube"}
	if err := providerReg.RegisterSearch(provider); err != nil {
		t.Fatal(err)
	}
	providerReg.Freeze()
	backends, err := BuildSearchBackends(SearchBackendBuildOpts{
		Logger: zap.NewNop(), ProviderReg: providerReg,
	})
	if err != nil {
		t.Fatalf("BuildSearchBackends: %v", err)
	}
	eligible := backends.Eligible(search.Query{
		Sources: []string{"youtube"}, Universe: search.SearchDiscovery,
	})
	if len(eligible) != 1 || eligible[0].Name() != "youtube" || eligible[0].Universe() != search.SearchDiscovery {
		t.Fatalf("eligible discovery backends=%+v, want the mounted YouTube provider", eligible)
	}
}

func TestProviderBackendResolvesCanonicalIdentityNotProviderID(t *testing.T) {
	provider := &fakeSearchProvider{
		name: "artlist",
		res: providers.SearchResult{Candidates: []providers.Candidate{
			{ID: "123456", ExternalID: "123456", Title: "Sunset", PageURL: "https://artlist.io/123456"},
		}},
	}
	backend := &providerSearchBackend{
		provider: provider,
		resolver: &canonicalIdentityStub{known: map[string]string{"artlist|123456": "canonical-abc"}},
	}

	items, err := backend.Search(context.Background(), search.Query{Text: "sunset", Limit: 10})
	if err != nil {
		t.Fatalf("Search err = %v, want nil", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].AssetID != "canonical-abc" {
		t.Fatalf("AssetID = %q, want canonical-abc (resolved canonical id, NOT the provider id 123456)", items[0].AssetID)
	}
	if items[0].SourceRef != "123456" {
		t.Fatalf("SourceRef = %q, want 123456 (provider-native reference)", items[0].SourceRef)
	}
	if items[0].Source != "artlist" {
		t.Fatalf("Source = %q, want artlist", items[0].Source)
	}
}

func TestProviderBackendUnknownSourceLeavesAssetIDEmpty(t *testing.T) {
	provider := &fakeSearchProvider{
		name: "artlist",
		res: providers.SearchResult{Candidates: []providers.Candidate{
			{ID: "999", ExternalID: "999", Title: "Unknown"},
		}},
	}
	// resolver is nil → noop (identity unknown) → AssetID must be empty.
	backend := &providerSearchBackend{provider: provider}

	items, err := backend.Search(context.Background(), search.Query{Text: "unknown", Limit: 10})
	if err != nil {
		t.Fatalf("Search err = %v, want nil", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].AssetID != "" {
		t.Fatalf("AssetID = %q, want empty (provider id MUST NOT be fabricated into canonical identity)", items[0].AssetID)
	}
	if items[0].SourceRef != "999" {
		t.Fatalf("SourceRef = %q, want 999", items[0].SourceRef)
	}
}

func TestProviderBackendUnresolvedResolverLeavesAssetIDEmpty(t *testing.T) {
	provider := &fakeSearchProvider{
		name: "artlist",
		res: providers.SearchResult{Candidates: []providers.Candidate{
			{ID: "42", ExternalID: "42", Title: "Not yet registered"},
		}},
	}
	// resolver knows nothing → AssetID empty (still no provider-ID fabrication).
	backend := &providerSearchBackend{
		provider: provider,
		resolver: &canonicalIdentityStub{known: map[string]string{}},
	}

	items, err := backend.Search(context.Background(), search.Query{Text: "x", Limit: 10})
	if err != nil {
		t.Fatalf("Search err = %v, want nil", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].AssetID != "" {
		t.Fatalf("AssetID = %q, want empty when resolver reports unknown", items[0].AssetID)
	}
}
