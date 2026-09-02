// Package media — mutations.go: the canonical PostgreSQL mutation surface
// for existing media assets.
//
// INDEXED_WRITER_SCOPE: clipindexer
// The terminal INDEXED CAS is exposed here solely as the persistence adapter
// invoked by the canonical outbox consumer; no workflow writes this state.
//
// Mirrors the SQLite mutation family (internal/platform/sqlite/assets/
// imagesregistry/asset_committer.go mutation methods + asset_committer_
// mutations.go primitives) statement-for-statement. Every mutation fails
// closed on an unknown asset (rows-affected gate); no mutation is ever a
// silent no-op success.
package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacommit"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// compile-time check: the alias preserves the capability draft's underlying
// type identity (alias, not a new struct).
var _ mediacommit.ImageDraft = mediacommitImageDraft{}

// ── Embedding JSON channels ─────────────────────────────────────────────

// PersistEmbeddingJSON persists one embedding channel through the canonical
// asset mutation boundary. Channel names are deliberately typed as a closed
// set at this infrastructure boundary; producers never select SQL columns.
func (c *PostgresAssetCommitter) PersistEmbeddingJSON(ctx context.Context, assetID, channel string, embedding []float64, status string) error {
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

func UpdateMediaAssetEmbeddingJSON(ctx context.Context, exec mediaAssetSQLExecutor, assetID, value string) error {
	return execAssetUpdate(ctx, exec, assetID, "semantic embedding update", `UPDATE media_assets SET embedding_json = $1, updated_at = $2 WHERE id = $3`, value, time.Now().UTC().Format(time.RFC3339), assetID)
}

func UpdateMediaAssetTranscriptEmbedding(ctx context.Context, exec mediaAssetSQLExecutor, assetID, value string) error {
	return execAssetUpdate(ctx, exec, assetID, "transcript embedding update", `UPDATE media_assets SET transcript_embedding = $1, updated_at = $2 WHERE id = $3`, value, time.Now().UTC().Format(time.RFC3339), assetID)
}

func UpdateMediaAssetVisualEmbedding(ctx context.Context, exec mediaAssetSQLExecutor, assetID, value string) error {
	return execAssetUpdate(ctx, exec, assetID, "visual embedding update", `UPDATE media_assets SET visual_embedding = $1, updated_at = $2 WHERE id = $3`, value, time.Now().UTC().Format(time.RFC3339), assetID)
}

func UpdateMediaAssetAudioEmbedding(ctx context.Context, exec mediaAssetSQLExecutor, assetID, value string) error {
	return execAssetUpdate(ctx, exec, assetID, "audio embedding update", `UPDATE media_assets SET audio_embedding = $1, updated_at = $2 WHERE id = $3`, value, time.Now().UTC().Format(time.RFC3339), assetID)
}

// ── Index state ─────────────────────────────────────────────────────────

// SetIndexState delegates the canonical index-state mutation. Indexing
// workers remain responsible for metrics and retry policy; this method owns
// only durable PostgreSQL state.
func (c *PostgresAssetCommitter) SetIndexState(ctx context.Context, assetID string, state asset.IndexState, lastError string) error {
	return UpdateMediaAssetIndexState(ctx, c.db, assetID, string(state), time.Now().UTC().Format(time.RFC3339), lastError)
}

// SetIndexed performs the compare-and-set terminal index transition through
// the canonical committer boundary.
func (c *PostgresAssetCommitter) SetIndexed(ctx context.Context, assetID, contentHash, sourceVersion, embeddingModel, embeddingVersion, contractHash string) (bool, error) {
	ok, err := SetMediaAssetIndexed(ctx, c.db, assetID, contentHash, sourceVersion,
		time.Now().UTC().Format(time.RFC3339), embeddingModel, embeddingVersion, contractHash)
	return ok, err
}

func UpdateMediaAssetIndexState(ctx context.Context, exec mediaAssetSQLExecutor, assetID, state, updatedAt, lastError string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	var query string
	var args []any
	if strings.TrimSpace(lastError) == "" {
		query = `UPDATE media_assets SET index_state = $1, index_state_updated_at = $2, metadata_json = (metadata_json::jsonb - 'last_index_error'::text)::text, updated_at = $3 WHERE id = $4`
		args = []any{state, updatedAt, updatedAt, assetID}
	} else {
		query = `UPDATE media_assets SET index_state = $1, index_state_updated_at = $2, metadata_json = jsonb_set(metadata_json::jsonb, '{last_index_error}', to_jsonb($3::text))::text, updated_at = $4 WHERE id = $5`
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
		SET index_state = 'INDEXED', index_state_updated_at = $1, updated_at = $2,
			metadata_json = jsonb_set(
				jsonb_set(
					jsonb_set(
						jsonb_set(
							jsonb_set(metadata_json::jsonb, '{indexed_at}', to_jsonb($3::text)),
							'{indexed_content_hash}', to_jsonb($4::text)),
						'{embedding_model}', to_jsonb($5::text)),
					'{embedding_model_version}', to_jsonb($6::text)),
				'{embedding_contract_hash}', to_jsonb($7::text))::text
		WHERE id = $8 AND source_version = $9 AND index_state = 'INDEXING'`,
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

// ── Metadata ────────────────────────────────────────────────────────────

// PatchMediaAssetMetadataJSON applies a JSON patch through the canonical
// committer (SQLite json_patch → PostgreSQL || merge operator).
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
		SET metadata_json = (metadata_json::jsonb || $1::jsonb)::text, updated_at = $2
		WHERE id = $3`, patchJSON, updatedAt, assetID)
}

// PatchMetadataJSONTx applies a metadata patch in a caller-owned
// transaction. It is the only tx-bound metadata mutation exposed to
// producer adapters.
func (c *PostgresAssetCommitter) PatchMetadataJSONTx(ctx context.Context, tx *sql.Tx, assetID, patchJSON, updatedAt string) error {
	return PatchMediaAssetMetadataJSON(ctx, tx, assetID, patchJSON, updatedAt)
}

// PatchMetadataJSON applies a JSON patch on the committer's pool.
func (c *PostgresAssetCommitter) PatchMetadataJSON(ctx context.Context, assetID, patchJSON, updatedAt string) error {
	return PatchMediaAssetMetadataJSON(ctx, c.db, assetID, patchJSON, updatedAt)
}

// ReplaceMetadataJSON replaces the metadata snapshot through the canonical
// committer. It is used by legacy enrichment adapters that still provide a
// complete JSON envelope rather than a typed patch.
func (c *PostgresAssetCommitter) ReplaceMetadataJSON(ctx context.Context, assetID, metadataJSON, updatedAt string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	// SQLite passes "metadata_json = ?, updated_at = ?" positionally; the
	// PostgreSQL mirror uses ordinal placeholders (see the set-clause guard
	// inside updateMediaAssetMetadata).
	return updateMediaAssetMetadata(ctx, c.db, assetID, metadataJSON,
		"metadata_json = $1, updated_at = $2", metadataJSON, updatedAt)
}

func updateMediaAssetMetadata(ctx context.Context, exec mediaAssetSQLExecutor, assetID, metadataJSON, setClause string, args ...any) error {
	if strings.TrimSpace(metadataJSON) == "" {
		metadataJSON = "{}"
	}
	if !json.Valid([]byte(metadataJSON)) {
		return fmt.Errorf("asset committer: metadata JSON is invalid")
	}
	if strings.Contains(setClause, "?") {
		return fmt.Errorf("asset committer: metadata update set clause must use ordinal placeholders ($1, $2, …)")
	}
	// PG contract: setClause references $1..$N in order for the caller's
	// args; the asset id is appended as $N+1. (SQLite's mirror helper
	// prepends metadataJSON before args; the PG convention avoids the
	// unreferenced-parameter error PostgreSQL raises for skipped ordinals.)
	return execAssetUpdate(ctx, exec, assetID, "metadata update", "UPDATE media_assets SET "+setClause+" WHERE id = $"+fmt.Sprintf("%d", len(args)+1), append(append([]any{}, args...), assetID)...)
}

// ── Folder path / lifecycle / taxonomy / content / search text ──────────

func (c *PostgresAssetCommitter) UpdateFolderPath(ctx context.Context, assetID, folderID, folderPath, updatedAt string) error {
	return UpdateMediaAssetFolderPath(ctx, c.db, assetID, folderID, folderPath, updatedAt)
}

// UpdateFolderPathTx applies a folder-path mutation in the caller-owned
// transaction so the caller can emit the canonical index request atomically.
func (c *PostgresAssetCommitter) UpdateFolderPathTx(ctx context.Context, tx *sql.Tx, assetID, folderID, folderPath, updatedAt string) error {
	return UpdateMediaAssetFolderPath(ctx, tx, assetID, folderID, folderPath, updatedAt)
}

func UpdateMediaAssetFolderPath(ctx context.Context, exec mediaAssetSQLExecutor, assetID, folderID, folderPath, updatedAt string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return execAssetUpdate(ctx, exec, assetID, "folder path update", `UPDATE media_assets SET folder_id = $1, folder_path = $2, updated_at = $3 WHERE id = $4`, folderID, folderPath, updatedAt, assetID)
}

func (c *PostgresAssetCommitter) UpdateLifecycle(ctx context.Context, assetID string, state, deletedAt, updatedAt string) error {
	return UpdateMediaAssetLifecycle(ctx, c.db, assetID, state, deletedAt, updatedAt)
}

func UpdateMediaAssetLifecycle(ctx context.Context, exec mediaAssetSQLExecutor, assetID, state, deletedAt, updatedAt string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return execAssetUpdate(ctx, exec, assetID, "lifecycle update", `UPDATE media_assets SET lifecycle_state = $1, deleted_at = $2, updated_at = $3 WHERE id = $4`, state, deletedAt, updatedAt, assetID)
}

func (c *PostgresAssetCommitter) UpdateTaxonomy(ctx context.Context, taxonomy mediaregistry.AssetTaxonomy) error {
	return UpdateMediaAssetTaxonomy(ctx, c.db, taxonomy)
}

func UpdateMediaAssetTaxonomy(ctx context.Context, exec mediaAssetSQLExecutor, taxonomy mediaregistry.AssetTaxonomy) error {
	if err := taxonomy.Validate(); err != nil {
		return fmt.Errorf("asset committer: taxonomy update: %w", err)
	}
	return execAssetUpdate(ctx, exec, taxonomy.AssetID, "taxonomy update",
		`UPDATE media_assets SET namespace = $1, asset_kind = $2, source_type = $3, semantic_role = $4, updated_at = $5 WHERE id = $6`,
		taxonomy.Namespace, taxonomy.AssetKind, taxonomy.SourceType, taxonomy.SemanticRole, time.Now().UTC().Format(time.RFC3339), taxonomy.AssetID)
}

func (c *PostgresAssetCommitter) LinkContent(ctx context.Context, assetID, contentSHA256 string) error {
	return LinkMediaAssetContent(ctx, c.db, assetID, contentSHA256)
}

func LinkMediaAssetContent(ctx context.Context, exec mediaAssetSQLExecutor, assetID, contentSHA256 string) error {
	return execAssetUpdate(ctx, exec, assetID, "content link", `UPDATE media_assets SET content_sha256 = $1, updated_at = $2 WHERE id = $3`, contentSHA256, time.Now().UTC().Format(time.RFC3339), assetID)
}

func (c *PostgresAssetCommitter) UpdateSearchText(ctx context.Context, assetID, searchText, updatedAt string) error {
	return UpdateMediaAssetSearchText(ctx, c.db, assetID, searchText, updatedAt)
}

func UpdateMediaAssetSearchText(ctx context.Context, exec mediaAssetSQLExecutor, assetID, searchText, updatedAt string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return execAssetUpdate(ctx, exec, assetID, "search text update", `UPDATE media_assets SET search_text = $1, updated_at = $2 WHERE id = $3`, searchText, updatedAt, assetID)
}

func (c *PostgresAssetCommitter) RefreshUpdatedAt(ctx context.Context, assetID, updatedAt string) error {
	return UpdateMediaAssetUpdatedAt(ctx, c.db, assetID, updatedAt)
}

func UpdateMediaAssetUpdatedAt(ctx context.Context, exec mediaAssetSQLExecutor, assetID, updatedAt string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return execAssetUpdate(ctx, exec, assetID, "updated-at refresh", `UPDATE media_assets SET updated_at = $1 WHERE id = $2`, updatedAt, assetID)
}

func (c *PostgresAssetCommitter) UpdateOrphanMetadata(ctx context.Context, assetID string, detectedAt time.Time, kind string) error {
	return UpdateMediaAssetOrphanMetadata(ctx, c.db, assetID, detectedAt, kind)
}

func UpdateMediaAssetOrphanMetadata(ctx context.Context, exec mediaAssetSQLExecutor, assetID string, detectedAt time.Time, kind string) error {
	at := detectedAt.UTC().Format(time.RFC3339)
	key := "orphan_" + strings.TrimSpace(kind)
	if key != "orphan_local" && key != "orphan_drive" {
		key = "orphan_unknown"
	}
	return execAssetUpdate(ctx, exec, assetID, "orphan metadata update", `
		UPDATE media_assets SET metadata_json = (
			jsonb_set(jsonb_set(
				jsonb_set(metadata_json::jsonb, '{`+key+`}', '1'::jsonb),
			'{orphan_reason}', to_jsonb($1::text)),
			'{orphan_detected_at}', to_jsonb($2::text))::text
		), updated_at = $3 WHERE id = $4`,
		kind, at, at, assetID)
}

// ── Drive delivery projection (SQLite mirror) ───────────────────────────

// UpdateDriveDeliveryByLegacyHash applies the post-commit Drive projection
// update through the canonical asset boundary and keeps asset_locations in
// sync in the same transaction.
func (c *PostgresAssetCommitter) UpdateDriveDeliveryByLegacyHash(ctx context.Context, hash string, mutation persistence.DriveDeliveryMutation) error {
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
		result, err = tx.ExecContext(ctx, `UPDATE media_assets SET metadata_json = (metadata_json::jsonb || jsonb_build_object('delivery_status', $1::text))::text, updated_at = to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"') WHERE source = 'image' AND legacy_file_md5 = $2`, mutation.Status, hash)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE media_assets SET drive_file_id = $1, drive_link = $2, download_link = $3, metadata_json = (metadata_json::jsonb || jsonb_build_object('delivery_status', $4::text))::text, updated_at = to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"') WHERE source = 'image' AND legacy_file_md5 = $5`, mutation.DriveFileID, mutation.DriveLink, mutation.DownloadLink, mutation.Status, hash)
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
		if err := tx.QueryRowContext(ctx, `SELECT id FROM media_assets WHERE source = 'image' AND legacy_file_md5 = $1`, hash).Scan(&assetID); err != nil {
			return fmt.Errorf("asset committer: resolve Drive delivery asset: %w", err)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := tx.ExecContext(ctx, `INSERT INTO asset_locations (asset_id, location_kind, uri, external_id, web_view_link, download_url, mime_type, file_size_bytes, legacy_file_md5, is_primary, created_at, updated_at) VALUES ($1, 'drive', $2, $3, $4, $5, '', 0, $6, 0, $7, $8) ON CONFLICT (asset_id, location_kind) DO UPDATE SET uri=excluded.uri, external_id=excluded.external_id, web_view_link=excluded.web_view_link, download_url=excluded.download_url, legacy_file_md5=excluded.legacy_file_md5, updated_at=excluded.updated_at`, assetID, "drive://"+mutation.DriveFileID, mutation.DriveFileID, mutation.DriveLink, mutation.DownloadLink, hash, now, now); err != nil {
			return fmt.Errorf("asset committer: upsert Drive location: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("asset committer: commit Drive delivery: %w", err)
	}
	return nil
}

// ── Image projection fields (SQLite mirror: projection_mutations) ───────

// UpdateMediaAssetImageFields persists the image projection through the
// canonical mutation implementation.
func UpdateMediaAssetImageFields(ctx context.Context, exec mediaAssetSQLExecutor, assetID string, image *mediacommitImageDraft) error {
	if image == nil {
		return nil
	}
	return execAssetUpdate(ctx, exec, assetID, "image fields update", `
		UPDATE media_assets
		SET url = $1, tags = $2, tags_norm = $3, width = $4, height = $5,
		    relative_path = $6, origin = $7, provider = $8, updated_at = $9
		WHERE id = $10`,
		image.URL, image.TagsJSON, image.TagsNorm, image.Width, image.Height,
		image.RelativePath, image.Origin, image.Provider,
		time.Now().UTC().Format(time.RFC3339), assetID)
}

// UpdateMediaAssetUsage increments the reuse counter through the canonical
// mutation implementation.
func UpdateMediaAssetUsage(ctx context.Context, exec mediaAssetSQLExecutor, assetID, usedAt string) error {
	if strings.TrimSpace(usedAt) == "" {
		usedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return execAssetUpdate(ctx, exec, assetID, "usage update", `
		UPDATE media_assets
		SET reuse_count = COALESCE(reuse_count, 0) + 1, last_used_at = $1, updated_at = $2
		WHERE id = $3`, usedAt, usedAt, assetID)
}
