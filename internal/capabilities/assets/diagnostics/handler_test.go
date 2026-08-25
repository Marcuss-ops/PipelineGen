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
)

// mockDiagService implements diagnostics.Service-like behavior for handler tests.
// We wrap a real Service created with mock ports.
// VectorStorePort + vsFn removed from this test helper surface.
type mockIndexHealth struct {
	checkFn func(ctx context.Context) (*IndexHealthReport, error)
}

func (m *mockIndexHealth) IndexHealth(ctx context.Context) (*IndexHealthReport, error) {
	return m.checkFn(ctx)
}

type mockAssetStats struct {
	statsFn func(ctx context.Context) (*AssetStats, error)
}

func (m *mockAssetStats) GetStats(ctx context.Context) (*AssetStats, error) {
	return m.statsFn(ctx)
}

type testLogger struct{ zap *zap.Logger }

func (l *testLogger) Info(msg string, kv ...any)  {}
func (l *testLogger) Warn(msg string, kv ...any)  {}
func (l *testLogger) Error(msg string, kv ...any) {}
func (l *testLogger) Debug(msg string, kv ...any) {}

// newTestDiagService creates a diagnostics.Service backed by mock ports.
func newTestDiagService(ih IndexHealthPort, as AssetStatsPort) *Service {
	return NewService(ih, as, &testLogger{zap: zap.NewNop()})
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
		checkFn: func(ctx context.Context) (*IndexHealthReport, error) {
			return &IndexHealthReport{
				OK: true, SQLiteAssets: 150,
			}, nil
		},
	}
	as := &mockAssetStats{
		statsFn: func(ctx context.Context) (*AssetStats, error) {
			return &AssetStats{Total: 200, ByType: map[string]int{"video": 150}}, nil
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
		checkFn: func(ctx context.Context) (*IndexHealthReport, error) {
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
		checkFn: func(ctx context.Context) (*IndexHealthReport, error) {
			return &IndexHealthReport{
				OK: true, SQLiteAssets: 100, SQLiteIndexed: 95,
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

// ── QdrantCleanup ─────────────────────────────────────────────────────

// TestQdrantCleanup_AlwaysOK removed — the
// cleanup route deleted along with the Qdrant capability.
