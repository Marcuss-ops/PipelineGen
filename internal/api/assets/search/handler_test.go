package search

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
)

// mockSearchService wraps appsearch.Service-like behavior for handler tests.
// We create a real appsearch.Service with mock ports.
type mockSearchProvider struct {
	nameFn         func() string
	capabilitiesFn func() []string
	searchFn       func(ctx context.Context, req appsearch.SearchRequest) (*appsearch.SearchResult, error)
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
func (m *mockSearchProvider) Search(ctx context.Context, req appsearch.SearchRequest) (*appsearch.SearchResult, error) {
	return m.searchFn(ctx, req)
}

type mockProviderRegistry struct {
	providers []appsearch.SearchProviderPort
}

func (m *mockProviderRegistry) SearchProviders() []appsearch.SearchProviderPort {
	return m.providers
}

type mockVectorSearch struct {
	embedFn func(ctx context.Context, text, vectorName string) ([]float32, error)
	vsFn    func() appsearch.VectorStorePort
}

func (m *mockVectorSearch) EmbedTextForVector(ctx context.Context, text, vectorName string) ([]float32, error) {
	return m.embedFn(ctx, text, vectorName)
}
func (m *mockVectorSearch) VectorStore() appsearch.VectorStorePort {
	if m.vsFn != nil {
		return m.vsFn()
	}
	return nil
}

type mockVectorStore struct {
	searchFn       func(ctx context.Context, req appsearch.VectorSearchRequest) ([]appsearch.VectorSearchResult, error)
	hybridSearchFn func(ctx context.Context, req appsearch.HybridSearchRequest) ([]appsearch.VectorSearchResult, error)
}

func (m *mockVectorStore) Search(ctx context.Context, req appsearch.VectorSearchRequest) ([]appsearch.VectorSearchResult, error) {
	return m.searchFn(ctx, req)
}
func (m *mockVectorStore) HybridSearch(ctx context.Context, req appsearch.HybridSearchRequest) ([]appsearch.VectorSearchResult, error) {
	return m.hybridSearchFn(ctx, req)
}

type mockCatalogPort struct {
	searchAllFn func(ctx context.Context, query string) ([]appsearch.CatalogSearchResult, error)
}

func (m *mockCatalogPort) SearchAll(ctx context.Context, query string) ([]appsearch.CatalogSearchResult, error) {
	return m.searchAllFn(ctx, query)
}

type mockClipPort struct {
	searchClipsFn func(ctx context.Context, source, query string) ([]appsearch.LocalClipResult, error)
}

func (m *mockClipPort) SearchClips(ctx context.Context, source, query string) ([]appsearch.LocalClipResult, error) {
	return m.searchClipsFn(ctx, source, query)
}

type mockConfigPort struct{ vc appsearch.VectorConfig }

func (m *mockConfigPort) VectorConfig() appsearch.VectorConfig { return m.vc }

type testLogger struct{ zap *zap.Logger }

func (l *testLogger) Info(msg string, kv ...any)  {}
func (l *testLogger) Warn(msg string, kv ...any)  {}
func (l *testLogger) Error(msg string, kv ...any) {}
func (l *testLogger) Debug(msg string, kv ...any) {}

func newTestSearchService(
	reg appsearch.SearchProviderRegistry,
	vec appsearch.VectorSearchPort,
	cat appsearch.LocalCatalogPort,
	clips appsearch.LocalClipPort,
	cfg appsearch.ConfigPort,
) *appsearch.Service {
	return appsearch.NewService(reg, vec, cat, clips, cfg, &testLogger{zap: zap.NewNop()})
}

func setupSearchRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	rg := r.Group("/media")
	h.RegisterRoutes(rg)
	return r
}

// ── Search (GET /search) ─────────────────────────────────────────────

func TestSearch_HappyPath(t *testing.T) {
	reg := &mockProviderRegistry{
		providers: []appsearch.SearchProviderPort{
			&mockSearchProvider{
				nameFn: func() string { return "youtube" },
				searchFn: func(ctx context.Context, req appsearch.SearchRequest) (*appsearch.SearchResult, error) {
					return &appsearch.SearchResult{
						Candidates: []appsearch.SearchCandidate{
							{Title: "Test Clip", Score: 0.95},
						},
					}, nil
				},
			},
		},
	}
	svc := newTestSearchService(reg, nil, nil, nil, &mockConfigPort{
		vc: appsearch.VectorConfig{TextVectorName: "text"},
	})
	handler := NewHandler(svc, zap.NewNop())
	r := setupSearchRouter(handler)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/media/search?q=test+query&type=video", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotNil(t, resp["results"])
}

