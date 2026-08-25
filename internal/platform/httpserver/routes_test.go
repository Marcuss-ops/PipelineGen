package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	mwidem "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
)

func TestRegistryRoutesKeepExpectedPrefixes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	registry := NewRegistry()

	// Create mock modules that simulate the FIXED behavior (creating sub-groups)
	artlistModule := &mockModuleWithGroup{name: "artlist", prefix: "/artlist", enabled: true}
	youtubeModule := &mockModuleWithGroup{name: "clips", prefix: "/clips", enabled: true}
	jobsModule := &mockModuleWithGroup{name: "jobs", prefix: "/jobs", enabled: true}
	mediaModule := &mockModuleWithGroup{name: "media", prefix: "/media", enabled: true}
	stockModule := &mockModuleWithGroup{name: "stock-pipeline", prefix: "/stock-pipeline", enabled: true}

	registry.Register(artlistModule)
	registry.Register(youtubeModule)
	registry.Register(jobsModule)
	registry.Register(mediaModule)
	registry.Register(stockModule)

	// Simulate what Router.Setup() does with registry
	engine := gin.New()
	apiGroup := engine.Group("/api")
	protected := apiGroup.Group("")

	// This is what RegisterAllRoutes does - calls RegisterRoutes on each module
	registry.RegisterAllRoutes(protected)

	routes := engine.Routes()

	// Check that routes are at correct paths (with module prefix)
	routeMap := make(map[string]bool)
	for _, route := range routes {
		key := route.Method + " " + route.Path
		routeMap[key] = true
	}

	// Artlist routes should be under /api/artlist/
	assert.True(t, routeMap["POST /api/artlist/run"], "POST /api/artlist/run should be registered")
	assert.True(t, routeMap["GET /api/artlist/runs/:run_id"], "GET /api/artlist/runs/:run_id should be registered")
	assert.True(t, routeMap["GET /api/artlist/stats"], "GET /api/artlist/stats should be registered")

	// Clips routes should be under /api/clips/
	assert.True(t, routeMap["POST /api/clips/process"], "POST /api/clips/process should be registered")
	assert.True(t, routeMap["GET /api/clips/info"], "GET /api/clips/info should be registered")

	// Jobs routes should be under /api/jobs/
	assert.True(t, routeMap["GET /api/jobs"], "GET /api/jobs should be registered")
	assert.True(t, routeMap["POST /api/jobs"], "POST /api/jobs should be registered")
	assert.True(t, routeMap["GET /api/jobs/:id"], "GET /api/jobs/:id should be registered")

	// Media routes should be under /api/media/
	assert.True(t, routeMap["POST /api/media/search"], "POST /api/media/search should be registered")
	assert.True(t, routeMap["POST /api/media/:source/clips/:id/download"], "POST /api/media/:source/clips/:id/download should be registered")

	// Stock routes must be published through the public /api registry.
	assert.True(t, routeMap["POST /api/stock-pipeline/run"], "POST /api/stock-pipeline/run should be registered")
	assert.True(t, routeMap["POST /api/stock-pipeline/search-and-run"], "POST /api/stock-pipeline/search-and-run should be registered")

	// Ensure routes are NOT at wrong paths (without module prefix)
	assert.False(t, routeMap["POST /api/run"], "POST /api/run should NOT be registered (missing artlist prefix)")
	assert.False(t, routeMap["POST /api/extract"], "POST /api/extract should NOT be registered (missing clips prefix)")
	assert.False(t, routeMap["GET /api"], "GET /api should NOT be registered (missing jobs prefix)")
}

// mockModuleWithGroup simulates the FIXED module behavior where RegisterRoutes
// creates a sub-group with the proper prefix
type mockModuleWithGroup struct {
	name    string
	prefix  string
	enabled bool
}

func (m *mockModuleWithGroup) Name() string {
	return m.name
}

func (m *mockModuleWithGroup) Enabled() bool {
	return m.enabled
}

