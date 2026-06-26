package common

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
)

// blockingChecker / healthyMock /
// failingMock no longer implement CheckQdrant. Three capability checks
// (db, drive, jobs) replace the previous four.

// blockingChecker blocks until ctx is done, then returns ok=false.
// Used for context-timeout tests.
type blockingChecker struct {
	blockCh chan struct{}
}

func (b *blockingChecker) CheckDB(ctx context.Context) systemhealth.CheckResult {
	<-ctx.Done()
	return systemhealth.CheckResult{"ok": false, "error": "context done", "duration_ms": int64(0)}
}
func (b *blockingChecker) CheckDrive(ctx context.Context) systemhealth.CheckResult {
	return b.CheckDB(ctx)
}
func (b *blockingChecker) CheckJobs(ctx context.Context) systemhealth.CheckResult {
	return b.CheckDB(ctx)
}

// healthyService returns a Service where all checks report ok=true.
func healthyService() *systemhealth.Service {
	m := &healthyMock{}
	return systemhealth.NewService(systemhealth.ServiceDeps{
		DB:    m,
		Drive: m,
		Jobs:  m,
	})
}

type healthyMock struct{}

func (h *healthyMock) CheckDB(ctx context.Context) systemhealth.CheckResult {
	return systemhealth.CheckResult{"ok": true, "duration_ms": int64(1)}
}
func (h *healthyMock) CheckDrive(ctx context.Context) systemhealth.CheckResult {
	return systemhealth.CheckResult{"ok": true, "duration_ms": int64(1)}
}
func (h *healthyMock) CheckJobs(ctx context.Context) systemhealth.CheckResult {
	return systemhealth.CheckResult{"ok": true, "duration_ms": int64(1)}
}

// failingService returns a Service where all checks report ok=false.
func failingService() *systemhealth.Service {
	m := &failingMock{}
	return systemhealth.NewService(systemhealth.ServiceDeps{
		DB:    m,
		Drive: m,
		Jobs:  m,
	})
}

type failingMock struct{}

func (f *failingMock) CheckDB(ctx context.Context) systemhealth.CheckResult {
	return systemhealth.CheckResult{"ok": false, "duration_ms": int64(1), "error": "fail"}
}
func (f *failingMock) CheckDrive(ctx context.Context) systemhealth.CheckResult {
	return systemhealth.CheckResult{"ok": false, "duration_ms": int64(1), "error": "fail"}
}
func (f *failingMock) CheckJobs(ctx context.Context) systemhealth.CheckResult {
	return systemhealth.CheckResult{"ok": false, "duration_ms": int64(1), "error": "fail"}
}

// ── /health fast ──────────────────────────────────────────────────────

func TestHealthHandler_FastHealth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := healthyService()
	ready := systemhealth.NewReadyChecker(svc)
	handler := NewHealthHandler(svc, ready)

	router := gin.New()
	router.GET("/health", handler.Health)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["ok"].(bool))
	assert.Equal(t, "healthy", resp["status"])
	_, hasChecks := resp["checks"]
	assert.False(t, hasChecks, "fast health should not include checks")
}

// ── /health?deep=true ─────────────────────────────────────────────────

func TestHealthHandler_DeepHealth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := healthyService()
	ready := systemhealth.NewReadyChecker(svc)
	handler := NewHealthHandler(svc, ready)

	router := gin.New()
	router.GET("/health", handler.Health)

	t.Run("healthy", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/health?deep=true", nil)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		checks := resp["checks"].(map[string]any)
		for _, name := range []string{"db", "drive", "jobs"} {
			assert.Contains(t, checks, name, "deep health missing check: %s", name)
		}
	})

	t.Run("unhealthy", func(t *testing.T) {
		failSvc := failingService()
		failReady := systemhealth.NewReadyChecker(failSvc)
		failHandler := NewHealthHandler(failSvc, failReady)
		r := gin.New()
		r.GET("/health", failHandler.Health)

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/health?deep=true", nil)
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.False(t, resp["ok"].(bool))
	})
}

// ── Query repeated params ─────────────────────────────────────────────

func TestHealthHandler_RepeatedCheckParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := healthyService()
	ready := systemhealth.NewReadyChecker(svc)
	handler := NewHealthHandler(svc, ready)

	router := gin.New()
	router.GET("/health", handler.Health)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health?check=db&check=jobs", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	checks := resp["checks"].(map[string]any)
	assert.Contains(t, checks, "db")
	assert.Contains(t, checks, "jobs")
	assert.NotContains(t, checks, "drive")
}

