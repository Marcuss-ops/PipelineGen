package httpserver

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"
)

// TestNewServerWithHealth_CfgBranch_WiresModelsEndpoint is the
// production-shaped gate for /models wiring in NewServerWithHealth
// when a real *config.Config and a non-empty ModelsSidecarURL are
// supplied — mirroring how BuildServer plumbs cfg.ClipIndexer.ServerURL
// into ServerDeps.ModelsSidecarURL (the production call site).
//
// Regression: the cfg != nil branch of NewServerWithHealth gates
// router.SetModelsHandler on `deps.ModelsSidecarURL != ""`. As long
// as BuildServer passes the canonical sidecar URL through, /models
// probes the sidecar; if BuildServer regresses to leaving the field
// zero-valued, /models stays permanently in the canonical
// "models sidecar not configured" 200 JSON response.
//
// Test surface:
//  1. STRUCTURAL: engine.Routes() contains GET /models.
//  2. BEHAVIOURAL: GET /models returns != 404. With a closed-port
//     sidecar URL, transport.ModelsHandler responds 200 with per-model
//     ok=false (JSON envelope with an error message). 200 != 404
//     means the route is live and reachable.
//
// The test mirrors TestNewServerWithHealth_CfgBranch_RegistersQdrantHealthRoutes
// (file internal/api/server_qdrant_health_test.go), so the load-bearing
// assertion is the same shape: if NewServerWithHealth regresses to
// skipping SetModelsHandler in the cfg != nil branch, the route vanishes
// from engine.Routes() and the test fails with a clear root cause.
func TestNewServerWithHealth_CfgBranch_WiresModelsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dataDir := t.TempDir()
	downloadDir := filepath.Join(dataDir, "downloads")

	// Closed-port sidecar URL keeps ModelsHandler.probeModel deterministic
	// and bounded: the httpClient.Post fails at TCP-connect immediately
	// (RST) on most platforms, avoiding the 15s transport timeout per
	// probe that would otherwise dominate the test runtime.
	const sidecarURL = "http://127.0.0.1:1"

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
		ClipIndexer: config.ClipIndexerConfig{
			Enabled:   true,
			ServerURL: sidecarURL,
		},
	}

	server := NewServerWithHealth(ServerDeps{
		Config:           cfg,
		ModelsSidecarURL: cfg.ClipIndexer.ServerURL,
	})

	engine := server.GetRouter()

	// (1) Structural assertion: the engine's Routes() table has the
	// route. The pre-fix code path skips SetModelsHandler when
	// ModelsSidecarURL is empty in cfg != nil, so missing the field
	// here is the canonical regression symptom.
	have := make(map[string]bool, len(engine.Routes()))
	for _, r := range engine.Routes() {
		have[r.Method+" "+r.Path] = true
	}
	if !have["GET /models"] {
		t.Errorf(
			"ModelsSidecarURL wiring regression: route %q must be registered "+
				"via NewServerWithHealth's cfg != nil branch when "+
				"ModelsSidecarURL is non-empty. If this fires, the cfg-branch "+
				"wiring in server.go regressed to skipping SetModelsHandler, "+
				"or BuildServer regressed on plumbing cfg.ClipIndexer.ServerURL "+
				"into ServerDeps.ModelsSidecarURL — /models would stay "+
				"permanently in the 'models sidecar not configured' 200 JSON.",
			"GET /models",
		)
	}

	// (2) Behavioural assertion: GET /models must NOT 404. With the
	// closed-port sidecar URL, transport.ModelsHandler responds 200
	// with ok=false and per-model error text; 200 != 404 confirms the
	// route is reachable through the gin engine.
	req := httptest.NewRequest("GET", "/models", nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotFound {
		t.Errorf(
			"ModelsSidecarURL wiring regression: GET /models returned 404 — " +
				"the route is unmounted. BuildServer (or the cfg != nil wiring " +
				"in server.go) regressed; plumb cfg.ClipIndexer.ServerURL into " +
				"ServerDeps.ModelsSidecarURL.",
		)
	}
}
