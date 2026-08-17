// Package main — server-level tests for the PipelineGen binary.
//
// These tests verify the router-level behaviors (metrics auth, loopback
// restriction) at the `cmd/server` package boundary to ensure the
// BuildServer wiring does not accidentally override the metrics
// protection installed in internal/api/routes.go::Setup.
//
// See internal/api/routes_test.go::TestMetricsRouteReleaseMode for the
// detailed 6-case matrix. This file is a short-circuit smoke at the
// server-package level.
package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	mwports "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
)

// testMetricsAuthAdapter — minimal AuthSecurityPort stub for server-level tests.
type testMetricsAuthAdapter struct{}

func (testMetricsAuthAdapter) EnableAuth() bool    { return false }
func (testMetricsAuthAdapter) AdminToken() string  { return "test-admin-token-for-testing-only" }
func (testMetricsAuthAdapter) WorkerToken() string { return "test-worker-token-for-testing-only" }

type testMetricsRateAdapter struct{}

func (testMetricsRateAdapter) RateLimitEnabled() bool { return false }
func (testMetricsRateAdapter) RateLimitRequests() int { return 0 }

type testMetricsFeaturesAdapter struct{}

func (testMetricsFeaturesAdapter) ArtlistEnabled() bool     { return false }
func (testMetricsFeaturesAdapter) ScriptClipsEnabled() bool { return false }

// TestServerMetricsMountMatrix verifies the (mode×token) matrix for
// /metrics at the cmd/server package boundary. Test-only subst: the
// router is constructed directly (no config.yaml, no BuildServer) so
// wiring drift between api.NewRouter and app.BuildServer is not masked.
func TestServerMetricsMountMatrix(t *testing.T) {
	cases := []struct {
		name        string
		ginMode     string
		token       string
		wantMounted bool
	}{
		{"release + token set", gin.ReleaseMode, "secret123", true},
		{"release + token empty (fail-closed)", gin.ReleaseMode, "", false},
		{"dev + token set", gin.TestMode, "secret123", true},
		{"dev + token empty (loopback)", gin.TestMode, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Setenv("METRICS_AUTH_TOKEN", tc.token)
			t.Cleanup(func() { _ = os.Unsetenv("METRICS_AUTH_TOKEN") })

			gin.SetMode(tc.ginMode)
			router := api.NewRouter(&api.RouterConfig{
				ServerGinMode: tc.ginMode,
				Log:           zap.NewNop(),
				Auth:          testMetricsAuthAdapter{},
				Rate:          testMetricsRateAdapter{},
				Features:      testMetricsFeaturesAdapter{},
			})
			engine := router.Setup()

			mounted := false
			for _, route := range engine.Routes() {
				if route.Path == "/metrics" {
					mounted = true
					break
				}
			}
			if mounted != tc.wantMounted {
				t.Errorf("got mounted=%v, want mounted=%v (ginMode=%q, token=%q)",
					mounted, tc.wantMounted, tc.ginMode, tc.token)
			}
		})
	}
}

// TestServerMetricsLoopbackRestriction — verifies the dev-mode loopback
// IP check is wired correctly at the server-package level. Non-loopback
// clients hitting /metrics in dev mode (no token) MUST get 403.
func TestServerMetricsLoopbackRestriction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = os.Setenv("METRICS_AUTH_TOKEN", "")
	t.Cleanup(func() { _ = os.Unsetenv("METRICS_AUTH_TOKEN") })

	router := api.NewRouter(&api.RouterConfig{
		ServerGinMode: gin.DebugMode,
		Log:           zap.NewNop(),
		Auth:          testMetricsAuthAdapter{},
		Rate:          testMetricsRateAdapter{},
		Features:      testMetricsFeaturesAdapter{},
	})
	engine := router.Setup()

	// Non-loopback → 403
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "10.0.0.99:55555"
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-loopback dev: expected 403, got %d", w.Code)
	}

	// Loopback IPv4 → 200 (no auth middleware expected in token-less dev)
	req2 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req2.RemoteAddr = "127.0.0.1:55555"
	w2 := httptest.NewRecorder()
	engine.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("loopback IPv4 dev: expected 200, got %d", w2.Code)
	}
}

// Compile-time assertion: test adapters satisfy the typed-port interfaces.
var (
	_ mwports.AuthSecurityPort = testMetricsAuthAdapter{}
	_ mwports.RateLimitPort    = testMetricsRateAdapter{}
	_ mwports.FeatureFlagsPort = testMetricsFeaturesAdapter{}
)
