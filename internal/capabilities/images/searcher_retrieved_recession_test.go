package images

import (
	"context"
	"testing"

	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

type scriptedRetrievalBackend struct {
	byQuery map[string][]RetrievalSearchResult
	seen    []string
}

func (b *scriptedRetrievalBackend) SearchAll(_ context.Context, query string, _ RetrievalSearchOptions) ([]RetrievalSearchResult, error) {
	b.seen = append(b.seen, query)
	return b.byQuery[query], nil
}

func TestRetrievedSearcher_RecedesToRelaxedQueryOnEmptyPrimary(t *testing.T) {
	backend := &scriptedRetrievalBackend{byQuery: map[string][]RetrievalSearchResult{
		"Elon Musk boxing arena": nil,
		"Elon Musk boxing":       nil,
		"Elon Musk":              {{Provider: detail.ProviderDuckDuckGo, PreviewURL: "https://ddg.example/elon.jpg"}},
	}}
	s := newRetrievedSearcher(backend)

	got, err := s.Search(context.Background(), ImageFilter{SubjectID: "Elon Musk boxing arena"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].PreviewURL != "https://ddg.example/elon.jpg" {
		t.Fatalf("Search = %+v, want the relaxed-query hit", got)
	}
	if got[0].Origin != string(detail.ImageOriginRetrieved) {
		t.Fatalf("origin = %q, want retrieved", got[0].Origin)
	}
	if len(backend.seen) < 2 {
		t.Fatalf("queries seen = %v, want the primary plus at least one relaxed rung", backend.seen)
	}
	if backend.seen[0] != "Elon Musk boxing arena" {
		t.Fatalf("first query = %q, want the primary query first", backend.seen[0])
	}
}

func TestRetrievedSearcher_NoRecessionWhenPrimaryHits(t *testing.T) {
	backend := &scriptedRetrievalBackend{byQuery: map[string][]RetrievalSearchResult{
		"Elon Musk boxing arena": {{Provider: detail.ProviderWikipedia, PreviewURL: "https://wiki.example/elon.jpg"}},
	}}
	s := newRetrievedSearcher(backend)

	got, err := s.Search(context.Background(), ImageFilter{SubjectID: "Elon Musk boxing arena"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Search = %+v, want 1 hit", got)
	}
	if len(backend.seen) != 1 {
		t.Fatalf("queries seen = %v, want only the primary query (no recession on a hit)", backend.seen)
	}
}

func TestRetrievedSearcher_RecessionExhaustedReturnsEmpty(t *testing.T) {
	backend := &scriptedRetrievalBackend{byQuery: map[string][]RetrievalSearchResult{}}
	s := newRetrievedSearcher(backend)

	got, err := s.Search(context.Background(), ImageFilter{SubjectID: "Elon Musk boxing arena"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Search = %+v, want no rows when every rung misses", got)
	}
	// The ladder is bounded: primary + at most maxRetrievalRecessionRungs.
	if len(backend.seen) > 1+maxRetrievalRecessionRungs {
		t.Fatalf("queries seen = %d, want at most %d", len(backend.seen), 1+maxRetrievalRecessionRungs)
	}
}
