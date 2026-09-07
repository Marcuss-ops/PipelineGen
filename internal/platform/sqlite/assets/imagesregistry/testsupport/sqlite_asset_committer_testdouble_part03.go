package testsupport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"strings"
	"time"
)

// upsertLocations writes the asset_locations rows inside the tx.
func (c *SQLiteAssetCommitter) upsertLocations(ctx context.Context, tx *sql.Tx, assetID string, locations []persistence.LocationCommit, nowStr string) error {
	if len(locations) == 0 {
		return nil
	}
	for i, loc := range locations {
		if loc.Kind == "" {
			return fmt.Errorf("asset committer: location[%d] has empty Kind", i)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO asset_locations
				(asset_id, location_kind, uri, external_id, web_view_link, download_url,
				 mime_type, file_size_bytes, legacy_file_md5, is_primary, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		`, assetID, loc.Kind, loc.URI, loc.ExternalID, loc.WebViewLink, loc.DownloadURL,
			loc.MimeType, loc.FileSizeBytes, loc.LegacyFileMD5, boolToInt(loc.IsPrimary), nowStr, nowStr); err != nil {
			return fmt.Errorf("asset committer: upsert location %s: %w", loc.Kind, err)
		}
	}
	return nil
}

// queryOutboxStatus returns the status of an existing outbox event.
func (c *SQLiteAssetCommitter) queryOutboxStatus(ctx context.Context, eventKey string) (string, error) {
	var status string
	err := c.db.QueryRowContext(ctx, `SELECT status FROM outbox_events WHERE event_key = ?`, eventKey).Scan(&status)
	if err != nil {
		return "", err
	}
	return status, nil
}

// primaryDriveFileID returns the ExternalID of the first primary
// location, falling back to the first location.
func primaryDriveFileID(locations []persistence.LocationCommit) string {
	for _, loc := range locations {
		if loc.IsPrimary && loc.ExternalID != "" {
			return loc.ExternalID
		}
	}
	for _, loc := range locations {
		if loc.ExternalID != "" {
			return loc.ExternalID
		}
	}
	return ""
}

// primaryWebViewLink returns the WebViewLink of the first primary
// location, falling back to the first location.
func primaryWebViewLink(locations []persistence.LocationCommit) string {
	for _, loc := range locations {
		if loc.IsPrimary && loc.WebViewLink != "" {
			return loc.WebViewLink
		}
	}
	for _, loc := range locations {
		if loc.WebViewLink != "" {
			return loc.WebViewLink
		}
	}
	return ""
}

// primaryDownloadURL returns the DownloadURL of the first primary
// location, falling back to the first location.
func primaryDownloadURL(locations []persistence.LocationCommit) string {
	for _, loc := range locations {
		if loc.IsPrimary && loc.DownloadURL != "" {
			return loc.DownloadURL
		}
	}
	for _, loc := range locations {
		if loc.DownloadURL != "" {
			return loc.DownloadURL
		}
	}
	return ""
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// mediaAssetSQLExecutor is the canonical SQL mutation boundary for
// media_assets. Both *sql.DB and *sql.Tx satisfy it; keeping the executor
// narrow lets callers preserve their transaction while the SQL itself stays
// owned by this file.
type mediaAssetSQLExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// PersistEmbeddingJSON persists one embedding channel through the canonical
// asset mutation boundary. Channel names are deliberately typed as a closed
// set at this infrastructure boundary; producers never select SQL columns.
func (c *SQLiteAssetCommitter) PersistEmbeddingJSON(ctx context.Context, assetID, channel string, embedding []float64, status string) error {
	raw, err := json.Marshal(embedding)
	if err != nil {
		return fmt.Errorf("asset committer: marshal %s embedding: %w", channel, err)
	}
	var update func(context.Context, mediaAssetSQLExecutor, string, string) error
	switch channel {
	case "semantic":
		update = UpdateMediaAssetEmbeddingJSON
	case "transcript":
		update = UpdateMediaAssetTranscriptEmbedding
	case "visual":
		update = UpdateMediaAssetVisualEmbedding
	case "audio":
		update = UpdateMediaAssetAudioEmbedding
	default:
		return fmt.Errorf("asset committer: unsupported embedding channel %q", channel)
	}
	if err := update(ctx, c.db, assetID, string(raw)); err != nil {
		return err
	}
	if status == "" {
		return nil
	}
	return PatchMediaAssetMetadataJSON(ctx, c.db, assetID, mustMarshalJSON(map[string]any{"embedding_status": status}), time.Now().UTC().Format(time.RFC3339))
}

// SetIndexState delegates the canonical index-state mutation to the same
// committer that owns asset creation. Indexing workers remain responsible for
// metrics and retry policy; this method owns only durable SQLite state.
func (c *SQLiteAssetCommitter) SetIndexState(ctx context.Context, assetID string, state asset.IndexState, lastError string) error {
	return UpdateMediaAssetIndexState(ctx, c.db, assetID, string(state), time.Now().UTC().Format(time.RFC3339), lastError)
}

// SetIndexed performs the compare-and-set terminal index transition through
// the canonical committer boundary.
func (c *SQLiteAssetCommitter) SetIndexed(ctx context.Context, assetID, contentHash, sourceVersion, embeddingModel, embeddingVersion, contractHash string) (bool, error) {
	ok, err := SetMediaAssetIndexed(ctx, c.db, assetID, contentHash, sourceVersion,
		time.Now().UTC().Format(time.RFC3339), embeddingModel, embeddingVersion, contractHash)
	return ok, err
}

// PatchMetadataJSON applies a JSON patch through the canonical committer.
func (c *SQLiteAssetCommitter) PatchMetadataJSON(ctx context.Context, assetID, patchJSON, updatedAt string) error {
	return PatchMediaAssetMetadataJSON(ctx, c.db, assetID, patchJSON, updatedAt)
}

// PatchMetadataJSONTx applies a metadata patch in a caller-owned transaction.
// It is the only tx-bound metadata mutation exposed to producer adapters.
func (c *SQLiteAssetCommitter) PatchMetadataJSONTx(ctx context.Context, tx *sql.Tx, assetID, patchJSON, updatedAt string) error {
	return PatchMediaAssetMetadataJSON(ctx, tx, assetID, patchJSON, updatedAt)
}

// ReplaceMetadataJSON replaces the metadata snapshot through the canonical
// committer. It is used by legacy enrichment adapters that still provide a
// complete JSON envelope rather than a typed patch.
func (c *SQLiteAssetCommitter) ReplaceMetadataJSON(ctx context.Context, assetID, metadataJSON, updatedAt string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return updateMediaAssetMetadata(ctx, c.db, assetID, metadataJSON,
		"metadata_json = ?, updated_at = ?", metadataJSON, updatedAt)
}

func (c *SQLiteAssetCommitter) UpdateFolderPath(ctx context.Context, assetID, folderID, folderPath, updatedAt string) error {
	return UpdateMediaAssetFolderPath(ctx, c.db, assetID, folderID, folderPath, updatedAt)
}

// UpdateFolderPathTx applies a folder-path mutation in the caller-owned
// transaction so the caller can emit the canonical index request atomically.
func (c *SQLiteAssetCommitter) UpdateFolderPathTx(ctx context.Context, tx *sql.Tx, assetID, folderID, folderPath, updatedAt string) error {
	return UpdateMediaAssetFolderPath(ctx, tx, assetID, folderID, folderPath, updatedAt)
}

func (c *SQLiteAssetCommitter) UpdateLifecycle(ctx context.Context, assetID string, state, deletedAt, updatedAt string) error {
	return UpdateMediaAssetLifecycle(ctx, c.db, assetID, state, deletedAt, updatedAt)
}

func (c *SQLiteAssetCommitter) UpdateTaxonomy(ctx context.Context, taxonomy mediaregistry.AssetTaxonomy) error {
	return UpdateMediaAssetTaxonomy(ctx, c.db, taxonomy)
}

func (c *SQLiteAssetCommitter) LinkContent(ctx context.Context, assetID, contentSHA256 string) error {
	return LinkMediaAssetContent(ctx, c.db, assetID, contentSHA256)
}

func (c *SQLiteAssetCommitter) UpdateSearchText(ctx context.Context, assetID, searchText, updatedAt string) error {
	return UpdateMediaAssetSearchText(ctx, c.db, assetID, searchText, updatedAt)
}

func (c *SQLiteAssetCommitter) RefreshUpdatedAt(ctx context.Context, assetID, updatedAt string) error {
	return UpdateMediaAssetUpdatedAt(ctx, c.db, assetID, updatedAt)
}

func (c *SQLiteAssetCommitter) UpdateOrphanMetadata(ctx context.Context, assetID string, detectedAt time.Time, kind string) error {
	return UpdateMediaAssetOrphanMetadata(ctx, c.db, assetID, detectedAt, kind)
}

// UpdateDriveDeliveryByLegacyHash applies the post-commit Drive projection
// update through the canonical asset boundary and keeps asset_locations in
// sync in the same transaction.
func (c *SQLiteAssetCommitter) UpdateDriveDeliveryByLegacyHash(ctx context.Context, hash string, mutation persistence.DriveDeliveryMutation) error {
	if strings.TrimSpace(hash) == "" {
		return fmt.Errorf("asset committer: legacy file hash is required")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("asset committer: begin Drive delivery tx: %w", err)
	}
	defer tx.Rollback()
	preserveIdentity := strings.HasPrefix(mutation.Status, "delivery_failed:") && mutation.DriveFileID == "" && mutation.DriveLink == "" && mutation.DownloadLink == ""
	var result sql.Result
	if preserveIdentity {
		result, err = tx.ExecContext(ctx, `UPDATE media_assets SET metadata_json = json_set(COALESCE(metadata_json, '{}'), '$.delivery_status', ?), updated_at = CURRENT_TIMESTAMP WHERE source = 'image' AND legacy_file_md5 = ?`, mutation.Status, hash)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE media_assets SET drive_file_id = ?, drive_link = ?, download_link = ?, metadata_json = json_set(COALESCE(metadata_json, '{}'), '$.delivery_status', ?), updated_at = CURRENT_TIMESTAMP WHERE source = 'image' AND legacy_file_md5 = ?`, mutation.DriveFileID, mutation.DriveLink, mutation.DownloadLink, mutation.Status, hash)
	}
	if err != nil {
		return fmt.Errorf("asset committer: update Drive delivery: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		if err != nil {
			return fmt.Errorf("asset committer: inspect Drive delivery update: %w", err)
		}
		return fmt.Errorf("asset committer: image with legacy hash %q not found", hash)
	}
	if !preserveIdentity && mutation.DriveFileID != "" {
		var assetID string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM media_assets WHERE source = 'image' AND legacy_file_md5 = ?`, hash).Scan(&assetID); err != nil {
			return fmt.Errorf("asset committer: resolve Drive delivery asset: %w", err)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := tx.ExecContext(ctx, `INSERT INTO asset_locations (asset_id, location_kind, uri, external_id, web_view_link, download_url, mime_type, file_size_bytes, legacy_file_md5, is_primary, created_at, updated_at) VALUES (?, 'drive', ?, ?, ?, ?, '', 0, ?, 0, ?, ?) ON CONFLICT(asset_id, location_kind) DO UPDATE SET uri=excluded.uri, external_id=excluded.external_id, web_view_link=excluded.web_view_link, download_url=excluded.download_url, legacy_file_md5=excluded.legacy_file_md5, updated_at=excluded.updated_at`, assetID, "drive://"+mutation.DriveFileID, mutation.DriveFileID, mutation.DriveLink, mutation.DownloadLink, hash, now, now); err != nil {
			return fmt.Errorf("asset committer: upsert Drive location: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("asset committer: commit Drive delivery: %w", err)
	}
	return nil
}

// mustMarshalJSON is used only for small internal metadata patches. The
// inputs are constructed by the canonical writer, so an encoding failure is
// a programmer error rather than a recoverable producer failure.
func mustMarshalJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("asset committer: marshal internal metadata patch: %v", err))
	}
	return string(raw)
}
