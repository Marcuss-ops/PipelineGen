package httpserver

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"
)

// TestNewServerWithHealth_CfgBranch_RegistersQdrantHealthRoutes gates
// the cfg != nil branch of api.NewServerWithHealth — the only path
// traversed by app.BuildServer at every cmd/server boot.
//
// Regression: the cfg != nil branch wired outbox + mediasearch +
// /health + /ready + /models handlers, but skipped
// SetQdrantHealthHandler. Routes were registered only via the cfg=nil
// fallback branch (the helper used by tests that elided *config.Config
// fixtures). In production the two routes were mounted by neither path,
// so /qdrant/live and /qdrant/ready silently 404'd when Qdrant was
// enabled.
//
// Test surface:
//  1. STRUCTURAL: the engine's Routes() table contains
//     GET /qdrant/live and GET /qdrant/ready.
//  2. BEHAVIOURAL: a request to each path returns a status other than
//  404. transport.NewQdrantHealthHandler(nil) returns 503 with a
//     structured JSON body (port==nil short-circuit) — 503 != 404 means
//     the route is live and reachable.
//
// Both assertions would have failed before the cfg-branch wiring was
// added. The test is symmetric to
// TestNewServerWithHealth_RegistersOutboxAndMediaSearchRoutes_ProductionShape
// (production-shape gate after constructor-time wiring) and is the
// matching load-bearing assertion for the Qdrant health surface.
func TestNewServerWithHealth_CfgBranch_RegistersQdrantHealthRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dataDir := t.TempDir()
	downloadDir := filepath.Join(dataDir, "downloads")

	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:         "127.0.0.1",
			Port:         0,
			GinMode:      gin.TestMode,
			ReadTimeout:  1,
			WriteTimeout: 1,
		},
		Storage: config.StorageConfig{
			DataDir: dataDir,
		},
		Security: config.SecurityConfig{
			EnableAuth:       false,
			RateLimitEnabled: false,
		},
		GoogleAccounting: config.GoogleAccountingConfig{
			DownloadDir: downloadDir,
		},
	}

	server := api.NewServerWithHealth(api.ServerDeps{
		Config:       cfg,
		QdrantHealth: transport.NewQdrantHealthHandler(nil),
	})

	engine := server.GetRouter()

	want := []struct {
		method, path string
	}{
		{"GET", "/qdrant/live"},
		{"GET", "/qdrant/ready"},
	}

	// (1) Structural assertion: the engine's Routes() table has both
	// routes. The pre-fix code path skipped SetQdrantHealthHandler in
	// this branch, so engine.Routes() never reported the paths.
	have := make(map[string]bool, len(engine.Routes()))
	for _, r := range engine.Routes() {
		have[r.Method+" "+r.Path] = true
	}
	for _, w := range want {
		key := w.method + " " + w.path
		if !have[key] {
			t.Errorf(
				"QdrantHealth wiring regression: route %q must be registered "+
					"via NewServerWithHealth's cfg != nil branch "+
					"(the production path used by app.BuildServer). "+
					"If this fires, server.go regressed to skipping "+
					"SetQdrantHealthHandler in the cfg != nil branch, so "+
					"/qdrant/live and /qdrant/ready will silently 404 in production.",
				key,
			)
		}
	}

	// (2) Behavioural assertion: a request to each path must NOT 404.
	// The nil-port handler returns 503 with a JSON body; 503 != 404
	// (a route that gin has not mounted returns 404 verbatim).
	for _, w := range want {
		req := httptest.NewRequest(w.method, w.path, nil)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound {
			t.Errorf(
				"QdrantHealth wiring regression: route %s %s returned 404 — "+
					"the cfg != nil branch is not registering it; gin is "+
					"treating the path as unmounted.",
				w.method, w.path,
			)
		}
	}
}
