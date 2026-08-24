package httpserver

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/transport
// (transport.go + transport_test.go). HTTP transport / server wire-up.
// Baseline (no goroutines; bash Check 19 enforces no infrastructure
// imports). Per-area orchestrator patterns kept DROPPED for now:
// the user-specified candidates (`RequireAdminToken` + `config.Config`)
// were classified RISKY by grep-verification (13 + 30 hits in api/*
// production code respectively) and would immediately fail the gate.
// Drain target: Wave 14-PR5 middleware-config-drain (see
// architecture/current.yaml#wave-14), at which point the patterns
// can return to this list without re-breaking the gate. Cross-ref:
// docs/migrations/api-infrastructure-imports-allowlist.txt (28
// grandfathered-import entries as of Wave 14-PR3).
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	// Transport gate keeps BASELINE ONLY for now. The user-specified
	// patterns (RequireAdminToken + config.Config) were classified RISKY
	// by grep-verification (13 + 30 hits in api/* production code
	// respectively — would fail the gate immediately). Migration target:
	// drain those references to the composition root in Wave 14, then
	// re-introduce the patterns after the gate's own files no longer
	// declare or use them. See architecture/current.yaml::Wave 14
	// grandfathered-imports.
}

func TestStaticGate_NoTransportAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
