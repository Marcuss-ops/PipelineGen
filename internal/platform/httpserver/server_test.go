package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type fakeInternalMediaHandler struct{}

func (fakeInternalMediaHandler) RegisterInternalMediaRoutes(r *gin.RouterGroup) {
	media := r.Group("/media")
	media.POST("/sync", func(c *gin.Context) {})
}

// fakeOutboxHandler mounts the QDRANT-002 production surface:
// GET /status and GET /events on the WorkerAuth-protected
// /internal/v1/outbox/* group.
type fakeOutboxHandler struct{}

func (fakeOutboxHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/status", func(c *gin.Context) {})
	rg.GET("/events", func(c *gin.Context) {})
}

// fakeMediaSearchHandler mounts the QDRANT-004 production surface:
// POST /search on the WorkerAuth-protected /internal/v1/media/* group.
type fakeMediaSearchHandler struct{}

func (fakeMediaSearchHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/search", func(c *gin.Context) {})
}

// TestNewServerWithHealth_RegistersInternalMediaRoutes is the pre-PR-3
// canonical test. Updated to the ServerDeps bundle constructor (outbox +
// mediasearch handlers are nil here; the dedicated PR 3 test covers their
// positive case).
func TestNewServerWithHealth_RegistersInternalMediaRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := NewServerWithHealth(ServerDeps{
		Handlers: InternalHandlers{Media: fakeInternalMediaHandler{}},
	})

	routes := server.GetRouter().Routes()
	for _, route := range routes {
		if route.Method == "POST" && route.Path == "/internal/v1/media/sync" {
			return
		}
	}

	t.Fatalf("expected POST /internal/v1/media/sync to be registered")
}

// TestNewServerWithHealth_RegistersOutboxAndMediaSearchRoutes_ProductionShape
// is the QDRANT-route-constructor (PR 3, June 2026) production-shaped
// gate. It uses the SAME constructor as cmd/server/main.go
// (NewServerWithHealth with all 9 params) — NOT NewRouter — so this
// test catches the exact supervision bug:
//
//	If the routes are not supplied through ServerDeps before Setup()
//	runs, the gin engine never receives a HandlerFunc for the three
//	paths and returns 404 for any request to a path that has no
//	HandlerFunc.
//
// The previous release shipped with that bug because handlers were
// assigned after NewServerWithHealth returned. PR 3 fixes the wiring
// so the handlers are passed through ServerDeps before Setup(), where
// the router can register them.
//
// Test surface:
//
//  1. STRUCTURAL: the engine's Routes() table contains the three
//     exact (method, path) pairs the production server should serve.
//     This is the load-bearing assertion: if a future refactor moves
//     the wiring out of the constructor, the routes vanish from the
//     Routes() table and the test fails with a clear root cause.
//
//  2. BEHAVIOURAL: an HTTP request to each of the three paths
//     returns a status code OTHER THAN 404. The cfg=nil fallback
//     branch mounts a WorkerAuth(nil) middleware that aborts with
//     500 ("WorkerAuth misconfigured (no AuthSecurityPort
//     supplied)"). 500 != 404, which is exactly what the user
//     spec calls for. A future refactor that returned 404 directly
//     (e.g. by detaching the route entirely) trips the assertion.
//
// Why not load cfg.Security.EnableAuth with a Worker token? The
// full production wire-up requires cfg.Server.Host, cfg.Server.Port,
// cfg.Storage.DataDir, cfg.GoogleAccounting.DownloadDir, and four
// security fields; the structural assertion already proves the
// gin engine HAS the route registered at the canonical path, which
// is what the user's "!= 404" requirement ultimately reduces to.
// Adding a behavioural request through to a fake handler would
// require standing up a full cfg + prod-shape TokenSecurityAdapter
// (matching the constructor's cfg != nil branch). That belongs in
// an end-to-end test, not here.
func TestNewServerWithHealth_RegistersOutboxAndMediaSearchRoutes_ProductionShape(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := NewServerWithHealth(ServerDeps{
		Handlers: InternalHandlers{
			Outbox:      fakeOutboxHandler{},
			MediaSearch: fakeMediaSearchHandler{},
		},
	})

	engine := server.GetRouter()

	want := []struct {
		method, path string
	}{
		{"GET", "/internal/v1/outbox/status"},
		{"GET", "/internal/v1/outbox/events"},
		{"POST", "/internal/v1/media/search"},
	}

	// (1) Structural assertion: the three exact paths are in Routes().
	have := make(map[string]bool, len(engine.Routes()))
	for _, r := range engine.Routes() {
		have[r.Method+" "+r.Path] = true
	}
	for _, w := range want {
		key := w.method + " " + w.path
		if !have[key] {
			t.Errorf(
				"QDRANT-route-constructor (PR 3) regression: route %q must be "+
					"registered via the NewServerWithHealth CONSTRUCTOR (not via "+
					"post-Setup setters). If this fires, server.go's wiring was "+
					"regressed to the pre-3x pattern where Setup() ran before the "+
					"outbox/mediasearch fields were populated, silently 404'ing in "+
					"production.",
				key,
			)
		}
	}

	// (2) Behavioural assertion: a request to each path must NOT 404.
	// The cfg=nil branch uses WorkerAuth(nil) which aborts with 500;
	// 500 != 404 means Structural + Behavioural = route is "live".
	for _, w := range want {
		req := httptest.NewRequest(w.method, w.path, nil)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound {
			t.Errorf(
				"QDRANT-route-constructor (PR 3) regression: route %s %s returned 404 — "+
					"the route is either not registered, or the gin engine is rejecting "+
					"the (method, path) pair. The pre-PR-3 post-Setup setter pattern "+
					"produced this exact symptom.",
				w.method, w.path,
			)
		}
	}
}
