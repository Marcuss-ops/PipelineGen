package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/idempotency"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
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
	if d.canonicalCommitter == nil {
		return errors.New("outbox.Dispatcher: canonical SQLiteAssetCommitter is required")
	}
	if clip == nil || clip.ID == "" {
		return errors.New("clip with non-empty ID is required")
	}

	// Folders are not vector-indexable, but their canonical media_assets row
	// still must be committed through SQLiteAssetCommitter. Never bypass the
	// committer with a direct clips.UpsertClipTx fallback.
	if !clip.IsFolder() && contentHash == "" {
		return fmt.Errorf("outbox.Dispatcher.EnqueueAndIndex: contentHash is required for non-folder clip %s (supersede gate cannot function without a content fingerprint — callers must set legacy_file_md5 before dispatching)", clip.ID)
	}
	name := clip.Name
	if name == "" {
		name = clip.ID
	}
	filename := clip.Filename
	if filename == "" {
		filename = name + ".asset"
	}
	mediaType := string(clip.MediaType)
	if mediaType == "" {
		mediaType = "video"
	}
	lifecycleState := string(clip.LifecycleState)
	if lifecycleState == "" {
		lifecycleState = string(asset.StateActive)
	}
	locations := make([]persistence.LocationCommit, 0, 2)
	if driveID, driveLink := clip.DriveFileID(), clip.DriveLink(); driveID != "" || driveLink != "" {
		locations = append(locations, persistence.LocationCommit{
			Kind: "drive", Provider: "drive", ExternalID: driveID,
			WebViewLink: driveLink, DownloadURL: clip.DownloadLink(), IsPrimary: true,
		})
	}
	if localPath := clip.LocalPath(); localPath != "" {
		locations = append(locations, persistence.LocationCommit{
			Kind: "local", Provider: "local", URI: localPath,
			LegacyFileMD5: contentHash, IsPrimary: len(locations) == 0,
		})
	}
	commitResult, err := d.canonicalCommitter.CommitAndIndex(ctx, persistence.CommitRequest{
		AssetID: clip.ID, Source: string(clip.Source), Name: name, Filename: filename,
		MediaType: mediaType, Category: clip.Category, DurationMs: clip.Duration.Milliseconds(),
		ContentHash: contentHash, Description: clip.Description(), SearchText: clip.SearchText,
		LifecycleState: lifecycleState, IndexState: clip.GetMetadataString("index_state"),
		LocalPath: clip.LocalPath(), FolderID: clip.FolderID(), FolderPath: clip.FolderPath(),
		ThumbnailURL: clip.ThumbnailURL, SourceURL: clip.SourceURL, Title: name,
		Metadata: persistence.TypedMetadata{Extra: clip.Metadata}, Locations: locations,
		EmitIndexEvent: !clip.IsFolder(),
	})
	if err != nil {
		return fmt.Errorf("outbox.Dispatcher.EnqueueAndIndex: canonical SQLiteAssetCommitter commit: %w", err)
	}
	if d.log != nil {
		d.log.Debug("dispatcher committed asset through canonical SQLiteAssetCommitter",
			zap.String("asset_id", clip.ID),
			zap.String("outbox_event_key", commitResult.OutboxEventKey),
			zap.Bool("index_event_emitted", !clip.IsFolder()),
		)
	}
	return nil
}

// SaveDiscoveredAsset is the discovery-only upsert path. It writes the clip
// row with the supplied lifecycle and index states but deliberately emits no
// indexing request. The processing finalizer emits one only after the clip has
// a real hash and its upload has completed.
func (d *Dispatcher) SaveDiscoveredAsset(ctx context.Context, clip *asset.Asset, lifecycle asset.LifecycleState, idx asset.IndexState) error {
	if d == nil {
		return errors.New("outbox.Dispatcher is nil")
	}
	if d.discoveryCommitter == nil {
		return errors.New("outbox.Dispatcher: canonical SQLiteAssetCommitter is required for discovery commits")
	}
	if d.txmgr == nil {
		return errors.New("outbox.Dispatcher: txmgr not configured")
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
		if err := d.discoveryCommitter.CommitDiscoveredAsset(ctx, tx, clip, lifecycle, idx); err != nil {
			return fmt.Errorf("dispatcher canonical discovery commit %s: %w", clip.ID, err)
		}
		if d.log != nil {
			d.log.Debug("dispatcher saved discovered asset through canonical SQLiteAssetCommitter",
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
func (d *Dispatcher) EnqueueIndexEvent(ctx context.Context, tx *sql.Tx, assetID, source, contentHash string) error {
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

	commitResult, err := imagesregistry.CommitIndexRequestTx(
		ctx,
		tx,
		d.outboxEventsRepo,
		imagesregistry.IndexRequest{
			AssetID:       assetID,
			Source:        source,
			SourceVersion: contentHash,
			RequestedAt:   time.Now(),
			MediaType:     "video",
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
