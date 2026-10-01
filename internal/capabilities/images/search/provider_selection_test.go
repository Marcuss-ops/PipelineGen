package retrieved

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

// fakeProvider is a deterministic RetrievalProvider used to exercise the
// selection primitives without any network round-trip.
type fakeProvider struct {
	name       detail.ImageProvider
	results    []RetrievalSearchResult
	searchErr  error
	healthyErr error
	delay      time.Duration
	calls      int32
}

func (p *fakeProvider) Name() detail.ImageProvider { return p.name }

func (p *fakeProvider) Healthy(ctx context.Context) error {
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return p.healthyErr
}

func (p *fakeProvider) Search(ctx context.Context, query string, opts RetrievalSearchOptions) ([]RetrievalSearchResult, error) {
	atomic.AddInt32(&p.calls, 1)
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.searchErr != nil {
		return nil, p.searchErr
	}
	return p.results, nil
}

func newTestRegistry(providers ...RetrievalProvider) *RetrievalProviderRegistry {
	return NewRetrievalProviderRegistry(zap.NewNop(), providers)
}

func TestSearchBest_PrefersHigherQualityLaterProvider(t *testing.T) {
	// Wikipedia runs first and returns a tiny, license-only thumbnail.
	wikipedia := &fakeProvider{name: detail.ProviderWikipedia, results: []RetrievalSearchResult{{
		Provider: detail.ProviderWikipedia, PreviewURL: "https://wiki.example/thumb.jpg",
		Width: 120, Height: 90, License: "CC-BY-SA-4.0",
	}}}
	// DuckDuckGo runs last and returns a high-resolution, well-scored hit.
	duckduckgo := &fakeProvider{name: detail.ProviderDuckDuckGo, results: []RetrievalSearchResult{{
		Provider: detail.ProviderDuckDuckGo, PreviewURL: "https://ddg.example/big.jpg",
		Width: 1920, Height: 1080, Score: 1, License: "Unknown",
	}}}

	registry := newTestRegistry(wikipedia, duckduckgo)
	got, err := registry.SearchBest(context.Background(), "boxing arena", RetrievalSearchOptions{Lang: "en"})
	if err != nil {
		t.Fatalf("SearchBest: %v", err)
	}
	// Union semantics: both providers contribute, ranked so the higher-quality
	// DuckDuckGo hit leads.
	if len(got) != 2 {
		t.Fatalf("SearchBest returned %d results, want the 2-provider union", len(got))
	}
	if got[0].PreviewURL != "https://ddg.example/big.jpg" {
		t.Fatalf("SearchBest[0] = %q, want the higher-quality DuckDuckGo hit", got[0].PreviewURL)
	}
	// Both providers must have been consulted: this is what makes the later
	// provider reachable at all.
	if atomic.LoadInt32(&wikipedia.calls) != 1 || atomic.LoadInt32(&duckduckgo.calls) != 1 {
		t.Fatalf("provider calls = wiki:%d ddg:%d, want both 1", wikipedia.calls, duckduckgo.calls)
	}
}

func TestSearchBest_LatencyBoundedBySlowestProvider(t *testing.T) {
	const delay = 80 * time.Millisecond
	providers := []RetrievalProvider{
		&fakeProvider{name: detail.ProviderWikipedia, delay: delay, results: []RetrievalSearchResult{{Provider: detail.ProviderWikipedia, PreviewURL: "https://wiki.example/a.jpg", Width: 1920, Height: 1080}}},
		&fakeProvider{name: detail.ProviderSearXNG, delay: delay},
		&fakeProvider{name: detail.ProviderDuckDuckGo, delay: delay},
	}
	registry := newTestRegistry(providers...)

	start := time.Now()
	if _, err := registry.SearchBest(context.Background(), "sports", RetrievalSearchOptions{}); err != nil {
		t.Fatalf("SearchBest: %v", err)
	}
	elapsed := time.Since(start)
	// Sequential first-hit-wins would take 3×delay (240ms); concurrent fan-out
	// must stay close to one delay. Bound generously for CI noise.
	if elapsed > 2*delay {
		t.Fatalf("SearchBest took %v, want < %v (providers must run concurrently)", elapsed, 2*delay)
	}
}

