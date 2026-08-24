// Package reconciler — ports.go (Blocco 3.2 commit 2/2, June 2026)
//
// Application-layer port declarations. Mirrors the
// qdrant/reconciler/ports.go "ports-only-no-concrete" pattern:
// this file declares INTERFACES that infrastructure adapters
// satisfy in production. Pattern 0 from AGENTS.md.
//
// Production wiring (composition root):
//
//	Scanner         ← internal/infrastructure/database/sqlite/deletion/stuck_row_scanner.go
//	                   (ListStuckRows against media_assets)
//	OutboxEnqueuer  ← *outbox.Dispatcher (EnqueueDriveDelete + EnqueueIndexDelete)
//
// Scanner is REQUIRED per ServiceDeps nil-checks. OutboxEnqueuer
// is REQUIRED for the same nil-panic principle (production
// half-wired Service silently no-op'd the entire dispatch phase
// pre-Blocco 2.0 PR 10 lessons — see qdrant/reconciler/ports.go
// comment on noopOutboxEnqueuer vs nil-check panic).
package reconciler

import (
	"context"
	"time"
)

// Clock abstracts time.Now for deterministic tests. Production
// caller passes time.Now; tests pass a closure over a fixed
// baseline. Mirrors qdrant/reconciler/ServiceDeps.Now convention.
// Inlined as the canonical Go func() time.Time signature so the
// production-wire path can assign time.Now directly without a type
// conversion (avoiding the named-type shadowing confusion in the
// service-goroutine tick loop).
type Clock = func() time.Time

// OutboxEnqueuer is the application-layer port for re-emitting
// outbox events for stuck rows. Production concrete satisfies
// this with *outbox.Dispatcher (compile-time assertion at the
// composition root).
//
// The port signatures are aligned 1:1 with the production
// Dispatcher methods (Pattern 0 — port declares the contract
// the single concrete adapter satisfies). For the reconciler
// re-emission path:
//
//   - EnqueueDriveDelete(ctx, assetID, false) is the safe
//     requeue: row is already in a deletion-chain state so the
//     state-flip SQL is idempotent (no-op WHERE filter); the
//     outbox INSERT emits EventAssetDriveDeleteRequested with the
//     `delete:<assetID>:trash` event_key (ON CONFLICT absorbs
//     any repeat). The Trash route is the safe fallback when
//     the original `permanently` intent isn't recoverable from
//     the row's own state.
//
//   - EnqueueIndexDelete(ctx, assetID) emits
//     EventAssetIndexDeleteRequested with the `delete:<assetID>`
//     event_key. Internally it re-stamps updated_at
//     (CIRCUIT-BREAKER pattern: rate-limits retries to once per
//     stuck threshold, see EnqueueIndexDelete docstring) and
//     leaves lifecycle_state untouched.
//
// Idempotency is enforced at TWO layers:
//
//	(a) outbox_events.event_key ON CONFLICT DO NOTHING absorbs
//	    repeated enqueues of the same {assetID, hop} pair.
//	(b) The handlers (DriveDeleteHandler, IndexDeleteHandler)
//	    have idempotent pre-flights that skip rows already past
//	    the relevant state-machine hop.
//
// Errors are non-nil ONLY on transient infrastructure failures
// (SQL I/O, txmgr-down, etc). On any non-nil error the
// ReconcileOnce loop logs + bumps the errored counter; the row is
// NOT removed from the next tick's scan (a transient dispatch
// failure leaves the row eligible for re-evaluation until the
// underlying issue is resolved).
type OutboxEnqueuer interface {
	// EnqueueDriveDelete emits EventAssetDriveDeleteRequested.
	// Reconciler callers pass permanently=false (the safe Trash
	// route) because the original `permanently=true` intent is
	// not preserved on a stuck row — re-emission is by definition
	// a recovery operation, not a user-initiated one.
	EnqueueDriveDelete(ctx context.Context, assetID string, permanently bool) error

	// EnqueueIndexDelete emits EventAssetIndexDeleteRequested +
	// re-stamps updated_at (CIRCUIT-BREAKER pattern). Used for
	// DRIVE_DELETE_PENDING and INDEX_DELETE_PENDING stuck rows.
	EnqueueIndexDelete(ctx context.Context, assetID string) error
}

// Metrics is the application-layer observability port. Production
// wiring satisfies this with the canonical Prometheus-backed
// implementations at
// internal/platform/observability/metrics_media.go.
//
// Each method is called at most ONCE per row per tick.
//
// Emission contract:
//   - RecordRepair: bumps deletion_reconciler_actions_total{action=...,
//     from_state=...} — the "published work" signal for dashboards.
//   - RecordSkipped: bumps deletion_reconciler_skipped_total{reason=...}
//     — and increments by 1 per skipped row (operator visibility).
//   - RecordErrored: bumps deletion_reconciler_errors_total by 1 per
//     failed dispatch.
//   - RecordRunComplete: sets last_success_timestamp_seconds gauge
//     AND populates the duration histogram (mode=manual).
type Metrics interface {
	RecordRepair(action, fromState string)
	RecordSkipped(reason string)
	RecordErrored()
	RecordRunComplete(durationSeconds float64)
}
