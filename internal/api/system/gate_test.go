package system

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/system.
// Baseline (no goroutines; bash Check 19 enforces no infrastructure
// imports) + grep-verified `RegisterJobHandlers` (3 matches across
// handler.go + handler_drive.go + Wave 14-PR4 extraction scope) and
// `config.Config` direct struct access.
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	{Name: "RegisterJobHandlers in API", Pattern: "RegisterJobHandlers"},
	{Name: "config.Config direct struct access", Pattern: "config.Config"},
}

func TestStaticGate_NoSystemAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
