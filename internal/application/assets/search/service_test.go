package search

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// ── Mock port implementations ──────────────────────────────────────────

type mockSearchProvider struct {
	nameFn         func() string
	capabilitiesFn func() []string
	searchFn       func(ctx context.Context, req SearchRequest) (*SearchResult, error)
}

func (m *mockSearchProvider) Name() string {
	if m.nameFn != nil {
		return m.nameFn()
	}
	return "mock-provider"
}
func (m *mockSearchProvider) Capabilities() []string {
	if m.capabilitiesFn != nil {
		return m.capabilitiesFn()
	}
	return []string{"video"}
}
func (m *mockSearchProvider) Search(ctx context.Context, req SearchRequest) (*SearchResult, error) {
	if m.searchFn != nil {
		return m.searchFn(ctx, req)
	}
	return &SearchResult{}, nil
}

type mockProviderRegistry struct {
	providers []SearchProviderPort
}

func (m *mockProviderRegistry) SearchProviders() []SearchProviderPort {
	return m.providers
}

type mockCatalogPort struct {
	searchAllFn func(ctx context.Context, query string) ([]CatalogSearchResult, error)
}

func (m *mockCatalogPort) SearchAll(ctx context.Context, query string) ([]CatalogSearchResult, error) {
	if m.searchAllFn != nil {
		return m.searchAllFn(ctx, query)
	}
	return nil, nil
}

type mockClipPort struct {
	searchClipsFn func(ctx context.Context, source, query string) ([]LocalClipResult, error)
}

func (m *mockClipPort) SearchClips(ctx context.Context, source, query string) ([]LocalClipResult, error) {
	if m.searchClipsFn != nil {
		return m.searchClipsFn(ctx, source, query)
	}
	return nil, nil
}

type mockConfigPort struct {
	vc VectorConfig
}

func (m *mockConfigPort) VectorConfig() VectorConfig { return m.vc }

type testLogger struct{}

func (l *testLogger) Info(msg string, kv ...any)  {}
func (l *testLogger) Warn(msg string, kv ...any)  {}
func (l *testLogger) Error(msg string, kv ...any) {}
func (l *testLogger) Debug(msg string, kv ...any) {}

// ── Search tests ───────────────────────────────────────────────────────

