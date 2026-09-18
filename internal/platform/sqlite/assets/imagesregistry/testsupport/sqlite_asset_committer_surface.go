package testsupport

// sqlite_asset_committer_surface.go owns the media-asset mutation surface and
// the commit-normalisation helpers of the TEST-ONLY SQLite AssetCommitter.
//
// Split out of sqlite_asset_committer_testdouble.go to stay under the 600-LOC
// strict cap (godlike/08 forward-prevention gate). Symbols moved verbatim: no
// behaviour change, same package. The KNOWN DEBT mirror note (see the package
// doc in sqlite_asset_committer_testdouble.go) applies to this file too.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// normalizeIndexTaxonomy is the compatibility bridge for legacy producers:
// an indexing commit may omit taxonomy at the call site, but the canonical
// writer derives and persists it before validation. No indexed row can leave
// this boundary with an empty taxonomy.
func normalizeIndexTaxonomy(req *persistence.CommitRequest) error {
	if req == nil || !req.EmitIndexEvent || !req.Taxonomy.IsZero() {
		return nil
	}
	taxonomy, err := mediaregistry.ResolveTaxonomy(mediaregistry.TaxonomyInput{
		AssetID:   req.AssetID,
		Provider:  req.Source,
		MediaType: mediaregistry.MediaType(req.MediaType),
	})
	if err != nil {
		return fmt.Errorf("asset committer: derive index taxonomy: %w", err)
	}
	req.Taxonomy = taxonomy
	return nil
}

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

func execAssetUpdate(ctx context.Context, exec mediaAssetSQLExecutor, assetID, operation, query string, args ...any) error {
	if exec == nil {
		return fmt.Errorf("asset committer: %s: executor is unavailable", operation)
	}
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("asset committer: %s: %w", operation, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("asset committer: %s rows affected: %w", operation, err)
	}
	if affected == 0 {
		return fmt.Errorf("asset committer: %s: asset %q not found", operation, assetID)
	}
	return nil
}

func updateMediaAssetMetadata(ctx context.Context, exec mediaAssetSQLExecutor, assetID, metadataJSON, setClause string, args ...any) error {
	if strings.TrimSpace(metadataJSON) == "" {
		metadataJSON = "{}"
	}
	if !json.Valid([]byte(metadataJSON)) {
		return fmt.Errorf("asset committer: metadata JSON is invalid")
	}
	values := append([]any{metadataJSON}, args...)
	return execAssetUpdate(ctx, exec, assetID, "metadata update", "UPDATE media_assets SET "+setClause+" WHERE id = ?", append(values, assetID)...)
}

func PatchMediaAssetMetadataJSON(ctx context.Context, exec mediaAssetSQLExecutor, assetID, patchJSON, updatedAt string) error {
	if strings.TrimSpace(patchJSON) == "" {
		patchJSON = "{}"
	}
	if !json.Valid([]byte(patchJSON)) {
		return fmt.Errorf("asset committer: metadata patch JSON is invalid")
	}
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return execAssetUpdate(ctx, exec, assetID, "metadata patch", `
		UPDATE media_assets
		SET metadata_json = json_patch(COALESCE(metadata_json, '{}'), ?), updated_at = ?
		WHERE id = ?`, patchJSON, updatedAt, assetID)
}

func UpdateMediaAssetEmbeddingJSON(ctx context.Context, exec mediaAssetSQLExecutor, assetID, value string) error {
	return execAssetUpdate(ctx, exec, assetID, "semantic embedding update", `UPDATE media_assets SET embedding_json = ?, updated_at = ? WHERE id = ?`, value, time.Now().UTC().Format(time.RFC3339), assetID)
}

func UpdateMediaAssetTranscriptEmbedding(ctx context.Context, exec mediaAssetSQLExecutor, assetID, value string) error {
	return execAssetUpdate(ctx, exec, assetID, "transcript embedding update", `UPDATE media_assets SET transcript_embedding = ?, updated_at = ? WHERE id = ?`, value, time.Now().UTC().Format(time.RFC3339), assetID)
}

func UpdateMediaAssetVisualEmbedding(ctx context.Context, exec mediaAssetSQLExecutor, assetID, value string) error {
	return execAssetUpdate(ctx, exec, assetID, "visual embedding update", `UPDATE media_assets SET visual_embedding = ?, updated_at = ? WHERE id = ?`, value, time.Now().UTC().Format(time.RFC3339), assetID)
}

func UpdateMediaAssetAudioEmbedding(ctx context.Context, exec mediaAssetSQLExecutor, assetID, value string) error {
	return execAssetUpdate(ctx, exec, assetID, "audio embedding update", `UPDATE media_assets SET audio_embedding = ?, updated_at = ? WHERE id = ?`, value, time.Now().UTC().Format(time.RFC3339), assetID)
}

