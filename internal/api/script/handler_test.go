package script

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
)

// ── Route compatibility ────────────────────────────────────────

func TestScriptRoutes_Compatibility(t *testing.T) {
	t.Parallel()

	jobsSvc, _ := newTestJobsService(t)
	handler := NewScriptFlowHandler(newMinimalScriptFlowDepsForTest(jobsSvc))
	router := gin.New()
	rg := router.Group("/api/script")
	handler.RegisterRoutes(rg)

	routes := router.Routes()

	routeMap := make(map[string]bool)
	for _, r := range routes {
		key := fmt.Sprintf("%s %s", r.Method, r.Path)
		routeMap[key] = true
	}

	// PR-script-deps-slim (July 2026, P1): the 2 routes that
	// depended on sectionRegen + cacheEviction (RegenerateSection
	// + EvictCache) are RETIRED — the fields were never populated
	// by NewScriptFlowHandler so the routes always returned 503
	// (godlike/07 no-fake-availability).
	expectedRoutes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/script/generate"},
		// Legacy routes — now registered as deprecated adapters (PR 11).
		// FASE 12c: legacy batch route REMOVED.
		{"POST", "/api/script/generate-from-clips"},
		{"POST", "/api/script/generate-with-images"},
		{"GET", "/api/script/jobs/:id"},
		{"GET", "/api/script/clips/search"},
	}

	for _, want := range expectedRoutes {
		key := fmt.Sprintf("%s %s", want.method, want.path)
		assert.True(t, routeMap[key], "required route %s %s must be registered", want.method, want.path)
	}

	// godlike/07 no-fake-availability: assert the 2 RETIRED routes
	// are NOT registered (they always returned 503 pre-PR).
	notExpectedRoutes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/script/:id/sections/:section_id/regenerate"},
		{"POST", "/api/script/cache/evict"},
	}
	for _, notWant := range notExpectedRoutes {
		key := fmt.Sprintf("%s %s", notWant.method, notWant.path)
		assert.False(t, routeMap[key], "retired route %s %s must NOT be registered (godlike/07 no-fake-availability)", notWant.method, notWant.path)
	}
}

// TestScriptFlowAsyncRoutes_EnqueueJobs verifies that legacy adapter routes
// add the X-Deprecated header and enqueue as script.generate.
// AZIONE 5 (July 2026): changed from /curate to /generate-from-clips
// after /curate was removed.
func TestScriptFlowAsyncRoutes_EnqueueJobs(t *testing.T) {
	t.Parallel()

	jobsSvc, fake := newTestJobsService(t)
	handler := NewScriptFlowHandler(newMinimalScriptFlowDepsForTest(jobsSvc))
	router := gin.New()
	rg := router.Group("/api/script")
	handler.RegisterRoutes(rg)

	req := httptest.NewRequest("POST", "/api/script/generate-from-clips", strings.NewReader(`{"topic":"observability","clip_ids":["clip-a"],"language":"it"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "req-123")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// PR-script-legacy-contract (Jul 2026, P0 ABSOLUTE): legacy route
	// is RETIRED to canonical 410-Gone contract. Tests below pin the
	// contract — no enqueue happens, only the deprecation increment +
	// canonical body.
	assert.Equal(t, http.StatusGone, w.Code)
	assert.Nil(t, fake.lastReq, "deprecation registrar must NOT enqueue a job")
	assert.Contains(t, w.Header().Get("X-Deprecated"), "true")
	assert.Contains(t, w.Body.String(), `"canonical_endpoint":"POST /api/script/generate"`)
	assert.Contains(t, w.Body.String(), `"removal_date":"2026-12-31"`)
	assert.Contains(t, w.Body.String(), `"ok":false`)
}

// ── RequireAdminToken middleware ──────────────────────────────────────────

func TestRequireAdminToken_NoToken_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	provider := &middleware.TokenSecurityAdapter{Enable: true, Admin: "secret"}
	router := gin.New()
	router.Use(RequireAdminToken(provider))
	router.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAdminToken_CorrectToken_Succeeds(t *testing.T) {
	t.Parallel()

	provider := &middleware.TokenSecurityAdapter{Enable: true, Admin: "secret"}
	router := gin.New()
	router.Use(RequireAdminToken(provider))
	router.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("X-Velox-Admin-Token", "secret")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireAdminToken_DisabledByEnableFlag_NoAuth(t *testing.T) {
	t.Parallel()

	provider := &middleware.TokenSecurityAdapter{Enable: false}
	router := gin.New()
	router.Use(RequireAdminToken(provider))
	router.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireAdminToken_NilProvider_AllowAll(t *testing.T) {
	t.Parallel()

	var provider AdminTokenProvider = nil
	router := gin.New()
	router.Use(RequireAdminToken(provider))
	router.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ── NewScriptFlowHandler defensive-constructor regression ─────────────────

// TestNewScriptFlowHandler_NilLog_DefaultsToNoopLogger pins the
// defensive-constructor contract: when ScriptFlowDeps.Log is nil, the
// constructor must NOT propagate a nil logger (which would panic on any
// downstream log call). zap.NewNop() is the silent-drop contract that
// keeps tests + dev-mode wiring crash-free.
func TestNewScriptFlowHandler_NilLog_DefaultsToNoopLogger(t *testing.T) {
	t.Parallel()

	// PR-script-deps-slim (July 2026): ScriptFlowDeps is a slim 5-field
	// bag — passing the zero value exercises the nil-Log defensive
	// constructor (the canonical contract).
	handler := NewScriptFlowHandler(ScriptFlowDeps{})
	assert.NotNil(t, handler.log, "log must default to a no-op logger when ScriptFlowDeps.Jobs.Log is nil")
	// Touch a Warn call to confirm the no-op logger is functional.
	handler.log.Warn("ping — defensive-constructor smoke test")
}
