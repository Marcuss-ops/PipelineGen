package storage

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/storage
// (handler.go + sync_drive_folder.go + internal_sync_drive_folder.go +
// dispatch_helpers.go + local_to_drive.go). Baseline (no goroutines;
// bash Check 19 enforces no infrastructure imports) + the grep-verified
// `storage.SyncFolder` orchestrator + PG-003 inline infra-import
// check.
//
// Pattern parity with soundeffect/gate_test.go (C1-Step 8 PG-003) +
// register/gate_test.go (C1-Step 9 followup). `storage.SyncFolder` is
// the canonical infra constructor; the api/ layer must reach the
// sync via the *catalogsync.Service threaded through storage.Build,
// not via direct construction. Grep-verified: zero hits in
// internal/api/** production code at HEAD, so the prohibition is
// safe to enforce as a hard-fail pattern.
//
// Blocco C1-Step 12 (June 2026; user-documented Step 11): the
// storage capability migrated to the canonical Build contract
// (storage.Build(deps) → *StorageDescriptor). The Prohibition set
// below continues to enforce the composition-root-only invariant
// for catalogsync.Service — patterns that would mean "the api/
// layer built a service that the composition root should have built"
// still fail this gate.
//
// The legacy allowlist (docs/migrations/api-infrastructure-imports-
// allowlist.txt) was deleted together with the retired roots; storage
// has 0 infra imports, so the `internal/infrastructure/` prohibition
// below is a forward-tripwire (any future infra import lands as a
// hard fail).
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	// PG-003 (June 2026) inline infra-import gate (Step 8/9
	// precedent): any `internal/infrastructure/` import fails the
	// static gate. Grep-verified: zero hits in
	// internal/api/assets/storage/* at HEAD.
	{Name: "no infrastructure imports", Pattern: "internal/infrastructure/"},
	// Step 6 NIT-B precedent: `storage.SyncFolder` would be a
	// direct-orchestrator constructor for the *catalogsync.Service
	// façade the storage Handler depends on; the api/ layer must
	// NOT call it directly. Composition-root-only invariant:
	// constructed exactly once at the composition root via the
	// catalogsync wiring under deps.Core.CatalogSyncService and
	// threaded through Build(deps.CatalogSync). Grep-verified: zero
	// hits in internal/api/** production code at HEAD.
	{Name: "storage.SyncFolder direct construction", Pattern: "storage.SyncFolder"},
}

func TestStaticGate_NoStorageAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
