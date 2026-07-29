package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	sqliteassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/idempotency"
)

// EnqueueAndIndex persists the asset and delegates the index-request outbox
// write to the canonical AssetCommitter emitter in the same transaction.
// After commit, the outbox worker invokes IndexClip asynchronously.
//
// Callers MUST NOT subsequently run SafeGoFunc(IndexClip(...)); the durable
// outbox row is the indexing trigger.
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

	// Folders are not vector-indexable. They are persisted without an index
	// request, and this branch intentionally precedes the content-hash guard.
	if clip.IsFolder() {
		return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
			if err := d.clips.UpsertClipTx(ctx, tx, clip); err != nil {
				return fmt.Errorf("dispatcher upsert folder %s: %w", clip.ID, err)
			}
			if d.log != nil {
				d.log.Debug("dispatcher skipped indexing request for folder",
					zap.String("asset_id", clip.ID),
				)
			}
			return nil
		})
	}
	if contentHash == "" {
		return fmt.Errorf("outbox.Dispatcher.EnqueueAndIndex: contentHash is required for non-folder clip %s (supersede gate cannot function without a content fingerprint — callers must set file_hash before dispatching)", clip.ID)
	}

	return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := d.clips.UpsertClipTx(ctx, tx, clip); err != nil {
			return fmt.Errorf("dispatcher upsert clip %s: %w", clip.ID, err)
		}

		commitResult, err := sqliteassets.CommitIndexRequestTx(
			ctx,
			tx,
			d.outboxEventsRepo,
			sqliteassets.IndexRequest{
				AssetID:                  clip.ID,
				Source:                   string(clip.Source),
				SourceVersion:            contentHash,
				RequestedAt:              time.Now(),
				UseProviderEventKey:      true,
				IncludeEmbeddingMetadata: true,
			},
		)
		if err != nil {
			return fmt.Errorf("dispatcher commit index request %s: %w", clip.ID, err)
		}

		if d.log != nil {
			d.log.Debug("dispatcher committed asset indexing request via canonical AssetCommitter",
				zap.String("asset_id", clip.ID),
				zap.String("outbox_event_id", commitResult.EventID),
				zap.String("outbox_event_key", commitResult.EventKey),
				zap.String("source", string(clip.Source)),
				zap.String("source_version", contentHash),
				zap.String("content_hash_prefix", shortHashPrefix(contentHash)),
			)
		}
		return nil
	})
}

// SaveDiscoveredAsset is the discovery-only upsert path. It writes the clip
// row with the supplied lifecycle and index states but deliberately emits no
// indexing request. The processing finalizer emits one only after the clip has
// a real hash and its upload has completed.
func (d *Dispatcher) SaveDiscoveredAsset(ctx context.Context, clip *asset.Asset, lifecycle asset.LifecycleState, idx asset.IndexState) error {
	if d == nil {
		return errors.New("outbox.Dispatcher is nil")
	}
	if d.txmgr == nil {
		return errors.New("outbox.Dispatcher: txmgr not configured")
	}
	if d.clips == nil {
		return errors.New("outbox.Dispatcher: clips repo not configured")
	}
	if clip == nil || clip.ID == "" {
		return errors.New("clip with non-empty ID is required")
	}
	if !lifecycle.Valid() {
		return fmt.Errorf("dispatcher.SaveDiscoveredAsset(%q): lifecycle_state %q is not canonical (Valid()=false)", clip.ID, lifecycle)
	}
	if !idx.Valid() {
		return fmt.Errorf("dispatcher.SaveDiscoveredAsset(%q): index_state %q is not canonical (Valid()=false)", clip.ID, idx)
	}

	clip.LifecycleState = lifecycle
	clip.SetMetadataString("index_state", string(idx))

	jobKey, err := idempotency.JobKey(string(clip.Source), clip.ID, "discovered")
	if err != nil {
		return fmt.Errorf("dispatcher.SaveDiscoveredAsset(%q): stamp job_key: %w", clip.ID, err)
	}
	clip.SetMetadataString("job_key", jobKey)

	return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := d.clips.UpsertClipTx(ctx, tx, clip); err != nil {
			return fmt.Errorf("dispatcher upsert clip %s: %w", clip.ID, err)
		}
		if d.log != nil {
			d.log.Debug("dispatcher saved discovered asset without indexing request",
				zap.String("asset_id", clip.ID),
				zap.String("lifecycle_state", string(lifecycle)),
				zap.String("index_state", string(idx)),
			)
		}
		return nil
	})
}

// EnqueueIndexEvent delegates a tx-bound indexing request to the canonical
// AssetCommitter emitter. The caller must already have persisted the matching
// media_assets state in tx before invoking this method.
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

	provider := asset.DetectSourceFromAssetID(assetID)
	commitResult, err := sqliteassets.CommitIndexRequestTx(
		ctx,
		tx,
		d.outboxEventsRepo,
		sqliteassets.IndexRequest{
			AssetID:                  assetID,
			Source:                   provider,
			SourceVersion:            contentHash,
			RequestedAt:              time.Now(),
			UseProviderEventKey:      true,
			IncludeEmbeddingMetadata: true,
		},
	)
	if err != nil {
		return fmt.Errorf("dispatcher commit index request %s: %w", assetID, err)
	}

	if d.log != nil {
		d.log.Debug("dispatcher committed caller-owned-tx indexing request via canonical AssetCommitter",
			zap.String("asset_id", assetID),
			zap.String("outbox_event_id", commitResult.EventID),
			zap.String("outbox_event_key", commitResult.EventKey),
			zap.String("source_version", contentHash),
			zap.String("content_hash_prefix", shortHashPrefix(contentHash)),
		)
	}
	return nil
}

// shortHashPrefix returns a short log-friendly prefix.
func shortHashPrefix(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}
