// §10 Feature Flags verification (PR-FEATURE-FLAGS-VERIFY-2026-07-09).
//
// Hermetic TDD tests that pin the wired-vs-unwired behavior contract for
// the feature flags:
//
//   - ArtlistEnabled:  route-level gate (module not registered when disabled)
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
package httpserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
)

// ─── ArtlistEnabled: module-level gate ─────────────────────────────

// TestFeatureFlag_Artlist_ModuleEnabled_WhenFlagTrue asserts that
// a RouteModule with EnabledFunc returning true reports Enabled().
func TestFeatureFlag_Artlist_ModuleEnabled_WhenFlagTrue(t *testing.T) {
	mod := NewRouteModule(
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
	mod := NewRouteModule(
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

	mod := NewRouteModule(
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
	// Walk up from internal/platform/httpserver/ to repo root.
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(thisFile))))
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
	registry := NewRegistry()

	enabled := NewRouteModule("enabled-mod", func() bool { return true }, "/enabled", &noopHandler{}, zap.NewNop())
	disabled := NewRouteModule("disabled-mod", func() bool { return false }, "/disabled", &noopHandler{}, zap.NewNop())

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

	registry := NewRegistry()

	enabledMod := NewRouteModule("enabled-mod", func() bool { return true }, "/enabled", &noopHandler{}, zap.NewNop())
	disabledMod := NewRouteModule("disabled-mod", func() bool { return false }, "/disabled", &noopHandler{}, zap.NewNop())

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
	withHandler := NewRouteModule("with-handler", nil, "/test", &noopHandler{}, zap.NewNop())
	assert.True(t, withHandler.Enabled(),
		"nil EnabledFunc + non-nil handler → Enabled()=true")

	withoutHandler := NewRouteModule("without-handler", nil, "/test", nil, zap.NewNop())
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
