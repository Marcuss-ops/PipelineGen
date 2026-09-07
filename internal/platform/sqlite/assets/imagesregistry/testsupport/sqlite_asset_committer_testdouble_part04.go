package testsupport

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"strings"
	"time"
)

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