// TestRouteCollisionDetection verifies that no two modules register the same
// method + path. Gin itself panics when a duplicate route is registered, so
// this test verifies that distinct prefixes do NOT panic (correct behavior)
// and that colliding prefixes DO panic (gin guards against silent overwrites).
// TestNoAPIPrefixedInternalRoutes uses a real Router.Setup() with module
// registry and mock handlers to verify that no route leaks under
// /api/internal/v1/. Internal routes (mediasearch, outbox, etc.) must be
// mounted on the WorkerAuth-protected /internal/v1/ group, not under /api.
//
// QDRANT-002 / QDRANT-004 closure gate: if this test fails, an internal
// route has been registered through the module system (under /api) that
// should only be on the internal WorkerAuth group.
func TestNoAPIPrefixedInternalRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	registry := NewRegistry()
	// Register a known module to verify the registry is working.
	medMod := &mockModuleWithGroup{name: "media", prefix: "/media", enabled: true}
	registry.Register(medMod)
	registry.Freeze()

	router := NewRouter(&RouterConfig{
		ServerGinMode: gin.TestMode,
	})
	router.SetRegistry(registry)

	engine := router.Setup()
	for _, route := range engine.Routes() {
		if strings.HasPrefix(route.Path, "/api/internal/v1/") {
			t.Errorf("internal route leaked under /api: %s %s — internal handlers must be wired via SetMediasearchHandler / SetOutboxHandler, not through the module registry", route.Method, route.Path)
		}
	}

	// Sanity check: a known /api/media/search route should exist via the
	// module registry (proves the engine actually has api-group routes).
	found := false
	for _, route := range engine.Routes() {
		if route.Path == "/api/media/search" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected /api/media/search to be registered via the module registry")
	}
}

func TestRouteCollisionDetection(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("colliding prefix panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic on duplicate route registration, but none occurred")
			} else {
				t.Logf("gin correctly panicked on duplicate route: %v", r)
			}
		}()

		engine := gin.New()
		apiGroup := engine.Group("/api")

		collidingModuleA := &mockModuleWithGroup{name: "clips", prefix: "/items", enabled: true}
		collidingModuleB := &mockModuleWithGroup{name: "clips", prefix: "/items", enabled: true}

		collidingModuleA.RegisterRoutes(apiGroup)
		collidingModuleB.RegisterRoutes(apiGroup)
	})

	t.Run("distinct prefixes do not panic", func(t *testing.T) {
		engine := gin.New()
		api := engine.Group("/api")

		distinctA := &mockModuleWithGroup{name: "clips", prefix: "/clips", enabled: true}
		distinctB := &mockModuleWithGroup{name: "artlist", prefix: "/artlist", enabled: true}

		// Neither registration should panic
		distinctA.RegisterRoutes(api)
		distinctB.RegisterRoutes(api)

		routes := engine.Routes()
		seen := make(map[string]int)
		for _, route := range routes {
			key := route.Method + " " + route.Path
			seen[key]++
		}
		for key, count := range seen {
			if count > 1 {
				t.Errorf("unexpected collision with distinct prefixes: %s (count=%d)", key, count)
			}
		}

		t.Logf("registered %d routes with zero collisions", len(routes))
	})
}

func (m *mockModuleWithGroup) RegisterRoutes(rg *gin.RouterGroup) {
	// This is the key fix: create a sub-group with the module's prefix
	group := rg.Group(m.prefix)

	switch m.name {
	case "artlist":
		group.POST("/run", func(c *gin.Context) {})
		group.GET("/runs/:run_id", func(c *gin.Context) {})
		group.GET("/stats", func(c *gin.Context) {})
	case "clips":
		group.POST("/process", func(c *gin.Context) {})
		group.GET("/info", func(c *gin.Context) {})
	case "jobs":
		group.GET("", func(c *gin.Context) {})
		group.POST("", func(c *gin.Context) {})
		group.GET("/:id", func(c *gin.Context) {})
	case "media":
		group.POST("/search", func(c *gin.Context) {})
		group.POST("/:source/clips/:id/download", func(c *gin.Context) {})
	case "stock-pipeline":
		group.POST("/run", func(c *gin.Context) {})
		group.POST("/search-and-run", func(c *gin.Context) {})
	}
}

