// Package processing — Wave 3 orchestration layer for media processing.
//
// AssetCommitter is the single canonical place for persisting a processed
// asset to the database and outbox. It wraps the two canonical persistence
// surfaces:
//
//   - finalization.AssetFinalizerTx for full asset/version/location/rendition
//     persistence inside a caller-owned transaction.
//   - mutations.AssetMutationDispatcher for the lightweight atomic UPSERT +
//     outbox enqueue path used by clips/reprocess flows.
package processing

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// AssetCommitter is the canonical persistence boundary for processed assets.
type AssetCommitter interface {
	// Commit persists a PublishedArtifact through AssetFinalizerTx inside a
	// fresh SQLite transaction. Use this when the asset has a canonical
	// remote location (Drive) and optional renditions.
	Commit(ctx context.Context, pa finalization.PublishedArtifact) error

	// EnqueueAndIndex performs the lightweight atomic UPSERT +
	// asset.index.requested outbox enqueue via mutations dispatcher.
	// Use this for callers that only need media_assets + outbox.
	EnqueueAndIndex(ctx context.Context, a *asset.Asset, contentHash string) error
}

// assetCommitter is the concrete implementation of AssetCommitter.
type assetCommitter struct {
	finalizer  finalization.AssetFinalizerTx
	dispatcher mutations.AssetMutationDispatcher
	db         *sql.DB
	log        *zap.Logger
}

// NewAssetCommitter creates an AssetCommitter.
//
// Either finalizer or dispatcher may be nil, but the corresponding method
// will fail-closed. At least one persistence path must be wired for the
// committer to be useful.
func NewAssetCommitter(
	finalizer finalization.AssetFinalizerTx,
	dispatcher mutations.AssetMutationDispatcher,
	db *sql.DB,
	log *zap.Logger,
) AssetCommitter {
	if log == nil {
		log = zap.NewNop()
	}
	return &assetCommitter{
		finalizer:  finalizer,
		dispatcher: dispatcher,
		db:         db,
		log:        log,
	}
}

// Commit opens a transaction, calls AssetFinalizerTx.FinalizeAsset, and commits.
func (c *assetCommitter) Commit(ctx context.Context, pa finalization.PublishedArtifact) error {
	if c.finalizer == nil {
		return fmt.Errorf("AssetCommitter.Commit: AssetFinalizerTx not wired")
	}
	if c.db == nil {
		return fmt.Errorf("AssetCommitter.Commit: DB not wired")
	}
	if pa.ArtifactID == "" {
		return fmt.Errorf("AssetCommitter.Commit: ArtifactID is required")
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("AssetCommitter.Commit: begin tx: %w", err)
	}

	_, _, err = c.finalizer.FinalizeAsset(ctx, finalizer.WrapTx(tx), pa)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("AssetCommitter.Commit: finalize asset %s: %w", pa.ArtifactID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("AssetCommitter.Commit: commit tx for %s: %w", pa.ArtifactID, err)
	}

	c.log.Info("AssetCommitter.Commit succeeded",
		zap.String("artifact_id", pa.ArtifactID),
		zap.String("source", pa.Source),
		zap.String("location_provider", pa.Location.Provider),
	)
	return nil
}

// EnqueueAndIndex delegates to the mutations dispatcher.
func (c *assetCommitter) EnqueueAndIndex(ctx context.Context, a *asset.Asset, contentHash string) error {
	if c.dispatcher == nil {
		return fmt.Errorf("AssetCommitter.EnqueueAndIndex: dispatcher not wired")
	}
	return c.dispatcher.EnqueueAndIndex(ctx, a, contentHash)
}
