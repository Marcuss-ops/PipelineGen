package images

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/images.
// Baseline (no goroutines; bash Check 19 enforces no infrastructure
// imports) + the grep-verified `imgservice.NewService` orchestrator
// (added 2026-06-24 followup). See architecture/migration.yaml::Wave 14
// + arch check Check 19. Cross-ref: docs/migrations/api-infrastructure-
// imports-allowlist.txt (28 grandfathered-import entries as of Wave
// 14-PR3).
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	// Per-area orchestrator pattern (added 2026-06-24 followup, code-review
	// NIT-B): `imgservice.NewService` is the canonical direct-orchestrator
	// constructor (used in internal/app/compose_images.go); the API layer
	// must reach images via DomainBundle, not via direct construction
	// here. Grep-verified: zero hits in internal/api/* production code
	// at HEAD, safe to enforce as hard-fail pattern.
	{Name: "imgservice.NewService direct construction", Pattern: "imgservice.NewService"},
}

func TestStaticGate_NoImagesAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
