package artlist

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/artlist.
// Baseline (no goroutines; bash Check 19 enforces no infrastructure
// imports against `docs/migrations/api-infrastructure-imports-allowlist.txt`)
// + the grep-verified `artlistadapter.NewAdapter` + `artlist.NewService`
// orchestrators (added 2026-06-24 followup). See
// architecture/current.yaml::Wave 14 + arch check Check 19.
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	// Per-area orchestrator patterns (added 2026-06-24 followup, code-review
	// NIT-B): `artlistadapter.NewAdapter` + `artlist.NewService` are the
	// canonical direct-orchestrator constructors; the API layer must reach
	// the artlist provider via the typed registry in
	// internal/capabilities/assets/providers, not via direct construction
	// here. Grep-verified: zero hits in internal/api/* production code
	// at HEAD, safe to enforce as hard-fail patterns.
	{Name: "artlistadapter.NewAdapter direct construction", Pattern: "artlistadapter.NewAdapter"},
	{Name: "artlist.NewService direct construction", Pattern: "artlist.NewService"},
	// Blocco C1-Step 3 followup (June 2026): the capability exposes the
	// canonical `artsources.Build(deps) (api.Descriptor, error)` entrypoint,
	// so any direct `api.Registry.Register` call inside the package would
	// bypass the composition root's capability_registry.go hoist site and
	// break the Build contract (godlike/07 + future C2-A gate). Grep-verified
	// at HEAD: zero hits in internal/api/assets/artlist production code.
	{Name: "no direct api.Registry.Register (Blocco C1-Step 3)", Pattern: "api.Registry.Register"},
	// Per-area hard-fail rule that mirrors the channels + images +
	// soundeffect + youtube precedent. Any reference to the
	// infrastructure path fails the gate so future regressions
	// surface before they reach the allowlist.
	{Name: "no infrastructure imports", Pattern: "internal/infrastructure/"},
}

func TestStaticGate_NoArtlistAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
