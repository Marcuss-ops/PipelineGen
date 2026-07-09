// §10 Feature Flags verification (PR-FEATURE-FLAGS-VERIFY-2026-07-09).
//
// Hermetic TDD tests that pin the wired-vs-unwired behavior contract for
// the three feature flags:
//
//   - ArtlistEnabled:  route-level gate (module not registered when disabled)
//   - ScriptDocsEnabled: route-level gate + nil-port 503 when enabled but
//     ReActPort not wired
//   - QdrantEnabled:   composition-time compatibility gate (not per-request)
//
// Each test function exercises ONE behavioral invariant. The test names
// follow the pattern TestFeatureFlag_{Flag}_{Scenario}_ExpectedStatus.
//
// godlike/06 SSOT: these tests probe the ACTUAL handler + module behavior
// via httptest.NewRecorder — zero live-stack dependency, zero config file
// dependency, zero composition-root dependency.
//
// godlike/07 NO-FAKE-AVAILABILITY: every assertion probes a falsifiable
// invariant that a future refactor cannot silently break.
package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	scriptdocs "github.com/Marcuss-ops/PipelineGen/internal/api/script-docs"
	"go.uber.org/zap"
)

// ─── ArtlistEnabled: module-level gate ─────────────────────────────

// TestFeatureFlag_Artlist_ModuleEnabled_WhenFlagTrue asserts that
// a RouteModule with EnabledFunc returning true reports Enabled().
func TestFeatureFlag_Artlist_ModuleEnabled_WhenFlagTrue(t *testing.T) {
	mod := module.NewRouteModule(
		"artlist",
		func() bool { return true },
		"/artlist",
		&noopHandler{},
		zap.NewNop(),
	)
	assert.True(t, mod.Enabled(),
		"module must report Enabled()=true when EnabledFunc returns true")
}

// TestFeatureFlag_Artlist_ModuleDisabled_WhenFlagFalse asserts that
// a RouteModule with EnabledFunc returning false reports !Enabled().
func TestFeatureFlag_Artlist_ModuleDisabled_WhenFlagFalse(t *testing.T) {
	mod := module.NewRouteModule(
		"artlist",
		func() bool { return false },
		"/artlist",
		&noopHandler{},
		zap.NewNop(),
	)
	assert.False(t, mod.Enabled(),
		"module must report Enabled()=false when EnabledFunc returns false")
}

// TestFeatureFlag_Artlist_ModuleDisabled_RegisterRoutesStillMounts asserts
// the separation-of-concerns contract: RegisterRoutes always mounts routes
// regardless of Enabled() state. The registry's GetEnabled() is the actual
// gate — modules should NOT self-censor in RegisterRoutes.
func TestFeatureFlag_Artlist_ModuleDisabled_RegisterRoutesStillMounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	apiGroup := r.Group("/api")

	mod := module.NewRouteModule(
		"artlist",
		func() bool { return false },
		"/artlist",
		&noopHandler{},
		zap.NewNop(),
	)

	mod.RegisterRoutes(apiGroup)

	req, _ := http.NewRequest("GET", "/api/artlist/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code,
		"RegisterRoutes always mounts; the registry uses Enabled() to filter — this is by design")
}

// ─── ArtlistEnabled: per-request middleware (nil-flags only) ───────
//
// NOTE: the enabled/disabled ArtlistEnabled middleware tests are already
// canonically pinned in internal/api/middleware/middleware_feature_flags_test.go
// (TestFeatureFlagCheckerDisabled + TestFeatureFlagCheckerEnabled). The
// nil-flags case is the UNIQUE gap not covered by the existing tests.

// TestFeatureFlag_Artlist_Middleware_Blocked_NilFlags asserts that
// the ArtlistEnabled middleware returns 503 when flags port is nil.
func TestFeatureFlag_Artlist_Middleware_Blocked_NilFlags(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(middleware.ArtlistEnabled(nil))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"ArtlistEnabled middleware must return 503 when flags port is nil")
}

// ─── ScriptDocsEnabled: module-level gate ──────────────────────────

// TestFeatureFlag_ScriptDocs_ModuleEnabled_WhenFlagTrue asserts that
// a RouteModule with ScriptDocsEnabled returning true reports Enabled().
func TestFeatureFlag_ScriptDocs_ModuleEnabled_WhenFlagTrue(t *testing.T) {
	mod := module.NewRouteModule(
		"script-docs",
		func() bool { return true },
		"/script-docs",
		&noopHandler{},
		zap.NewNop(),
	)
	assert.True(t, mod.Enabled(),
		"script-docs module must report Enabled()=true when flag is true")
}

