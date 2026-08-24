package webresearch

import (
	"context"
	"errors"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"go.uber.org/zap"
)

// mockProvider is a minimal WebSearchProvider for tests.
type mockProvider struct {
	name string
	hits []scriptports.WebSearchHit
	err  error
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) Search(_ context.Context, _ string, _ int) ([]scriptports.WebSearchHit, error) {
	return m.hits, m.err
}

func TestMultiWebSearcher_MergesProviders(t *testing.T) {
	p1 := &mockProvider{
		name: "provider1",
		hits: []scriptports.WebSearchHit{
			{Title: "A", URL: "https://a.com/1"},
			{Title: "B", URL: "https://b.com/1"},
		},
	}
	p2 := &mockProvider{
		name: "provider2",
		hits: []scriptports.WebSearchHit{
			{Title: "C", URL: "https://c.com/1"},
			{Title: "B", URL: "https://b.com/1"}, // duplicate
		},
	}

	ms := NewMultiWebSearcher(zap.NewNop(), p1, p2)
	hits, err := ms.Search(context.Background(), "test query", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 3 {
		t.Errorf("expected 3 unique hits, got %d", len(hits))
		for _, h := range hits {
			t.Logf("  %s %s", h.Title, h.URL)
		}
	}
}

func TestMultiWebSearcher_SkipsFailingProvider(t *testing.T) {
	p1 := &mockProvider{
		name: "failing",
		err:  errors.New("connection refused"),
	}
	p2 := &mockProvider{
		name: "working",
		hits: []scriptports.WebSearchHit{
			{Title: "OK", URL: "https://ok.com/1"},
		},
	}

	ms := NewMultiWebSearcher(zap.NewNop(), p1, p2)
	hits, err := ms.Search(context.Background(), "test", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("expected 1 hit from working provider, got %d", len(hits))
	}
}

func TestMultiWebSearcher_AllFailing(t *testing.T) {
	p1 := &mockProvider{name: "f1", err: errors.New("fail1")}
	p2 := &mockProvider{name: "f2", err: errors.New("fail2")}

	ms := NewMultiWebSearcher(zap.NewNop(), p1, p2)
	hits, err := ms.Search(context.Background(), "test", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits when all providers fail, got %d", len(hits))
	}
}

func TestMultiWebSearcher_ProviderNames(t *testing.T) {
	p1 := &mockProvider{name: "a"}
	p2 := &mockProvider{name: "b"}
	ms := NewMultiWebSearcher(zap.NewNop(), p1, p2)

	names := ms.ProviderNames()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Errorf("ProviderNames() = %v, want [a b]", names)
	}
}

func TestMultiWebSearcher_HasProvider(t *testing.T) {
	p1 := &mockProvider{name: "searxng"}
	ms := NewMultiWebSearcher(zap.NewNop(), p1)

	if !ms.HasProvider("searxng") {
		t.Error("HasProvider(searxng) = false, want true")
	}
	if ms.HasProvider("duckduckgo") {
		t.Error("HasProvider(duckduckgo) = true, want false")
	}
}

func TestMultiWebSearcher_NilSearcher(t *testing.T) {
	ms := (*MultiWebSearcher)(nil)
	hits, err := ms.Search(context.Background(), "test", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil {
		t.Errorf("expected nil hits from nil searcher, got %d", len(hits))
	}
}
