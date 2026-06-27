package storage

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/storage
// (handler.go + sync_drive_folder.go + local_to_drive.go). Baseline (no
// goroutines; bash Check 19 enforces no infrastructure imports) + the
// grep-verified `storage.SyncFolder` orchestrator (added 2026-06-24
// followup). See architecture/current.yaml::Wave 14 grandfathered-
// imports + scripts/ci-architectural-checks.sh::Check 19 for the
// cross-cutting enforcement map. Cross-ref: docs/migrations/
// api-infrastructure-imports-allowlist.txt (28 grandfathered-import
// entries as of Wave 14-PR3).
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	// Per-area orchestrator pattern (added 2026-06-24 followup, code-review
	// NIT-B): `storage.SyncFolder` is the canonical infra constructor; the
	// API layer must reach the sync via the use-case in
	// internal/application/assets/catalogsync, not via direct construction.
	// Grep-verified: zero hits in internal/api/* production code at HEAD,
	// safe to enforce as a hard-fail pattern.
	{Name: "storage.SyncFolder direct construction", Pattern: "storage.SyncFolder"},
}

func TestStaticGate_NoStorageAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
