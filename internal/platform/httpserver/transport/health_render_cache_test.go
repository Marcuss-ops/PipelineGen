package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/rendercacheinfo"
)

// fixedRenderCacheStatus is a fully wired cache snapshot: the document a
// canary compares to prove the process it certified is actually reusing
// certified overlay renders under the expected deployment epoch.
func fixedRenderCacheStatus() rendercacheinfo.Status {
	return rendercacheinfo.Status{
		Enabled: true,
		Wired:   true,
		Epoch:   "a1b2c3d4e5f6@2026-10-23T19:00:00Z",
		Table:   rendercacheinfo.TableName,
	}
}

func renderCacheHandler(t *testing.T, status func() rendercacheinfo.Status) *HealthHandler {
	t.Helper()
	handler := buildIdentityHandler(t)
	if status != nil {
		handler.SetRenderCacheStatus(status)
	}
	return handler
}

// TestHealth_PublishesRenderCacheStatus pins the operational contract: one
// fast liveness poll reports whether the overlay render cache is enabled and
// wired, under which epoch, and which table backs it — without grepping the
// startup log.
func TestHealth_PublishesRenderCacheStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := renderCacheHandler(t, fixedRenderCacheStatus)
	router := gin.New()
	router.GET("/health", handler.Health)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeBody(t, w)
	cache, ok := resp["render_cache"].(map[string]any)
	require.True(t, ok, "/health must publish a render_cache object: %v", resp)
	assert.Equal(t, true, cache["enabled"])
	assert.Equal(t, true, cache["wired"])
	assert.Equal(t, "a1b2c3d4e5f6@2026-10-23T19:00:00Z", cache["epoch"])
	assert.Equal(t, rendercacheinfo.TableName, cache["table"])
	// The stable document always carries reason, so "wired" and "why not"
	// share one shape.
	_, hasReason := cache["reason"]
	assert.True(t, hasReason)
}

// TestReady_PublishesRenderCacheStatus: the canary that waits on /ready must
// also observe the reuse plane of the process it just certified.
func TestReady_PublishesRenderCacheStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := renderCacheHandler(t, fixedRenderCacheStatus)
	router := gin.New()
	router.GET("/ready", handler.Ready)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ready", nil))

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeBody(t, w)
	cache, ok := resp["render_cache"].(map[string]any)
	require.True(t, ok, "/ready must publish the render_cache object: %v", resp)
	assert.Equal(t, true, cache["wired"])
	assert.Equal(t, "a1b2c3d4e5f6@2026-10-23T19:00:00Z", cache["epoch"])
}

// TestHealth_RenderCacheDisabledReportsWhy covers the failure mode this field
// exists to make visible: the cache is off (kill switch or missing cache DB)
// and the reason is on the wire instead of only in a log line.
func TestHealth_RenderCacheDisabledReportsWhy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := renderCacheHandler(t, func() rendercacheinfo.Status {
		return rendercacheinfo.Status{
			Enabled: false,
			Wired:   false,
			Reason:  rendercacheinfo.ReasonKillSwitch,
		}
	})
	router := gin.New()
	router.GET("/health", handler.Health)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	require.Equal(t, http.StatusOK, w.Code)
	cache, ok := decodeBody(t, w)["render_cache"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, cache["enabled"])
	assert.Equal(t, false, cache["wired"])
	assert.Equal(t, rendercacheinfo.ReasonKillSwitch, cache["reason"])
}

// TestHealth_UnwiredRenderCacheOmitsField keeps direct fixtures on the
// historical payload shape, exactly like the build-identity accessor.
func TestHealth_UnwiredRenderCacheOmitsField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := renderCacheHandler(t, nil)
	router := gin.New()
	router.GET("/health", handler.Health)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	_, hasCache := decodeBody(t, w)["render_cache"]
	assert.False(t, hasCache, "an unwired cache accessor must not publish an invented status")
}