func TestSearchBest_SkipsErrorsAndEmptyProviders(t *testing.T) {
	errored := &fakeProvider{name: detail.ProviderWikipedia, searchErr: errors.New("503")}
	empty := &fakeProvider{name: detail.ProviderWikimediaCommons}
	hit := &fakeProvider{name: detail.ProviderDuckDuckGo, results: []RetrievalSearchResult{{Provider: detail.ProviderDuckDuckGo, PreviewURL: "https://ddg.example/hit.jpg", Width: 1280, Height: 720}}}

	registry := newTestRegistry(errored, empty, hit)
	got, err := registry.SearchBest(context.Background(), "query", RetrievalSearchOptions{})
	if err != nil {
		t.Fatalf("SearchBest: %v", err)
	}
	if len(got) != 1 || got[0].PreviewURL != "https://ddg.example/hit.jpg" {
		t.Fatalf("SearchBest = %+v, want the surviving DuckDuckGo hit", got)
	}
}

func TestSearchBest_ReturnsUnionRankedAndDeduped(t *testing.T) {
	// The same image URL is returned by two providers: it must appear once.
	shared := "https://cdn.example/shared.jpg"
	wikipedia := &fakeProvider{name: detail.ProviderWikipedia, results: []RetrievalSearchResult{
		{Provider: detail.ProviderWikipedia, PreviewURL: shared, Width: 400, Height: 300, License: "CC-BY-SA-4.0"},
		{Provider: detail.ProviderWikipedia, PreviewURL: "https://wiki.example/low.jpg", Width: 200, Height: 150},
	}}
	duckduckgo := &fakeProvider{name: detail.ProviderDuckDuckGo, results: []RetrievalSearchResult{
		{Provider: detail.ProviderDuckDuckGo, PreviewURL: shared, Width: 400, Height: 300},
		{Provider: detail.ProviderDuckDuckGo, PreviewURL: "https://ddg.example/high.jpg", Width: 1920, Height: 1080, Score: 1},
	}}

	registry := newTestRegistry(wikipedia, duckduckgo)
	got, err := registry.SearchBest(context.Background(), "union", RetrievalSearchOptions{})
	if err != nil {
		t.Fatalf("SearchBest: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("SearchBest returned %d results, want 3 after de-dup (shared + low + high)", len(got))
	}
	if got[0].PreviewURL != "https://ddg.example/high.jpg" {
		t.Fatalf("SearchBest[0] = %q, want the 1080p hit ranked first", got[0].PreviewURL)
	}
	countShared := 0
	for _, result := range got {
		if result.PreviewURL == shared {
			countShared++
		}
	}
	if countShared != 1 {
		t.Fatalf("shared URL appeared %d times, want exactly 1 after de-dup", countShared)
	}
}

func TestSearchBest_ReturnsNilWhenAllProvidersMiss(t *testing.T) {
	registry := newTestRegistry(
		&fakeProvider{name: detail.ProviderWikipedia},
		&fakeProvider{name: detail.ProviderDuckDuckGo, searchErr: errors.New("quota")},
	)
	got, err := registry.SearchBest(context.Background(), "query", RetrievalSearchOptions{})
	if err != nil {
		t.Fatalf("SearchBest: %v", err)
	}
	if got != nil {
		t.Fatalf("SearchBest = %+v, want nil", got)
	}
}

func TestSearchBest_NilRegistryAndEmptyQuery(t *testing.T) {
	var nilRegistry *RetrievalProviderRegistry
	if got, err := nilRegistry.SearchBest(context.Background(), "q", RetrievalSearchOptions{}); err != nil || got != nil {
		t.Fatalf("nil registry SearchBest = (%v, %v), want (nil, nil)", got, err)
	}
	registry := newTestRegistry(&fakeProvider{name: detail.ProviderWikipedia, results: []RetrievalSearchResult{{PreviewURL: "x"}}})
	if got, err := registry.SearchBest(context.Background(), "   ", RetrievalSearchOptions{}); err != nil || got != nil {
		t.Fatalf("empty query SearchBest = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestSearchReceding_EmptyPrimaryRecedesToNextQuery(t *testing.T) {
	// The provider only answers the second (relaxed) rung.
	provider := &scriptedProvider{
		name: detail.ProviderDuckDuckGo,
		byQuery: map[string][]RetrievalSearchResult{
			"very narrow primary query": nil,
			"narrow primary query":      {{Provider: detail.ProviderDuckDuckGo, PreviewURL: "https://ddg.example/relaxed.jpg"}},
		},
	}
	registry := newTestRegistry(provider)

	got, used, err := registry.SearchReceding(context.Background(), []string{"very narrow primary query", "narrow primary query"}, RetrievalSearchOptions{})
	if err != nil {
		t.Fatalf("SearchReceding: %v", err)
	}
	if len(got) != 1 || got[0].PreviewURL != "https://ddg.example/relaxed.jpg" {
		t.Fatalf("SearchReceding = %+v, want the relaxed-query hit", got)
	}
	if used != "narrow primary query" {
		t.Fatalf("used query = %q, want the receded query", used)
	}
}

func TestSearchReceding_AllQueriesMiss(t *testing.T) {
	registry := newTestRegistry(&fakeProvider{name: detail.ProviderDuckDuckGo})
	got, used, err := registry.SearchReceding(context.Background(), []string{"a", "b"}, RetrievalSearchOptions{})
	if err != nil || got != nil || used != "" {
		t.Fatalf("SearchReceding = (%v, %q, %v), want (nil, \"\", nil)", got, used, err)
	}
}

func TestRelaxImageQuery_ProducesOrderedLadder(t *testing.T) {
	got := RelaxImageQuery("Describe Elon Musk (Tesla event)")
	if len(got) < 3 {
		t.Fatalf("RelaxImageQuery ladder = %v, want several ordered variants", got)
	}
	if got[0] != "Describe Elon Musk (Tesla event)" {
		t.Fatalf("first rung = %q, want the exact original query", got[0])
	}
	if got[1] != "Elon Musk (Tesla event)" {
		t.Fatalf("second rung = %q, want the instruction prefix stripped", got[1])
	}
	// The most relaxed rung is the single leading token.
	if last := got[len(got)-1]; last != "Describe" {
		t.Fatalf("last rung = %q, want single-token truncation \"Describe\"", last)
	}
	// No duplicates anywhere in the ladder.
	seen := map[string]struct{}{}
	for _, q := range got {
		if _, dup := seen[q]; dup {
			t.Fatalf("duplicate rung %q in ladder %v", q, got)
		}
		seen[q] = struct{}{}
	}
}

func TestRelaxImageQuery_EmptyInput(t *testing.T) {
	if got := RelaxImageQuery("   "); got != nil {
		t.Fatalf("RelaxImageQuery(blank) = %v, want nil", got)
	}
}

func TestHealthReport_NamesUnhealthyProviders(t *testing.T) {
	healthy := &fakeProvider{name: detail.ProviderWikipedia}
	broken := &fakeProvider{name: detail.ProviderDuckDuckGo, healthyErr: errors.New("unreachable")}
	registry := newTestRegistry(healthy, broken)

	report := registry.HealthReport(context.Background())
	if !report.AllHealthy() {
		t.Fatal("AllHealthy = false, want true with one usable provider")
	}
	if len(report.Healthy) != 1 || report.Healthy[0] != detail.ProviderWikipedia {
		t.Fatalf("healthy = %v, want [wikipedia]", report.Healthy)
	}
	if reason, ok := report.Unhealthy[detail.ProviderDuckDuckGo]; !ok || reason == "" {
		t.Fatalf("unhealthy = %v, want duckduckgo with a reason", report.Unhealthy)
	}
}

func TestHealthReport_AllUnhealthyIsNotAllHealthy(t *testing.T) {
	registry := newTestRegistry(
		&fakeProvider{name: detail.ProviderWikipedia, healthyErr: errors.New("down")},
		&fakeProvider{name: detail.ProviderDuckDuckGo, healthyErr: errors.New("down")},
	)
	report := registry.HealthReport(context.Background())
	if report.AllHealthy() {
		t.Fatal("AllHealthy = true, want false when every provider is unhealthy")
	}
}

func TestHealthyProviders_PreservesRegisteredOrder(t *testing.T) {
	registry := newTestRegistry(
		&fakeProvider{name: detail.ProviderWikipedia},
		&fakeProvider{name: detail.ProviderSearXNG, healthyErr: errors.New("down")},
		&fakeProvider{name: detail.ProviderDuckDuckGo},
	)
	got := registry.HealthyProviders(context.Background())
	if len(got) != 2 || got[0].Name() != detail.ProviderWikipedia || got[1].Name() != detail.ProviderDuckDuckGo {
		t.Fatalf("HealthyProviders = %v, want [wikipedia duckduckgo] in order", got)
	}
}

// scriptedProvider answers per-query, so recession can be tested without the
// shared fakeProvider's single result set.
type scriptedProvider struct {
	name    detail.ImageProvider
	byQuery map[string][]RetrievalSearchResult
}

func (p *scriptedProvider) Name() detail.ImageProvider    { return p.name }
func (p *scriptedProvider) Healthy(context.Context) error { return nil }
func (p *scriptedProvider) Search(_ context.Context, query string, _ RetrievalSearchOptions) ([]RetrievalSearchResult, error) {
	return p.byQuery[query], nil
}
