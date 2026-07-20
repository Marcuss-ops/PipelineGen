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
	deps, _ := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)
	router := gin.New()
	rg := router.Group("/api/script")
	handler.RegisterRoutes(rg)

	routes := router.Routes()

	routeMap := make(map[string]bool)
	for _, r := range routes {
		key := fmt.Sprintf("%s %s", r.Method, r.Path)
		routeMap[key] = true
	}

	// PR-script-deps-slim (July 2026, P1): the canonical ScriptFlow
	// surface mounts 6 base routes via ScriptFlowHandler.RegisterRoutes
	// (1 POST /generate + 3 POST /shorts/* + 1 GET /jobs/:id + 1 GET
	// /clips/search). Sprint 1.3-unblock (July 2026): the enriched
	// GET /jobs/:id/full route, registered by JobsHandler.RegisterJobRoutes
	// (handler_jobs.go) and dispatched to GetFullJobRun
	// (handler_run_full.go), is now part of the ScriptFlow route
	// surface — the verdict's "/full" endpoint surface lives here
	// so the orchestrator can serve enriched job status without a
	// second router mount. Total: 7 routes. The earlier "6 routes,
	// /jobs/:id/full is NOT in scope" comment was a PR-script-deps-slim
	// intermediate state; the route was promoted in a later wave
	// without the test being updated in lockstep, producing the
	// test drift surfaced by `make verify-main` on 2026-07-19.
	// assert.Equal on a map produces an exact-match diff: any route
	// the handler LEGITIMATELY adds or removes fails the test loudly
	// here AND must trigger a paired update of architecture/ownership
	// SSOT in lockstep (godlike/06 SSOT: routes.yaml ↔
	// architecture/ownership.generated.yaml ↔ this test are 4-way
	// co-regenerated; any drift among the 4 surfaces is a first-class
	// regression).
	expectedRoutes := map[string]bool{
		"POST /api/script/generate":            true,
		"POST /api/script/shorts/generate":     true,
		"POST /api/script/shorts/render":       true,
		"POST /api/script/shorts/render/async": true,
		"GET /api/script/jobs/:id":             true,
		"GET /api/script/clips/search":         true,
		"GET /api/script/jobs/:id/full":        true,
	}
	assert.Equal(t, expectedRoutes, routeMap,
		"ScriptFlow routes must match canonical set EXACTLY (drift = regressions in either direction; update this test AND architecture/ownership SSOT in lockstep)")

	// godlike/07 no-fake-availability: assert the 6 RETIRED legacy
	// per-source routes are NOT registered. GenerationEnvelopeV2
	// (PR-script-deps-slim + PR-script-deps-slim-p2) unified the
	// /text, /clips, /catalog, /search source-type discriminations
	// under the single POST /api/script/generate endpoint with
	// items[].source.type=. The 5 retired generation-style routes
	// each had fields never populated by NewScriptFlowHandler so
	// they ALWAYS returned 503 (canonical fake-availability surface
	// retired):
	//   - /generate-from-clips                 (commit 09af3cdd3 + SSOT 6ec3e95b6)
	//   - /generate-from-catalog               (commit 09af3cdd3 + SSOT 6ec3e95b6)
	//   - /generate-with-images                (commit 09af3cdd3 + SSOT 6ec3e95b6)
	//   - /curate                              (commit 09af3cdd3 + SSOT 6ec3e95b6)
	//   - /cache/evict                         (commit 069b36ad2)
	//   - /:id/sections/:section_id/regenerate (commit 09af3cdd3)
	// Any return of these is a regression: the canonical entry point
	// is POST /api/script/generate with items[i].source.{type,...};
	// “legacy_adapter” aliases for them are explicitly banded.
	notExpectedRoutes := map[string]struct{}{
		"POST /api/script/:id/sections/:section_id/regenerate": {},
		"POST /api/script/cache/evict":                         {},
		"POST /api/script/generate-from-clips":                 {},
		"POST /api/script/generate-from-catalog":               {},
		"POST /api/script/generate-with-images":                {},
		"POST /api/script/curate":                              {},
	}
	for key := range routeMap {
		_, isRetired := notExpectedRoutes[key]
		assert.False(t, isRetired,
			"retired legacy route %q must NOT be registered (godlike/07 no-fake-availability; canonical entry is POST /api/script/generate with items[].source.type)", key)
	}
}

// TestScriptFlowAsyncRoutes_EnqueueJobs verifies that the active
// /generate route enqueues a script.generate job.
func TestScriptFlowAsyncRoutes_EnqueueJobs(t *testing.T) {
	t.Parallel()

	jobsSvc, fake := newTestJobsService(t)
	deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)
	router := gin.New()
	rg := router.Group("/api/script")
	handler.RegisterRoutes(rg)

	req := httptest.NewRequest("POST", "/api/script/generate", strings.NewReader(`{"version":2,"preset":"custom","items":[{"id":"job-1","title":"Observability","language":"it","script_params":{"target_words":150},"source":{"type":"text","topic":"observability","source_text":"observability fixture"}}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "req-123")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.NotNil(t, submit.lastReq, "generate route must submit a job")
	assert.Equal(t, "script.generate", submit.lastReq.JobType)
	assert.Contains(t, w.Body.String(), `"ok":true`)
	assert.Contains(t, w.Body.String(), `"status":"QUEUED"`)
	assert.Equal(t, 1, submit.submitCount)
	_ = fake
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
