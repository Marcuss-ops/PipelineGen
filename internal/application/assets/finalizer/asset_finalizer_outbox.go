// Package finalizer — asset_finalizer_outbox.go (split from
// asset_finalizer_tx.go, July 2026): helper SQL for the canonical
// outbox_events table.
//
// Owns:
//
//  1. func (s *AssetTxFinalizer) insertOutboxEvent — INSERT a
//     finalization.OutboxEvent row inside the caller's tx so
//     the IndexingHandler can pick it up atomically after the
//     caller's tx.Commit succeeds.
//
// Idempotency contract: ON CONFLICT(event_key) WHERE event_key
// != ” DO NOTHING. Mirrors the codebase's partial-index pattern
// (idx_jobs_active_key, idx_artifacts_sha256): uniqueness triggers
// ONLY when event_key is non-empty, so one-shot inserts with
// event_key=” are NOT uniqueness-constrained. This is the
// fail-closed idempotency contract for re-finalization — same
// artifact, same content_hash, idempotent no-op on retry.
//
// Caller-owned-tx discipline (godlike/06 SSOT, non-negotiable
// architectural rule): same as sibling helpers. Does NOT own
// BeginTx.
//
// aggregate_type="media_asset" (godlike/06 SSOT): the canonical
// aggregate-type label for the IndexingHandler's dispatcher-side
// partition. Hardcoded here because every canonical asset-level
// outbox event is keyed on a media_assets.id; per-rendition
// events would route to a different aggregate_type but they are
// out of scope for AssetTxFinalizer's surface area.
//
// Mechanical split from asset_finalizer_tx.go. Zero behavior
// change. The receiver (s *AssetTxFinalizer) is unchanged so the
// orchestrator can call this helper as `s.insertOutboxEvent(...)`
// without any wiring change.
package finalizer

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// insertOutboxEvent persists an outbox event inside the caller's
// transaction.
func (s *AssetTxFinalizer) insertOutboxEvent(
	ctx context.Context,
	tx finalization.Transaction,
	event finalization.OutboxEvent,
	nowStr string,
) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO outbox_events (
			event_type, aggregate_id, aggregate_type, payload_json, event_key, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING
	`,
		event.EventType,
		event.AggregateID,
		"media_asset",
		string(event.Payload),
		event.EventKey,
		nowStr,
		nowStr,
	)
	if err != nil {
		return fmt.Errorf("asset finalizer: insert outbox event %s: %w", event.EventKey, err)
	}
	return nil
}
