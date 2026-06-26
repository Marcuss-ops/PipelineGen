// Feature flag middleware tests (PG-006, June 2026).
//
// Previously these tests constructed `&config.Config{Features:
// config.FeaturesConfig{...}}` literals to drive the ArtlistEnabled
// per-feature gate. With the typed-port cascade, the package no longer
// imports `internal/platform/config` — the testFlags stub from
// port_fakes_test.go (a 3-method FeatureFlagsPort fake) replaces the
// config literal.
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestFeatureFlagCheckerDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	flags := &testFlags{artlist: false}
	r := gin.New()
	r.Use(ArtlistEnabled(flags))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
	}
}

func TestFeatureFlagCheckerEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	flags := &testFlags{artlist: true}
	r := gin.New()
	r.Use(ArtlistEnabled(flags))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}
}