// ── QDRANT-002 + QDRANT-004 anti-regression test ────────────────────────
// TestRoutes_NoApiInternalV1Prefix locks in the routing split between
// the public /api registry and the WorkerAuth-protected /internal/v1
// internalGroup.
//
// Regression target: the outbox endpoint ("/internal/v1/outbox/*")
// and the mediasearch endpoint ("/internal/v1/media/search") MUST
// register on the internalGroup, NOT on the /api registry. The pre-fix
// code used module.NewRouteModule(..., "/internal/v1/outbox", ...)
// which mounted those handlers under /api/internal/v1/outbox/* since
// the registry was attached to engine.Group("/api"). The current
// canonical path requires that:
//
//  1. NO route appears under any /api/internal/v1/* prefix.
//  2. The outbox handlers DO register at /internal/v1/outbox/{status,events}.
//  3. The mediasearch handler DOES register at /internal/v1/media/search.
//
// If you change the wiring in routes.go (Setup), cmd/server/main.go,
// or registry.go, this test will fail and force you to update the
// expected surface in this test file rather than silently re-introduce
// the regression.
//
// The test deliberately uses minimal stubs (no DB, no config.yaml,
// no token resolution) because the assertion is structural, not
// behavioral.
func TestRoutes_NoApiInternalV1Prefix(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Minimal typed-port adapters so Router.Setup() can compose.
	// Auth is disabled so the WorkerAuth middleware lets /internal/v1
	// requests through (the test never issues a request, but Setup()
	// still constructs the middleware chain).
	authAdapter := &middleware.TokenSecurityAdapter{
		Enable: false,
	}
	rateAdapter := testRateLimitAdapter{}
	featuresAdapter := testFeatureFlagsAdapter{}

	router := NewRouter(&RouterConfig{
		Auth:          authAdapter,
		Rate:          rateAdapter,
		Features:      featuresAdapter,
		Log:           zap.NewNop(),
		ServerGinMode: gin.TestMode,
	})
	// Wire the handlers the closure registers onto the WorkerAuth-
	// protected internalGroup (per QDRANT-002 + QDRANT-004 split).
	router.SetInternalMediaHandler(fakeInternalMediaHandlerStub{}) // already wired in router_test.go
	router.SetOutboxHandler(&fakeOutboxHandlerStub{})
	router.SetMediasearchHandler(&fakeMediaSearchHandlerStub{})

	engine := router.Setup()

	// (1) Hard recheck: NO /api/internal/v1/* routes are allowed.
	for _, route := range engine.Routes() {
		if strings.HasPrefix(route.Path, "/api/internal/") {
			t.Errorf(
				"QDRANT-002/004 routing regression: route %s %q lives under /api/internal/* — "+
					"server-to-server routes MUST be mounted on the /internal/v1 WorkerAuth-protected group, not the /api registry",
				route.Method, route.Path,
			)
		}
	}

	// (2) Positive recheck: the canonical outbox + mediasearch paths
	// are reachable. Use a presence map so missing routes are reported
	// with the canonical expectation rather than silently passing.
	have := make(map[string]bool, len(engine.Routes()))
	for _, route := range engine.Routes() {
		have[route.Method+" "+route.Path] = true
	}
	want := []string{
		"GET /internal/v1/outbox/status",
		"GET /internal/v1/outbox/events",
		"POST /internal/v1/media/search",
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("expected route %q to be registered under /internal/v1/* but it is missing", w)
		}
	}
}

// fakeInternalMediaHandlerStub is a minimal sync stub for
// the antiregression test. Mirrors the production fake used elsewhere
// in the package. The handler returns no routes — the assertion is on
// the OUTBOX + MEDIASEARCH surface, not on the internal-media surface.
type fakeInternalMediaHandlerStub struct{}

func (fakeInternalMediaHandlerStub) RegisterInternalMediaRoutes(_ *gin.RouterGroup) {}

// fakeOutboxHandlerStub mounts GET /status and /events on the supplied
// group — mirrors production internal/api/outbox/handler.go::Handler.
type fakeOutboxHandlerStub struct{}

func (fakeOutboxHandlerStub) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/status", func(c *gin.Context) {})
	rg.GET("/events", func(c *gin.Context) {})
}

// fakeMediaSearchHandlerStub mounts POST /search on the supplied group
// — mirrors production internal/api/mediasearch/handler.go.
type fakeMediaSearchHandlerStub struct{}

func (fakeMediaSearchHandlerStub) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/search", func(c *gin.Context) {})
}

// testRateLimitAdapter is a no-op RateLimitPort for the antiregression
// test. Construction of Router.Setup() requires a non-nil Rate port;
// the value here disables the limiter so the test does not touch
// background goroutines.
type testRateLimitAdapter struct{}

func (testRateLimitAdapter) RateLimitEnabled() bool { return false }
func (testRateLimitAdapter) RateLimitRequests() int { return 0 }