func TestSearch_MissingQuery(t *testing.T) {
	svc := newTestSearchService(&mockProviderRegistry{}, nil, nil, nil, &mockConfigPort{})
	handler := NewHandler(svc, zap.NewNop())
	r := setupSearchRouter(handler)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/media/search", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSearch_NilService(t *testing.T) {
	handler := NewHandler(nil, zap.NewNop())
	r := setupSearchRouter(handler)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/media/search?q=test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestSearch_ServiceError(t *testing.T) {
	reg := &mockProviderRegistry{
		providers: []appsearch.SearchProviderPort{
			&mockSearchProvider{
				searchFn: func(ctx context.Context, req appsearch.SearchRequest) (*appsearch.SearchResult, error) {
					return nil, errors.New("provider crash")
				},
			},
		},
	}
	svc := newTestSearchService(reg, nil, nil, nil, &mockConfigPort{})
	handler := NewHandler(svc, zap.NewNop())
	r := setupSearchRouter(handler)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/media/search?q=test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code) // provider errors are logged, not surfaced
}

// ── SemanticSearch (GET /semantic-search) ────────────────────────────

func TestSemanticSearch_HappyPath(t *testing.T) {
	vs := &mockVectorStore{
		searchFn: func(ctx context.Context, req appsearch.VectorSearchRequest) ([]appsearch.VectorSearchResult, error) {
			return []appsearch.VectorSearchResult{
				{AssetID: "clip1", Name: "Space clip", Score: 0.88},
			}, nil
		},
	}
	vec := &mockVectorSearch{
		embedFn: func(ctx context.Context, text, vectorName string) ([]float32, error) {
			return []float32{0.1, 0.2, 0.3}, nil
		},
		vsFn: func() appsearch.VectorStorePort { return vs },
	}
	svc := newTestSearchService(nil, vec, nil, nil, &mockConfigPort{
		vc: appsearch.VectorConfig{
			TextVectorName:  "text",
			MinInstantScore: 0.5,
		},
	})
	handler := NewHandler(svc, zap.NewNop())
	r := setupSearchRouter(handler)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/media/semantic-search?q=space&vector=text", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(1), resp["count"])
	results := resp["results"].([]any)
	assert.Equal(t, "clip1", results[0].(map[string]any)["asset_id"])
}

func TestSemanticSearch_MissingQuery(t *testing.T) {
	svc := newTestSearchService(nil, nil, nil, nil, &mockConfigPort{})
	handler := NewHandler(svc, zap.NewNop())
	r := setupSearchRouter(handler)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/media/semantic-search", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSemanticSearch_NilService(t *testing.T) {
	handler := NewHandler(nil, zap.NewNop())
	r := setupSearchRouter(handler)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/media/semantic-search?q=test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// ── Recommend (POST /recommend) ──────────────────────────────────────

func TestRecommend_HappyPath(t *testing.T) {
	vs := &mockVectorStore{
		hybridSearchFn: func(ctx context.Context, req appsearch.HybridSearchRequest) ([]appsearch.VectorSearchResult, error) {
			return []appsearch.VectorSearchResult{
				{AssetID: "clip_x", Name: "action shot", Score: 0.92, Source: "stock", MediaType: "video"},
			}, nil
		},
	}
	vec := &mockVectorSearch{
		embedFn: func(ctx context.Context, text, vectorName string) ([]float32, error) {
			return []float32{0.1, 0.2}, nil
		},
		vsFn: func() appsearch.VectorStorePort { return vs },
	}
	svc := newTestSearchService(nil, vec, nil, nil, &mockConfigPort{
		vc: appsearch.VectorConfig{
			TextVectorName:       "text",
			TranscriptVectorName: "transcript",
			MinInstantScore:      0.5,
		},
	})
	handler := NewHandler(svc, zap.NewNop())
	r := setupSearchRouter(handler)

	body := map[string]any{
		"script_text": "Scene one. Action sequence.",
	}
	raw, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/media/recommend", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["ok"].(bool))
	assert.Equal(t, float64(1), resp["total_clips"])
}

func TestRecommend_MissingScriptText(t *testing.T) {
	svc := newTestSearchService(nil, nil, nil, nil, &mockConfigPort{})
	handler := NewHandler(svc, zap.NewNop())
	r := setupSearchRouter(handler)

	w := doSearchJSON(t, r, "POST", "/media/recommend", map[string]any{
		"script_text": "",
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRecommend_NilService(t *testing.T) {
	handler := NewHandler(nil, zap.NewNop())
	r := setupSearchRouter(handler)

	w := doSearchJSON(t, r, "POST", "/media/recommend", map[string]any{
		"script_text": "test",
	})
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func doSearchJSON(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	var req *http.Request
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		req = httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	r.ServeHTTP(w, req)
	return w
}
