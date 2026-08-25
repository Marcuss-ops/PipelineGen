package images

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/images.
// Baseline (no goroutines; bash Check 19 enforces no infrastructure
// imports) + the grep-verified `NewService` orchestrator
// (added 2026-06-24 followup). See architecture/current.yaml::Wave 14
// + arch check Check 19. Cross-ref: docs/migrations/api-infrastructure-
// imports-allowlist.txt (28 grandfathered-import entries as of Wave
// 14-PR3).
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	// PG-002 (June 2026): enforce no infrastructure imports locally so a
	// regression cannot sneak past CI's Check 19 unnoticed while a
	// developer runs `go test ./internal/api/images/...` in isolation.
	// Cross-ref: docs/migrations/api-infrastructure-imports-allowlist.txt.
	{Name: "infrastructure imports", Pattern: "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/"},
}

func TestStaticGate_NoImagesAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
