//go:build !integration
// +build !integration

package wiring

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	middlewarepkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
	"go.uber.org/zap"
)

// TestRoutes_DoNotPassNilReadyChecker verifies that when both healthSvc
// and readyChecker are wired into the Router, the /ready endpoint
// responds without the "ready checker not initialized" error.
// This test fails if the production router constructs NewHealthHandler
// with ReadyChecker nil (the bug this PR fixes).
func TestRoutes_DoNotPassNilReadyChecker(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{
			Host: "127.0.0.1", Port: 0, GinMode: "test",
			ReadTimeout: 30000, WriteTimeout: 30000,
		},
		Storage: config.StorageConfig{
			DataDir: t.TempDir(),
		},
		Security: config.SecurityConfig{},
	}

	// Build a Service with all optional checkers nil (healthy empty state).
	svc := systemhealth.NewService(systemhealth.ServiceDeps{})
	ready := systemhealth.NewReadyChecker(svc)
	require.NotNil(t, ready)

	router := module.NewRouter(&module.RouterConfig{
		Auth: &middleware.TokenSecurityAdapter{
			Enable: cfg.Security.EnableAuth,
			Admin:  cfg.Security.AdminToken,
			Worker: cfg.Security.WorkerToken,
		},
		Rate:          newMiddlewareRateLimitAdapter(cfg),
		Features:      newMiddlewareFeatureFlagsAdapter(cfg),
		Log:           zap.NewNop(),
		ServerGinMode: cfg.Server.GinMode,
		DataDir:       cfg.Storage.DataDir,
		DownloadDir:   cfg.GoogleAccounting.DownloadDir,
		CORSOrigins:   cfg.Security.CORSOrigins,
	})
	router.SetHealthService(svc)
	router.SetReadyChecker(ready)

	engine := router.Setup()

	// /ready must respond without returning "ready checker not initialized".
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ready", nil)
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		" /ready with empty checker set returns 503 per contract")
	body := w.Body.String()
	assert.NotContains(t, body, "ready checker not initialized",
		" /ready must NOT say 'ready checker not initialized' when ReadyChecker is wired")

	// /health still works (fast liveness).
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/health", nil)
	engine.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
}

// TestRoutes_WithoutReadyChecker_ReturnsNotInitialized verifies that
// when ReadyChecker is NOT set on the Router, /ready returns the
// expected error. This is the pre-fix behaviour preserved for
// deployments that don't wire ReadyChecker.
func TestRoutes_WithoutReadyChecker_ReturnsNotInitialized(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{
			Host: "127.0.0.1", Port: 0, GinMode: "test",
			ReadTimeout: 30000, WriteTimeout: 30000,
		},
		Storage: config.StorageConfig{
			DataDir: t.TempDir(),
		},
		Security: config.SecurityConfig{},
	}

	svc := systemhealth.NewService(systemhealth.ServiceDeps{})

	router := module.NewRouter(&module.RouterConfig{
		Auth: &middleware.TokenSecurityAdapter{
			Enable: cfg.Security.EnableAuth,
			Admin:  cfg.Security.AdminToken,
			Worker: cfg.Security.WorkerToken,
		},
		Rate:          newMiddlewareRateLimitAdapter(cfg),
		Features:      newMiddlewareFeatureFlagsAdapter(cfg),
		Log:           zap.NewNop(),
		ServerGinMode: cfg.Server.GinMode,
		DataDir:       cfg.Storage.DataDir,
		DownloadDir:   cfg.GoogleAccounting.DownloadDir,
		CORSOrigins:   cfg.Security.CORSOrigins,
	})
	router.SetHealthService(svc)
	// Intentionally NOT calling SetReadyChecker — simulates pre-fix state.

	engine := router.Setup()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ready", nil)
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "not ready")
}

// newMiddlewareRateLimitAdapter and newMiddlewareFeatureFlagsAdapter used to
// live in adapters_middleware.go with no production caller; they are test
// fixtures for the Router contract, so they live with the test that needs
// them (TICKET-APP-WIRING-TEST-ONLY-HELPERS-2026-11-19).
func newMiddlewareRateLimitAdapter(cfg *config.Config) middlewarepkg.RateLimitPort {
	if cfg == nil {
		return nil
	}
	return &middleware.RateLimitAdapter{
		Enabled:  cfg.Security.RateLimitEnabled,
		Requests: cfg.Security.RateLimitRequests,
	}
}

func newMiddlewareFeatureFlagsAdapter(cfg *config.Config) middlewarepkg.FeatureFlagsPort {
	if cfg == nil {
		return nil
	}
	return &middleware.FeatureFlagsAdapter{
		ScriptClips: cfg.Features.ScriptClipsEnabled,
	}
}
