package diagnostics

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

	appdiag "github.com/Marcuss-ops/PipelineGen/internal/application/assets/diagnostics"
)

// mockDiagService implements diagnostics.Service-like behavior for handler tests.
// We wrap a real appdiag.Service created with mock ports.
type mockIndexHealth struct {
	checkFn func(ctx context.Context) (*appdiag.IndexHealthReport, error)
	vsFn    func() appdiag.VectorStorePort
}

func (m *mockIndexHealth) IndexHealth(ctx context.Context) (*appdiag.IndexHealthReport, error) {
	return m.checkFn(ctx)
}
func (m *mockIndexHealth) VectorStore() appdiag.VectorStorePort {
	if m.vsFn != nil {
		return m.vsFn()
	}
	return nil
}

type mockAssetStats struct {
	statsFn func(ctx context.Context) (*appdiag.AssetStats, error)
}

func (m *mockAssetStats) GetStats(ctx context.Context) (*appdiag.AssetStats, error) {
	return m.statsFn(ctx)
}

type testLogger struct{ zap *zap.Logger }

func (l *testLogger) Info(msg string, kv ...any)  {}
func (l *testLogger) Warn(msg string, kv ...any)  {}
func (l *testLogger) Error(msg string, kv ...any) {}
func (l *testLogger) Debug(msg string, kv ...any) {}

// newTestDiagService creates a diagnostics.Service backed by mock ports.
func newTestDiagService(ih appdiag.IndexHealthPort, as appdiag.AssetStatsPort) *appdiag.Service {
	return appdiag.NewService(ih, as, &testLogger{zap: zap.NewNop()})
}

func setupDiagRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	rg := r.Group("/media")
	h.RegisterRoutes(rg)
	return r
}

func doDiagJSON(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
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

// ── Diagnostics ───────────────────────────────────────────────────────

func TestDiagnostics_HappyPath(t *testing.T) {
	ih := &mockIndexHealth{
		checkFn: func(ctx context.Context) (*appdiag.IndexHealthReport, error) {
			return &appdiag.IndexHealthReport{
				OK: true, SQLiteAssets: 150, QdrantPoints: 150,
			}, nil
		},
	}
	as := &mockAssetStats{
		statsFn: func(ctx context.Context) (*appdiag.AssetStats, error) {
			return &appdiag.AssetStats{Total: 200, ByType: map[string]int{"video": 150}}, nil
		},
	}
	svc := newTestDiagService(ih, as)
	handler := NewHandler(svc, zap.NewNop())
	r := setupDiagRouter(handler)

	w := doDiagJSON(t, r, "GET", "/media/diagnostics", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["ok"].(bool))
	checks := resp["checks"].(map[string]any)
	assert.NotNil(t, checks["index_health"])
	assert.NotNil(t, checks["asset_stats"])
}

func TestDiagnostics_ServiceError(t *testing.T) {
	ih := &mockIndexHealth{
		checkFn: func(ctx context.Context) (*appdiag.IndexHealthReport, error) {
			return nil, errors.New("qdrant unreachable")
		},
	}
	svc := newTestDiagService(ih, nil)
	handler := NewHandler(svc, zap.NewNop())
	r := setupDiagRouter(handler)

	w := doDiagJSON(t, r, "GET", "/media/diagnostics", nil)
	assert.Equal(t, http.StatusOK, w.Code) // degraded but still OK
}

func TestDiagnostics_NilService(t *testing.T) {
	handler := NewHandler(nil, zap.NewNop())
	r := setupDiagRouter(handler)

	w := doDiagJSON(t, r, "GET", "/media/diagnostics", nil)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// ── IndexHealth ───────────────────────────────────────────────────────

func TestIndexHealth_HappyPath(t *testing.T) {
	ih := &mockIndexHealth{
		checkFn: func(ctx context.Context) (*appdiag.IndexHealthReport, error) {
			return &appdiag.IndexHealthReport{
				OK: true, SQLiteAssets: 100, SQLiteIndexed: 95,
				QdrantPoints: 95, MissingInQdrant: 5,
			}, nil
		},
	}
	svc := newTestDiagService(ih, nil)
	handler := NewHandler(svc, zap.NewNop())
	r := setupDiagRouter(handler)

	w := doDiagJSON(t, r, "GET", "/media/index-health", nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestIndexHealth_NilService(t *testing.T) {
	handler := NewHandler(nil, zap.NewNop())
	r := setupDiagRouter(handler)

	w := doDiagJSON(t, r, "GET", "/media/index-health", nil)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// ── QdrantHealth ──────────────────────────────────────────────────────

func TestQdrantHealth_HappyPath(t *testing.T) {
	ih := &mockIndexHealth{
		checkFn: func(ctx context.Context) (*appdiag.IndexHealthReport, error) {
			return &appdiag.IndexHealthReport{OK: true}, nil
		},
	}
	svc := newTestDiagService(ih, nil)
	handler := NewHandler(svc, zap.NewNop())
	r := setupDiagRouter(handler)

	w := doDiagJSON(t, r, "GET", "/media/qdrant/health", nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestQdrantHealth_NilService(t *testing.T) {
	handler := NewHandler(nil, zap.NewNop())
	r := setupDiagRouter(handler)

	w := doDiagJSON(t, r, "GET", "/media/qdrant/health", nil)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// ── QdrantCleanup ─────────────────────────────────────────────────────

func TestQdrantCleanup_AlwaysOK(t *testing.T) {
	// Cleanup is a no-op placeholder that always returns 200.
	handler := NewHandler(nil, zap.NewNop())
	r := setupDiagRouter(handler)

	w := doDiagJSON(t, r, "POST", "/media/qdrant/cleanup", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["ok"].(bool))
	assert.Contains(t, resp["message"], "Qdrant stale-link cleaner")
}
