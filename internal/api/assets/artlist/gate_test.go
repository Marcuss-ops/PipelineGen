package artlist

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/artlist.
// Baseline (no goroutines; bash Check 19 enforces no infrastructure
// imports against `docs/migrations/api-infrastructure-imports-allowlist.txt`).
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
}

func TestStaticGate_NoArtlistAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
