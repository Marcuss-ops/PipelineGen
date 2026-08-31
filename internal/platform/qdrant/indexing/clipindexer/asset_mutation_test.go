package clipindexer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type testAssetMutationCommitter struct {
	db *sql.DB
}

var _ persistence.AssetMutationCommitter = (*testAssetMutationCommitter)(nil)

func newTestAssetMutationCommitter(db *sql.DB) persistence.AssetMutationCommitter {
	return &testAssetMutationCommitter{db: db}
}

func (m *testAssetMutationCommitter) PersistEmbeddingJSON(ctx context.Context, assetID, channel string, embedding []float64, status string) error {
	raw, err := json.Marshal(embedding)
	if err != nil {
		return err
	}
	columns := map[string]string{
		"semantic":   "embedding_json",
		"transcript": "transcript_embedding",
		"visual":     "visual_embedding",
		"audio":      "audio_embedding",
	}
	column, ok := columns[channel]
	if !ok {
		return fmt.Errorf("unknown test embedding channel %q", channel)
	}
	query := fmt.Sprintf("UPDATE media_assets SET %s = ?", column)
	args := []any{string(raw)}
	if status != "" {
		query += ", metadata_json = json_set(COALESCE(metadata_json, '{}'), '$.embedding_status', ?)"
		args = append(args, status)
	}
	query += " WHERE id = ?"
	args = append(args, assetID)
	_, err = m.db.ExecContext(ctx, query, args...)
	return err
}

func (m *testAssetMutationCommitter) SetIndexState(ctx context.Context, assetID string, state asset.IndexState, lastError string) error {
	query := `UPDATE media_assets SET index_state = ?, index_state_updated_at = ?, metadata_json = `
	args := []any{string(state), time.Now().UTC().Format(time.RFC3339)}
	if lastError != "" {
		query += `json_set(COALESCE(metadata_json, '{}'), '$.last_index_error', ?)`
		args = append(args, lastError)
	} else {
		query += `json_remove(COALESCE(metadata_json, '{}'), '$.last_index_error')`
	}
	query += ` WHERE id = ?`
	args = append(args, assetID)
	_, err := m.db.ExecContext(ctx, query, args...)
	return err
}

func (m *testAssetMutationCommitter) SetIndexed(ctx context.Context, assetID, contentHash, sourceVersion, embeddingModel, embeddingVersion, contractHash string) (bool, error) {
	res, err := m.db.ExecContext(ctx, `
		UPDATE media_assets SET index_state = 'INDEXED', index_state_updated_at = ?,
			metadata_json = json_set(json_set(json_set(json_set(json_set(COALESCE(metadata_json, '{}'), '$.indexed_at', ?), '$.indexed_content_hash', ?), '$.embedding_model', ?), '$.embedding_model_version', ?), '$.embedding_contract_hash', ?)
		WHERE id = ? AND source_version = ? AND index_state = 'INDEXING'`,
		time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339), contentHash,
		embeddingModel, embeddingVersion, contractHash, assetID, sourceVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (m *testAssetMutationCommitter) PatchMetadataJSON(ctx context.Context, assetID, patchJSON, updatedAt string) error {
	if updatedAt == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := m.db.ExecContext(ctx, `UPDATE media_assets SET metadata_json = json_patch(COALESCE(metadata_json, '{}'), ?), updated_at = ? WHERE id = ?`, patchJSON, updatedAt, assetID)
	return err
}

func (m *testAssetMutationCommitter) ReplaceMetadataJSON(ctx context.Context, assetID, metadataJSON, updatedAt string) error {
	if updatedAt == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := m.db.ExecContext(ctx, `UPDATE media_assets SET metadata_json = ?, updated_at = ? WHERE id = ?`, metadataJSON, updatedAt, assetID)
	return err
}

func (m *testAssetMutationCommitter) PatchMetadataJSONTx(ctx context.Context, tx *sql.Tx, assetID, patchJSON, updatedAt string) error {
	if updatedAt == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := tx.ExecContext(ctx, `UPDATE media_assets SET metadata_json = json_patch(COALESCE(metadata_json, '{}'), ?), updated_at = ? WHERE id = ?`, patchJSON, updatedAt, assetID)
	return err
}

func (m *testAssetMutationCommitter) UpdateFolderPath(context.Context, string, string, string, string) error {
	return fmt.Errorf("test folder mutation is not configured")
}
func (m *testAssetMutationCommitter) UpdateFolderPathTx(context.Context, *sql.Tx, string, string, string, string) error {
	return fmt.Errorf("test tx-bound folder mutation is not configured")
}
func (m *testAssetMutationCommitter) UpdateLifecycle(context.Context, string, string, string, string) error {
	return fmt.Errorf("test lifecycle mutation is not configured")
}
func (m *testAssetMutationCommitter) UpdateTaxonomy(context.Context, capregistry.AssetTaxonomy) error {
	return fmt.Errorf("test taxonomy mutation is not configured")
}
func (m *testAssetMutationCommitter) LinkContent(context.Context, string, string) error {
	return fmt.Errorf("test content mutation is not configured")
}
func (m *testAssetMutationCommitter) UpdateSearchText(context.Context, string, string, string) error {
	return fmt.Errorf("test search-text mutation is not configured")
}
func (m *testAssetMutationCommitter) RefreshUpdatedAt(context.Context, string, string) error {
	return fmt.Errorf("test timestamp mutation is not configured")
}
func (m *testAssetMutationCommitter) UpdateOrphanMetadata(context.Context, string, time.Time, string) error {
	return fmt.Errorf("test orphan mutation is not configured")
}
func (m *testAssetMutationCommitter) UpdateDriveDeliveryByLegacyHash(context.Context, string, persistence.DriveDeliveryMutation) error {
	return fmt.Errorf("test drive delivery mutation is not configured")
}