// testFeatureFlagsAdapter is a no-op FeatureFlagsPort for the
// antiregression test. Mirrors serverFeatureFlagsAdapter without
// requiring a *config.Config pointer.
type testFeatureFlagsAdapter struct{}

func (testFeatureFlagsAdapter) ArtlistEnabled() bool     { return false }
func (testFeatureFlagsAdapter) ScriptClipsEnabled() bool { return false }

// TestMetricsRouteReleaseMode is the fail-closed matrix for /metrics
// (PR-METRICS-FAILCLOSED). In release mode METRICS_AUTH_TOKEN MUST be
// set; otherwise the route is NOT registered. In dev/local modes the
// route is mounted as before (with token if set, without if not) so
// local dev workflows don't break. The 4-case matrix covers every
// (mode, token_set) combination.
func TestMetricsRouteReleaseMode(t *testing.T) {
	cases := []struct {
		name        string
		ginMode     string
		token       string
		wantMounted bool
	}{
		{"release + token set", gin.ReleaseMode, "secret", true},
		{"release + token unset (fail-closed)", gin.ReleaseMode, "", false},
		{"dev (TestMode) + token set", gin.TestMode, "secret", true},
		{"dev (TestMode) + token unset (preserved)", gin.TestMode, "", true},
		{"dev (DebugMode) + token set", gin.DebugMode, "secret", true},
		{"dev (DebugMode) + token unset (preserved)", gin.DebugMode, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("METRICS_AUTH_TOKEN", tc.token)

			router := NewRouter(&RouterConfig{
				ServerGinMode: tc.ginMode,
				Log:           zap.NewNop(),
				Rate:          testRateLimitAdapter{},
				Features:      testFeatureFlagsAdapter{},
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
				t.Errorf("got mounted=%v, want mounted=%v", mounted, tc.wantMounted)
			}
		})
	}
}

// TestMetricsDevModeLoopbackRestriction verifies that in dev mode,
// /metrics is mounted but rejects non-loopback clients. The loopback
// restriction is a middleware check on each request's RemoteAddr —
// requests from 127.0.0.0/8 or ::1 succeed; all others return 403
// Forbidden (PR-METRICS-FAILCLOSED loopback addendum, July 2026).
func TestMetricsDevModeLoopbackRestriction(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		wantStatus int
	}{
		{"loopback IPv4", "127.0.0.1:54321", http.StatusOK},
		{"loopback IPv6", "[::1]:54321", http.StatusOK},
		{"non-loopback", "192.168.1.1:54321", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			t.Setenv("METRICS_AUTH_TOKEN", "")

			router := NewRouter(&RouterConfig{
				ServerGinMode: gin.DebugMode,
				Log:           zap.NewNop(),
				Rate:          testRateLimitAdapter{},
				Features:      testFeatureFlagsAdapter{},
			})
			engine := router.Setup()

			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			req.RemoteAddr = tc.remoteAddr
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("%s: got status %d, want %d", tc.name, w.Code, tc.wantStatus)
			}
		})
	}
}

// TestMetricsDevModeTokenSetIgnoresLoopbackCheck verifies that when
// METRICS_AUTH_TOKEN is set in dev mode, the loopback check is not
// applied (the bearer token check supersedes IP restriction).
func TestMetricsDevModeTokenSetIgnoresLoopbackCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("METRICS_AUTH_TOKEN", "my-token")

	router := NewRouter(&RouterConfig{
		ServerGinMode: gin.DebugMode,
		Log:           zap.NewNop(),
		Rate:          testRateLimitAdapter{},
		Features:      testFeatureFlagsAdapter{},
	})
	engine := router.Setup()

	// Non-loopback, no bearer → 401 (not 403)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "10.0.0.1:54321"
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 (no bearer) for non-loopback+token-env set, got %d", w.Code)
	}

	// Non-loopback, with bearer → 200
	req2 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req2.RemoteAddr = "10.0.0.1:54321"
	req2.Header.Set("Authorization", "Bearer my-token")
	w2 := httptest.NewRecorder()
	engine.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("expected 200 (with bearer) for non-loopback+token-env set, got %d", w2.Code)
	}
}

// Compile-time assertion: test adapters satisfy the typed-port interfaces
// expected by internal/api::RouterConfig. Drift is caught at compile, not
// at runtime when Router.Setup() is invoked.
var (
	_ mwidem.RateLimitPort    = testRateLimitAdapter{}
	_ mwidem.FeatureFlagsPort = testFeatureFlagsAdapter{}
)
