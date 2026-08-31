package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// EnqueueAndDelete performs the canonical DISPATCH step of an
// asset.delete flow (QDRANT-002 PR7, Blocco 3.1):
//
//	tx body:
//	  1. SET lifecycle_state=DELETE_REQUESTED on media_assets
//	  2. INSERT outbox_events (event_type='asset.drive.delete_requested')
//
// Both writes commit atomically. After commit, the outboxevents
// Pool picks up the event and runs DriveDeleteHandler.Handle, which
// performs the actual Drive API call (Trash or Delete) and stamps
// the next hop (INDEX_DELETE_PENDING) plus emits the second event.
//
// IMPORTANT: this function does NOT call repo.SoftDelete and does
// NOT call any external API (Drive or Qdrant). Every step is
// durable, retryable on outbox-pool lease-fence, and individually
// reconcilable by deletion.DeletionReconciler.
//
// Blocco 3.1: this is the BACKWARD-COMPATIBILITY SHIM for the
// pre-Blocco 3.1 EnqueueAndDelete(assetID) signature. New callers
// MUST use EnqueueDriveDelete(assetID, permanently) to express
// intent (permanent delete vs. recoverable trash). The shim
// routes Trashed=false so legacy callers (the AssetMutationDispatcher
// port surface in internal/capabilities/assets/mutations/dispatcher.go,
// admin tooling in cmd/admin/qdrant_maintenance.go, test stubs in
// internal/capabilities/assets/artifacts/dispatcher_fail_closed_test.go
// etc.) continue to operate under the new chain.
func (d *Dispatcher) EnqueueAndDelete(ctx context.Context, assetID string) error {
	return d.EnqueueDriveDelete(ctx, assetID, false)
}

// EnqueueDriveDelete performs the FIRST HOP of the Blocco 3.1
// deletion state machine:
//
//	tx body:
//	  1. SET lifecycle_state='DELETE_REQUESTED' on media_assets
//	  2. INSERT outbox_events (event_type=EventAssetDriveDeleteRequested)
//
// Both writes commit atomically. After commit, the outboxevents
// Pool picks up the event and runs DriveDeleteHandler.Handle (in
// application/jobs/outbox/drive_delete.go), which performs the
// actual Drive API call (Trash or Delete depending on the
// permanently flag in the envelope) and stamps the second hop
// (INDEX_DELETE_PENDING) plus emits EventAssetIndexDeleteRequested.
//
// Per-step retryability: on a transient Drive API failure, the
// handler leaves the row in DRIVE_DELETE_PENDING and returns a
// retryable error so the outbox pool retries with exponential
// backoff. Idempotency is enforced at TWO layers:
//   - (a) outbox_events.event_key ON CONFLICT DO NOTHING absorbs
//     repeated enqueues of the same {asset_id, permanently}.
//   - (b) DriveDeleteHandler pre-flight skips if lifecycle_state
//     has already advanced past DRIVE_DELETE_PENDING.
//
// Per-step reconcilability: DeletionReconciler runs every
// reconciliationInterval (e.g. 15 min) and re-enqueues the correct
// event for any row that's stuck in DELETE_REQUESTED or
// DRIVE_DELETE_PENDING beyond a stuck threshold — picking up where
// a crashed worker left off.
//
// Blocco 3.1: production deletion (deletion.DeletionService.DeleteClip)
// now routes exclusively through this method; EnqueueAndDelete is
// preserved only for the AssetMutationDispatcher port compat shim.
//
// IMPORTANT: this function does NOT call drive.FileLifecycle.Trash,
// drive.FileLifecycle.Delete, qdrant.DeletePoints, or
// repo.SoftDelete. Every side-effect lives in a later outbox handler.
func (d *Dispatcher) EnqueueDriveDelete(ctx context.Context, assetID string, permanently bool) error {
	if d == nil {
		return errors.New("Dispatcher is nil")
	}
	if d.txmgr == nil {
		return errors.New("Dispatcher: txmgr not configured")
	}
	if d.outboxEventsRepo == nil {
		return errors.New("Dispatcher: outbox events repo not configured")
	}
	if assetID == "" {
		return errors.New("Dispatcher.EnqueueDriveDelete: assetID is required")
	}

	return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		// Stamp lifecycle_state=DELETE_REQUESTED only on rows that
		// are NOT already in a deletion chain (idempotency envelope
		// at the state-machine layer). DELETE_PENDING is part of the
		// legacy enum and is treated as "already in flight" so the
		// reconciler picks it up via the legacy rewrite path
		// (asset.IsValidTransition handles DELETE_PENDING →
		// DRIVE_DELETE_PENDING on a future re-enqueue).
		//
		// Blocco 3.2 commit 1/2 prerequisite fix: also stamp
		// `updated_at = <now>` on the flip. SQLite does NOT
		// auto-update updated_at on UPDATE; without this stamp
		// the DeletionReconciler's `WHERE updated_at < now-threshold`
		// stuck-row query returns EVERY deletion-chain row
		// regardless of when the flip happened (the column would
		// still reflect the original INSERT timestamp). Mirrors the
		// repository-layer SetLifecycleState at clips_lifecycle_state.go.
		nowStr := timeutil.FormatRFC3339(time.Now())
		if _, err := tx.ExecContext(ctx, `
	UPDATE media_assets
	   SET lifecycle_state = 'DELETE_REQUESTED',
	       updated_at = ?
	 WHERE id = ?
	   AND lifecycle_state NOT IN ('DELETE_REQUESTED', 'DELETE_PENDING', 'DRIVE_DELETE_PENDING', 'INDEX_DELETE_PENDING', 'DELETED')
	`, nowStr, assetID); err != nil {
			return fmt.Errorf("dispatcher drive-delete: stamp lifecycle_state=DELETE_REQUESTED %s: %w", assetID, err)
		}

		payload := buildDriveDeleteRequestV1(assetID, permanently)
		eventKey := driveDeleteEventKey(assetID, permanently)
		if payload.IdempotencyKey != eventKey {
			return fmt.Errorf("dispatcher: drive-delete payload.IdempotencyKey (%q) != event_key (%q) — v1 conflation invariant broken", payload.IdempotencyKey, eventKey)
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("dispatcher marshal v1 drive-delete payload %s: %w", assetID, err)
		}

		if _, err := d.outboxEventsRepo.Enqueue(
			ctx, tx,
			outboxevents.EventAssetDriveDeleteRequested,
			assetID,
			"media_asset",
			string(payloadJSON),
			eventKey,
		); err != nil {
			return fmt.Errorf("dispatcher enqueue outbox drive-delete event %s: %w", assetID, err)
		}

		if d.log != nil {
			d.log.Debug("dispatcher enqueued asset for outbox drive-delete (v1 envelope)",
				zap.String("asset_id", assetID),
				zap.Bool("permanently", permanently),
				zap.String("outbox_event_id", payload.EventID),
			)
		}
		return nil
	})
}

