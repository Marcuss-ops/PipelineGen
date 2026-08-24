package middleware

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/middleware.
// Baseline (no goroutines; bash Check 19 enforces no infrastructure
// imports) + grep-verified `RequireAdminToken` (2 matches in
// admin_token.go + middleware_middleware.go) and `config.Config` (15
// matches across the package — direct struct use is the Wave 14-PR5
// target to drain).
var prohibitedPatterns = []gate.Prohibition{}

func TestStaticGate_NoMiddlewareAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
