// Package retrieved — provider_registry_test.go locks the Step 8
// contract for the RetrievalProviderRegistry: fallback ordering,
// error-skip semantics, SearchByName lookup, nil safety, and
// Diagnostics probe surfacing.
package retrieved

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"go.uber.org/zap"
)

// fakeBridge is a hand-rolled StorageBridge stub used by every
// test in this file. Each test populates only the methods it
// expects; nil entries return zero values which is the documented
// behaviour of every provider's Search path (no → empty result, no
// error).
type fakeBridge struct {
	wikiURL        string
	wikiTitle      string
	searxngURL     string
	ddgURL         string
	bySlugURLs     map[string][]string
	anyErr         error // override all provider calls if set
}

func (f *fakeBridge) SearchWikipedia(_ context.Context, _, _ string) (string, string) {
	return f.wikiURL, f.wikiTitle
}
func (f *fakeBridge) SearchSearXNGImages(_ context.Context, _ string) string { return f.searxngURL }
func (f *fakeBridge) SearchDDGWide(_ context.Context, _ string) string      { return f.ddgURL }
func (f *fakeBridge) SearchBySlug(_ context.Context, slug string, _ int) []string {
	return f.bySlugURLs[slug]
}

func newFakeRetrievalProvider(t *testing.T, name asset.ImageProvider, br StorageBridge, client httpDoer) RetrievalProvider {
	t.Helper()
	switch name {
	case asset.ProviderWikipedia:
		return NewWikipediaProvider(br, client, zap.NewNop(), "en")
	case asset.ProviderSearXNG:
		return NewSearXNGProvider(br, client, zap.NewNop(), "http://searx.test")
	case asset.ProviderDuckDuckGo:
		return NewDuckDuckGoProvider(br, client, zap.NewNop())
	case asset.ProviderDrive:
		return NewDriveImageProvider(br, zap.NewNop())
	}
	t.Fatalf("unknown provider %q", name)
	return nil
}