func UpdateMediaAssetIndexState(ctx context.Context, exec mediaAssetSQLExecutor, assetID, state, updatedAt, lastError string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	var query string
	var args []any
	if strings.TrimSpace(lastError) == "" {
		query = `UPDATE media_assets SET index_state = ?, index_state_updated_at = ?, metadata_json = json_remove(COALESCE(metadata_json, '{}'), '$.last_index_error'), updated_at = ? WHERE id = ?`
		args = []any{state, updatedAt, updatedAt, assetID}
	} else {
		query = `UPDATE media_assets SET index_state = ?, index_state_updated_at = ?, metadata_json = json_set(COALESCE(metadata_json, '{}'), '$.last_index_error', ?), updated_at = ? WHERE id = ?`
		args = []any{state, updatedAt, lastError, updatedAt, assetID}
	}
	return execAssetUpdate(ctx, exec, assetID, "index state update", query, args...)
}

func SetMediaAssetIndexed(ctx context.Context, exec mediaAssetSQLExecutor, assetID, contentHash, sourceVersion, updatedAt, embeddingModel, embeddingVersion, contractHash string) (bool, error) {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	result, err := exec.ExecContext(ctx, `
		UPDATE media_assets
		SET index_state = 'INDEXED', index_state_updated_at = ?, updated_at = ?,
			metadata_json = json_set(
				json_set(
					json_set(
						json_set(
							json_set(COALESCE(metadata_json, '{}'), '$.indexed_at', ?),
							'$.indexed_content_hash', ?),
						'$.embedding_model', ?),
					'$.embedding_model_version', ?),
				'$.embedding_contract_hash', ?)
		WHERE id = ? AND source_version = ? AND index_state = 'INDEXING'`,
		updatedAt, updatedAt, updatedAt, contentHash, embeddingModel, embeddingVersion, contractHash, assetID, sourceVersion)
	if err != nil {
		return false, fmt.Errorf("asset committer: indexed state update: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("asset committer: indexed state rows affected: %w", err)
	}
	return affected == 1, nil
}

func UpdateMediaAssetFolderPath(ctx context.Context, exec mediaAssetSQLExecutor, assetID, folderID, folderPath, updatedAt string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return execAssetUpdate(ctx, exec, assetID, "folder path update", `UPDATE media_assets SET folder_id = ?, folder_path = ?, updated_at = ? WHERE id = ?`, folderID, folderPath, updatedAt, assetID)
}

func UpdateMediaAssetLifecycle(ctx context.Context, exec mediaAssetSQLExecutor, assetID, state, deletedAt, updatedAt string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return execAssetUpdate(ctx, exec, assetID, "lifecycle update", `UPDATE media_assets SET lifecycle_state = ?, deleted_at = ?, updated_at = ? WHERE id = ?`, state, deletedAt, updatedAt, assetID)
}

func UpdateMediaAssetTaxonomy(ctx context.Context, exec mediaAssetSQLExecutor, taxonomy mediaregistry.AssetTaxonomy) error {
	if err := taxonomy.Validate(); err != nil {
		return fmt.Errorf("asset committer: taxonomy update: %w", err)
	}
	return execAssetUpdate(ctx, exec, taxonomy.AssetID, "taxonomy update", `UPDATE media_assets SET namespace = ?, asset_kind = ?, source_type = ?, semantic_role = ?, updated_at = ? WHERE id = ?`, taxonomy.Namespace, taxonomy.AssetKind, taxonomy.SourceType, taxonomy.SemanticRole, time.Now().UTC().Format(time.RFC3339), taxonomy.AssetID)
}

func LinkMediaAssetContent(ctx context.Context, exec mediaAssetSQLExecutor, assetID, contentSHA256 string) error {
	return execAssetUpdate(ctx, exec, assetID, "content link", `UPDATE media_assets SET content_sha256 = ?, updated_at = ? WHERE id = ?`, contentSHA256, time.Now().UTC().Format(time.RFC3339), assetID)
}

func UpdateMediaAssetSearchText(ctx context.Context, exec mediaAssetSQLExecutor, assetID, searchText, updatedAt string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return execAssetUpdate(ctx, exec, assetID, "search text update", `UPDATE media_assets SET search_text = ?, updated_at = ? WHERE id = ?`, searchText, updatedAt, assetID)
}

func UpdateMediaAssetUpdatedAt(ctx context.Context, exec mediaAssetSQLExecutor, assetID, updatedAt string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return execAssetUpdate(ctx, exec, assetID, "updated-at refresh", `UPDATE media_assets SET updated_at = ? WHERE id = ?`, updatedAt, assetID)
}

func UpdateMediaAssetOrphanMetadata(ctx context.Context, exec mediaAssetSQLExecutor, assetID string, detectedAt time.Time, kind string) error {
	at := detectedAt.UTC().Format(time.RFC3339)
	key := "orphan_" + strings.TrimSpace(kind)
	if key != "orphan_local" && key != "orphan_drive" {
		key = "orphan_unknown"
	}
	return execAssetUpdate(ctx, exec, assetID, "orphan metadata update", `UPDATE media_assets SET metadata_json = json_set(json_set(json_set(COALESCE(metadata_json, '{}'), '$.`+key+`', 1), '$.orphan_reason', ?), '$.orphan_detected_at', ?), updated_at = ? WHERE id = ?`, kind, at, at, assetID)
}
