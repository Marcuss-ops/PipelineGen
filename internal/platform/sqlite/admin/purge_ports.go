// Package admin exposes the strictly-scoped offline / physical-purge
// surface for cmd/admin tooling + offline backfill scripts.
//
// WHY THIS PACKAGE EXISTS — QDRANT-asset-mutation isolation (June 2026)
// --------------------------------------------------------------------
// AssetMutationPrimitives (internal/application/assets/mutations) is the
// surface for the OUTBOX DISPATCHER. Production writes — even from
// the admin CLI at runtime — go through outbox.Dispatcher because the
// worker pool is live; physical deletes need to cascade to Qdrant via
// the outbox supersede-gate so the indexes don't drift.
//
// THIS package is the SURFACE FOR OFFLINE TOOLING:
//   - cmd/admin/* subcommands that run when the worker is down
//   - backfill scripts that need to physically repair a row that
//     somehow ended up in an inconsistent state (e.g. a partial
//     tx-commit from an older binary rolling back mid-flight)
//   - the offline DR drill (scripts/diagnostics/marker_audit.sh
//     runs against a standalone SQLite snapshot — needs raw
//     HardDelete without going through the live outbox)
//
// Production code paths in internal/application/** and internal/api/**
// MUST NOT import this package. The CI lint
// scripts/ci-architectural-checks.sh enforces that boundary — a hit
// on `internal/infrastructure/database/sqlite/admin` from
// `internal/application/**` or `internal/api/**` fails the gate.
//
// Bifurcation rationale:
//   - AssetMutationPrimitives: in-process, called by the dispatcher
//     inside a tx.
//   - InternalAdminPurge: post-mortem / offline repair, called from
//     cmd/admin/* with NO worker pool active. Routing this through
//     the dispatcher would be incorrect (no one to dispatch TO) and
//     would risk emitting outbox events for rows that the worker
//     pool will never see.
package admin

import (
	"context"
	"errors"
)

// ErrUnavailable is the sentinel returned when an admin tool asks for a
// purge primitive that hasn't been wired (e.g. admin.HardDeleteClip
// called against a nil *admin.Service). Identity matches
// assets.ErrAssetMutationDispatcherUnavailable so the diagnostic is
// uniform across the asset mutation surface.
var ErrUnavailable = errors.New("admin: purge primitive unavailable")

// InternalAdminPurge is the strictly-scoped admin-only physical-purge
// surface. It is intentionally separate from AssetMutationPrimitives —
// an admin tool's restore/hard-delete MUST NOT emit an outbox event
// because there is no live worker to consume it.
//
// Acceptable callers (allowlist for the CI lint):
//   - cmd/admin/**/*                                : the canonical
//     CLI-driven admin.
//   - scripts/diagnostics/marker_audit.sh           : offline DR drill.
//   - internal/infrastructure/database/sqlite/admin/*_test.go
//     : unit tests for
//     the adapter that
//     satisfies this
//     interface.
//
// Implementation MUST live at
// `internal/infrastructure/database/sqlite/admin/purge.go` and call
// the lower-level *assets.ClipsRepository mutation primitives (the
// ones marked `//nolint:production` in clips_repository.go) — there
// is intentionally NO other implementation site.
type InternalAdminPurge interface {
	// HardDeleteClip physically deletes a clip row plus its dependent
	// rows (asset_locations, asset_processing, asset_versions). It
	// bypasses the outbox because admin tooling runs offline; running
	// this through dispatcher.EnqueueAndDelete would emit an event
	// the worker pool won't process.
	//
	// Idempotent: deleting a non-existent id is a no-op (returns nil).
	// DO NOT add a non-idempotent retry loop around this method.
	HardDeleteClip(ctx context.Context, id string) error

	// RestoreClip flips lifecycle_state back to 'ready' and clears
	// deleted_at. Idempotent; offline equivalent of the canonical
	// dispatcher path for production recovery, but without an outbox
	// event (the row is restored to the search index by an explicit
	// reindex command after the admin run).
	RestoreClip(ctx context.Context, id string) error
}