// TestRetrievalRegistry_SearchAll_FallbackChain ensures the
// registry returns the first non-empty provider's result in the
// order Wikipedia → SearXNG → DuckDuckGo → Drive.
func TestRetrievalRegistry_SearchAll_FallbackChain(t *testing.T) {
	bridge := &fakeBridge{
		wikiURL:    "https://upload.wikimedia.org/wiki.png",
		wikiTitle:  "Albert Einstein",
		searxngURL: "https://searx.test/img.png",
		ddgURL:     "https://duckduckgo.com/img.png",
		bySlugURLs: map[string][]string{"einstein": {"https://drive.test/einstein.png"}},
	}
	cli := &http.Client{}
	reg := NewRetrievalProviderRegistry(zap.NewNop(), []RetrievalProvider{
		newFakeRetrievalProvider(t, asset.ProviderWikipedia, bridge, cli),
		newFakeRetrievalProvider(t, asset.ProviderSearXNG, bridge, cli),
		newFakeRetrievalProvider(t, asset.ProviderDuckDuckGo, bridge, cli),
		newFakeRetrievalProvider(t, asset.ProviderDrive, bridge, cli),
	})

	got, err := reg.SearchAll(context.Background(), "einstein", RetrievalSearchOptions{})
	if err != nil {
		t.Fatalf("SearchAll errored: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if got[0].Provider != asset.ProviderWikipedia {
		t.Fatalf("expected Wikipedia hit first, got %s", got[0].Provider)
	}
	if got[0].License != "CC-BY-SA-4.0" {
		t.Fatalf("expected CC-BY-SA-4.0 license, got %q", got[0].License)
	}
	if got[0].Author != "Wikipedia Contributors" {
		t.Fatalf("expected Wikipedia Contributors author, got %q", got[0].Author)
	}
}

// TestRetrievalRegistry_SearchAll_FallbackChain_AfterWikipediaEmpty verifies
// the registry cascades past Wikipedia when its search returns empty.
func TestRetrievalRegistry_SearchAll_FallbackChain_AfterWikipediaEmpty(t *testing.T) {
	bridge := &fakeBridge{
		wikiURL:        "", // Wikipedia fails first
		wikiTitle:      "",
		searxngURL:     "", // SearXNG fails second
		ddgURL:         "https://duckduckgo.com/img.png",
		bySlugURLs:     nil, // Drive has nothing
	}
	cli := &http.Client{}
	reg := NewRetrievalProviderRegistry(zap.NewNop(), []RetrievalProvider{
		newFakeRetrievalProvider(t, asset.ProviderWikipedia, bridge, cli),
		newFakeRetrievalProvider(t, asset.ProviderSearXNG, bridge, cli),
		newFakeRetrievalProvider(t, asset.ProviderDuckDuckGo, bridge, cli),
		newFakeRetrievalProvider(t, asset.ProviderDrive, bridge, cli),
	})
	got, err := reg.SearchAll(context.Background(), "einstein", RetrievalSearchOptions{})
	if err != nil {
		t.Fatalf("SearchAll errored: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 hit from DDG, got %d", len(got))
	}
	if got[0].Provider != asset.ProviderDuckDuckGo {
		t.Fatalf("expected DDG fallback, got %s", got[0].Provider)
	}
}

// TestRetrievalRegistry_SearchAll_AllEmpty returns nil + nil when
// every source is exhausted. No error.
func TestRetrievalRegistry_SearchAll_AllEmpty(t *testing.T) {
	bridge := &fakeBridge{} // nothing configured
	cli := &http.Client{}
	reg := NewRetrievalProviderRegistry(zap.NewNop(), []RetrievalProvider{
		newFakeRetrievalProvider(t, asset.ProviderWikipedia, bridge, cli),
		newFakeRetrievalProvider(t, asset.ProviderDrive, bridge, cli),
	})
	got, err := reg.SearchAll(context.Background(), "nobody", RetrievalSearchOptions{})
	if err != nil {
		t.Fatalf("SearchAll errored: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil result, got %v", got)
	}
}

// TestRetrievalRegistry_SearchAll_NilProviderSafe verifies a nil
// registry doesn't panic.
func TestRetrievalRegistry_SearchAll_NilProviderSafe(t *testing.T) {
	var reg *RetrievalProviderRegistry
	got, err := reg.SearchAll(context.Background(), "anything", RetrievalSearchOptions{})
	if err != nil {
		t.Fatalf("nil-registry SearchAll errored: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil result on nil-registry, got %v", got)
	}
}

// TestRetrievalRegistry_SearchByName verifies the explicit-provider
// lookup works for the 4 canonical names and returns nil for unknown.
func TestRetrievalRegistry_SearchByName(t *testing.T) {
	bridge := &fakeBridge{}
	cli := &http.Client{}
	reg := NewDefaultProviderRegistry(bridge, cli, zap.NewNop(), "en", "http://searx.test")

	tests := []struct {
		name   asset.ImageProvider
		expect bool
	}{
		{asset.ProviderWikipedia, true},
		{asset.ProviderSearXNG, true},
		{asset.ProviderDuckDuckGo, true},
		{asset.ProviderDrive, true},
		{asset.ProviderFlux, false}, // not part of retrieval registry
		{asset.ProviderUnknown, false},
	}
	for _, tc := range tests {
		t.Run(string(tc.name), func(t *testing.T) {
			p := reg.SearchByName(tc.name)
			if (p != nil) != tc.expect {
				t.Fatalf("SearchByName(%s) expect=%v got=%v", tc.name, tc.expect, p != nil)
			}
		})
	}
}

// TestRetrievalRegistry_Diagnostics_HealthySurface verifies each
// provider is reachable in its probe (mocked via httptest server).
func TestRetrievalRegistry_Diagnostics_HealthySurface(t *testing.T) {
	// Spin up a tiny HTTP server for the Wikipedia/SearXNG probes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	bridge := &fakeBridge{}
	cli := srv.Client()
	// Point SearXNG URL at the test server.
	reg := NewRetrievalProviderRegistry(zap.NewNop(), []RetrievalProvider{
		NewSearXNGProvider(bridge, cli, zap.NewNop(), srv.URL),
		NewDuckDuckGoProvider(bridge, cli, zap.NewNop()),
		NewDriveImageProvider(bridge, zap.NewNop()),
	})

	got := reg.Diagnostics(context.Background())
	if err, ok := got[asset.ProviderSearXNG]; !ok || err != nil {
		t.Fatalf("expected SearXNG to be healthy, got %v", err)
	}
	// DDG + Drive always report nil (DDG has no probe; Drive is local).
	for _, name := range []asset.ImageProvider{asset.ProviderDuckDuckGo, asset.ProviderDrive} {
		if err, ok := got[name]; !ok || err != nil {
			t.Fatalf("expected %s healthy, got %v", name, err)
		}
	}
}

// TestRetrievalRegistry_SearXNG_Unconfigured verifies the
// SearXNGProvider gracefully returns nil + nil when its base URL
// is empty — UNCONFIGURED is NOT an error per Step 8 contract.
func TestRetrievalRegistry_SearXNG_Unconfigured(t *testing.T) {
	bridge := &fakeBridge{searxngURL: "https://searx.test/img.png"} // bridge could return a URL...
	cli := &http.Client{}
	p := NewSearXNGProvider(bridge, cli, zap.NewNop(), "" /* unconfigured */)
	got, err := p.Search(context.Background(), "anything", RetrievalSearchOptions{Lang: "en"})
	if err != nil {
		t.Fatalf("unconfigured SearXNG Search errored: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for unconfigured SearXNG, got %v", got)
	}
	// Healthy() must report the misconfiguration.
	if err := p.Healthy(context.Background()); err == nil {
		t.Fatalf("expected Health error for unconfigured SearXNG")
	}
}

// TestRetrievalRegistry_Drive_LocalSearch verifies DriveImageProvider
// surfaces previously-ingested assets by slug without HTTP probes.
func TestRetrievalRegistry_Drive_LocalSearch(t *testing.T) {
	bridge := &fakeBridge{
		bySlugURLs: map[string][]string{
			"albert-einstein": {"https://drive.test/einstein-1.png"},
		},
	}
	p := NewDriveImageProvider(bridge, zap.NewNop())
	got, err := p.Search(context.Background(), "albert-einstein", RetrievalSearchOptions{})
	if err != nil {
		t.Fatalf("DriveProvider.Search errored: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 Drive hit, got %d", len(got))
	}
	if got[0].Provider != asset.ProviderDrive {
		t.Fatalf("expected Drive provider, got %s", got[0].Provider)
	}
}

// TestRetrievalRegistry_SearchAll_ContinuesAfterError verifies that
// if a provider returns an error (logged + skipped), the
// next provider in the chain is tried.
func TestRetrievalRegistry_SearchAll_ContinuesAfterError(t *testing.T) {
	// Custom provider that returns an explicit error to verify
	// error-skip semantics in SearchAll.
	errProv := &errProvider{name: asset.ProviderWikipedia, err: errors.New("simulated network failure")}
	goodProv := &stubProvider{name: asset.ProviderSearXNG, result: RetrievalSearchResult{
		Provider:   asset.ProviderSearXNG,
		Origin:     asset.ImageOriginRetrieved,
		PreviewURL: "https://searx.test/img.png",
	}}

	reg := NewRetrievalProviderRegistry(zap.NewNop(), []RetrievalProvider{errProv, goodProv})
	got, err := reg.SearchAll(context.Background(), "anything", RetrievalSearchOptions{})
	if err != nil {
		t.Fatalf("SearchAll errored: %v", err)
	}
	if len(got) != 1 || got[0].Provider != asset.ProviderSearXNG {
		t.Fatalf("expected SearXNG fallback after Wikipedia error, got %v", got)
	}
}

// ── Test helpers ──────────────────────────────────────────────────────

type stubProvider struct {
	name   asset.ImageProvider
	result RetrievalSearchResult
}

func (p *stubProvider) Name() asset.ImageProvider { return p.name }
func (p *stubProvider) Healthy(_ context.Context) error { return nil }
func (p *stubProvider) Search(_ context.Context, _ string, _ RetrievalSearchOptions) ([]RetrievalSearchResult, error) {
	return []RetrievalSearchResult{p.result}, nil
}

type errProvider struct {
	name asset.ImageProvider
	err  error
}

func (p *errProvider) Name() asset.ImageProvider { return p.name }
func (p *errProvider) Healthy(_ context.Context) error { return p.err }
func (p *errProvider) Search(_ context.Context, _ string, _ RetrievalSearchOptions) ([]RetrievalSearchResult, error) {
	return nil, p.err
}
