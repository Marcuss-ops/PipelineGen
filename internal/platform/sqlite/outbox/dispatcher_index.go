package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
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
	if d.committer == nil {
		return errors.New("outbox.Dispatcher: canonical AssetCommitter is required")
	}
	if clip == nil || clip.ID == "" {
		return errors.New("clip with non-empty ID is required")
	}

	// Folders are not vector-indexable, but their canonical media_assets row
	// still must be committed through the canonical AssetCommitter. Never
	// bypass the committer with a direct clip-writer fallback.
	if !clip.IsFolder() && contentHash == "" {
		return fmt.Errorf("outbox.Dispatcher.EnqueueAndIndex: contentHash is required for non-folder clip %s (supersede gate cannot function without a content fingerprint — callers must set legacy_file_md5 before dispatching)", clip.ID)
	}
	// The dispatcher NEVER opens its own transaction for media: the canonical
	// committer owns its engine-native transaction so media_assets and the
	// media outbox event commit together on the media SSOT. Handing a SQLite
	// `*sql.Tx` to the PostgreSQL writer (the former tx-bound branch) is no
	// longer representable — see persistence.CanonicalAssetWriter.
	commitResult, err := d.committer.CommitAndIndex(ctx, buildPortableCommitRequest(clip, contentHash, !clip.IsFolder()))
	if err != nil {
		return fmt.Errorf("outbox.Dispatcher.EnqueueAndIndex: canonical AssetCommitter commit: %w", err)
	}
	if d.log != nil {
		d.log.Debug("dispatcher committed asset through canonical AssetCommitter",
			zap.String("asset_id", clip.ID),
			zap.String("outbox_event_key", commitResult.OutboxEventKey),
			zap.Bool("index_event_emitted", !clip.IsFolder()),
		)
	}
	return nil
}

// buildPortableCommitRequest translates a clip into the engine-neutral
// persistence.CommitRequest consumed by AssetCommitter.CommitAndIndex. It is
// the single translation site for the portable path so EnqueueAndIndex and
// the discovery branch cannot drift in field coverage.
func buildPortableCommitRequest(clip *asset.Asset, contentHash string, emitIndexEvent bool) persistence.CommitRequest {
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
	// Producer-declared taxonomy overrides are honoured so that a clip whose
	// producer declared its taxonomy — notably a stock clip declaring
	// asset_kind="stock_video" / semantic_role="stock" — commits the SAME
	// dimensions the stock job finalizer's spine write resolves for the same
	// asset. Two producers committing one asset must not disagree on taxonomy,
	// or the persisted row is decided by arrival order: the media upsert
	// writes taxonomy insert-wins / COALESCE-keep, so whichever writer runs
	// second can silently restamp semantic_role (the September 2026 stock
	// certification observed semantic_role="discovery" persisted for a
	// YouTube-acquired stock clip for exactly this reason).
	//
	// When the producer declares nothing, Taxonomy stays zero and the
	// committer keeps its previous behaviour (COALESCE-keep of the stored
	// dimensions), so no legacy producer's projection changes.
	//
	// A resolve error is not surfaced here because it can only mean the
	// producer declared an empty-but-present dimension; the committer's own
	// contract validation (ErrAssetCommitIndexTaxonomyRequired /
	// CommitRequest.Validate) is the owner of that diagnosis.
	taxonomy, _, _ := capregistry.ResolveDeclaredTaxonomy(capregistry.TaxonomyInput{
		AssetID:      clip.ID,
		Provider:     string(clip.Source),
		MediaType:    capregistry.MediaType(mediaType),
		AssetKind:    capregistry.AssetKind(clip.GetMetadataString("asset_kind")),
		SemanticRole: clip.GetMetadataString("semantic_role"),
	})
	indexPriority := 0
	if taxonomy.AssetKind == capregistry.AssetStockVideo {
		// Stock acquisition is on the script-generation critical path. The
		// stock extract step commits only after Drive publication, but it is
		// the first writer to emit the idempotent index event; waiting for the
		// later job finalizer would leave the event at normal priority.
		indexPriority = persistence.IndexPriorityHigh
	}
	return persistence.CommitRequest{
		AssetID: clip.ID, Source: string(clip.Source), Name: name, Filename: filename,
		MediaType: mediaType, Category: clip.Category, DurationMs: clip.Duration.Milliseconds(),
		ContentHash: contentHash, Description: clip.Description(), SearchText: clip.SearchText,
		LifecycleState: lifecycleState, IndexState: clip.GetMetadataString("index_state"),
		LocalPath: clip.LocalPath(), FolderID: clip.FolderID(), FolderPath: clip.FolderPath(),
		ThumbnailURL: clip.ThumbnailURL, SourceURL: clip.SourceURL, Title: name,
		Metadata: persistence.TypedMetadata{Extra: clip.Metadata}, Locations: locations,
		Taxonomy: taxonomy, IndexPriority: indexPriority,
		EmitIndexEvent: emitIndexEvent,
	}
}

// SaveDiscoveredAsset is the discovery-only upsert path. It writes the clip
// row with the supplied lifecycle and index states but deliberately emits no
// indexing request. The processing finalizer emits one only after the clip has
// a real hash and its upload has completed.
func (d *Dispatcher) SaveDiscoveredAsset(ctx context.Context, clip *asset.Asset, lifecycle asset.LifecycleState, idx asset.IndexState) error {
	if d == nil {
		return errors.New("outbox.Dispatcher is nil")
	}
	if d.committer == nil {
		return errors.New("outbox.Dispatcher: canonical AssetCommitter is required for discovery commits")
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

	// The discovery committer lives on another engine (PostgreSQL media
	// SSOT), so the dispatcher must NOT hand it its own SQLite transaction.
	// Delegate to the committer's self-owned transaction
	// (CommitDiscoveredAssetAndIndex), which commits media_assets + the
	// registry provenance atomically on that engine and deliberately emits no
	// indexing request at discovery time.
	committer, ok := d.committer.(DiscoveryCommitAndIndexCommitter)
	if !ok {
		return fmt.Errorf("dispatcher.SaveDiscoveredAsset(%q): discovery commit requires a self-owned-transaction discovery committer (got %T)", clip.ID, d.committer)
	}
	if err := committer.CommitDiscoveredAssetAndIndex(ctx, clip, lifecycle, idx); err != nil {
		return fmt.Errorf("dispatcher canonical discovery commit %s: %w", clip.ID, err)
	}
	if d.log != nil {
		d.log.Debug("dispatcher saved discovered asset through canonical AssetCommitter (self-owned tx)",
			zap.String("asset_id", clip.ID),
			zap.String("lifecycle_state", string(lifecycle)),
			zap.String("index_state", string(idx)),
		)
	}
	return nil
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