// TestFeatureFlag_ScriptDocs_ModuleDisabled_WhenFlagFalse asserts that
// a RouteModule with ScriptDocsEnabled returning false reports !Enabled().
func TestFeatureFlag_ScriptDocs_ModuleDisabled_WhenFlagFalse(t *testing.T) {
	mod := module.NewRouteModule(
		"script-docs",
		func() bool { return false },
		"/script-docs",
		&noopHandler{},
		zap.NewNop(),
	)
	assert.False(t, mod.Enabled(),
		"script-docs module must report Enabled()=false when flag is false")
}

// ─── ScriptDocsEnabled: per-request handler behavior ──────────────

// TestFeatureFlag_ScriptDocs_Returns503_WhenNilPort asserts that the
// ScriptDocs Generate handler returns 503 with ErrReActNotWired diagnostic
// when the ReActPort is nil (composition root hasn't wired it yet).
func TestFeatureFlag_ScriptDocs_Returns503_WhenNilPort(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := scriptdocs.NewHandler(nil, zap.NewNop())
	r := gin.New()
	r.POST("/api/script-docs/generate", handler.Generate)

	body := `{"topic":"test topic"}`
	req, _ := http.NewRequest("POST", "/api/script-docs/generate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"ScriptDocs handler must return 503 when ReActPort is nil")
	assert.Contains(t, w.Body.String(), "service_unavailable",
		"503 body must contain 'service_unavailable' error class")
	assert.Contains(t, w.Body.String(), "ReAct port is not wired",
		"503 body must contain ErrReActNotWired diagnostic message")
}

// TestFeatureFlag_ScriptDocs_Returns400_WhenEmptyTopic asserts that the
// ScriptDocs Generate handler returns 400 when topic is empty, even when
// the port is nil (validation fires before port check).
func TestFeatureFlag_ScriptDocs_Returns400_WhenEmptyTopic(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := scriptdocs.NewHandler(nil, zap.NewNop())
	r := gin.New()
	r.POST("/api/script-docs/generate", handler.Generate)

	body := `{"topic":""}`
	req, _ := http.NewRequest("POST", "/api/script-docs/generate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"ScriptDocs handler must return 400 when topic is empty (validation before port check)")
	assert.Contains(t, w.Body.String(), "topic is required",
		"400 body must name the missing field")
}

// TestFeatureFlag_ScriptDocs_Returns400_WhenMalformedBody asserts that the
// ScriptDocs Generate handler returns 400 when the JSON body is malformed.
func TestFeatureFlag_ScriptDocs_Returns400_WhenMalformedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := scriptdocs.NewHandler(nil, zap.NewNop())
	r := gin.New()
	r.POST("/api/script-docs/generate", handler.Generate)

	body := `{not json}`
	req, _ := http.NewRequest("POST", "/api/script-docs/generate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"ScriptDocs handler must return 400 when body is malformed JSON")
}

// TestFeatureFlag_ScriptDocs_Returns200_WhenPortWired asserts that the
// ScriptDocs Generate handler returns 200 when the ReActPort is wired
// and the call succeeds.
func TestFeatureFlag_ScriptDocs_Returns200_WhenPortWired(t *testing.T) {
	gin.SetMode(gin.TestMode)

	port := &stubReActPort{
		response: scriptdocs.ReActResponse{
			Result:     "test result",
			Status:     "ok",
			StepsTaken: 3,
		},
	}
	handler := scriptdocs.NewHandler(port, zap.NewNop())
	r := gin.New()
	r.POST("/api/script-docs/generate", handler.Generate)

	body := `{"topic":"test topic"}`
	req, _ := http.NewRequest("POST", "/api/script-docs/generate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code,
		"ScriptDocs handler must return 200 when ReActPort is wired and succeeds")
	assert.Contains(t, w.Body.String(), "test result",
		"200 body must contain the ReActResponse result")
	assert.Contains(t, w.Body.String(), `"steps_taken":3`,
		"200 body must contain steps_taken field")
}

// TestFeatureFlag_ScriptDocs_Returns500_WhenPortReturnsError asserts that
// the ScriptDocs Generate handler returns 500 (NOT 503) when the ReActPort
// is wired but returns an error. The distinction is critical: 503 = "not
// wired", 500 = "wired but broken".
func TestFeatureFlag_ScriptDocs_Returns500_WhenPortReturnsError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	port := &stubReActPort{
		err: assert.AnError,
	}
	handler := scriptdocs.NewHandler(port, zap.NewNop())
	r := gin.New()
	r.POST("/api/script-docs/generate", handler.Generate)

	body := `{"topic":"test topic"}`
	req, _ := http.NewRequest("POST", "/api/script-docs/generate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"ScriptDocs handler must return 500 when port is wired but returns error (NOT 503)")
	assert.Contains(t, w.Body.String(), "internal_error",
		"500 body must contain 'internal_error' error class")
}

// ─── ScriptDocsEnabled: middleware gate (nil-flags only) ───────────
//
// NOTE: the enabled/disabled ScriptDocsEnabled middleware tests are already
// canonically pinned in internal/api/middleware/middleware_feature_flags_test.go.
// The nil-flags case is the UNIQUE gap.

// TestFeatureFlag_ScriptDocs_Middleware_Blocked_NilFlags asserts that
// the ScriptDocsEnabled middleware returns 503 when flags port is nil.
func TestFeatureFlag_ScriptDocs_Middleware_Blocked_NilFlags(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(middleware.ScriptDocsEnabled(nil))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"ScriptDocsEnabled middleware must return 503 when flags port is nil")
}

// ─── QdrantEnabled: composition-time gate ──────────────────────────
//
// The QdrantEnabled flag is NOT a per-request feature flag. It is a
// composition-time compatibility gate that validates (Qdrant.Enabled,
// ClipIndexer.Enabled) pair consistency at boot. The canonical 4 TDD
// tests for this gate live in
// internal/app/build_bundles_qdrant_gates_test.go.

// TestFeatureFlag_Qdrant_GateExists_RegressionGuard is a structural
// regression guard that verifies the canonical Qdrant compatibility gate
// FILE exists on disk. If the file were deleted (e.g. during a god-object
// split), this test fails and the operator is warned.
func TestFeatureFlag_Qdrant_GateExists_RegressionGuard(t *testing.T) {
	// Locate the canonical gate file relative to the repo root.
	// runtime.Caller gives us this test file's path; walk up to go.mod.
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller must succeed")

	// Walk up from internal/api/ to repo root (3 levels: api -> internal -> root).
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	gateFile := filepath.Join(repoRoot, "internal", "app", "build_bundles_qdrant_gates.go")

	_, err := os.Stat(gateFile)
	require.NoError(t, err, "canonical Qdrant gate file must exist at internal/app/build_bundles_qdrant_gates.go")

	// Also verify the test file exists.
	testFile := filepath.Join(repoRoot, "internal", "app", "build_bundles_qdrant_gates_test.go")
	_, err = os.Stat(testFile)
	require.NoError(t, err, "canonical Qdrant gate test file must exist at internal/app/build_bundles_qdrant_gates_test.go")

	// Verify the gate file contains the canonical helper function name.
	content, err := os.ReadFile(gateFile)
	require.NoError(t, err, "must be able to read the gate file")
	assert.Contains(t, string(content), "validateQdrantIndexerCompatibility",
		"gate file must contain the canonical validateQdrantIndexerCompatibility helper")

	// Verify the test file contains the 4 canonical test functions.
	testContent, err := os.ReadFile(testFile)
	require.NoError(t, err, "must be able to read the gate test file")
	testStr := string(testContent)
	assert.Contains(t, testStr, "TestValidateQdrantIndexerCompatibility_NilCfg_ReturnsError",
		"test file must contain nil-cfg test (canonical TDD case 1)")
	assert.Contains(t, testStr, "TestValidateQdrantIndexerCompatibility_BothDisabled_ReturnsNil",
		"test file must contain both-disabled test (canonical TDD case 2)")
	assert.Contains(t, testStr, "TestValidateQdrantIndexerCompatibility_BothEnabled_ReturnsNil",
		"test file must contain both-enabled test (canonical TDD case 3)")
	assert.Contains(t, testStr, "TestValidateQdrantIndexerCompatibility_QdrantEnabledNoClipIndexer_FailsClosed",
		"test file must contain the RED POINT test (canonical TDD case 4)")
}

// ─── FeatureFlagChecker: generic factory ───────────────────────────

// TestFeatureFlag_GenericChecker_Returns503_WhenDisabled asserts that the
// generic FeatureFlagChecker factory produces middleware that returns 503
// with the correct module name when isEnabled=false.
func TestFeatureFlag_GenericChecker_Returns503_WhenDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(middleware.FeatureFlagChecker("TestModule", false))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "module disabled",
		"503 body must contain 'module disabled'")
	assert.Contains(t, w.Body.String(), "testmodule",
		"503 body must contain lowercase module name")
}

