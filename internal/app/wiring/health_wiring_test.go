//go:build !integration
// +build !integration

package wiring

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// TestProductionHealthWiring_ReadyCheckerIsNilForWireMinimal documents
// that WireMinimal returns nil ReadyChecker (no composition root).
// The full production wiring (WireServices) always produces a non-nil
// ReadyChecker via root.Utility.ReadyChecker → AppDeps.ReadyChecker.
func TestProductionHealthWiring_ReadyCheckerIsNilForWireMinimal(t *testing.T) {
	// Change working directory to the project root so the
	// hardcoded relative path "scripts/bridges/whisper_transcriber.py"
	// inside NewWhisperTranscriberAdapter resolves successfully.
	// The adapter fails-closed (godlike/07 no-fake-availability) on
	// os.Stat failure, so the test's working directory MUST be the
	// repo root (NOT internal/app/ where `go test` runs by default).
	// t.Cleanup restores the original cwd so subsequent tests
	// in this package are not poisoned.
	origDir, _ := os.Getwd()
	if err := os.Chdir("../.."); err != nil {
		t.Fatalf("chdir to project root: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	cfg := &config.Config{
		Server: config.ServerConfig{
			Host: "127.0.0.1", Port: 0, GinMode: "test",
			ReadTimeout: 30000, WriteTimeout: 30000,
		},
		Storage: config.StorageConfig{
			DataDir: t.TempDir(),
		},
		Security: config.SecurityConfig{},
		// pythontransformer fail-closed posture (godlike/07
		// no-fake-availability): NewSubprocessTransformer
		// panics on empty cfg.Books.ScriptPath, empty
		// cfg.Books.PythonBin, OR on cfg.Books.Enabled=false.
		// The fixture wires all three fields so WireMinimal
		// can complete composition without triggering the
		// fail-closed gate. The books.Service.enabled flag
		// remains the runtime per-request gate; the test
		// never invokes the books service so the
		// Enabled=true value is a no-op at test time.
		Books: config.BooksConfig{
			ScriptPath: "scripts/bridges/book_summarizer.py",
			PythonBin:  "python3",
			Enabled:    true,
		},
		// texttracks.NewMaterializer fail-closed posture (godlike/07
		// no-fake-availability): rejects empty SourceLanguage AND
		// empty Languages. The fixture sets both to their
		// canonical defaults (matches the config yaml defaults at
		// internal/platform/config) so WireMinimal can
		// complete composition without triggering the fail-closed
		// gates. The test never exercises the translation path, so
		// these values are purely non-empty sentinels. A future
		// refactor (see code-reviewer-minimax-m3 feedback on this
		// commit) may add a `minimal bool` flag to NewComposition
		// so WireMinimal skips optional bundles entirely; today the
		// fixture walks the canonical-default surface.
		Media: config.MediaConfig{
			Multilingual: config.MultilingualConfig{
				SourceLanguage: "en",
				Languages: config.LanguageSpecSlice{
					{Code: "en", Enabled: true, TranslateClips: true, GenerateTTS: true},
				},
			},
		},
	}
	log := zap.NewNop()

	deps, err := WireMinimal(cfg, log, "")
	require.NoError(t, err)
	t.Cleanup(func() { deps.Runtime.Lifecycle.Stop(context.Background()) })

	// WireMinimal returns nil ReadyChecker (no composition root).
	assert.Nil(t, deps.Health.ReadyChecker, "WireMinimal should have nil ReadyChecker")
}

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
