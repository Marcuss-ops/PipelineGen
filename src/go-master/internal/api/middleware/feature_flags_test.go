package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"velox/go-master/pkg/config"
)

func TestFeatureFlagCheckerDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Features: config.FeaturesConfig{
			ArtlistEnabled: false,
		},
	}

	r := gin.New()
	r.Use(ArtlistEnabled(cfg))
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

	cfg := &config.Config{
		Features: config.FeaturesConfig{
			ArtlistEnabled: true,
		},
	}

	r := gin.New()
	r.Use(ArtlistEnabled(cfg))
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
