package buildbundle

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"
)

type artifactAssetIndexAdapter struct{ service *assetindex.Service }

func newArtifactAssetIndexAdapter(service *assetindex.Service) artifacts.AssetIndexPort {
	if service == nil {
		return nil
	}
	return &artifactAssetIndexAdapter{service: service}
}

func (a *artifactAssetIndexAdapter) Upsert(ctx context.Context, rec *artifacts.AssetIndexRecord) error {
	if a == nil || a.service == nil {
		return errors.New("artifacts: asset index adapter unavailable")
	}
	if rec == nil {
		return errors.New("artifacts: asset index record is nil")
	}
	// asset_index carries ONE content-address slot (content_hash); the
	// compatibility bucket is gone (migration 281), so the canonical value
	// wins and the legacy spelling is only a fallback for unpromoted rows.
	contentHash := rec.ContentHash
	if contentHash == "" {
		contentHash = rec.ContentSHA256
	}
	return a.service.Upsert(ctx, &assetindex.AssetRecord{
		AssetID: rec.AssetID, AssetType: rec.AssetType, Source: rec.Source, SourceID: rec.SourceID,
		GroupName: rec.GroupName, Subfolder: rec.Subfolder, LocalPath: rec.LocalPath,
		DriveLink: rec.DriveLink, DownloadLink: rec.DownloadLink,
		ContentHash: contentHash, Status: rec.Status, Metadata: rec.Metadata,
		CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt,
	})
}
