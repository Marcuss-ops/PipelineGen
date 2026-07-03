// Package app — package-level adapter vars extracted from module_media.go
// (PR-GODOBJ-7 composition target, July 2026).
package app

import (
	infraassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/assets"
)

// processRunnerAdapter is a package-level adapter for the infrastructure ProcessRunner port.
// Used by ScraperHandler and other handlers in registry.go that need subprocess execution.
var processRunnerAdapter = infraassets.NewProcessRunnerAdapter()

// toolCheckerAdapter is a package-level adapter for the infrastructure ToolChecker port.
// Used by YouTubeClipHandler and system handler to check external tool availability.
var toolCheckerAdapter = infraassets.NewToolCheckerAdapter()

// dbHealthCheckerAdapter is a package-level adapter for the infrastructure DBHealthChecker port.
// Used by system handler to check database health.
var dbHealthCheckerAdapter = infraassets.NewDBHealthCheckerAdapter(nil)
