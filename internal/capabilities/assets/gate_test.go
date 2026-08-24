package assets

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for the top-level internal/api/assets/
// package (handler_searchqueries.go lives here). Subpackages of assets/
// (artlist, clips, soundeffect, youtube, …) carry their own dedicated
// gates, so SkipDir prunes them out of this walk.
//
// Baseline only (bash Check 19 already enforces no infrastructure imports).
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
}

func TestStaticGate_NoAssetsAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
		// Subpackages have their own dedicated gates — exclude them so
		// each call site owns its own area without overlap.
		SkipDir: func(path string) bool {
			switch path {
			case "artlist", "clips", "soundeffect", "youtube",
				"storage", "register", "diagnostics", "search",
				"voiceover", "stock":
				return true
			}
			return false
		},
	})
}
