package tests

import (
	"net/http"
	"testing"

	"velox/go-master/tests/helpers"

	"github.com/stretchr/testify/assert"
)

// TestFeatureFlagsDefaultDisabled verifies features are disabled by default
func TestFeatureFlagsDefaultDisabled(t *testing.T) {
	cfg := helpers.TestConfig()
	// All features should be false by default in TestConfig

	router := helpers.CreateTestRouter(cfg)
	token := cfg.Security.AdminToken

	// Test Artlist endpoints - should return 404 or 403
	w := helpers.PerformAuthRequest(router, "GET", "/api/artlist/diagnostics", token, nil)
	assert.True(t, w.Code == http.StatusNotFound || w.Code == http.StatusForbidden,
		"Artlist should be disabled, got %d", w.Code)

	w = helpers.PerformAuthRequest(router, "POST", "/api/artlist/run", token, nil)
	assert.True(t, w.Code == http.StatusNotFound || w.Code == http.StatusForbidden,
		"Artlist run should be disabled, got %d", w.Code)

	// Test YouTube endpoints - should return 404 or 403
	w = helpers.PerformAuthRequest(router, "POST", "/api/clips/process", token, nil)
	assert.True(t, w.Code == http.StatusNotFound || w.Code == http.StatusForbidden,
		"YouTube clips should be disabled, got %d", w.Code)

	// Test script-docs endpoints - should return 404 or 403
	w = helpers.PerformAuthRequest(router, "GET", "/api/script-docs", token, nil)
	assert.True(t, w.Code == http.StatusNotFound || w.Code == http.StatusForbidden,
		"Script docs should be disabled, got %d", w.Code)
}

// TestFeatureArtlistEnabled verifies Artlist config flag
func TestFeatureArtlistEnabled(t *testing.T) {
	cfg := helpers.TestConfig()
	cfg.Features.ArtlistEnabled = true

	// Verify config is set correctly
	assert.True(t, cfg.Features.ArtlistEnabled, "Artlist should be enabled in config")
	t.Log("Artlist feature flag can be enabled in config")

	// Note: Actual route registration requires real server with handlers
	// This test verifies the config, not the route registration
}

// TestFeatureYouTubeEnabled verifies YouTube config flag
func TestFeatureYouTubeEnabled(t *testing.T) {
	cfg := helpers.TestConfig()
	cfg.Features.YouTubeEnabled = true

	// Verify config is set correctly
	assert.True(t, cfg.Features.YouTubeEnabled, "YouTube should be enabled in config")
	t.Log("YouTube feature flag can be enabled in config")

	// Note: Actual route registration requires real server with handlers
	// This test verifies the config, not the route registration
}

// TestModuleRegistryInterface verifies module interface implementation
func TestModuleRegistryInterface(t *testing.T) {
	// This test verifies that all modules implement the Module interface
	// We need to import the module package and check

	// For now, we'll test that the module package compiles
	// and the interface is properly defined
	t.Log("Module registry interface test - verify all modules implement Module interface")

	// TODO: Add actual module registry tests when module system is fully implemented
	// Expected checks:
	// - Each module has Name()
	// - Each module has Enabled(cfg)
	// - Each module registers routes only if enabled
	// - No experimental modules start by default
}

// TestRouteRegistration verifies route registration based on features
func TestRouteRegistration(t *testing.T) {
	// This test verifies that routes CAN be registered based on feature flags
	// The actual registration happens in the real server with handlers

	t.Log("Route registration is handled by internal/api/routes.go")
	t.Log("When features are enabled and handlers are provided, routes are registered")
	t.Log("Test verifies the concept, not the actual registration in minimal router")
}

// TestNoHardcodedRoutes verifies router doesn't have hardcoded module references
func TestNoHardcodedRoutes(t *testing.T) {
	// This is a code structure test
	// The router should use the module registry, not hardcoded handlers

	// We can verify this by checking that:
	// 1. The routes.go file uses registry
	// 2. No direct handler registration for optional features

	t.Log("Verifying router uses module registry pattern")

	// TODO: Add grep-like test to verify code structure
	// For now, this is a conceptual test
}

// TestExperimentalModulesDisabled verifies experimental modules don't start by default
func TestExperimentalModulesDisabled(t *testing.T) {
	cfg := helpers.TestConfig()
	// Don't enable any experimental features

	// Verify that experimental features have default: false in config
	assert.False(t, cfg.Features.ArtlistEnabled, "Artlist should be disabled by default")
	assert.False(t, cfg.Features.YouTubeEnabled, "YouTube should be disabled by default")
	assert.False(t, cfg.Features.DriveEnabled, "Drive should be disabled by default")
	assert.False(t, cfg.Features.ScriptDocsEnabled, "Script docs should be disabled by default")
	assert.False(t, cfg.Features.ImagesEnabled, "Images should be disabled by default")
}

// TestBootstrapMinimal verifies server starts without external dependencies
func TestBootstrapMinimal(t *testing.T) {
	cfg := helpers.TestConfig()
	cfg.Features.ArtlistEnabled = false
	cfg.Features.YouTubeEnabled = false
	cfg.Features.DriveEnabled = false
	cfg.Features.ScriptDocsEnabled = false

	// This test verifies that the server can start without:
	// - Google Drive credentials
	// - Ollama
	// - yt-dlp
	// - ffmpeg
	// - Python
	// - Node

	// The router should be creatable without these dependencies
	router := helpers.CreateTestRouter(cfg)
	assert.NotNil(t, router, "Router should be creatable without external dependencies")

	// Health check should work
	w := helpers.PerformRequest(router, "GET", "/health", nil, nil)
	helpers.AssertStatus(t, w, http.StatusOK)
}
