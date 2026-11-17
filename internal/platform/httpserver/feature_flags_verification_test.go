// §10 Feature Flags verification (PR-FEATURE-FLAGS-VERIFY-2026-07-09).
//
// Hermetic TDD tests that pin the wired-vs-unwired behavior contract for
// the feature flags:
//
//   - ScriptClipsEnabled:  route-level gate (module not registered when disabled)
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
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
)

// ─── ScriptClipsEnabled: module-level gate ─────────────────────────────

// TestFeatureFlag_ScriptClips_ModuleEnabled_WhenFlagTrue asserts that
// a RouteModule with EnabledFunc returning true reports Enabled().
func TestFeatureFlag_ScriptClips_ModuleEnabled_WhenFlagTrue(t *testing.T) {
	mod := NewRouteModule(
		"script-clips",
		func() bool { return true },
		"/script-clips",
		&noopHandler{},
		zap.NewNop(),
	)
	assert.True(t, mod.Enabled(),
		"module must report Enabled()=true when EnabledFunc returns true")
}

// TestFeatureFlag_ScriptClips_ModuleDisabled_WhenFlagFalse asserts that
// a RouteModule with EnabledFunc returning false reports !Enabled().
func TestFeatureFlag_ScriptClips_ModuleDisabled_WhenFlagFalse(t *testing.T) {
	mod := NewRouteModule(
		"script-clips",
		func() bool { return false },
		"/script-clips",
		&noopHandler{},
		zap.NewNop(),
	)
	assert.False(t, mod.Enabled(),
		"module must report Enabled()=false when EnabledFunc returns false")
}

// TestFeatureFlag_ScriptClips_ModuleDisabled_RegisterRoutesStillMounts asserts
// the separation-of-concerns contract: RegisterRoutes always mounts routes
// regardless of Enabled() state. The registry's GetEnabled() is the actual
// gate — modules should NOT self-censor in RegisterRoutes.
func TestFeatureFlag_ScriptClips_ModuleDisabled_RegisterRoutesStillMounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	apiGroup := r.Group("/api")

	mod := NewRouteModule(
		"script-clips",
		func() bool { return false },
		"/script-clips",
		&noopHandler{},
		zap.NewNop(),
	)

	mod.RegisterRoutes(apiGroup)

	req, _ := http.NewRequest("GET", "/api/script-clips/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code,
		"RegisterRoutes always mounts; the registry uses Enabled() to filter — this is by design")
}

// ─── ScriptClipsEnabled: per-request middleware (nil-flags only) ───────
//
// NOTE: the enabled/disabled ScriptClipsEnabled middleware tests are already
// canonically pinned in internal/platform/httpserver/middleware/middleware_feature_flags_test.go
// (TestFeatureFlagCheckerDisabled + TestFeatureFlagCheckerEnabled). The
// nil-flags case is the UNIQUE gap not covered by the existing tests.

// TestFeatureFlag_ScriptClips_Middleware_Blocked_NilFlags asserts that a
// feature-gate middleware returns 503 when the flags port is nil, so a missing
// feature source can never be mistaken for "enabled".
func TestFeatureFlag_ScriptClips_Middleware_Blocked_NilFlags(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(middleware.ScriptClipsEnabled(nil))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"feature-flag middleware must return 503 when the flags port is nil")
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