// TestFeatureFlag_GenericChecker_Passes_WhenEnabled asserts that the
// generic FeatureFlagChecker factory produces middleware that passes
// through when isEnabled=true.
func TestFeatureFlag_GenericChecker_Passes_WhenEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(middleware.FeatureFlagChecker("TestModule", true))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ─── Registry: GetEnabled filters by flag ─────────────────────────

// TestFeatureFlag_Registry_GetEnabled_FiltersByFlag asserts that the
// module registry's GetEnabled() only returns modules whose Enabled()
// closure returns true.
func TestFeatureFlag_Registry_GetEnabled_FiltersByFlag(t *testing.T) {
	registry := module.NewRegistry()

	enabled := module.NewRouteModule("enabled-mod", func() bool { return true }, "/enabled", &noopHandler{}, zap.NewNop())
	disabled := module.NewRouteModule("disabled-mod", func() bool { return false }, "/disabled", &noopHandler{}, zap.NewNop())

	require.NoError(t, registry.Register(enabled))
	require.NoError(t, registry.Register(disabled))

	enabledModules := registry.GetEnabled()
	require.Len(t, enabledModules, 1, "GetEnabled must return only enabled modules")
	assert.Equal(t, "enabled-mod", enabledModules[0].Name())
}

// TestFeatureFlag_Registry_RegisterAllRoutes_SkipsDisabled asserts that
// RegisterAllRoutes only mounts routes for enabled modules.
func TestFeatureFlag_Registry_RegisterAllRoutes_SkipsDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	registry := module.NewRegistry()

	enabledMod := module.NewRouteModule("enabled-mod", func() bool { return true }, "/enabled", &noopHandler{}, zap.NewNop())
	disabledMod := module.NewRouteModule("disabled-mod", func() bool { return false }, "/disabled", &noopHandler{}, zap.NewNop())

	require.NoError(t, registry.Register(enabledMod))
	require.NoError(t, registry.Register(disabledMod))

	r := gin.New()
	apiGroup := r.Group("/api")
	registry.RegisterAllRoutes(apiGroup)

	// Enabled module's route should respond.
	reqEnabled, _ := http.NewRequest("GET", "/api/enabled/test", nil)
	wEnabled := httptest.NewRecorder()
	r.ServeHTTP(wEnabled, reqEnabled)
	assert.Equal(t, http.StatusOK, wEnabled.Code,
		"enabled module's route must be mounted")

	// Disabled module's route should NOT respond (404).
	reqDisabled, _ := http.NewRequest("GET", "/api/disabled/test", nil)
	wDisabled := httptest.NewRecorder()
	r.ServeHTTP(wDisabled, reqDisabled)
	assert.Equal(t, http.StatusNotFound, wDisabled.Code,
		"disabled module's route must NOT be mounted (404)")
}

