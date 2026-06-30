package diagnostics

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/diagnostics
// (handler.go).
//
// Pattern parity with soundeffect/gate_test.go (C1-Step 8 PG-003):
// baseline goroutine safety + PG-003 inline infra-import gate.
// Bash Check 19 + the 28-entry grandfatherlist
// (`docs/migrations/api-infrastructure-imports-allowlist.txt`) already
// enforce no infrastructure imports at the cross-cutting level; this
// per-area prohibition is a forward-tripwire that fires BEFORE the
// allowlist ratchet, catching regressions at the gate boundary
// instead of leaking through to the cross-cutting CI check.
//
// INTENTIONAL OMISSION — `appdiag.New*` orchestrator prohibition:
// unlike stock / register / voiceover, the diagnostics handler_test.go
// fixture at line 48 legitimately calls `appdiag.NewService(ih, as,
// &testLogger{zap: zap.NewNop()})` to construct a real *appdiag.Service
// for the inline test (the application-layer Services require typed-port
// adapters that are not trivially faked). A blanket `appdiag.New`
// prohibition would break this fixture. Pattern parity is preserved by
// relying on bash Check 19's infra ban + the canonical composition
// invariant documented in diagnostic's module.go godoc (the api/
// layer MUST NOT build *appdiag.Service directly — it must consume
// the one constructed in internal/app/module_media.go::WireAssets via
// Build(deps.Service)). Compare: soundeffect/gate_test.go uses the
// same 3-prohibition shape without an orchestrator pattern for the
// same reason.
//
// Blocco C1-Step 10 hardening (June 2026): the diagnostics
// capability Build contract (`diagnostics.Build(deps) (api.Descriptor,
// error)`) is structurally complete and matches the artlist /
// youtube / clips / stock / voiceover / soundeffect / register
// precedent. This gate_test.go is the per-area parity fortification
// — plus a comment refresh to document why the orchestrator
// prohibition is intentionally NOT added (matches soundeffect parity
// exactly).
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	// PG-003 (June 2026) inline infra-import gate (Step 8 soundeffect
	// precedent): any `internal/infrastructure/` import fails the static
	// gate. Grep-verified: zero hits in internal/api/assets/diagnostics/*
	// at HEAD. The forward-tripwire catches operator attempts to import
	// drive / qdrant / ffmpeg concrete types into the api/ layer — the
	// api/ layer must reach those via the typed-port adapters in
	// `internal/app/adapters_infra.go` (composition root only).
	{Name: "no infrastructure imports", Pattern: "internal/infrastructure/"},
}

func TestStaticGate_NoDiagnosticsAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