// EnqueueIndexDelete re-emits an asset.index.delete_requested outbox
// event WITHOUT advancing the lifecycle state. Used by the Blocco 3.2
// DeletionReconciler for rows stuck in {DRIVE_DELETE_PENDING,
// INDEX_DELETE_PENDING}: the reconciler re-emits the event so a fresh
// lease-fenced worker can pick it up; the IndexDeleteHandler does the
// actual Qdrant-delete + SoftDelete + DELETED state flip on its own.
//
// Unlike EnqueueDriveDelete + AdvanceAndEmit (which ATOMICALLY stamp
// lifecycle_state + emit), this method is emit-only. The signature
// is intentionally distinct from EnqueueDriveDelete(assetID, bool)
// because re-emitting an index-delete from the reconciler is a
// recovery operation, not a user-initiated one:
//
//	tx body:
//	  UPDATE media_assets SET updated_at = ?
//	   WHERE id = ?
//	  INSERT outbox_events (event_type=EventAssetIndexDeleteRequested,
//	                        aggregate_id=assetID,
//	                        aggregate_type="media_asset",
//	                        event_key=delete:<assetID>)
//
// updated_at re-stamp policy (CIRCUIT-BREAKER pattern, June 2026):
//
// The UPDATE re-stamps `updated_at = <now>` so the row exits the
// stuck-threshold window immediately after re-emission. This is a
// deliberate rate-limit on retries:
//
//   - Without the re-stamp: the row stays "stuck" (updated_at
//     unchanged), so the NEXT reconciler tick (typically 15 min later)
//     re-emits the same row, amplifying any underlying failure into
//     a hot loop. A permanently-failing Drive API would get hammered
//     every 15 min with no operator-visible backoff.
//
//   - With the re-stamp: the row exits the stuck-threshold window for
//     `threshold` minutes (default 30). If the worker succeeds within
//     that window, the row transitions to DELETED and never re-surfaces.
//     If the worker fails, the row's updated_at goes stale again
//     after `threshold` minutes and the reconciler picks it up — a
//     explicit circuit-breaker with bounded retry rate.
//
// The downside: if the worker processes the event PARTIALLY
// (e.g. Qdrant delete succeeded, SoftDelete write crashed mid-flight),
// the row remains in INDEX_DELETE_PENDING with fresh updated_at and
// won't be re-emitted for `threshold` minutes. This is an accepted
// trade-off — the 30-min rate-limit applies uniformly and partial
// failures are caught by the IndexDeleteHandler's idempotent clip_id
// ledger on the next genuine re-emission. See
// internal/capabilities/jobs/outbox/index_delete.go::Handle for the
// pre-flight idempotency contract.
//
// v1 conflation invariant: payload.IdempotencyKey MUST equal the
// canonical event_key `delete:<assetID>` — same host as
// delete_envelope.go::deleteEventKey. Mismatch surfaces as a runtime
// error rather than silently corrupting the dedup layer.
func (d *Dispatcher) EnqueueIndexDelete(ctx context.Context, assetID string) error {
	if d == nil {
		return errors.New("Dispatcher is nil")
	}
	if d.txmgr == nil {
		return errors.New("Dispatcher: txmgr not configured")
	}
	if d.outboxEventsRepo == nil {
		return errors.New("Dispatcher: outbox events repo not configured")
	}
	if assetID == "" {
		return errors.New("Dispatcher.EnqueueIndexDelete: assetID is required")
	}

	payload := buildDeleteRequestV1(assetID)
	eventKey := deleteEventKey(assetID)
	if payload.IdempotencyKey != eventKey {
		return fmt.Errorf("dispatcher: index-delete payload.IdempotencyKey (%q) != event_key (%q) — v1 conflation invariant broken", payload.IdempotencyKey, eventKey)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("dispatcher marshal v1 index-delete payload %s: %w", assetID, err)
	}

	return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		// Re-stamp updated_at (no lifecycle_state change): keeps the
		// row's timestamp fresh for the next reconciler tick so the
		// same row doesn't re-surface immediately. Mirrors the
		// updated_at stamping convention from Blocco 3.2 commit 1/2.
		nowStr := timeutil.FormatRFC3339(time.Now())
		if _, err := tx.ExecContext(ctx, `UPDATE media_assets SET updated_at = ? WHERE id = ?`,
			nowStr, assetID); err != nil {
			return fmt.Errorf("dispatcher index-delete: re-stamp updated_at %s: %w", assetID, err)
		}

		if _, err := d.outboxEventsRepo.Enqueue(
			ctx, tx,
			outboxevents.EventAssetIndexDeleteRequested,
			assetID,
			"media_asset",
			string(payloadJSON),
			eventKey,
		); err != nil {
			return fmt.Errorf("dispatcher enqueue outbox index-delete event %s: %w", assetID, err)
		}

		if d.log != nil {
			d.log.Debug("dispatcher enqueued asset for outbox index-delete (v1 envelope — reconciler recovery path)",
				zap.String("asset_id", assetID),
				zap.String("outbox_event_id", payload.EventID),
			)
		}
		return nil
	})
}