// ─── Registry: nil EnabledFunc falls back to handler != nil ───────

// TestFeatureFlag_Module_NilEnabledFunc_FallsBackToHandler asserts that
// when EnabledFunc is nil, Enabled() returns handler != nil.
func TestFeatureFlag_Module_NilEnabledFunc_FallsBackToHandler(t *testing.T) {
	withHandler := module.NewRouteModule("with-handler", nil, "/test", &noopHandler{}, zap.NewNop())
	assert.True(t, withHandler.Enabled(),
		"nil EnabledFunc + non-nil handler → Enabled()=true")

	withoutHandler := module.NewRouteModule("without-handler", nil, "/test", nil, zap.NewNop())
	assert.False(t, withoutHandler.Enabled(),
		"nil EnabledFunc + nil handler → Enabled()=false")
}

// ─── Stubs ────────────────────────────────────────────────────────

// noopHandler implements RegisterRoutes for test purposes.
type noopHandler struct{}

func (h *noopHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
}

// stubReActPort implements scriptdocs.ReActPort for tests.
type stubReActPort struct {
	response scriptdocs.ReActResponse
	err      error
}

// Compile-time pin: stubReActPort must satisfy scriptdocs.ReActPort.
// Catches future port signature drift at build time (Pattern 0 discipline).
var _ scriptdocs.ReActPort = (*stubReActPort)(nil)

func (p *stubReActPort) Generate(_ context.Context, _ scriptdocs.ReActRequest) (scriptdocs.ReActResponse, error) {
	return p.response, p.err
}
