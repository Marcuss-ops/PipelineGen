package wiring

import (
	"context"
	"errors"
	"testing"

	imagesrouting "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"go.uber.org/zap"
)

type routingFakeSearcher struct {
	provider string
	results  []imagesrouting.ImageSearchResult
	calls    int
}

func (s *routingFakeSearcher) Search(context.Context, imagesrouting.ImageFilter) ([]imagesrouting.ImageSearchResult, error) {
	s.calls++
	return s.results, nil
}

type routingFakeResolver struct {
	providers map[string]imagesrouting.ImageSearcher
}

func (r *routingFakeResolver) Resolve(imagesrouting.ImageSearchTerritory) (imagesrouting.ImageSearcher, error) {
	searcher, ok := r.providers["duckduckgo"]
	if !ok {
		return nil, errors.New("no retrieved searcher")
	}
	return searcher, nil
}

func (r *routingFakeResolver) ResolveProvider(name string) (imagesrouting.ImageSearcher, error) {
	searcher, ok := r.providers[name]
	if !ok {
		return nil, errors.New("provider not registered: " + name)
	}
	return searcher, nil
}

func (r *routingFakeResolver) ExistingImages(context.Context, string, int) ([]imagesrouting.ImageSearchResult, error) {
	return nil, nil
}

func TestInternetImageSearchRoutesPeopleAwayFromCommons(t *testing.T) {
	ddg := &routingFakeSearcher{provider: "duckduckgo", results: []imagesrouting.ImageSearchResult{
		{PreviewURL: "https://ddg.invalid/stasi.jpg", Provider: "duckduckgo", Score: 1},
	}}
	commons := &routingFakeSearcher{provider: "wikimedia_commons"}
	adapter := newInternetImageSearchAdapter(&routingFakeResolver{providers: map[string]imagesrouting.ImageSearcher{
		"duckduckgo": ddg, "wikimedia_commons": commons,
	}}, zap.NewNop())

	results, err := adapter.SearchImages(context.Background(), adapters.InternetImageSearchRequest{
		SegmentID: "vc_1", Query: "Alberto Stasi", Entity: "Alberto Stasi", QueryClass: "person", Limit: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if commons.calls != 0 {
		t.Fatalf("person query reached Commons %d times", commons.calls)
	}
	if ddg.calls != 2 {
		t.Fatalf("person query DDG calls = %d, want primary + portrait", ddg.calls)
	}
	if len(results) == 0 {
		t.Fatal("person query lost its DDG candidates")
	}
}

func TestInternetImageSearchPrefersCommonsForEditorialClasses(t *testing.T) {
	ddg := &routingFakeSearcher{provider: "duckduckgo", results: []imagesrouting.ImageSearchResult{
		{PreviewURL: "https://ddg.invalid/corte.jpg", Provider: "duckduckgo"},
	}}
	commons := &routingFakeSearcher{provider: "wikimedia_commons", results: []imagesrouting.ImageSearchResult{
		{PreviewURL: "https://commons.invalid/corte.jpg", Provider: "wikimedia_commons", License: "CC BY 4.0"},
	}}
	adapter := newInternetImageSearchAdapter(&routingFakeResolver{providers: map[string]imagesrouting.ImageSearcher{
		"duckduckgo": ddg, "wikimedia_commons": commons,
	}}, zap.NewNop())

	results, err := adapter.SearchImages(context.Background(), adapters.InternetImageSearchRequest{
		SegmentID: "vc_1", Query: "Corte d'Appello", Entity: "Corte d'Appello", QueryClass: "organization", Limit: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ddg.calls != 0 {
		t.Fatalf("a Commons hit must avoid DuckDuckGo, got %d calls", ddg.calls)
	}
	// The candidate keeps the provider that actually answered (the plan cache
	// invalidates per provider), not the generic `internet_images` lane label.
	if commons.calls != 1 || len(results) != 1 || results[0].Provider != "wikimedia_commons" {
		t.Fatalf("editorial routing result = %+v calls=%d", results, commons.calls)
	}
	if results[0].SourceURL != "https://commons.invalid/corte.jpg" {
		t.Fatalf("editorial result lost the Commons source: %+v", results[0])
	}
}

func TestInternetImageSearchFallsBackToDDGWhenCommonsMisses(t *testing.T) {
	ddg := &routingFakeSearcher{provider: "duckduckgo", results: []imagesrouting.ImageSearchResult{
		{PreviewURL: "https://ddg.invalid/pavia.jpg", Provider: "duckduckgo"},
	}}
	commons := &routingFakeSearcher{provider: "wikimedia_commons"}
	adapter := newInternetImageSearchAdapter(&routingFakeResolver{providers: map[string]imagesrouting.ImageSearcher{
		"duckduckgo": ddg, "wikimedia_commons": commons,
	}}, zap.NewNop())

	results, err := adapter.SearchImages(context.Background(), adapters.InternetImageSearchRequest{
		SegmentID: "vc_1", Query: "Pavia", Entity: "Pavia", QueryClass: "location", Limit: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if commons.calls != 1 || ddg.calls != 1 {
		t.Fatalf("calls = commons:%d ddg:%d, want one each", commons.calls, ddg.calls)
	}
	if len(results) != 1 || results[0].SourceURL != "https://ddg.invalid/pavia.jpg" || results[0].Provider != "duckduckgo" {
		t.Fatalf("fallback result = %+v", results)
	}
}
