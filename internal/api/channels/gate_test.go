package channels

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/channels.
// Baseline only (bash Check 19 enforces no infrastructure imports).
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	// PG-002 (June 2026): enforce no infrastructure imports locally so a
	// regression cannot sneak past CI's Check 19 unnoticed while a
	// developer runs `go test ./internal/api/channels/...` in isolation.
	// Cross-ref: docs/migrations/api-infrastructure-imports-allowlist.txt.
	{Name: "infrastructure imports", Pattern: "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/"},
}

func TestStaticGate_NoChannelsAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
