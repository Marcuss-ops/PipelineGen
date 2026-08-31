package imagesregistry

import (
	"context"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacommit"
)

// UpdateMediaAssetImageFields delegates image projection persistence to the
// canonical mutation implementation.
func UpdateMediaAssetImageFields(ctx context.Context, exec mediaAssetSQLExecutor, assetID string, image *mediacommit.ImageDraft) error {
	return persistMediaAssetImageFields(ctx, exec, assetID, image)
}

// UpdateMediaAssetUsage delegates reuse-counter persistence to the canonical
// mutation implementation.
func UpdateMediaAssetUsage(ctx context.Context, exec mediaAssetSQLExecutor, assetID, usedAt string) error {
	return persistMediaAssetUsage(ctx, exec, assetID, usedAt)
}

func persistMediaAssetUsage(ctx context.Context, exec mediaAssetSQLExecutor, assetID, usedAt string) error {
	if strings.TrimSpace(usedAt) == "" {
		usedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return execAssetUpdate(ctx, exec, assetID, "usage update", `
		UPDATE media_assets
		SET reuse_count = COALESCE(reuse_count, 0) + 1, last_used_at = ?, updated_at = ?
		WHERE id = ?`, usedAt, usedAt, assetID)
}

func persistMediaAssetImageFields(ctx context.Context, exec mediaAssetSQLExecutor, assetID string, image *mediacommit.ImageDraft) error {
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
