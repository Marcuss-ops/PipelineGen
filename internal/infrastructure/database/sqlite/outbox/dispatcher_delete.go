package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"github.com/google/uuid"
)

// EnqueueAndDelete performs the canonical DISPATCH step of an
// asset.delete flow (QDRANT-002 PR7):
//
//	tx body:
//	  1. SET lifecycle_state=DELETE_PENDING on media_assets
//	  2. INSERT outbox_events (event_type='asset.index.delete_requested')
//
// Both writes commit atomically. After commit, the outboxevents
// Pool picks up the event and runs IndexDeleteHandler.Handle.
//
// IMPORTANT: this function does NOT call repo.SoftDelete.
func (d *Dispatcher) EnqueueAndDelete(ctx context.Context, assetID string) error {
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
		return errors.New("outbox.Dispatcher.EnqueueAndDelete: assetID is required")
	}

	return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
		UPDATE media_assets
		   SET lifecycle_state = 'DELETE_PENDING'
		 WHERE id = ?
		   AND lifecycle_state NOT IN ('DELETE_PENDING', 'DELETED')
	`, assetID); err != nil {
			return fmt.Errorf("dispatcher delete: stamp lifecycle_state=DELETE_PENDING %s: %w", assetID, err)
		}

		payload := buildDeleteRequestV1(assetID)
		eventKey := deleteEventKey(assetID)
		if payload.IdempotencyKey != eventKey {
			return fmt.Errorf("dispatcher: delete payload.IdempotencyKey (%q) != event_key (%q) — v1 conflation invariant broken", payload.IdempotencyKey, eventKey)
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("dispatcher marshal v1 delete payload %s: %w", assetID, err)
		}

		if _, err := d.outboxEventsRepo.Enqueue(
			ctx, tx,
			outboxevents.EventAssetIndexDeleteRequested,
			assetID,
			"media_asset",
			string(payloadJSON),
			eventKey,
		); err != nil {
			return fmt.Errorf("dispatcher enqueue outbox delete event %s: %w", assetID, err)
		}

		if d.log != nil {
			d.log.Debug("dispatcher enqueued asset for outbox_events deletion (v1 envelope)",
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
		return errors.New("outbox.Dispatcher is nil")
	}
	if d.txmgr == nil {
		return errors.New("outbox.Dispatcher: txmgr not configured")
	}
	if d.stateWriter == nil {
		return errors.New("outbox.Dispatcher: state writer not configured (required for EnqueueAndRestore — wire *assets.ClipsRepository)")
	}
	if d.outboxEventsRepo == nil {
		return errors.New("outbox.Dispatcher: outbox events repo not configured")
	}
	if assetID == "" {
		return errors.New("outbox.Dispatcher.EnqueueAndRestore: assetID is required")
	}

	return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := d.stateWriter.SetIndexStateTx(ctx, tx, assetID, asset.StateIndexPending); err != nil {
			return fmt.Errorf("dispatcher restore: set index_state=PENDING %s: %w", assetID, err)
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
