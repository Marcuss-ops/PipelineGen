package imagesregistry

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacommit"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// UpdateMediaAssetImageFields persists the image projection inside the caller's
// transaction. It is kept in the canonical imagesregistry package so image
// producers never own media_assets SQL.
func UpdateMediaAssetImageFields(ctx context.Context, exec mediaAssetSQLExecutor, assetID string, image *mediacommit.ImageDraft) error {
	if image == nil {
		return nil
	}
	return execAssetUpdate(ctx, exec, assetID, "image fields update", `
		UPDATE media_assets
		SET url = ?, tags = ?, tags_norm = ?, width = ?, height = ?,
		    relative_path = ?, origin = ?, provider = ?, updated_at = ?
		WHERE id = ?`,
		image.URL, image.TagsJSON, image.TagsNorm, image.Width, image.Height,
		image.RelativePath, image.Origin, image.Provider,
		time.Now().UTC().Format(time.RFC3339), assetID)
}

// UpdateMediaAssetUsage increments the canonical reuse counters.
func UpdateMediaAssetUsage(ctx context.Context, exec mediaAssetSQLExecutor, assetID, usedAt string) error {
	if strings.TrimSpace(usedAt) == "" {
		usedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return execAssetUpdate(ctx, exec, assetID, "usage update", `
		UPDATE media_assets
		SET reuse_count = COALESCE(reuse_count, 0) + 1, last_used_at = ?, updated_at = ?
		WHERE id = ?`, usedAt, usedAt, assetID)
}

// UpdateMediaAssetEnrichState writes the enrichment state and timestamp.
func UpdateMediaAssetEnrichState(ctx context.Context, exec mediaAssetSQLExecutor, assetID, state, updatedAt string) (int64, error) {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	result, err := exec.ExecContext(ctx, `
		UPDATE media_assets
		SET enrich_state = ?, enrich_state_updated_at = ?, updated_at = ?
		WHERE id = ?`, state, updatedAt, updatedAt, assetID)
	if err != nil {
		return 0, fmt.Errorf("asset committer: enrich state update: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("asset committer: enrich state rows affected: %w", err)
	}
	return affected, nil
}

// UpdateMediaAssetEnrichStateIfCurrent performs the CAS form of the
// enrichment transition.
func UpdateMediaAssetEnrichStateIfCurrent(ctx context.Context, exec mediaAssetSQLExecutor, assetID, from, to, updatedAt string) (int64, error) {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	result, err := exec.ExecContext(ctx, `
		UPDATE media_assets
		SET enrich_state = ?, enrich_state_updated_at = ?, updated_at = ?
		WHERE id = ? AND enrich_state = ?`, to, updatedAt, updatedAt, assetID, from)
	if err != nil {
		return 0, fmt.Errorf("asset committer: enrich state CAS update: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("asset committer: enrich state CAS rows affected: %w", err)
	}
	return affected, nil
}

// HardDeleteMediaAssetTx is the legacy repository-shaped name for the
// canonical tx-bound deletion primitive. The deletion itself remains owned
// by HardDeleteTx; this alias exists only while repository callers converge.
func HardDeleteMediaAssetTx(ctx context.Context, tx *sql.Tx, assetID string) error {
	return HardDeleteTx(ctx, tx, assetID)
}

// Keep the canonical taxonomy type visible in this file's SQL boundary and
// prevent accidental removal of the mediaregistry dependency from the
// mutation family when helper implementations evolve.
var _ = mediaregistry.AssetTaxonomy{}
