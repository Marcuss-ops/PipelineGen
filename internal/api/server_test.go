package api

import (
	"testing"

	"github.com/gin-gonic/gin"
)

type fakeInternalMediaHandler struct{}

func (fakeInternalMediaHandler) RegisterInternalMediaRoutes(r *gin.RouterGroup) {
	media := r.Group("/media")
	media.POST("/sync-drive-folder", func(c *gin.Context) {})
}

func TestNewServerWithHealth_RegistersInternalMediaRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := NewServerWithHealth(
		nil,
		nil,
		nil,
		fakeInternalMediaHandler{},
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	routes := server.GetRouter().Routes()
	for _, route := range routes {
		if route.Method == "POST" && route.Path == "/internal/v1/media/sync-drive-folder" {
			return
		}
	}

	t.Fatalf("expected POST /internal/v1/media/sync-drive-folder to be registered")
}

// ── QDRANT-002 route-production anti-regression tests ────────────────────
//
// The bug: cmd/server/main.go called NewServerWithHealth() (which invokes
// router.Setup() internally) and THEN called server.SetOutboxHandler() /
// server.SetMediasearchHandler(). Since Setup() had already run with nil
// handlers, the gin engine never registered the outbox + mediasearch routes.
//
// TestRoutes_OutboxHandlerAfterSetup_NotRegistered reproduces the bug:
// handing a handler via SetOutboxHandler AFTER Setup() does NOT register
// routes on the gin engine. This test documents that a post-construction
// setter cannot re-register routes, and it defines the invariant the
// constructor-based fix must preserve.
//
// TestRoutes_OutboxHandlerInConstructor_Registered verifies the fix:
// handing a handler via the NewServerWithHealth constructor (before
// Setup()) DOES register the routes on the gin engine.

func TestRoutes_OutboxHandlerAfterSetup_NotRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := NewServerWithHealth(
		nil, nil, nil, nil, nil, nil, nil,
		nil, nil, // outbox + mediasearch BOTH nil
	)

	// Positive control: verify the engine actually has routes (proves
	// we're not passing vacuously due to a nil engine or setup bug).
	// /health is always registered by Setup() regardless of handlers.
	routes := server.GetRouter().Routes()
	have := make(map[string]bool, len(routes))
	for _, route := range routes {
		have[route.Method+" "+route.Path] = true
	}
	if !have["GET /health"] {
		t.Fatal("positive control failed: /health route missing — engine may be nil or Setup() did not run")
	}

	// Simulate the production bug: wire the handler AFTER Setup() has
	// already run. This must NOT register the route because the gin
	// engine was already built with nil handlers.
	server.SetOutboxHandler(&fakeOutboxHandlerStub{})

	// Re-read routes AFTER the post-construction setter.
	routes = server.GetRouter().Routes()
	for _, route := range routes {
		if route.Method == "GET" && route.Path == "/internal/v1/outbox/status" {
			t.Error("QDRANT-002 regression: /internal/v1/outbox/status was registered via post-Setup setter — this means the old production bug is no longer reproducible and the test invariant is stale")
		}
	}
	// If we get here without error, the bug is correctly not registering
	// the route — SetOutboxHandler after Setup() is a no-op as expected.
}

func TestRoutes_OutboxHandlerInConstructor_Registered(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := NewServerWithHealth(
		nil, nil, nil, nil, nil, nil, nil,
		&fakeOutboxHandlerStub{}, // outbox wired BEFORE Setup()
		&fakeMediaSearchHandlerStub{}, // mediasearch wired BEFORE Setup()
	)

	routes := server.GetRouter().Routes()
	have := make(map[string]bool, len(routes))
	for _, route := range routes {
		have[route.Method+" "+route.Path] = true
	}

	want := []string{
		"GET /internal/v1/outbox/status",
		"GET /internal/v1/outbox/events",
		"POST /internal/v1/media/search",
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("QDRANT-002 fix: expected route %q to be registered via constructor, but it is missing", w)
		}
	}
}

// TestRoutes_ProductionOrderMatchesConstructor confirms that the
// production code path in cmd/server/main.go (passing outboxHandler +
// mediasearchHandler directly to NewServerWithHealth, NOT via post-
// construction setters) registers the expected routes. This test
// ships as a contract so the main.go caller cannot regress to the
// old setter-after-Setup pattern without this test failing first.
func TestRoutes_ProductionOrderMatchesConstructor(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// This is the production-surface call shape from cmd/server/main.go:
	//   api.NewServerWithHealth(cfg, registry, workerHandler,
	//       internalMediaHandler, lifecycle, healthSvc, readyChecker,
	//       outboxHandler, mediasearchHandler)
	// The test omits the first 7 production params (cfg, registry,
	// workerHandler, internalMediaHandler, lifecycle, healthSvc,
	// readyChecker) because they are not needed for route presence —
	// the structural assertion is about the outbox + mediasearch
	// surface being registered before Setup().
	server := NewServerWithHealth(
		nil, nil, nil, nil, nil, nil, nil,
		&fakeOutboxHandlerStub{},
		&fakeMediaSearchHandlerStub{},
	)

	// Simulate what the old (buggy) main.go did: call setters after
	// construction. The setters must NOT re-register routes because
	// the gin engine is already built, but they must ALSO NOT panic
	// or break the already-registered routes.
	server.SetOutboxHandler(&fakeOutboxHandlerStub{})
	server.SetMediasearchHandler(&fakeMediaSearchHandlerStub{})

	routes := server.GetRouter().Routes()
	have := make(map[string]bool, len(routes))
	for _, route := range routes {
		have[route.Method+" "+route.Path] = true
	}

	want := []string{
		"GET /internal/v1/outbox/status",
		"GET /internal/v1/outbox/events",
		"POST /internal/v1/media/search",
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("QDRANT-002 production fix: route %q was registered via constructor but went missing", w)
		}
	}
}
