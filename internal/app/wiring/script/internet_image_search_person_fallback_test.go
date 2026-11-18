package script

import (
	"context"
	"errors"
	"testing"

	imagesrouting "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
)

// These tests pin the PERSON fallback contract: Wikipedia is a prior, not a
// gate. A person without a pageimage, a Wikipedia outage or a worker where the
// provider is not registered must still return the DuckDuckGo candidates,
// otherwise the Commons-first improvement would silently remove coverage for
// non-encyclopedic subjects (Poggi, Stasi, Burry, Ferretti).

type personFallbackSearcher struct {
	results []imagesrouting.ImageSearchResult
	err     error
	queries []string
}

func (s *personFallbackSearcher) Search(_ context.Context, filter imagesrouting.ImageFilter) ([]imagesrouting.ImageSearchResult, error) {
	s.queries = append(s.queries, filter.SubjectID)
	if s.err != nil {
		return nil, s.err
	}
	return s.results, nil
}

type personFallbackResolver struct {
	providers map[string]imagesrouting.ImageSearcher
	errors    map[string]error
}

func (r *personFallbackResolver) Resolve(imagesrouting.ImageSearchTerritory) (imagesrouting.ImageSearcher, error) {
	return nil, errors.New("PERSON routing must resolve explicit providers")
}

func (r *personFallbackResolver) ResolveProvider(name string) (imagesrouting.ImageSearcher, error) {
	if err := r.errors[name]; err != nil {
		return nil, err
	}
	if searcher, ok := r.providers[name]; ok {
		return searcher, nil
	}
	return nil, errors.New("provider not configured: " + name)
}

func personFallbackDuckDuckGoResult() imagesrouting.ImageSearchResult {
	return imagesrouting.ImageSearchResult{
		AssetID:    "ddg-john-cena",
		Provider:   "duckduckgo",
		PreviewURL: "https://images.example/john-cena-ddg.jpg",
		Width:      1200,
		Height:     1200,
	}
}

func runPersonFallbackSearch(t *testing.T, resolver imagesrouting.ImageSearchResolver) {
	t.Helper()
	adapter := NewInternetImageSearchAdapter(resolver, nil)
	got, err := adapter.SearchImages(context.Background(), adapters.InternetImageSearchRequest{
		Entity:     "John Cena",
		Query:      "John Cena",
		QueryClass: "PERSON",
		Limit:      6,
	})
	if err != nil {
		t.Fatalf("SearchImages: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("got no candidates: the DuckDuckGo fallback was dropped")
	}
	for _, candidate := range got {
		if candidate.Provider != "duckduckgo" {
			t.Fatalf("candidate provider = %q, want duckduckgo", candidate.Provider)
		}
	}
	if got[0].SourceURL != "https://images.example/john-cena-ddg.jpg" {
		t.Fatalf("first candidate URL = %q, want the DuckDuckGo hit", got[0].SourceURL)
	}
}

func TestPersonRouteKeepsDuckDuckGoWhenWikipediaHasNoPageImage(t *testing.T) {
	wikipedia := &personFallbackSearcher{}
	resolver := &personFallbackResolver{providers: map[string]imagesrouting.ImageSearcher{
		"wikipedia":         wikipedia,
		"duckduckgo":        &personFallbackSearcher{results: []imagesrouting.ImageSearchResult{personFallbackDuckDuckGoResult()}},
		"searxng":           &personFallbackSearcher{},
		"wikimedia_commons": &personFallbackSearcher{},
	}}
	runPersonFallbackSearch(t, resolver)

	if len(wikipedia.queries) != 1 || wikipedia.queries[0] != "John Cena" {
		t.Fatalf("wikipedia queries = %v, want the entity name", wikipedia.queries)
	}
}

func TestPersonRouteKeepsDuckDuckGoWhenWikipediaSearchFails(t *testing.T) {
	resolver := &personFallbackResolver{
		providers: map[string]imagesrouting.ImageSearcher{
			"wikipedia":  &personFallbackSearcher{err: errors.New("wikipedia 429")},
			"duckduckgo": &personFallbackSearcher{results: []imagesrouting.ImageSearchResult{personFallbackDuckDuckGoResult()}},
		},
	}
	runPersonFallbackSearch(t, resolver)
}

func TestPersonRouteKeepsDuckDuckGoWhenWikipediaProviderIsMissing(t *testing.T) {
	resolver := &personFallbackResolver{
		providers: map[string]imagesrouting.ImageSearcher{
			"duckduckgo": &personFallbackSearcher{results: []imagesrouting.ImageSearchResult{personFallbackDuckDuckGoResult()}},
		},
		errors: map[string]error{"wikipedia": errors.New("provider not configured")},
	}
	runPersonFallbackSearch(t, resolver)
}