// ── Query comma-separated ─────────────────────────────────────────────

func TestHealthHandler_CommaSeparatedChecks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := healthyService()
	ready := systemhealth.NewReadyChecker(svc)
	handler := NewHealthHandler(svc, ready)

	router := gin.New()
	router.GET("/health", handler.Health)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health?check=db,jobs", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	checks := resp["checks"].(map[string]any)
	assert.Contains(t, checks, "db")
	assert.Contains(t, checks, "jobs")
}

// ── Query mixed syntax ────────────────────────────────────────────────

func TestHealthHandler_MixedCheckSyntax(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := healthyService()
	ready := systemhealth.NewReadyChecker(svc)
	handler := NewHealthHandler(svc, ready)

	router := gin.New()
	router.GET("/health", handler.Health)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health?check=db,jobs&check=db", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	checks := resp["checks"].(map[string]any)
	assert.Contains(t, checks, "db")
	assert.Contains(t, checks, "jobs")
}

// ── Unknown check → HTTP 400 ──────────────────────────────────────────

func TestHealthHandler_UnknownCheckMapsTo400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := healthyService()
	ready := systemhealth.NewReadyChecker(svc)
	handler := NewHealthHandler(svc, ready)

	router := gin.New()
	router.GET("/health", handler.Health)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health?check=nonesiste", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"unknown check should return HTTP 400, not 503")
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp["ok"].(bool))
	assert.Equal(t, "bad request", resp["status"])
	assert.Contains(t, resp["error"].(string), "unknown health check")
	assert.Contains(t, resp["error"].(string), "nonesiste")
}

// ── Nil service → 503 ─────────────────────────────────────────────────

func TestHealthHandler_NilServiceReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewHealthHandler(nil, nil)
	router := gin.New()
	router.GET("/health", handler.Health)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp["ok"].(bool))
	assert.Equal(t, "unhealthy", resp["status"])
	assert.Contains(t, resp["error"].(string), "not initialized")
}

// ── Nil ReadyChecker → 503 ────────────────────────────────────────────

func TestHealthHandler_NilReadyCheckerReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := healthyService()
	handler := NewHealthHandler(svc, nil) // ReadyChecker nil
	router := gin.New()
	router.GET("/ready", handler.Ready)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ready", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp["ok"].(bool))
	assert.Contains(t, resp["error"].(string), "ready checker not initialized")
}

// ── /ready status mapping ─────────────────────────────────────────────

func TestHealthHandler_ReadyStatusMapping(t *testing.T) {
	type tc struct {
		name       string
		svcHealthy bool
		wantHTTP   int
		wantStatus string
	}
	cases := []tc{
		{name: "ok_maps_to_200_ready", svcHealthy: true, wantHTTP: http.StatusOK, wantStatus: "ready"},
		{name: "fail_maps_to_503_not_ready", svcHealthy: false, wantHTTP: http.StatusServiceUnavailable, wantStatus: "not ready"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			var svc *systemhealth.Service
			if c.svcHealthy {
				svc = healthyService()
			} else {
				svc = failingService()
			}
			ready := systemhealth.NewReadyChecker(svc)
			handler := NewHealthHandler(svc, ready)

			router := gin.New()
			router.GET("/ready", handler.Ready)

			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/ready", nil)
			router.ServeHTTP(w, req)

			assert.Equal(t, c.wantHTTP, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, c.wantStatus, resp["status"])
		})
	}
}

// ── Context timeout ───────────────────────────────────────────────────

func TestHealthHandler_ContextTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)

	blocker := &blockingChecker{}
	svc := systemhealth.NewService(systemhealth.ServiceDeps{
		DB:    blocker,
		Drive: blocker,
		Jobs:  blocker,
	})
	ready := systemhealth.NewReadyChecker(svc)
	handler := NewHealthHandler(svc, ready)

	router := gin.New()
	router.GET("/health", handler.Health)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health?deep=true", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	cancel()

	start := time.Now()
	router.ServeHTTP(w, req)
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 500*time.Millisecond,
		"handler should terminate quickly with cancelled context, took %v", elapsed)
}
