// Package app — search_backend_provider_test.go pins the
// PR-SEARCH-UNIVERSE invariant: the provider backend MUST NOT fabricate a
// canonical AssetID from the provider-native ID. It delegates
// source_type|source_ref → canonical-asset resolution to the injected
// CanonicalIdentityResolver and leaves AssetID empty when the source is
// unknown or the resolver is not wired.
package wiring

import (
	"context"
	"testing"

	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	providers "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
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
	name string
	res  providers.SearchResult
	err  error
}

func (f *fakeSearchProvider) Name() string { return f.name }
func (f *fakeSearchProvider) Capabilities() []providers.Capability {
	return []providers.Capability{providers.CapabilitySearch, providers.CapabilityVideo}
}
func (f *fakeSearchProvider) Search(_ context.Context, _ providers.SearchRequest) (providers.SearchResult, error) {
	return f.res, f.err
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
