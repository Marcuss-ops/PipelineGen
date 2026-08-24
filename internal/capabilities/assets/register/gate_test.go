package assets

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/register
// (handler.go). Baseline only — bash Check 19 + the 28-entry
// grandfatherlist already enforce no infrastructure imports; this gate
// focuses on goroutines that bash Check 19 cannot catch. Per-area
// orchestrator patterns can be added after grep-verification during
// Wave 14 grandfathered-import drain.
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
}

func TestStaticGate_NoRegisterAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
