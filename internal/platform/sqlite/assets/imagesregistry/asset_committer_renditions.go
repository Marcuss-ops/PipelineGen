package imagesregistry

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
)

var _ persistence.AssetRenditionCommitter = (*SQLiteAssetCommitter)(nil)

// CommitRenditionTx persists a technical rendition and its physical location
// inside the caller-owned transaction. The canonical asset committer owns
// both schema writes so capability code remains independent of SQLite tables.
func (c *SQLiteAssetCommitter) CommitRenditionTx(
	ctx context.Context,
	tx *sql.Tx,
	assetID string,
	r persistence.RenditionCommit,
	nowStr string,
) error {
	if c == nil || tx == nil {
		return fmt.Errorf("asset committer: rendition transaction is required")
	}
	if assetID == "" || r.Kind == "" || r.URI == "" {
		return fmt.Errorf("asset committer: rendition asset id, kind and URI are required")
	}

	locationKind := r.Provider
	if locationKind != "drive" && locationKind != "object_storage" {
		locationKind = "local"
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO asset_locations
			(asset_id, location_kind, uri, external_id, web_view_link, download_url,
			 mime_type, file_size_bytes, legacy_file_md5, is_primary, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
		ON CONFLICT(asset_id, location_kind) DO UPDATE SET
			uri = excluded.uri,
			external_id = excluded.external_id,
			web_view_link = excluded.web_view_link,
			download_url = excluded.download_url,
			mime_type = excluded.mime_type,
			file_size_bytes = excluded.file_size_bytes,
			legacy_file_md5 = excluded.legacy_file_md5,
			is_primary = excluded.is_primary,
			updated_at = excluded.updated_at
	`, assetID, locationKind, r.URI, r.FileID, r.WebViewLink, r.DownloadURL,
		r.MimeType, r.SizeBytes, r.SHA256, nowStr, nowStr); err != nil {
		return fmt.Errorf("asset committer: upsert rendition location %s/%s: %w", assetID, r.Kind, err)
	}

	var locationID int64
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM asset_locations WHERE asset_id = ? AND location_kind = ?`,
		assetID, locationKind).Scan(&locationID); err != nil {
		return fmt.Errorf("asset committer: resolve rendition location %s/%s: %w", assetID, r.Kind, err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO asset_renditions
			(id, asset_id, location_id, kind, container, codec, width, height,
			 fps, bitrate, sha256, size_bytes, created_at, updated_at)
		VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(asset_id, kind) DO UPDATE SET
			location_id = excluded.location_id,
			container = excluded.container,
			codec = excluded.codec,
			width = excluded.width,
			height = excluded.height,
			fps = excluded.fps,
			bitrate = excluded.bitrate,
			sha256 = excluded.sha256,
			size_bytes = excluded.size_bytes,
			updated_at = excluded.updated_at
	`, assetID, locationID, r.Kind, r.Container, r.Codec, r.Width, r.Height,
		r.FPS, r.Bitrate, r.SHA256, r.SizeBytes, nowStr, nowStr); err != nil {
		return fmt.Errorf("asset committer: upsert rendition %s/%s: %w", assetID, r.Kind, err)
	}
	return nil
}