func TestSearch_HappyPath(t *testing.T) {
	ctx := context.Background()
	reg := &mockProviderRegistry{
		providers: []SearchProviderPort{
			&mockSearchProvider{
				nameFn: func() string { return "youtube" },
				searchFn: func(ctx context.Context, req SearchRequest) (*SearchResult, error) {
					return &SearchResult{
						Candidates: []SearchCandidate{
							{Title: "Clip A", Score: 0.95},
						},
					}, nil
				},
			},
		},
	}
	cat := &mockCatalogPort{
		searchAllFn: func(ctx context.Context, query string) ([]CatalogSearchResult, error) {
			return []CatalogSearchResult{{ID: "cat1", Name: "Catalog Item", Type: "video"}}, nil
		},
	}
	clips := &mockClipPort{
		searchClipsFn: func(ctx context.Context, source, query string) ([]LocalClipResult, error) {
			return []LocalClipResult{{ID: "clip1", Name: "Local Clip"}}, nil
		},
	}
	svc := NewService(reg, cat, clips, &mockConfigPort{}, &testLogger{})

	result, err := svc.Search(ctx, SearchRequest{Query: "test", Limit: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := result.Results["youtube"]; !ok {
		t.Error("expected youtube provider in results")
	}
	if _, ok := result.Results["catalog"]; !ok {
		t.Error("expected catalog results")
	}
	if _, ok := result.Results["local"]; !ok {
		t.Error("expected local clip results")
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	ctx := context.Background()
	svc := NewService(nil, nil, nil, &mockConfigPort{}, &testLogger{})

	_, err := svc.Search(ctx, SearchRequest{Query: ""})
	if err == nil {
		t.Fatal("expected error for empty query")
	}
	if err.Error() != "query is required" {
		t.Errorf("got %q, want %q", err.Error(), "query is required")
	}
}

func TestSearch_AllNilPorts(t *testing.T) {
	ctx := context.Background()
	svc := NewService(nil, nil, nil, &mockConfigPort{}, &testLogger{})

	result, err := svc.Search(ctx, SearchRequest{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Results) != 0 {
		t.Errorf("expected empty results with all nil ports, got %d entries", len(result.Results))
	}
}

func TestSearch_ProviderError(t *testing.T) {
	ctx := context.Background()
	reg := &mockProviderRegistry{
		providers: []SearchProviderPort{
			&mockSearchProvider{
				nameFn: func() string { return "broken" },
				searchFn: func(ctx context.Context, req SearchRequest) (*SearchResult, error) {
					return nil, errors.New("crash")
				},
			},
		},
	}
	svc := NewService(reg, nil, nil, &mockConfigPort{}, &testLogger{})

	result, err := svc.Search(ctx, SearchRequest{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	broken := result.Results["broken"]
	if broken.Count != 0 {
		t.Error("expected 0 results from broken provider")
	}
	if broken.Error == "" {
		t.Error("expected error field for broken provider")
	}
}

func TestSearch_CatalogError(t *testing.T) {
	ctx := context.Background()
	cat := &mockCatalogPort{
		searchAllFn: func(ctx context.Context, query string) ([]CatalogSearchResult, error) {
			return nil, errors.New("db down")
		},
	}
	svc := NewService(nil, cat, nil, &mockConfigPort{}, &testLogger{})

	result, err := svc.Search(ctx, SearchRequest{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Catalog errors are logged but not surfaced — results map should be empty
	if len(result.Results) != 0 {
		t.Errorf("expected empty results when catalog fails, got %d entries", len(result.Results))
	}
}

func TestSearch_LimitDefaultsTo20(t *testing.T) {
	ctx := context.Background()
	reg := &mockProviderRegistry{
		providers: []SearchProviderPort{
			&mockSearchProvider{
				searchFn: func(ctx context.Context, req SearchRequest) (*SearchResult, error) {
					if req.Limit != 20 {
						return nil, fmt.Errorf("expected limit 20, got %d", req.Limit)
					}
					return &SearchResult{}, nil
				},
			},
		},
	}
	svc := NewService(reg, nil, nil, &mockConfigPort{}, &testLogger{})

	_, err := svc.Search(ctx, SearchRequest{Query: "test", Limit: 0})
	if err != nil {
		t.Fatalf("limit defaulting failed: %v", err)
	}
}

func TestSearch_MediaTypeFiltering(t *testing.T) {
	ctx := context.Background()
	reg := &mockProviderRegistry{
		providers: []SearchProviderPort{
			&mockSearchProvider{
				nameFn:         func() string { return "video-only" },
				capabilitiesFn: func() []string { return []string{"video"} },
				searchFn: func(ctx context.Context, req SearchRequest) (*SearchResult, error) {
					return &SearchResult{Candidates: []SearchCandidate{{Title: "V"}}}, nil
				},
			},
			&mockSearchProvider{
				nameFn:         func() string { return "audio-only" },
				capabilitiesFn: func() []string { return []string{"music"} },
				searchFn: func(ctx context.Context, req SearchRequest) (*SearchResult, error) {
					return &SearchResult{Candidates: []SearchCandidate{{Title: "A"}}}, nil
				},
			},
		},
	}
	svc := NewService(reg, nil, nil, &mockConfigPort{}, &testLogger{})

	// Request audio — video-only provider should be excluded
	result, err := svc.Search(ctx, SearchRequest{Query: "test", MediaType: "audio"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := result.Results["video-only"]; ok {
		t.Error("video-only provider should be excluded for audio request")
	}
	if _, ok := result.Results["audio-only"]; !ok {
		t.Error("audio-only provider should be included for audio request")
	}
}

func TestSearch_ClipsNotSearchedForNonVideo(t *testing.T) {
	ctx := context.Background()
	// When MediaType is "image", clips should NOT be searched.
	var clipsCalled bool
	clips := &mockClipPort{
		searchClipsFn: func(ctx context.Context, source, query string) ([]LocalClipResult, error) {
			clipsCalled = true
			return nil, nil
		},
	}
	svc := NewService(nil, nil, clips, &mockConfigPort{}, &testLogger{})

	_, err := svc.Search(ctx, SearchRequest{Query: "test", MediaType: "image"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clipsCalled {
		t.Error("clips should NOT be searched for MediaType=image")
	}
}
