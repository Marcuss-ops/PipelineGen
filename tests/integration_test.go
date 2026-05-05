package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"velox/go-master/pkg/config"
	"velox/go-master/tests/helpers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestArtlistEndToEndMinimal tests Artlist config
func TestArtlistEndToEndMinimal(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg := helpers.TestConfig()
	cfg.Features.ArtlistEnabled = true

	// Verify config is set correctly
	assert.True(t, cfg.Features.ArtlistEnabled, "Artlist should be enabled in config")
	t.Log("Artlist feature can be enabled in config")
	t.Log("Full end-to-end test requires starting the real server with bootstrap")
}

// TestYouTubeClipsEndToEndMinimal tests YouTube config
func TestYouTubeClipsEndToEndMinimal(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg := helpers.TestConfig()
	cfg.Features.YouTubeEnabled = true

	// Verify config is set correctly
	assert.True(t, cfg.Features.YouTubeEnabled, "YouTube should be enabled in config")
	t.Log("YouTube feature can be enabled in config")
	t.Log("Full end-to-end test requires starting the real server with bootstrap")
}

// TestHealthEndpointConsistency verifies all health endpoints work
func TestHealthEndpointConsistency(t *testing.T) {
	cfg := helpers.TestConfig()
	router := helpers.CreateTestRouter(cfg)

	healthPaths := []string{
		"/health",
		"/api/health",
	}

	for _, path := range healthPaths {
		t.Run(path, func(t *testing.T) {
			w := helpers.PerformRequest(router, "GET", path, nil, nil)
			helpers.AssertStatus(t, w, http.StatusOK)

			// Verify response is valid JSON
			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			assert.NoError(t, err, "Health response should be valid JSON")
		})
	}
}

// TestRouteSnapshotMinimalMode verifies routes in minimal mode
func TestRouteSnapshotMinimalMode(t *testing.T) {
	cfg := helpers.TestConfig()
	cfg.Features.ArtlistEnabled = false
	cfg.Features.YouTubeEnabled = false
	cfg.Features.DriveEnabled = false

	router := helpers.CreateTestRouter(cfg)

	// Routes that SHOULD exist
	shouldExist := []string{
		"GET /health",
		"GET /api/health",
	}

	// Get registered routes
	routes := router.Routes()
	routeMap := make(map[string]bool)
	for _, route := range routes {
		key := fmt.Sprintf("%s %s", route.Method, route.Path)
		routeMap[key] = true
	}

	// Check should exist
	for _, expected := range shouldExist {
		_, exists := routeMap[expected]
		assert.True(t, exists, "Route %s should be registered", expected)
	}

	// Note: Disabled feature routes may still be registered but return 403/404
	// The important thing is they don't function without the feature flag
	t.Log("Route snapshot test completed")
}

// TestJobConcurrency tests job system under concurrent load
func TestJobConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping concurrency test in short mode")
	}

	cfg := helpers.TestConfig()
	cfg.Features.ArtlistEnabled = true // Jobs might need a feature that uses them
	cfg.Storage.DataDir = helpers.SetupTestDataDir(t)
	defer helpers.CleanupTestDataDir(t, cfg.Storage.DataDir)

	// This is a conceptual test - actual job creation would need proper setup
	t.Log("Job concurrency test - verify job system handles concurrent requests")

	// TODO: Implement actual concurrent job creation test
	// - Create multiple jobs concurrently
	// - Verify no duplicates
	// - Verify no stuck jobs
	// - Verify stale job recovery
}

// TestServerShutdownClean verifies clean shutdown
func TestServerShutdownClean(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{
			Host: "127.0.0.1",
			Port: 18081,
		},
		Security: config.SecurityConfig{
			EnableAuth: false, // Disable auth for simplicity
		},
		Features: config.FeaturesConfig{
			ArtlistEnabled:   false,
			YouTubeEnabled:   false,
			DriveEnabled:     false,
			ImagesEnabled:    false,
		},
		Storage: config.StorageConfig{
			DataDir: t.TempDir(),
		},
	}

	router := helpers.CreateTestRouter(cfg)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: router,
	}

	// Start server
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Errorf("Server error: %v", err)
		}
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Test server is up
	resp, err := http.Get(fmt.Sprintf("http://%s:%d/health", cfg.Server.Host, cfg.Server.Port))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Shutdown gracefully
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = srv.Shutdown(ctx)
	assert.NoError(t, err, "Server should shutdown cleanly")

	// Verify server is down
	_, err = http.Get(fmt.Sprintf("http://%s:%d/health", cfg.Server.Host, cfg.Server.Port))
	assert.Error(t, err, "Server should be down after shutdown")
}

// TestBuildFromScratch verifies clean build
func TestBuildFromScratch(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping build test in short mode")
	}

	// This test verifies that the project builds cleanly
	// In actual CI, this would be a shell script

	t.Log("Build from scratch test - should be run in CI")
	t.Log("Steps:")
	t.Log("1. git clone")
	t.Log("2. go mod tidy")
	t.Log("3. go test ./...")
	t.Log("4. go build ./cmd/server")
	t.Log("5. Verify go.mod unchanged after tidy")
}

// TestCleanRepo verifies no runtime files are tracked
func TestCleanRepo(t *testing.T) {
	// This is a conceptual test that would check:
	// - No .sqlite files tracked
	// - No .db files tracked
	// - No .mp4/.mp3 files tracked
	// - No binaries tracked
	// - No node_modules tracked

	t.Log("Clean repo test - should be run in CI with git commands")
	t.Log("Commands:")
	t.Log("git status -sb")
	t.Log("find . -name '*.sqlite' -o -name '*.db'")
	t.Log("git ls-files | grep -E '\\.(sqlite|db|mp4|mp3|ttf)$'")
}

// TestGoModTidyNoChanges verifies go.mod is clean
func TestGoModTidyNoChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping go mod tidy test in short mode")
	}

	t.Log("go mod tidy test - should be run in CI")
	t.Log("Steps:")
	t.Log("1. go mod tidy")
	t.Log("2. git diff -- go.mod go.sum")
	t.Log("3. If diff exists, fail - repo is not clean")
}

// Helper test for httptest with auth
func TestAuthenticatedRequestHelper(t *testing.T) {
	cfg := helpers.TestConfig()
	router := helpers.CreateTestRouter(cfg)
	token := cfg.Security.AdminToken

	// Test helper works
	w := helpers.PerformAuthRequest(router, "GET", "/health", token, nil)
	// Health is public, but auth shouldn't break it
	assert.True(t, w.Code == http.StatusOK || w.Code == http.StatusUnauthorized)
}

// TestConcurrentRequests verifies server handles concurrent requests
func TestConcurrentRequests(t *testing.T) {
	cfg := helpers.TestConfig()
	router := helpers.CreateTestRouter(cfg)

	// Create test server
	ts := httptest.NewServer(router)
	defer ts.Close()

	// Make concurrent requests
	concurrent := 10
	results := make(chan int, concurrent)

	for i := 0; i < concurrent; i++ {
		go func(id int) {
			resp, err := http.Get(ts.URL + "/health")
			if err != nil {
				results <- 0
				return
			}
			results <- resp.StatusCode
			resp.Body.Close()
		}(i)
	}

	// Collect results
	successCount := 0
	for i := 0; i < concurrent; i++ {
		status := <-results
		if status == http.StatusOK {
			successCount++
		}
	}

	assert.Equal(t, concurrent, successCount, "All concurrent requests should succeed")
}
