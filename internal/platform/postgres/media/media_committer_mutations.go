// Package media — media_committer_mutations.go.
//
// Narrow post-commit mutation surface of PostgresMediaCommitter: the
// canonical one-fact-at-a-time delegates used by enrichment, capability
// agents and admin tooling (embedding channel, metadata snapshot, index
// state, lifecycle, taxonomy, folder path, Drive delivery). They exist so a
// producer never needs its own SQL writer: every narrow mutation still lands
// on the canonical committer.
//
// Split out of media_committer.go to stay under the 600-LOC strict cap
// (godlike/08 forward-prevention gate; the file had reached 618 lines). The
// type, the constructor, the commit entry points and the discovery surface
// remain in media_committer.go, which is the SSOT owner the cap applies to.
package media

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacommit"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// CommitLegacy bridges a legacy persistence.CommitRequest onto the canonical
// MediaCommitter so existing writers converge with a one-line change.
func (c *PostgresMediaCommitter) CommitLegacy(ctx context.Context, req persistence.CommitRequest) (mediacommit.CommitMediaAssetResult, error) {
	return c.CommitMediaAsset(ctx, legacyToMediaCommitRequest(req))
}

// PersistEmbeddingJSON delegates post-commit embedding persistence to the
// canonical PostgresAssetCommitter owned by this same aggregate committer.
func (c *PostgresMediaCommitter) PersistEmbeddingJSON(ctx context.Context, assetID, channel string, embedding []float64, status string) error {
	return c.assets.PersistEmbeddingJSON(ctx, assetID, channel, embedding, status)
}

// UpdateAssetMetadata is the canonical narrow mutation used by metadata
// enrichment (idempotent, transactionally isolated).
func (c *PostgresMediaCommitter) UpdateAssetMetadata(ctx context.Context, assetID, metadataJSON string) error {
	if c == nil || c.assets == nil {
		return errors.New("media committer: canonical asset committer is unavailable")
	}
	if assetID == "" {
		return errors.New("media committer: asset id is required")
	}
	return c.assets.ReplaceMetadataJSON(ctx, assetID, metadataJSON, "")
}

// SetIndexState delegates the canonical index-state mutation.
func (c *PostgresMediaCommitter) SetIndexState(ctx context.Context, assetID string, state asset.IndexState, lastError string) error {
	return c.assets.SetIndexState(ctx, assetID, state, lastError)
}

// SetIndexed performs the compare-and-set terminal index transition.
func (c *PostgresMediaCommitter) SetIndexed(ctx context.Context, assetID, contentHash, sourceVersion, embeddingModel, embeddingVersion, contractHash string) (bool, error) {
	return c.assets.SetIndexed(ctx, assetID, contentHash, sourceVersion, embeddingModel, embeddingVersion, contractHash)
}

// PatchMetadataJSON applies a JSON patch through the canonical committer.
func (c *PostgresMediaCommitter) PatchMetadataJSON(ctx context.Context, assetID, patchJSON, updatedAt string) error {
	return c.assets.PatchMetadataJSON(ctx, assetID, patchJSON, updatedAt)
}

func (c *PostgresMediaCommitter) PatchMetadataJSONTx(ctx context.Context, tx *sql.Tx, assetID, patchJSON, updatedAt string) error {
	return c.assets.PatchMetadataJSONTx(ctx, tx, assetID, patchJSON, updatedAt)
}

// ReplaceMetadataJSON replaces the metadata snapshot through the canonical
// committer.
func (c *PostgresMediaCommitter) ReplaceMetadataJSON(ctx context.Context, assetID, metadataJSON, updatedAt string) error {
	return c.assets.ReplaceMetadataJSON(ctx, assetID, metadataJSON, updatedAt)
}

func (c *PostgresMediaCommitter) UpdateFolderPath(ctx context.Context, assetID, folderID, folderPath, updatedAt string) error {
	return c.assets.UpdateFolderPath(ctx, assetID, folderID, folderPath, updatedAt)
}

func (c *PostgresMediaCommitter) UpdateFolderPathTx(ctx context.Context, tx *sql.Tx, assetID, folderID, folderPath, updatedAt string) error {
	return c.assets.UpdateFolderPathTx(ctx, tx, assetID, folderID, folderPath, updatedAt)
}

func (c *PostgresMediaCommitter) UpdateLifecycle(ctx context.Context, assetID string, state, deletedAt, updatedAt string) error {
	return c.assets.UpdateLifecycle(ctx, assetID, state, deletedAt, updatedAt)
}

func (c *PostgresMediaCommitter) UpdateTaxonomy(ctx context.Context, taxonomy capregistry.AssetTaxonomy) error {
	return c.assets.UpdateTaxonomy(ctx, taxonomy)
}

func (c *PostgresMediaCommitter) LinkContent(ctx context.Context, assetID, contentSHA256 string) error {
	return c.assets.LinkContent(ctx, assetID, contentSHA256)
}

func (c *PostgresMediaCommitter) UpdateSearchText(ctx context.Context, assetID, searchText, updatedAt string) error {
	return c.assets.UpdateSearchText(ctx, assetID, searchText, updatedAt)
}

func (c *PostgresMediaCommitter) RefreshUpdatedAt(ctx context.Context, assetID, updatedAt string) error {
	return c.assets.RefreshUpdatedAt(ctx, assetID, updatedAt)
}

func (c *PostgresMediaCommitter) UpdateOrphanMetadata(ctx context.Context, assetID string, detectedAt time.Time, kind string) error {
	return c.assets.UpdateOrphanMetadata(ctx, assetID, detectedAt, kind)
}

func (c *PostgresMediaCommitter) UpdateDriveDeliveryByLegacyHash(ctx context.Context, hash string, mutation persistence.DriveDeliveryMutation) error {
	return c.assets.UpdateDriveDeliveryByLegacyHash(ctx, hash, mutation)
}
