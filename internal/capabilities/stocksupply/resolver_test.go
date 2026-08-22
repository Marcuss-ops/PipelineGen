package stocksupply

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/stocksupply"
)

// ── Test stubs ────────────────────────────────────────────────────────────

type testLocalSearcher struct {
	hits map[string][]LocalHit
	err  error
}

func (s *testLocalSearcher) SearchCatalog(ctx context.Context, query string, limit int) ([]LocalHit, error) {
	if s.err != nil {
		return nil, s.err
	}
	hits := s.hits[query]
	if limit > 0 && len(hits) > limit {
		return hits[:limit], nil
	}
	return hits, nil
}

type testSearchProvider struct {
	name         string
	hits         map[string][]providers.Candidate
	err          error
	searchCalled int
}

func (p *testSearchProvider) Name() string { return p.name }
func (p *testSearchProvider) Capabilities() []providers.Capability {
	return []providers.Capability{providers.CapabilitySearch, providers.CapabilityFetch}
}
func (p *testSearchProvider) Search(ctx context.Context, req providers.SearchRequest) (providers.SearchResult, error) {
	p.searchCalled++
	if p.err != nil {
		return providers.SearchResult{}, p.err
	}
	hits := p.hits[req.Query]
	if len(hits) > req.Limit {
		hits = hits[:req.Limit]
	}
	return providers.SearchResult{Candidates: hits}, nil
}

type testFetchProvider struct {
	name        string
	err         error
	fetchCalled int
	assets      map[string]testFetchedAsset
}

type testFetchedAsset struct {
	assetID    string
	durationMs int64
}

func (p *testFetchProvider) Name() string { return p.name }
func (p *testFetchProvider) Capabilities() []providers.Capability {
	return []providers.Capability{providers.CapabilityFetch}
}
func (p *testFetchProvider) Search(ctx context.Context, req providers.SearchRequest) (providers.SearchResult, error) {
	return providers.SearchResult{}, errors.New("fetch-only: Search not supported")
}
func (p *testFetchProvider) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchedAsset, error) {
	p.fetchCalled++
	if p.err != nil {
		return nil, p.err
	}
	return &providers.FetchedAsset{LocalPath: "/tmp/test.mp4", Bytes: 1024, Asset: nil}, nil
}

type testProviderRegistry struct {
	searchProvs map[string]providers.SearchProvider
	fetchProvs  map[string]providers.FetchProvider
}

func (r *testProviderRegistry) SearchProvider(name string) providers.SearchProvider {
	return r.searchProvs[name]
}
func (r *testProviderRegistry) FetchProvider(name string) providers.FetchProvider {
	return r.fetchProvs[name]
}

type testClipIngester struct {
	ingested []providers.FetchRequest
	err      error
	// per-url asset response
	assets map[string]testFetchedAsset
}

