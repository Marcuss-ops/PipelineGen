package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/api/handlers/jobs"
	"velox/go-master/pkg/config"
)

func TestHealthRoutesArePublic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		Security: config.SecurityConfig{
			EnableAuth: true,
			AdminToken: "test-token",
		},
		Server: config.ServerConfig{
			Port:        8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
		},
		External: config.ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
		Storage: config.StorageConfig{
			DataDir: "/tmp/test-pipelinegen",
		},
	}
	handlers := &Handlers{
		Health: &common.HealthHandler{},
	}
	r := NewRouter(cfg, handlers)
	engine := r.Setup()

	tests := []struct {
		name   string
		path   string
		method string
	}{
		{"GET /health", "/health", "GET"},
		{"GET /api/health", "/api/health", "GET"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(tt.method, tt.path, nil)
			resp := httptest.NewRecorder()
			engine.ServeHTTP(resp, req)
			if resp.Code != http.StatusOK {
				t.Errorf("expected 200 for %s, got %d", tt.path, resp.Code)
			}
		})
	}
}

func TestCatalogFoldersIsPublic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		Security: config.SecurityConfig{
			EnableAuth: true,
			AdminToken: "test-token",
		},
		Server: config.ServerConfig{
			Port:        8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
		},
		External: config.ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
		Storage: config.StorageConfig{
			DataDir: "/tmp/test-pipelinegen",
		},
	}
	handlers := &Handlers{
		Catalog: &common.CatalogHandler{},
	}
	r := NewRouter(cfg, handlers)
	engine := r.Setup()

	req, _ := http.NewRequest("GET", "/api/catalog/folders", nil)
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code == http.StatusUnauthorized {
		t.Error("expected /api/catalog/folders to be public, got 401")
	}
}

func TestProtectedRoutesRequireAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		Security: config.SecurityConfig{
			EnableAuth: true,
			AdminToken: "test-token",
		},
		Server: config.ServerConfig{
			Port:        8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
		},
		External: config.ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
		Storage: config.StorageConfig{
			DataDir: "/tmp/test-pipelinegen",
		},
	}
	handlers := &Handlers{
		Jobs: &jobs.Handler{},
	}
	r := NewRouter(cfg, handlers)
	engine := r.Setup()

	tests := []struct {
		name string
		path string
	}{
		{"GET /api/jobs", "/api/jobs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", tt.path, nil)
			resp := httptest.NewRecorder()
			engine.ServeHTTP(resp, req)
			if resp.Code != http.StatusUnauthorized {
				t.Errorf("expected 401 for %s without token, got %d", tt.path, resp.Code)
			}
		})
	}
}

func TestInvalidTokenIsRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		Security: config.SecurityConfig{
			EnableAuth: true,
			AdminToken: "test-token",
		},
		Server: config.ServerConfig{
			Port:        8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
		},
		External: config.ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
		Storage: config.StorageConfig{
			DataDir: "/tmp/test-pipelinegen",
		},
	}
	handlers := &Handlers{
		Jobs: &jobs.Handler{},
	}
	r := NewRouter(cfg, handlers)
	engine := r.Setup()

	req, _ := http.NewRequest("GET", "/api/jobs", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid token, got %d", resp.Code)
	}
}

func TestValidTokenCanAccessProtectedRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		Security: config.SecurityConfig{
			EnableAuth: true,
			AdminToken: "test-token",
		},
		Server: config.ServerConfig{
			Port:        8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
		},
		External: config.ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
		Storage: config.StorageConfig{
			DataDir: "/tmp/test-pipelinegen",
		},
	}
	handlers := &Handlers{
		Jobs: &jobs.Handler{},
	}
	r := NewRouter(cfg, handlers)
	engine := r.Setup()

	req, _ := http.NewRequest("GET", "/api/jobs", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code == http.StatusUnauthorized {
		t.Error("expected valid token to access protected route, got 401")
	}
}