// EnqueueAndRestore performs the canonical DISPATCH step of an
// asset.restore flow (Wave 22, June 2026 — task 1 of 5 foundation).
//
//	tx body:
//	  1. SET index_state=PENDING on media_assets
//	  2. INSERT outbox_events (event_type='asset.index.restore_requested')
func (d *Dispatcher) EnqueueAndRestore(ctx context.Context, assetID string) error {
	if d == nil {
		return errors.New("Dispatcher is nil")
	}
	if d.txmgr == nil {
		return errors.New("Dispatcher: txmgr not configured")
	}
	if d.stateWriter == nil {
		return errors.New("Dispatcher: state writer not configured (required for EnqueueAndRestore — wire *assets.ClipsRepository)")
	}
	if d.outboxEventsRepo == nil {
		return errors.New("Dispatcher: outbox events repo not configured")
	}
	if assetID == "" {
		return errors.New("Dispatcher.EnqueueAndRestore: assetID is required")
	}

	return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := d.stateWriter.SetIndexStateTx(ctx, tx, assetID, asset.StateDiscovered); err != nil {
			return fmt.Errorf("dispatcher restore: set index_state=DISCOVERED %s: %w", assetID, err)
		}

		eventID := uuid.NewString()
		eventKey := fmt.Sprintf("restore:%s", assetID)
		payload := restoreRequestV1{
			SchemaVersion:  "asset.index.restore_requested.v1",
			EventID:        eventID,
			AssetID:        assetID,
			Operation:      "RESTORE",
			IdempotencyKey: eventKey,
			RequestedAt:    timeutil.FormatRFC3339(time.Now()),
		}
		if payload.IdempotencyKey != eventKey {
			return fmt.Errorf("dispatcher: restore payload.IdempotencyKey (%q) != event_key (%q) — v1 conflation invariant broken", payload.IdempotencyKey, eventKey)
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("dispatcher marshal v1 restore payload %s: %w", assetID, err)
		}

		if _, err := d.outboxEventsRepo.Enqueue(
			ctx, tx,
			outboxevents.EventAssetIndexRestoreRequested,
			assetID,
			"media_asset",
			string(payloadJSON),
			eventKey,
		); err != nil {
			return fmt.Errorf("dispatcher enqueue outbox restore event %s: %w", assetID, err)
		}

		if d.log != nil {
			d.log.Debug("dispatcher enqueued asset for outbox_events restoration (v1 envelope)",
				zap.String("asset_id", assetID),
				zap.String("outbox_event_id", eventID),
			)
		}
		return nil
	})
}
