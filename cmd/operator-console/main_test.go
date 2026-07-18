package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

func TestSetupRouter_HealthNoAuth(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Security: config.SecurityConfig{EnableAuth: false},
	}
	apiBase, _ := url.Parse("http://127.0.0.1:8000")
	apiClient := NewAPIClient(apiBase.String(), "")
	router := setupRouter(cfg, apiBase, apiClient, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"ok":true`)
	assert.Contains(t, rec.Body.String(), `"service":"operator-console"`)
}

func TestSetupRouter_DashboardRequiresAPI(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Security: config.SecurityConfig{EnableAuth: false},
	}
	apiBase, _ := url.Parse("http://127.0.0.1:8000")
	apiClient := NewAPIClient(apiBase.String(), "")
	router := setupRouter(cfg, apiBase, apiClient, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// Dashboard should render even if API is unreachable (graceful degradation)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "PipelineGen")
}
