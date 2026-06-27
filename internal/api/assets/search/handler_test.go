package search

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
)

// mockSearchService wraps appsearch.Service-like behavior for handler tests.
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
	cat appsearch.LocalCatalogPort,
	clips appsearch.LocalClipPort,
	cfg appsearch.ConfigPort,
) *appsearch.Service {
	return appsearch.NewService(reg, cat, clips, cfg, &testLogger{zap: zap.NewNop()})
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
	svc := newTestSearchService(reg, nil, nil, &mockConfigPort{
		vc: appsearch.VectorConfig{TextVectorName: "text", SparseVectorName: "bm25_text"},
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
	svc := newTestSearchService(&mockProviderRegistry{}, nil, nil, &mockConfigPort{})
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
	svc := newTestSearchService(reg, nil, nil, &mockConfigPort{})
	handler := NewHandler(svc, zap.NewNop())
	r := setupSearchRouter(handler)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/media/search?q=test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code) // provider errors are logged, not surfaced
}
