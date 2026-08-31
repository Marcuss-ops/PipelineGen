// Package outbox — dispatcher_advance.go (Blocco 3.1 commit 3/3; updated Blocco 3.2 commit 1/2, June 2026)
//
// Blocco 3.2 commit 1/2 prerequisite fix: the UPDATE now stamps
// `updated_at = '<now>'` alongside the lifecycle_state flip.
// SQLite does NOT auto-update `updated_at` on UPDATE (the
// `CURRENT_TIMESTAMP` default only fires on INSERT); without the
// explicit stamp the Blocco 3.2 DeletionReconciler's
// `WHERE updated_at < now-threshold` stuck-row query returns every
// deletion-chain row regardless of when the flip happened. See
// `clips_lifecycle_state.go::SetLifecycleState` for the repository
// precedent (which has always stamped updated_at — this fix
// brings the dispatcher paths into the same agreement).
//
// AdvanceAndEmit is the canonical primitive for state-machine
// transitions that REQUIRE atomic coupling between a lifecycle_state
// flip AND the emission of the next outbox event in the chain.
//
// Lifecycle:
//
//	ACTIVE → DELETE_REQUESTED → DRIVE_DELETE_PENDING → INDEX_DELETE_PENDING → DELETED
//
// The Dispatcher.EnqueueDriveDelete stamps the FIRST-HOP
// (lifecycle_state='DELETE_REQUESTED') AND emits the FIRST event
// ('asset.drive.delete_requested.v1') in a single tx — that path
// already exists in dispatcher_delete.go and does not need a new
// method. The MIDDLE-HOP transitions (DRIVE_DELETE_PENDING →
// INDEX_DELETE_PENDING, INDEX_DELETE_PENDING → DELETED) need the
// AdvanceAndEmit primitive because:
//
//	(a) the state-flip is the operator-visible marker the row has
//	    progressed past a side-effect (Drive API success / Qdrant
//	    delete + SQLite soft-delete);
//
//	(b) the next-event emission is the durable handoff to the
//	    next-stage worker — losing it would strand the row in
//	    a permanent in-flight state without a consumer.
//
// Pattern 0 (AGENTS.md): the application-layer handler (e.g.
// DriveDeleteHandler in application/jobs/outbox/drive_delete.go)
// calls this method WITHOUT seeing a *sql.Tx — the tx boundary
// stays encapsulated in Dispatcher. The application-layer handler
// declares a narrow port (StateAdvancer in ports.go) that mirrors
// AdvanceAndEmit's signature exactly; production wiring satisfies
// it via `*outbox.Dispatcher` (compile-time assertion lives in
// the composition root).
//
// Idempotency contract:
//
//   - outbox_events.event_key ON CONFLICT DO NOTHING absorbs repeated
//     enqueues of the same key.
//   - UPDATE media_assets SET lifecycle_state WHERE lifecycle_state
//     = expectedState absorbs re-enqueues whose state has already
//     advanced — the UPDATE row count is 0, the function returns
//     nil without emitting a redundant event.
//
// The contract is essential for retryability: when the outbox
// pool's lease-fence re-issues an event after a worker crash mid-
// flow, the row's lifecycle_state has already advanced past
// expectedState, the UPDATE is a no-op, and the pool moves the
// event to completed without a duplicate EventAssetIndexDeleteRequested
// (or whichever event the AdvanceAndEmit was about to emit).
package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// AdvanceAndEmit atomically transitions a media_assets row from
// expectedState to newState (lifecycle_state) AND emits an outbox
// event of the specified type in a SINGLE transaction. Either both
// writes commit, or neither does.
//
// expectedState gates the UPDATE so re-enqueues from a CRASH-MID-
// FLOW case (the outbox-pool lease-fence re-issues the event after
// a worker crash) are no-ops at the state-machine layer: the row
// has already advanced past expectedState, the UPDATE row count
// is 0, and the function returns nil without emitting the redundant
// event. The outbox ON CONFLICT(event_key) DO NOTHING layer is a
// belt-and-suspenders defence for the moment an event_key collision
// occurs with the row in an unexpected state.
//
// Pre-conditions:
//
//   - Dispatcher.txmgr + outboxEventsRepo must be wired (else error).
//   - assetID must be non-empty (else error).
//   - expectedState and newState must both be Valid() lifecycle
//     states (else error).
//   - eventType must be non-empty (else error).
//   - payloadJSON can be empty for marker events; eventKey MUST be
//     non-empty (else error).
//
// Side-effects on the media_assets row:
//
//   - lifecycle_state flipped from expectedState to newState.
//   - updated_at column stamped to the current RFC3339 timestamp.
//     Blocco 3.2 commit 1/2 fix: SQLite does NOT auto-update
//     `updated_at` on UPDATE; the explicit stamp is required for
//     the DeletionReconciler's stuck-row threshold query
//     (`WHERE updated_at < now-threshold`) to be meaningful.
//     This mirrors the repository-layer SetLifecycleState precedent
//     at clips_lifecycle_state.go (which has always stamped
//     updated_at).
//
// Side-effects on the outbox_events row:
//
//   - INSERT a row keyed by event_key (uniqueness gate via the
//     existing schema index). The producer-side buildXxxRequestV1
//     helpers produce payloads whose IdempotencyKey MUST equal
//     eventKey — the caller enforces this invariant explicitly
//     because there is no schema-level coupling.
//
// Returns:
//   - nil + (no error) on success or on idempotent re-enqueue.
//   - non-nil error on tx-level failure (e.g. SQL I/O, schema
//     mismatch); the outbox pool retries with exponential backoff.
func (d *Dispatcher) AdvanceAndEmit(
	ctx context.Context,
	assetID string,
	expectedState, newState asset.LifecycleState,
	eventType string,
	payloadJSON []byte,
	eventKey string,
) error {
	if d == nil {
		return errors.New("outbox.Dispatcher is nil")
	}
	if d.txmgr == nil {
		return errors.New("outbox.Dispatcher: txmgr not configured")
	}
	if d.outboxEventsRepo == nil {
		return errors.New("outbox.Dispatcher: outbox events repo not configured")
	}
	if assetID == "" {
		return errors.New("outbox.Dispatcher.AdvanceAndEmit: assetID is required")
	}
	if !expectedState.Valid() {
		return fmt.Errorf("outbox.Dispatcher.AdvanceAndEmit: expectedState %q is not canonical", expectedState)
	}
	if !newState.Valid() {
		return fmt.Errorf("outbox.Dispatcher.AdvanceAndEmit: newState %q is not canonical", newState)
	}
	if eventType == "" {
		return errors.New("outbox.Dispatcher.AdvanceAndEmit: eventType is required")
	}
	if eventKey == "" {
		return errors.New("outbox.Dispatcher.AdvanceAndEmit: eventKey is required")
	}

	return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		// Blocco 3.2 commit 1/2 prerequisite fix: explicit `updated_at = <now>`
		// stamp alongside the lifecycle_state flip. SQLite does not
		// auto-update updated_at on UPDATE (the column default
		// CURRENT_TIMESTAMP only fires on INSERT), so without this
		// stamp the DeletionReconciler's `WHERE updated_at < now-threshold`
		// query would return every deletion-chain row regardless of
		// when the flip actually happened. Mirrors the repository-layer
		// SetLifecycleState at clips_lifecycle_state.go.
		nowStr := timeutil.FormatRFC3339(time.Now())
		affected, err := imagesregistry.UpdateMediaAssetLifecycleCAS(ctx, tx, assetID, string(expectedState), string(newState), nowStr)
		if err != nil {
			return fmt.Errorf("advance-and-emit: stamp %s -> %s for %s: %w",
				expectedState, newState, assetID, err)
		}
		// No-op: row has already advanced past expectedState, OR the
		// row does not exist. Both are idempotent-retry friendly;
		// skip the event emission so a re-enqueue from CRASH-MID-FLOW
		// does not produce a duplicate EventAssetIndexDeleteRequested
		// (or whichever event was queued).
		// The canonical helper performs the expected-state CAS. A zero-row
		// transition is idempotent and does not need event emission.
		if affected == 0 {
			if d.log != nil {
				d.log.Debug("advance-and-emit: row not in expected state, skipping event emission (idempotent re-enqueue)",
					zap.String("asset_id", assetID),
					zap.String("expected_state", string(expectedState)),
					zap.String("new_state", string(newState)),
				)
			}
			return nil
		}

		if _, err := d.outboxEventsRepo.Enqueue(
			ctx, tx,
			eventType,
			assetID,
			"media_asset",
			string(payloadJSON),
			eventKey,
		); err != nil {
			return fmt.Errorf("advance-and-emit: enqueue %s for %s: %w", eventType, assetID, err)
		}

		if d.log != nil {
			d.log.Debug("advance-and-emit: lifecycle flipped + event emitted",
				zap.String("asset_id", assetID),
				zap.String("from_state", string(expectedState)),
				zap.String("to_state", string(newState)),
				zap.String("event_type", eventType),
			)
		}
		return nil
	})
}
