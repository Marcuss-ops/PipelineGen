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
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"github.com/google/uuid"
)

// EnqueueAndIndex performs UPSERT media_assets + INSERT outbox_events
// (event_type='asset.index.requested') in a single atomic transaction,
// then commits. After commit, the outboxevents Pool will see the new
// pending event and run IndexClip on it asynchronously via the
// IndexingHandler.
//
// Callers MUST NOT subsequently run SafeGoFunc(IndexClip(...)) — the
// outbox event IS the indexing trigger.
//
// contentHash should be the canonical content fingerprint. Used to build
// the event_key for deduplication, so duplicate ingestions are safe.
//
// Folders (clip.IsFolder == true) MUST be filtered by the caller before
// calling — vector indexing of folders is meaningless.
func (d *Dispatcher) EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error {
	if d == nil {
		return errors.New("outbox.Dispatcher is nil")
	}
	if d.txmgr == nil {
		return errors.New("outbox.Dispatcher: txmgr not configured")
	}
	if d.clips == nil {
		return errors.New("outbox.Dispatcher: clips repo not configured")
	}
	if d.outboxEventsRepo == nil {
		return errors.New("outbox.Dispatcher: outbox events repo not configured")
	}
	if clip == nil || clip.ID == "" {
		return errors.New("clip with non-empty ID is required")
	}
	// Folders are not vector-indexable.
	if clip.IsFolder() {
		return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
			if err := d.clips.UpsertClipTx(ctx, tx, clip); err != nil {
				return fmt.Errorf("dispatcher upsert folder %s: %w", clip.ID, err)
			}
			if d.log != nil {
				d.log.Debug("dispatcher skipped outbox enqueue for folder",
					zap.String("asset_id", clip.ID),
				)
			}
			return nil
		})
	}

	return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := d.clips.UpsertClipTx(ctx, tx, clip); err != nil {
			return fmt.Errorf("dispatcher upsert clip %s: %w", clip.ID, err)
		}

		eventID := uuid.NewString()
		eventKey := indexEventKey(clip.ID, contentHash)
		idempotencyKey := eventKey
		payload := indexRequestV1{
			SchemaVersion:      "asset.index.requested.v1",
			EventID:            eventID,
			AssetID:            clip.ID,
			Operation:          "UPSERT",
			SourceVersion:      contentHash,
			TargetIndexVersion: clipindexer.CollectionVersion(),
			RequestedVectors:   []string{"text", "transcript"},
			RequestedAt:        timeutil.FormatRFC3339(time.Now()),
			EmbeddingModel:     clipindexer.EmbeddingModel(),
			EmbeddingVersion:   clipindexer.EmbeddingModelVersion(),
			IdempotencyKey:     idempotencyKey,
		}
		if payload.IdempotencyKey != eventKey {
			return fmt.Errorf("dispatcher: payload.IdempotencyKey (%q) != event_key (%q) — v1 conflation invariant broken", payload.IdempotencyKey, eventKey)
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("dispatcher marshal v1 index payload %s: %w", clip.ID, err)
		}

		if err := d.outboxEventsRepo.Enqueue(
			ctx, tx,
			outboxevents.EventAssetIndexRequested,
			clip.ID,
			"media_asset",
			string(payloadJSON),
			eventKey,
		); err != nil {
			return fmt.Errorf("dispatcher enqueue outbox event %s: %w", clip.ID, err)
		}

		if d.log != nil {
			d.log.Debug("dispatcher enqueued asset for outbox_events indexing (v1 envelope)",
				zap.String("asset_id", clip.ID),
				zap.String("outbox_event_id", eventID),
				zap.String("source", string(clip.Source)),
				zap.String("source_version", contentHash),
				zap.String("content_hash_prefix", shortHashPrefix(contentHash)),
			)
		}
		return nil
	})
}

// EnqueueIndexEvent emits the canonical asset.index.requested.v1
// envelope INSIDE a caller-owned *sql.Tx. PR-VO-A3 (Outbox-based
// Qdrant indexing, June 2026): this method is the Voiceover path's
// narrow entry point.
func (d *Dispatcher) EnqueueIndexEvent(ctx context.Context, tx *sql.Tx, assetID, contentHash string) error {
	if d == nil {
		return errors.New("outbox.Dispatcher is nil")
	}
	if d.outboxEventsRepo == nil {
		return errors.New("outbox.Dispatcher: outbox events repo not configured")
	}
	if assetID == "" {
		return errors.New("outbox.Dispatcher.EnqueueIndexEvent: assetID is required")
	}
	if contentHash == "" {
		return errors.New("outbox.Dispatcher.EnqueueIndexEvent: contentHash is required (supersede gate cannot function without a content fingerprint)")
	}

	eventID := uuid.NewString()
	eventKey := indexEventKey(assetID, contentHash)
	payload := buildIndexRequestV1(eventID, assetID, contentHash, eventKey)
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("dispatcher marshal v1 index payload %s: %w", assetID, err)
	}

	if err := d.outboxEventsRepo.Enqueue(
		ctx, tx,
		outboxevents.EventAssetIndexRequested,
		assetID,
		"media_asset",
		string(payloadJSON),
		eventKey,
	); err != nil {
		return fmt.Errorf("dispatcher enqueue outbox event %s: %w", assetID, err)
	}

	if d.log != nil {
		d.log.Debug("dispatcher enqueued asset for outbox_events indexing via EnqueueIndexEvent (v1 envelope, caller-owned tx)",
			zap.String("asset_id", assetID),
			zap.String("outbox_event_id", eventID),
			zap.String("source_version", contentHash),
			zap.String("content_hash_prefix", shortHashPrefix(contentHash)),
		)
	}
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────

// indexEventKey is the canonical event_key constructor for the
// asset.index.requested.v1 envelope. Uses the FULL content hash.
func indexEventKey(assetID, contentHash string) string {
	return fmt.Sprintf("index:%s:%s:%s:%s:%s",
		assetID,
		contentHash,
		clipindexer.EmbeddingModel(),
		clipindexer.EmbeddingModelVersion(),
		clipindexer.CollectionVersion(),
	)
}

// buildIndexRequestV1 is the canonical v1 envelope builder shared
// between EnqueueAndIndex and EnqueueIndexEvent.
func buildIndexRequestV1(eventID, assetID, contentHash, eventKey string) indexRequestV1 {
	return indexRequestV1{
		SchemaVersion:      "asset.index.requested.v1",
		EventID:            eventID,
		AssetID:            assetID,
		Operation:          "UPSERT",
		SourceVersion:      contentHash,
		TargetIndexVersion: clipindexer.CollectionVersion(),
		RequestedVectors:   []string{"text", "transcript"},
		RequestedAt:        timeutil.FormatRFC3339(time.Now()),
		EmbeddingModel:     clipindexer.EmbeddingModel(),
		EmbeddingVersion:   clipindexer.EmbeddingModelVersion(),
		IdempotencyKey:     eventKey,
	}
}

// shortHashPrefix returns a short log-friendly prefix; the empty
// string yields "" so log readers do not see a misleading
// "(empty)" marker.
func shortHashPrefix(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}
