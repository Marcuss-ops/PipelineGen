package httpserver

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for the top-level internal/api/
// package (server.go, routes.go, module_core_modules.go, etc. live here).
// Subpackages of api/ (assets, channels, images, middleware, system,
// script, …) carry their own dedicated gates, so SkipDir prunes them
// out of this walk.
//
// Baseline only (bash Check 19 + the 28-entry grandfatherlist already
// enforce no infrastructure imports).
var prohibitedPatterns = []gate.Prohibition{}

func TestStaticGate_NoAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
		// Subpackages have their own dedicated gates — exclude them so
		// each call site owns its own area without overlap.
		SkipDir: func(path string) bool {
			switch path {
			case "assets", "channels", "images", "middleware",
				"system", "script", "content", "jobs":
				return true
			}
			return false
		},
	})
}
