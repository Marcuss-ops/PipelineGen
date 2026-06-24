package voiceover

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/voiceover
// (handler.go). Baseline (no goroutines; bash Check 19 enforces no
// infrastructure imports) + the grep-verified `voiceover.NewService`
// orchestrator (added 2026-06-24 followup). Note: handler.go's
// legitimate `pkg/concurrent.SafeGo("promo-voiceover", ...)` call
// trips the baseline `SafeGo` pattern; flagged as a known issue to
// be drained in Wave 14 voiceover surface cleanup (tracked in
// architecture/migration.yaml). See also arch check script Check 19.
// Cross-ref: docs/migrations/api-infrastructure-imports-allowlist.txt
// (28 grandfathered-import entries as of Wave 14-PR3).
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	// Per-area orchestrator pattern (added 2026-06-24 followup, code-review
	// NIT-B): `voiceover.NewService` is the canonical direct-orchestrator
	// constructor; the API layer must reach voiceover capabilities via the
	// composition root in internal/app/composition.go, not via local
	// construction. Grep-verified: zero hits in internal/api/* production
	// code at HEAD, safe to enforce as a hard-fail pattern.
	{Name: "voiceover.NewService direct construction", Pattern: "voiceover.NewService"},
}

func TestStaticGate_NoVoiceoverAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
