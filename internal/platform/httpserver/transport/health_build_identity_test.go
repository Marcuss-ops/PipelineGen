package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/buildinfo"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedBuildIdentity is the tuple a canary compares against. It is fixed so
// the assertion is about the WIRE contract, not about this machine's build.
func fixedBuildIdentity() buildinfo.Identity {
	return buildinfo.Identity{
		Version:      "1.2.3",
		GitCommit:    "a1b2c3d4e5f6",
		BuildTime:    "2026-09-17T09:00:00Z",
		BinarySHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		ConfigPath:   "/etc/pipelinegen/pipelinegen.yaml",
		Mode:         "all",
		WorkerID:     "canary-1",
		PID:          4242,
		StartedAt:    "2026-09-17T09:00:01Z",
		IdentityHash: "deadbeefdeadbeef",
	}
}

func buildIdentityHandler(t *testing.T) *HealthHandler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := healthyService()
	handler := NewHealthHandler(svc, systemhealth.NewReadyChecker(svc))
	handler.SetBuildInfo(fixedBuildIdentity)
	return handler
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

// TestHealth_PublishesBuildIdentity is the acceptance criterion for the
// "which binary am I running?" question: the fast liveness probe must carry
// git_commit, binary_sha256, build_time, config_path and worker_id, so one
// curl answers it without inspecting systemd units or binary mtimes.
func TestHealth_PublishesBuildIdentity(t *testing.T) {
	router := gin.New()
	router.GET("/health", buildIdentityHandler(t).Health)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	resp := decodeBody(t, w)

	build, ok := resp["build"].(map[string]any)
	require.True(t, ok, "/health must publish a build object: %v", resp)
	assert.Equal(t, "a1b2c3d4e5f6", build["git_commit"])
	assert.Equal(t, "1.2.3", build["version"])
	assert.Equal(t, "2026-09-17T09:00:00Z", build["build_time"])
	assert.Equal(t, "/etc/pipelinegen/pipelinegen.yaml", build["config_path"])
	assert.Equal(t, "canary-1", build["worker_id"])
	assert.Len(t, build["binary_sha256"], 64, "binary_sha256 must be a full digest")

	// The fast-liveness contract is unchanged: identity is additive.
	_, hasChecks := resp["checks"]
	assert.False(t, hasChecks, "identity must not turn fast liveness into a deep check")
	assert.True(t, resp["ok"].(bool))
}

// TestHealth_DeepAndUnhealthyStillCarryIdentity keeps the identity on every
// health shape, so a script never has to guess which probe variant carries it.
func TestHealth_DeepAndUnhealthyStillCarryIdentity(t *testing.T) {
	handler := buildIdentityHandler(t)
	router := gin.New()
	router.GET("/health", handler.Health)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health?deep=true", nil))
	require.Equal(t, http.StatusOK, w.Code)
	_, ok := decodeBody(t, w)["build"].(map[string]any)
	assert.True(t, ok, "deep /health must carry the build identity")

	failing := failingService()
	broken := NewHealthHandler(failing, systemhealth.NewReadyChecker(failing))
	broken.SetBuildInfo(fixedBuildIdentity)
	brokenRouter := gin.New()
	brokenRouter.GET("/health", broken.Health)

	w = httptest.NewRecorder()
	brokenRouter.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health?deep=true", nil))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	resp := decodeBody(t, w)
	_, ok = resp["build"].(map[string]any)
	assert.True(t, ok, "an unhealthy /health must still say WHICH binary is unhealthy")
}

// TestHealth_UnwiredBuildOmitsField documents the nil-safe default: fixtures
// that construct the handler directly keep the historical payload shape.
func TestHealth_UnwiredBuildOmitsField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := healthyService()
	handler := NewHealthHandler(svc, systemhealth.NewReadyChecker(svc))

	router := gin.New()
	router.GET("/health", handler.Health)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	_, hasBuild := decodeBody(t, w)["build"]
	assert.False(t, hasBuild, "an unwired build accessor must not emit an empty identity")
}

// TestReady_PublishesBuildIdentity is what the certifier polls: readiness and
// identity must be answered by the SAME request, otherwise waiting for a
// deploy to land takes two probes that can disagree.
func TestReady_PublishesBuildIdentity(t *testing.T) {
	router := gin.New()
	router.GET("/ready", buildIdentityHandler(t).Ready)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ready", nil))

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeBody(t, w)

	build, ok := resp["build"].(map[string]any)
	require.True(t, ok, "/ready must publish a build object: %v", resp)
	assert.Equal(t, "a1b2c3d4e5f6", build["git_commit"])
	assert.Equal(t, "deadbeefdeadbeef", build["identity_hash"])
	_, hasWire := resp["wire"]
	assert.True(t, hasWire, "the wire field contract must be preserved")
	assert.Equal(t, "ready", resp["status"])
}

// TestReady_503StillCarriesIdentity: a not-ready process must still be
// identifiable, otherwise "the canary never became ready" is undiagnosable.
func TestReady_503StillCarriesIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewHealthHandler(healthyService(), nil)
	handler.SetBuildInfo(fixedBuildIdentity)

	router := gin.New()
	router.GET("/ready", handler.Ready)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ready", nil))

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	build, ok := decodeBody(t, w)["build"].(map[string]any)
	require.True(t, ok, "503 /ready must still identify the binary")
	assert.Equal(t, "a1b2c3d4e5f6", build["git_commit"])
}
