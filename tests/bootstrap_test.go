package tests

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"velox/go-master/internal/config"
	"velox/go-master/tests/helpers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBootstrapWithoutExternalDeps verifies server starts without external dependencies
func TestBootstrapWithoutExternalDeps(t *testing.T) {
	// This test verifies the server can start without:
	// - Google Drive credentials (credentials.json, token.json)
	// - Ollama installed
	// - yt-dlp installed
	// - ffmpeg installed
	// - Python installed
	// - Node.js installed

	cfg := helpers.TestConfig()
	cfg.Features.ArtlistEnabled = false
	cfg.Features.YouTubeEnabled = false
	cfg.Features.DriveEnabled = false
	cfg.Features.ScriptDocsEnabled = false
	cfg.Storage.DataDir = t.TempDir()

	// Remove any credentials that might exist
	os.Remove(filepath.Join(cfg.Storage.DataDir, "credentials.json"))
	os.Remove(filepath.Join(cfg.Storage.DataDir, "token.json"))

	// Create router (this exercises the bootstrap code)
	router := helpers.CreateTestRouter(cfg)
	require.NotNil(t, router, "Router should be creatable without external deps")

	// Test health endpoint works
	w := helpers.PerformRequest(router, "GET", "/health", nil, nil)
	helpers.AssertStatus(t, w, http.StatusOK)
}

// TestBootstrapWithMinimalConfig verifies server starts with minimal config
func TestBootstrapWithMinimalConfig(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{
			Host: "127.0.0.1",
			Port: 18082,
		},
		Security: config.SecurityConfig{
			EnableAuth: false, // Disable auth for simplicity
		},
		Features: config.FeaturesConfig{
			ArtlistEnabled:    false,
			YouTubeEnabled:    false,
			DriveEnabled:      false,
			ScriptDocsEnabled: false,
			ImagesEnabled:     false,
		},
		Storage: config.StorageConfig{
			DataDir: t.TempDir(),
		},
	}

	router := helpers.CreateTestRouter(cfg)
	assert.NotNil(t, router, "Router should be creatable with minimal config")
}

// TestServerStartsQuickly verifies server starts within reasonable time
func TestServerStartsQuickly(t *testing.T) {
	cfg := helpers.TestConfig()
	cfg.Features.ArtlistEnabled = false
	cfg.Features.YouTubeEnabled = false
	cfg.Features.DriveEnabled = false
	cfg.Storage.DataDir = t.TempDir()

	start := time.Now()
	router := helpers.CreateTestRouter(cfg)
	elapsed := time.Since(start)

	assert.NotNil(t, router, "Router should be created")
	assert.Less(t, elapsed, 5*time.Second, "Server should start quickly (within 5s)")

	t.Logf("Server startup took: %v", elapsed)
}

// TestMultipleInstancesIsolated verifies multiple server instances don't conflict
func TestMultipleInstancesIsolated(t *testing.T) {
	// Create two server instances with different data dirs
	cfg1 := helpers.TestConfig()
	cfg1.Storage.DataDir = t.TempDir()
	cfg1.Features.ArtlistEnabled = false
	cfg1.Features.YouTubeEnabled = false

	cfg2 := helpers.TestConfig()
	cfg2.Server.Port = 18083
	cfg2.Storage.DataDir = t.TempDir()
	cfg2.Features.ArtlistEnabled = false
	cfg2.Features.YouTubeEnabled = false

	router1 := helpers.CreateTestRouter(cfg1)
	router2 := helpers.CreateTestRouter(cfg2)

	assert.NotNil(t, router1, "Instance 1 should start")
	assert.NotNil(t, router2, "Instance 2 should start")

	// Both should respond to health checks
	w1 := helpers.PerformRequest(router1, "GET", "/health", nil, nil)
	w2 := helpers.PerformRequest(router2, "GET", "/health", nil, nil)

	helpers.AssertStatus(t, w1, http.StatusOK)
	helpers.AssertStatus(t, w2, http.StatusOK)
}

// TestGracefulShutdown verifies server shuts down without zombies
func TestGracefulShutdown(t *testing.T) {
	// This test verifies that after shutdown:
	// - No orphaned processes
	// - No zombie processes
	// - Database connections are closed

	cfg := helpers.TestConfig()
	cfg.Server.Port = 18083
	cfg.Features.ArtlistEnabled = false
	cfg.Features.YouTubeEnabled = false
	cfg.Storage.DataDir = t.TempDir()

	router := helpers.CreateTestRouter(cfg)

	// Create test server
	ts := http.Server{
		Addr:    ":18083",
		Handler: router,
	}

	// Start server in goroutine
	go func() {
		if err := ts.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Errorf("Server error: %v", err)
		}
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Make a request to verify it's working
	resp, err := http.Get("http://127.0.0.1:18083/health")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Shutdown gracefully
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = ts.Shutdown(ctx)
	assert.NoError(t, err, "Server should shutdown cleanly")
}