func (i *testClipIngester) IngestFromFetch(ctx context.Context, req providers.FetchRequest) (string, int64, error) {
	i.ingested = append(i.ingested, req)
	if i.err != nil {
		return "", 0, i.err
	}
	if a, ok := i.assets[req.SourceRef]; ok {
		return a.assetID, a.durationMs, nil
	}
	return "asset-" + req.SourceRef, 5000, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────

func newTestResolver(localSe LocalSearcher, reg ProviderRegistry, ingest ClipIngester) StockSupplyResolver {
	r, err := NewResolver(localSe, reg, ingest)
	if err != nil {
		panic(err)
	}
	return r
}

// ── Tests ────────────────────────────────────────────────────────────────

func TestNewResolver_RejectsNilPorts(t *testing.T) {
	tests := []struct {
		name  string
		local LocalSearcher
		reg   ProviderRegistry
		ing   ClipIngester
		want  string
	}{
		{"nil local", nil, &testProviderRegistry{}, &testClipIngester{}, "LocalSearcher"},
		{"nil registry", &testLocalSearcher{}, nil, &testClipIngester{}, "ProviderRegistry"},
		{"nil ingester", &testLocalSearcher{}, &testProviderRegistry{}, nil, "ClipIngester"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewResolver(tt.local, tt.reg, tt.ing)
			if err == nil {
				t.Fatal("expected error for nil port, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestResolve_LocalOnly_Sufficient(t *testing.T) {
	local := &testLocalSearcher{
		hits: map[string][]LocalHit{
			"boxing gym": {
				{AssetID: "a1", DurationMs: 30000, RelevanceScore: 0.9},
				{AssetID: "a2", DurationMs: 45000, RelevanceScore: 0.85},
				{AssetID: "a3", DurationMs: 60000, RelevanceScore: 0.80},
				{AssetID: "a4", DurationMs: 25000, RelevanceScore: 0.75},
				{AssetID: "a5", DurationMs: 50000, RelevanceScore: 0.70},
			},
		},
	}
	reg := &testProviderRegistry{}
	ing := &testClipIngester{}
	resolver := newTestResolver(local, reg, ing)

	result, err := resolver.Resolve(context.Background(), stocksupply.SupplyQuery{
		Queries:       []string{"boxing gym"},
		ReuseExisting: true,
		Strategy:      stocksupply.StrategyLocalFirst,
		Target: stocksupply.SupplyTarget{
			TargetDurationSec: 60,
			MinimumReadySec:   30,
		},
		SearchLimit: 10,
	})
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if result.State != stocksupply.StateReady {
		t.Fatalf("expected READY, got %s", result.State)
	}
	if result.ReusedAssets != 5 {
		t.Fatalf("expected 5 reused assets, got %d", result.ReusedAssets)
	}
	if result.NewAssets != 0 {
		t.Fatalf("expected 0 new assets, got %d", result.NewAssets)
	}
	dur := result.Queries[0].DurationSec
	if dur < 60 {
		t.Fatalf("expected >= 60s, got %d", dur)
	}
}

func TestResolve_LocalInsufficient_FallsBackToProvider(t *testing.T) {
	local := &testLocalSearcher{
		hits: map[string][]LocalHit{
			"Mike Tyson interview": {
				{AssetID: "local1", DurationMs: 10000, RelevanceScore: 0.8},
			},
		},
	}
	searchProv := &testSearchProvider{
		name: "youtube",
		hits: map[string][]providers.Candidate{
			"Mike Tyson interview": {
				{SourceRef: "https://youtube.com/watch?v=ABC", DurationMs: 30000},
				{SourceRef: "https://youtube.com/watch?v=DEF", DurationMs: 45000},
			},
		},
	}
	fetchProv := &testFetchProvider{name: "youtube"}
	reg := &testProviderRegistry{
		searchProvs: map[string]providers.SearchProvider{"youtube": searchProv},
		fetchProvs:  map[string]providers.FetchProvider{"youtube": fetchProv},
	}
	ing := &testClipIngester{
		assets: map[string]testFetchedAsset{
			"https://youtube.com/watch?v=ABC": {"yt-abc", 30000},
			"https://youtube.com/watch?v=DEF": {"yt-def", 45000},
		},
	}
	resolver := newTestResolver(local, reg, ing)

	result, err := resolver.Resolve(context.Background(), stocksupply.SupplyQuery{
		Queries:       []string{"Mike Tyson interview"},
		ReuseExisting: true,
		Strategy:      stocksupply.StrategyYouTubeFirst,
		Target: stocksupply.SupplyTarget{
			TargetDurationSec: 60,
			MinimumReadySec:   30,
		},
		SearchLimit: 10,
	})
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	// Local gave 10s, provider added 75s → total 85s ≥ 60s target → READY
	t.Logf("State=%s NewAssets=%d Reused=%d DurSec=%d",
		result.State, result.NewAssets, result.ReusedAssets, result.TotalDurationSec)
	if result.State != stocksupply.StateReady {
		t.Fatalf("expected READY, got %s", result.State)
	}
	if result.NewAssets < 1 {
		t.Fatalf("expected at least 1 new asset, got %d", result.NewAssets)
	}
	if searchProv.searchCalled != 1 {
		t.Fatalf("youtube search called %d times, want 1", searchProv.searchCalled)
	}
}

func TestResolve_LocalInsufficient_StrategyLocalFirst_StopsAtLocal(t *testing.T) {
	local := &testLocalSearcher{
		hits: map[string][]LocalHit{
			"sunsets": {
				{AssetID: "sun1", DurationMs: 5000, RelevanceScore: 0.9},
			},
		},
	}
	// youtube provider exists but with StrategyLocalFirst should NOT be called
	searchProv := &testSearchProvider{
		name: "youtube",
		hits: map[string][]providers.Candidate{
			"sunsets": {{SourceRef: "yt://sun", DurationMs: 30000}},
		},
	}
	fetchProv := &testFetchProvider{name: "youtube"}
	reg := &testProviderRegistry{
		searchProvs: map[string]providers.SearchProvider{"youtube": searchProv},
		fetchProvs:  map[string]providers.FetchProvider{"youtube": fetchProv},
	}
	ing := &testClipIngester{}
	resolver := newTestResolver(local, reg, ing)

	result, err := resolver.Resolve(context.Background(), stocksupply.SupplyQuery{
		Queries:       []string{"sunsets"},
		ReuseExisting: true,
		Strategy:      stocksupply.StrategyLocalFirst,
		Target: stocksupply.SupplyTarget{
			TargetDurationSec: 600,
			MinimumReadySec:   120,
		},
		SearchLimit: 5,
	})
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	// StrategyLocalFirst → only local used; 5s < 120s min → PARTIAL_READY
	if result.State != stocksupply.StatePartialReady {
		t.Fatalf("expected PARTIAL_READY, got %s", result.State)
	}
	if searchProv.searchCalled > 0 {
		t.Fatalf("YouTube search called %d times with local_first strategy", searchProv.searchCalled)
	}
}

func TestResolve_EmptyQueries_Error(t *testing.T) {
	resolver := newTestResolver(&testLocalSearcher{}, &testProviderRegistry{}, &testClipIngester{})
	_, err := resolver.Resolve(context.Background(), stocksupply.SupplyQuery{
		Queries: []string{},
	})
	if err == nil {
		t.Fatal("expected error for empty queries")
	}
}

func TestResolve_AllProvidersFail_ReturnsFailed(t *testing.T) {
	local := &testLocalSearcher{hits: map[string][]LocalHit{}}
	searchProv := &testSearchProvider{
		name: "youtube",
		err:  errors.New("network timeout"),
	}
	fetchProv := &testFetchProvider{name: "youtube"}
	reg := &testProviderRegistry{
		searchProvs: map[string]providers.SearchProvider{"youtube": searchProv},
		fetchProvs:  map[string]providers.FetchProvider{"youtube": fetchProv},
	}
	ing := &testClipIngester{}
	resolver := newTestResolver(local, reg, ing)

	result, err := resolver.Resolve(context.Background(), stocksupply.SupplyQuery{
		Queries:       []string{"Mike Tyson interview"},
		ReuseExisting: true,
		Strategy:      stocksupply.StrategyYouTubeFirst,
		Target: stocksupply.SupplyTarget{
			TargetDurationSec: 120,
			MinimumReadySec:   30,
		},
	})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	if result.State != stocksupply.StateFailed {
		t.Fatalf("expected FAILED, got %s", result.State)
	}
}

func TestResolve_WarmReuse_NoLiveCalls(t *testing.T) {
	local := &testLocalSearcher{
		hits: map[string][]LocalHit{
			"sunsets": {
				{AssetID: "a1", DurationMs: 60000, RelevanceScore: 0.95},
				{AssetID: "a2", DurationMs: 60000, RelevanceScore: 0.90},
			},
		},
	}
	searchProv := &testSearchProvider{name: "youtube", hits: map[string][]providers.Candidate{}}
	fetchProv := &testFetchProvider{name: "youtube"}
	reg := &testProviderRegistry{
		searchProvs: map[string]providers.SearchProvider{"youtube": searchProv},
		fetchProvs:  map[string]providers.FetchProvider{"youtube": fetchProv},
	}
	ing := &testClipIngester{}
	resolver := newTestResolver(local, reg, ing)

	result, err := resolver.Resolve(context.Background(), stocksupply.SupplyQuery{
		Queries:       []string{"sunsets"},
		ReuseExisting: true,
		Strategy:      stocksupply.StrategyYouTubeFirst,
		Target: stocksupply.SupplyTarget{
			TargetDurationSec: 60,
			MinimumReadySec:   30,
		},
	})
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if result.State != stocksupply.StateReady {
		t.Fatalf("expected READY, got %s", result.State)
	}
	if searchProv.searchCalled > 0 {
		t.Fatalf("YouTube search was called even though local was sufficient: %d calls", searchProv.searchCalled)
	}
	if result.NewAssets != 0 {
		t.Fatalf("expected 0 new assets for warm reuse, got %d", result.NewAssets)
	}
}

func TestResolve_MultipleQueries_PartialSuccess(t *testing.T) {
	local := &testLocalSearcher{
		hits: map[string][]LocalHit{
			"boxing training": {
				{AssetID: "b1", DurationMs: 60000, RelevanceScore: 0.9},
				{AssetID: "b2", DurationMs: 60000, RelevanceScore: 0.85},
			},
			// "boxing crowd" has zero local hits
		},
	}
	searchProv := &testSearchProvider{
		name: "youtube",
		hits: map[string][]providers.Candidate{
			"boxing crowd": {{SourceRef: "yt://crowd", DurationMs: 20000}},
		},
	}
	fetchProv := &testFetchProvider{name: "youtube"}
	reg := &testProviderRegistry{
		searchProvs: map[string]providers.SearchProvider{"youtube": searchProv},
		fetchProvs:  map[string]providers.FetchProvider{"youtube": fetchProv},
	}
	ing := &testClipIngester{
		assets: map[string]testFetchedAsset{
			"yt://crowd": {"crowd-1", 20000},
		},
	}
	resolver := newTestResolver(local, reg, ing)

	result, err := resolver.Resolve(context.Background(), stocksupply.SupplyQuery{
		Queries:       []string{"boxing training", "boxing crowd"},
		ReuseExisting: true,
		Strategy:      stocksupply.StrategyYouTubeFirst,
		Target: stocksupply.SupplyTarget{
			TargetDurationSec: 200,
			MinimumReadySec:   30,
		},
	})
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	t.Logf("State=%s TotalSec=%d New=%d Reused=%d",
		result.State, result.TotalDurationSec, result.NewAssets, result.ReusedAssets)
	// 120s local (training, satisfies its 100s share) + 20s new (crowd) = 140s.
	// 140s < 200s target but ≥ 30s minimum → PARTIAL_READY.
	if result.State != stocksupply.StatePartialReady {
		t.Fatalf("expected PARTIAL_READY, got %s", result.State)
	}
	if result.ReusedAssets != 2 {
		t.Fatalf("expected 2 reused assets, got %d", result.ReusedAssets)
	}
	if result.NewAssets != 1 {
		t.Fatalf("expected 1 new asset, got %d", result.NewAssets)
	}
}

func TestResolve_FetchProviderMissing_FallsThroughToNext(t *testing.T) {
	local := &testLocalSearcher{hits: map[string][]LocalHit{}}
	// artlist has a SearchProvider but NO FetchProvider.
	artlistSearch := &testSearchProvider{
		name: "artlist",
		hits: map[string][]providers.Candidate{
			"nature": {{SourceRef: "art://nature", DurationMs: 40000}},
		},
	}
	// youtube has both search + fetch and can satisfy the query.
	youtubeSearch := &testSearchProvider{
		name: "youtube",
		hits: map[string][]providers.Candidate{
			"nature": {{SourceRef: "yt://nature", DurationMs: 60000}},
		},
	}
	youtubeFetch := &testFetchProvider{name: "youtube"}
	reg := &testProviderRegistry{
		searchProvs: map[string]providers.SearchProvider{
			"artlist": artlistSearch,
			"youtube": youtubeSearch,
		},
		fetchProvs: map[string]providers.FetchProvider{
			"youtube": youtubeFetch, // artlist fetch intentionally missing
		},
	}
	ing := &testClipIngester{
		assets: map[string]testFetchedAsset{
			"yt://nature": {"nature-yt", 60000},
		},
	}
	resolver := newTestResolver(local, reg, ing)

	result, err := resolver.Resolve(context.Background(), stocksupply.SupplyQuery{
		Queries:       []string{"nature"},
		ReuseExisting: true,
		Strategy:      stocksupply.StrategyArtlistFirst,
		Target: stocksupply.SupplyTarget{
			TargetDurationSec: 60,
			MinimumReadySec:   30,
		},
	})
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	// artlist search succeeded but fetch was missing → fell through to youtube
	// which acquired 60s → total 60s ≥ 60s target → READY.
	if result.State != stocksupply.StateReady {
		t.Fatalf("expected READY via youtube fallback, got %s", result.State)
	}
	if result.NewAssets != 1 {
		t.Fatalf("expected 1 new asset, got %d", result.NewAssets)
	}
	qr := result.Queries[0]
	if qr.ProviderUsed != "youtube" {
		t.Fatalf("expected provider_used=youtube, got %q", qr.ProviderUsed)
	}
	if !strings.Contains(qr.FallbackReason, "artlist") {
		t.Fatalf("expected fallback reason to reference artlist, got %q", qr.FallbackReason)
	}
}

func TestResolver_ValidateQuery_BadMode(t *testing.T) {
	resolver := newTestResolver(&testLocalSearcher{}, &testProviderRegistry{}, &testClipIngester{})
	_, err := resolver.Resolve(context.Background(), stocksupply.SupplyQuery{
		Queries: []string{"x"},
		Mode:    "invalid_mode",
	})
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), "unsupported mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolver_ValidateQuery_BadStrategy(t *testing.T) {
	resolver := newTestResolver(&testLocalSearcher{}, &testProviderRegistry{}, &testClipIngester{})
	_, err := resolver.Resolve(context.Background(), stocksupply.SupplyQuery{
		Queries:  []string{"x"},
		Strategy: "bad_strategy",
	})
	if err == nil {
		t.Fatal("expected error for invalid strategy")
	}
	if !strings.Contains(err.Error(), "unsupported strategy") {
		t.Fatalf("unexpected error: %v", err)
	}
}
