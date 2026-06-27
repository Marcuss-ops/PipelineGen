package jobs

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/jobs (impl.go
// + handler_workers.go). Job HTTP transport layer. Baseline (no
// goroutines; bash Check 19 enforces no infrastructure imports) + the
// grep-verified `jobs.NewService` orchestrator (added 2026-06-24
// followup). See architecture/current.yaml::Wave 14 grandfathered-
// imports + scripts/ci-architectural-checks.sh::Check 19. Cross-ref:
// docs/migrations/api-infrastructure-imports-allowlist.txt (28
// grandfathered-import entries as of Wave 14-PR3).
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	// Per-area orchestrator pattern (added 2026-06-24 followup, code-review
	// NIT-B): `jobs.NewService` is the canonical direct-orchestrator
	// constructor; the API layer must reach the job broker via the
	// composition root's JobsBundle, not via direct construction.
	// Grep-verified: zero hits in internal/api/* production code at HEAD,
	// safe to enforce as a hard-fail pattern. `appjobs.NewService` mirrors
	// the same risk and is intentionally NOT included yet — see Wave 14
	// grandfathered-import drain (architecture/current.yaml).
	{Name: "jobs.NewService direct construction", Pattern: "jobs.NewService"},
}

func TestStaticGate_NoJobsAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
