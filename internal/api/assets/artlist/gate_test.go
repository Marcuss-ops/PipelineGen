package artlist

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/artlist.
// Baseline (no goroutines; bash Check 19 enforces no infrastructure
// imports against `docs/migrations/api-infrastructure-imports-allowlist.txt`)
// + the grep-verified `artlistadapter.NewAdapter` + `artlist.NewService`
// orchestrators (added 2026-06-24 followup). See
// architecture/migration.yaml::Wave 14 + arch check Check 19.
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	// Per-area orchestrator patterns (added 2026-06-24 followup, code-review
	// NIT-B): `artlistadapter.NewAdapter` + `artlist.NewService` are the
	// canonical direct-orchestrator constructors; the API layer must reach
	// the artlist provider via the typed registry in
	// internal/application/assets/providers, not via direct construction
	// here. Grep-verified: zero hits in internal/api/* production code
	// at HEAD, safe to enforce as hard-fail patterns.
	{Name: "artlistadapter.NewAdapter direct construction", Pattern: "artlistadapter.NewAdapter"},
	{Name: "artlist.NewService direct construction", Pattern: "artlist.NewService"},
}

func TestStaticGate_NoArtlistAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
