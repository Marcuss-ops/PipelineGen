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

type mockVectorSearch struct {
	embedFn func(ctx context.Context, text, vectorName string) ([]float32, error)
	vsFn    func() VectorStorePort
}

func (m *mockVectorSearch) EmbedTextForVector(ctx context.Context, text, vectorName string) ([]float32, error) {
	if m.embedFn != nil {
		return m.embedFn(ctx, text, vectorName)
	}
	return []float32{0.1, 0.2}, nil
}
func (m *mockVectorSearch) VectorStore() VectorStorePort {
	if m.vsFn != nil {
		return m.vsFn()
	}
	return nil
}

type mockVectorStore struct {
	searchFn       func(ctx context.Context, req VectorSearchRequest) ([]VectorSearchResult, error)
	hybridSearchFn func(ctx context.Context, req HybridSearchRequest) ([]VectorSearchResult, error)
}

func (m *mockVectorStore) Search(ctx context.Context, req VectorSearchRequest) ([]VectorSearchResult, error) {
	if m.searchFn != nil {
		return m.searchFn(ctx, req)
	}
	return nil, nil
}
func (m *mockVectorStore) HybridSearch(ctx context.Context, req HybridSearchRequest) ([]VectorSearchResult, error) {
	if m.hybridSearchFn != nil {
		return m.hybridSearchFn(ctx, req)
	}
	return nil, nil
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
	svc := NewService(reg, nil, cat, clips, &mockConfigPort{}, &testLogger{})

	result, err := svc.Search(ctx, SearchRequest{Query: "test", Limit: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	results := result["results"].(map[string]any)
	if _, ok := results["youtube"]; !ok {
		t.Error("expected youtube provider in results")
	}
	if _, ok := results["catalog"]; !ok {
		t.Error("expected catalog results")
	}
	if _, ok := results["local"]; !ok {
		t.Error("expected local clip results")
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	ctx := context.Background()
	svc := NewService(nil, nil, nil, nil, &mockConfigPort{}, &testLogger{})

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
	svc := NewService(nil, nil, nil, nil, &mockConfigPort{}, &testLogger{})

	result, err := svc.Search(ctx, SearchRequest{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	results := result["results"].(map[string]any)
	if len(results) != 0 {
		t.Errorf("expected empty results with all nil ports, got %d entries", len(results))
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
	svc := NewService(reg, nil, nil, nil, &mockConfigPort{}, &testLogger{})

	result, err := svc.Search(ctx, SearchRequest{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	results := result["results"].(map[string]any)
	broken := results["broken"].(map[string]any)
	if broken["count"].(int) != 0 {
		t.Error("expected 0 results from broken provider")
	}
	if broken["error"] == nil {
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
	svc := NewService(nil, nil, cat, nil, &mockConfigPort{}, &testLogger{})

	result, err := svc.Search(ctx, SearchRequest{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Catalog errors are logged but not surfaced — results map should be empty
	results := result["results"].(map[string]any)
	if len(results) != 0 {
		t.Errorf("expected empty results when catalog fails, got %d entries", len(results))
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
	svc := NewService(reg, nil, nil, nil, &mockConfigPort{}, &testLogger{})

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
	svc := NewService(reg, nil, nil, nil, &mockConfigPort{}, &testLogger{})

	// Request audio — video-only provider should be excluded
	result, err := svc.Search(ctx, SearchRequest{Query: "test", MediaType: "audio"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	results := result["results"].(map[string]any)
	if _, ok := results["video-only"]; ok {
		t.Error("video-only provider should be excluded for audio request")
	}
	if _, ok := results["audio-only"]; !ok {
		t.Error("audio-only provider should be included for audio request")
	}
}

// ── SemanticSearch tests ──────────────────────────────────────────────

func TestSemanticSearch_HappyPath_ANN(t *testing.T) {
	ctx := context.Background()
	vs := &mockVectorStore{
		searchFn: func(ctx context.Context, req VectorSearchRequest) ([]VectorSearchResult, error) {
			return []VectorSearchResult{
				{AssetID: "a1", Name: "Space clip", Score: 0.88, Source: "stock"},
			}, nil
		},
	}
	vec := &mockVectorSearch{
		vsFn: func() VectorStorePort { return vs },
	}
	svc := NewService(nil, vec, nil, nil, &mockConfigPort{
		vc: VectorConfig{TextVectorName: "text", MinInstantScore: 0.5},
	}, &testLogger{})

	result, err := svc.SemanticSearch(ctx, SemanticSearchRequest{
		Query:      "space",
		VectorName: "text",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Count != 1 {
		t.Errorf("Count = %d, want 1", result.Count)
	}
	if result.Mode != "ann" {
		t.Errorf("Mode = %q, want %q", result.Mode, "ann")
	}
	if len(result.Results) != 1 {
		t.Fatalf("len(Results) = %d, want 1", len(result.Results))
	}
	if result.Results[0].AssetID != "a1" {
		t.Errorf("Results[0].AssetID = %q, want %q", result.Results[0].AssetID, "a1")
	}
	// Reason should be populated
	if result.Results[0].Reason == "" {
		t.Error("expected non-empty Reason on result")
	}
}

func TestSemanticSearch_HappyPath_Hybrid(t *testing.T) {
	ctx := context.Background()
	vs := &mockVectorStore{
		hybridSearchFn: func(ctx context.Context, req HybridSearchRequest) ([]VectorSearchResult, error) {
			return []VectorSearchResult{
				{AssetID: "h1", Name: "Hybrid hit", Score: 0.92},
			}, nil
		},
	}
	vec := &mockVectorSearch{
		vsFn: func() VectorStorePort { return vs },
	}
	svc := NewService(nil, vec, nil, nil, &mockConfigPort{
		vc: VectorConfig{
			TextVectorName:       "text",
			TranscriptVectorName: "transcript",
			MinInstantScore:      0.5,
		},
	}, &testLogger{})

	result, err := svc.SemanticSearch(ctx, SemanticSearchRequest{
		Query:      "space exploration",
		VectorName: "text",
		Mode:       "hybrid",
		Limit:      5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Mode != "hybrid" {
		t.Errorf("Mode = %q, want %q", result.Mode, "hybrid")
	}
	if result.Count != 1 {
		t.Errorf("Count = %d, want 1", result.Count)
	}
}

func TestSemanticSearch_NilVectorPort(t *testing.T) {
	ctx := context.Background()
	svc := NewService(nil, nil, nil, nil, &mockConfigPort{}, &testLogger{})

	_, err := svc.SemanticSearch(ctx, SemanticSearchRequest{Query: "test", VectorName: "text"})
	if err == nil {
		t.Fatal("expected error for nil vector port")
	}
	if err.Error() != "vector search not configured" {
		t.Errorf("got %q, want %q", err.Error(), "vector search not configured")
	}
}

func TestSemanticSearch_EmptyQuery(t *testing.T) {
	ctx := context.Background()
	vec := &mockVectorSearch{}
	svc := NewService(nil, vec, nil, nil, &mockConfigPort{
		vc: VectorConfig{TextVectorName: "text"},
	}, &testLogger{})

	_, err := svc.SemanticSearch(ctx, SemanticSearchRequest{Query: "", VectorName: "text"})
	if err == nil {
		t.Fatal("expected error for empty query")
	}
	if err.Error() != "query is required" {
		t.Errorf("got %q, want %q", err.Error(), "query is required")
	}
}

func TestSemanticSearch_InvalidVectorName(t *testing.T) {
	ctx := context.Background()
	vec := &mockVectorSearch{}
	svc := NewService(nil, vec, nil, nil, &mockConfigPort{
		vc: VectorConfig{TextVectorName: "text"},
	}, &testLogger{})

	_, err := svc.SemanticSearch(ctx, SemanticSearchRequest{Query: "test", VectorName: "unknown"})
	if err == nil {
		t.Fatal("expected error for invalid vector name")
	}
}

func TestSemanticSearch_EmbedError(t *testing.T) {
	ctx := context.Background()
	vec := &mockVectorSearch{
		embedFn: func(ctx context.Context, text, vectorName string) ([]float32, error) {
			return nil, errors.New("ollama down")
		},
	}
	svc := NewService(nil, vec, nil, nil, &mockConfigPort{
		vc: VectorConfig{TextVectorName: "text"},
	}, &testLogger{})

	_, err := svc.SemanticSearch(ctx, SemanticSearchRequest{Query: "test", VectorName: "text"})
	if err == nil {
		t.Fatal("expected error for embed failure")
	}
}

func TestSemanticSearch_SearchError(t *testing.T) {
	ctx := context.Background()
	vs := &mockVectorStore{
		searchFn: func(ctx context.Context, req VectorSearchRequest) ([]VectorSearchResult, error) {
			return nil, errors.New("qdrant timeout")
		},
	}
	vec := &mockVectorSearch{
		vsFn: func() VectorStorePort { return vs },
	}
	svc := NewService(nil, vec, nil, nil, &mockConfigPort{
		vc: VectorConfig{TextVectorName: "text"},
	}, &testLogger{})

	_, err := svc.SemanticSearch(ctx, SemanticSearchRequest{Query: "test", VectorName: "text"})
	if err == nil {
		t.Fatal("expected error for qdrant search failure")
	}
}

func TestSemanticSearch_MinScoreFromConfig(t *testing.T) {
	ctx := context.Background()
	vs := &mockVectorStore{
		searchFn: func(ctx context.Context, req VectorSearchRequest) ([]VectorSearchResult, error) {
			if req.MinScore != 0.7 {
				t.Errorf("MinScore = %f, want 0.7 (from config)", req.MinScore)
			}
			return nil, nil
		},
	}
	vec := &mockVectorSearch{
		vsFn: func() VectorStorePort { return vs },
	}
	svc := NewService(nil, vec, nil, nil, &mockConfigPort{
		vc: VectorConfig{TextVectorName: "text", MinInstantScore: 0.7},
	}, &testLogger{})

	_, err := svc.SemanticSearch(ctx, SemanticSearchRequest{
		Query:      "test",
		VectorName: "text",
		MinScore:   0, // should fall back to config
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── Recommend tests ────────────────────────────────────────────────────

func TestRecommend_HappyPath(t *testing.T) {
	ctx := context.Background()
	vs := &mockVectorStore{
		hybridSearchFn: func(ctx context.Context, req HybridSearchRequest) ([]VectorSearchResult, error) {
			return []VectorSearchResult{
				{AssetID: "clip_a", Name: "action shot", Score: 0.92, Source: "stock", MediaType: "video"},
				{AssetID: "clip_b", Name: "wide landscape", Score: 0.78, Source: "artlist", MediaType: "video"},
			}, nil
		},
	}
	vec := &mockVectorSearch{
		vsFn: func() VectorStorePort { return vs },
	}
	svc := NewService(nil, vec, nil, nil, &mockConfigPort{
		vc: VectorConfig{
			TextVectorName:       "text",
			TranscriptVectorName: "transcript",
			MinInstantScore:      0.4,
		},
	}, &testLogger{})

	result, err := svc.Recommend(ctx, RecommendRequest{
		ScriptText: "Scene one. Action sequence with explosions.\n\nScene two. Wide landscape shots of the countryside.",
		TopK:       2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SceneCount != 2 {
		t.Errorf("SceneCount = %d, want 2", result.SceneCount)
	}
	if result.TotalClips < 1 {
		t.Error("expected at least 1 recommended clip")
	}
	if len(result.Scenes) != 2 {
		t.Errorf("len(Scenes) = %d, want 2", len(result.Scenes))
	}
}

func TestRecommend_NilVectorPort(t *testing.T) {
	ctx := context.Background()
	svc := NewService(nil, nil, nil, nil, &mockConfigPort{}, &testLogger{})

	_, err := svc.Recommend(ctx, RecommendRequest{ScriptText: "test"})
	if err == nil {
		t.Fatal("expected error for nil vector port")
	}
	if err.Error() != "vector search not configured" {
		t.Errorf("got %q, want %q", err.Error(), "vector search not configured")
	}
}

func TestRecommend_EmptyScript(t *testing.T) {
	ctx := context.Background()
	vec := &mockVectorSearch{}
	svc := NewService(nil, vec, nil, nil, &mockConfigPort{
		vc: VectorConfig{TextVectorName: "text", TranscriptVectorName: "transcript"},
	}, &testLogger{})

	_, err := svc.Recommend(ctx, RecommendRequest{ScriptText: ""})
	if err == nil {
		t.Fatal("expected error for empty script")
	}
	if err.Error() != "script_text is required" {
		t.Errorf("got %q, want %q", err.Error(), "script_text is required")
	}
}

func TestRecommend_TopKDefaults(t *testing.T) {
	ctx := context.Background()
	callCount := 0
	vs := &mockVectorStore{
		hybridSearchFn: func(ctx context.Context, req HybridSearchRequest) ([]VectorSearchResult, error) {
			callCount++
			// TopK=0 should default to 5, so Limit should be 10 (TopK*2)
			return []VectorSearchResult{
				{AssetID: fmt.Sprintf("clip_%d", callCount), Score: 0.9, Name: "OK"},
			}, nil
		},
	}
	vec := &mockVectorSearch{
		vsFn: func() VectorStorePort { return vs },
	}
	svc := NewService(nil, vec, nil, nil, &mockConfigPort{
		vc: VectorConfig{
			TextVectorName:       "text",
			TranscriptVectorName: "transcript",
			MinInstantScore:      0.5,
		},
	}, &testLogger{})

	result, err := svc.Recommend(ctx, RecommendRequest{
		ScriptText: "Scene one. A single scene here.",
		TopK:       0, // should default to 5
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalClips == 0 {
		t.Error("expected clips when TopK defaults")
	}
}

func TestRecommend_EmbedFailureSkipsScene(t *testing.T) {
	ctx := context.Background()
	vs := &mockVectorStore{
		hybridSearchFn: func(ctx context.Context, req HybridSearchRequest) ([]VectorSearchResult, error) {
			return []VectorSearchResult{
				{AssetID: "c1", Score: 0.85, Name: "OK"},
			}, nil
		},
	}
	failCount := 0
	vec := &mockVectorSearch{
		embedFn: func(ctx context.Context, text, vectorName string) ([]float32, error) {
			failCount++
			if failCount == 1 {
				return nil, errors.New("embed down")
			}
			return []float32{0.1, 0.2}, nil
		},
		vsFn: func() VectorStorePort { return vs },
	}
	svc := NewService(nil, vec, nil, nil, &mockConfigPort{
		vc: VectorConfig{
			TextVectorName:       "text",
			TranscriptVectorName: "transcript",
			MinInstantScore:      0.5,
		},
	}, &testLogger{})

	result, err := svc.Recommend(ctx, RecommendRequest{
		ScriptText: "Scene one. First scene with action.\n\nScene two. Second scene with drama and tension here.",
		TopK:       2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Scene 1 embed should have failed and been skipped; scene 2 should have results
	// The failed scene should not appear in the results
	if result.TotalClips == 0 {
		t.Error("expected at least 1 clip from the non-failing scene")
	}
}

func TestRecommend_SearchFailureSkipsScene(t *testing.T) {
	ctx := context.Background()
	callCount := 0
	vs := &mockVectorStore{
		hybridSearchFn: func(ctx context.Context, req HybridSearchRequest) ([]VectorSearchResult, error) {
			callCount++
			if callCount == 1 {
				return nil, errors.New("qdrant timeout")
			}
			return []VectorSearchResult{
				{AssetID: "c2", Score: 0.88, Name: "Recovered"},
			}, nil
		},
	}
	vec := &mockVectorSearch{
		vsFn: func() VectorStorePort { return vs },
	}
	svc := NewService(nil, vec, nil, nil, &mockConfigPort{
		vc: VectorConfig{
			TextVectorName:       "text",
			TranscriptVectorName: "transcript",
			MinInstantScore:      0.5,
		},
	}, &testLogger{})

	result, err := svc.Recommend(ctx, RecommendRequest{
		ScriptText: "Scene one. First scene with car chase.\n\nScene two. Second scene with dialogue.",
		TopK:       2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalClips == 0 {
		t.Error("expected clips from the non-failing scene")
	}
}

func TestRecommend_DedupAcrossScenes(t *testing.T) {
	ctx := context.Background()
	vs := &mockVectorStore{
		hybridSearchFn: func(ctx context.Context, req HybridSearchRequest) ([]VectorSearchResult, error) {
			// Same asset returned for every scene — should be deduplicated
			return []VectorSearchResult{
				{AssetID: "dup1", Score: 0.95, Name: "Duplicate Clip", Source: "stock"},
			}, nil
		},
	}
	vec := &mockVectorSearch{
		vsFn: func() VectorStorePort { return vs },
	}
	svc := NewService(nil, vec, nil, nil, &mockConfigPort{
		vc: VectorConfig{
			TextVectorName:       "text",
			TranscriptVectorName: "transcript",
			MinInstantScore:      0.5,
		},
	}, &testLogger{})

	result, err := svc.Recommend(ctx, RecommendRequest{
		ScriptText: "Scene one. Forest scene with animals.\n\nScene two. Ocean scene with waves.",
		TopK:       3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalClips != 1 {
		t.Errorf("TotalClips = %d, want 1 (dedup across scenes)", result.TotalClips)
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
	svc := NewService(nil, nil, nil, clips, &mockConfigPort{}, &testLogger{})

	_, err := svc.Search(ctx, SearchRequest{Query: "test", MediaType: "image"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clipsCalled {
		t.Error("clips should NOT be searched for MediaType=image")
	}
}

// ── SemanticSearch edge cases ──────────────────────────────────────────

func TestSemanticSearch_EmptyResults(t *testing.T) {
	ctx := context.Background()
	vs := &mockVectorStore{
		searchFn: func(ctx context.Context, req VectorSearchRequest) ([]VectorSearchResult, error) {
			return []VectorSearchResult{}, nil
		},
	}
	vec := &mockVectorSearch{
		vsFn: func() VectorStorePort { return vs },
	}
	svc := NewService(nil, vec, nil, nil, &mockConfigPort{
		vc: VectorConfig{TextVectorName: "text"},
	}, &testLogger{})

	result, err := svc.SemanticSearch(ctx, SemanticSearchRequest{
		Query:      "no matches",
		VectorName: "text",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Count != 0 {
		t.Errorf("Count = %d, want 0", result.Count)
	}
	if len(result.Results) != 0 {
		t.Errorf("len(Results) = %d, want 0", len(result.Results))
	}
}

func TestSemanticSearch_NilVectorStoreFromPort(t *testing.T) {
	ctx := context.Background()
	// VectorSearchPort is non-nil but VectorStore() returns nil — this
	// should fail loudly with a regular error instead of panicking.
	vec := &mockVectorSearch{
		vsFn: func() VectorStorePort { return nil },
	}
	svc := NewService(nil, vec, nil, nil, &mockConfigPort{
		vc: VectorConfig{TextVectorName: "text"},
	}, &testLogger{})

	_, err := svc.SemanticSearch(ctx, SemanticSearchRequest{Query: "test", VectorName: "text"})
	if err == nil {
		t.Fatal("expected error when VectorStore() returns nil")
	}
	if err.Error() != "vector search not configured" {
		t.Errorf("got %q, want %q", err.Error(), "vector search not configured")
	}
}

func TestRecommend_MinScoreFallback(t *testing.T) {
	ctx := context.Background()
	vs := &mockVectorStore{
		hybridSearchFn: func(ctx context.Context, req HybridSearchRequest) ([]VectorSearchResult, error) {
			if req.MinScore != 0.5 {
				t.Errorf("MinScore = %f, want 0.5 (hardcoded fallback when config has 0)", req.MinScore)
			}
			return nil, nil
		},
	}
	vec := &mockVectorSearch{
		vsFn: func() VectorStorePort { return vs },
	}
	// Config has MinInstantScore=0, so the hardcoded fallback of 0.5 should kick in
	svc := NewService(nil, vec, nil, nil, &mockConfigPort{
		vc: VectorConfig{
			TextVectorName:       "text",
			TranscriptVectorName: "transcript",
			MinInstantScore:      0,
		},
	}, &testLogger{})

	_, err := svc.Recommend(ctx, RecommendRequest{
		ScriptText: "Scene one. A test.",
		TopK:       2,
		MinScore:   0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
