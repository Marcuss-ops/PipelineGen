package imagesregistry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacommit"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// mediaAssetSQLExecutor is the canonical SQL mutation boundary for
// media_assets. Both *sql.DB and *sql.Tx satisfy it; keeping the executor
// narrow lets callers preserve their transaction while the SQL itself stays
// owned by this file.
type mediaAssetSQLExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func execAssetUpdate(ctx context.Context, exec mediaAssetSQLExecutor, assetID, operation, query string, args ...any) error {
	if exec == nil {
		return fmt.Errorf("media asset mutations: %s: executor is unavailable", operation)
	}
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("media asset mutations: %s: %w", operation, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("media asset mutations: %s rows affected: %w", operation, err)
	}
	if affected == 0 {
		return fmt.Errorf("media asset mutations: %s: asset %q not found", operation, assetID)
	}
	return nil
}

// ── Projection mutations (retained from the deleted projection-mutations file) ──
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
func resolveMutationIndexIdentity(ctx context.Context, tx *sql.Tx, patch persistence.AssetPatch) (string, string, string, error) {
	source := strings.TrimSpace(patch.Source)
	mediaType := strings.TrimSpace(patch.MediaType)
	sourceVersion := strings.TrimSpace(patch.SourceVersion)
	if source != "" && mediaType != "" && sourceVersion != "" {
		return source, mediaType, sourceVersion, nil
	}
	var storedSource, storedMediaType, storedVersion, contentHash string
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(source,''), COALESCE(media_type,''),
		       COALESCE(source_version,''), COALESCE(legacy_file_md5,'')
		FROM media_assets WHERE id = ?`, patch.AssetID).
		Scan(&storedSource, &storedMediaType, &storedVersion, &contentHash); err != nil {
		if err == sql.ErrNoRows {
			return "", "", "", fmt.Errorf("asset mutator: asset %q not found", patch.AssetID)
		}
		return "", "", "", fmt.Errorf("asset mutator: resolve index identity %q: %w", patch.AssetID, err)
	}
	if source == "" {
		source = storedSource
	}
	if mediaType == "" {
		mediaType = storedMediaType
	}
	if sourceVersion == "" {
		sourceVersion = storedVersion
		if sourceVersion == "" {
			sourceVersion = contentHash
		}
	}
	if source == "" || mediaType == "" || sourceVersion == "" {
		return "", "", "", fmt.Errorf("asset mutator: asset %q lacks source/media_type/source_version for index request", patch.AssetID)
	}
	return source, mediaType, sourceVersion, nil
}

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

// CheckAndIncrementMediaAssetVersion performs the canonical optimistic
// concurrency update used by the admin console. The read-back remains here
// so the compare-and-swap and its SSOT lookup share one writer family.

func CheckAndIncrementMediaAssetVersion(ctx context.Context, db *sql.DB, assetID string, expectedVersion int) (currentVersion int, ok bool, err error) {
	if db == nil {
		return 0, false, fmt.Errorf("asset committer: database is required")
	}
	if strings.TrimSpace(assetID) == "" {
		return 0, false, fmt.Errorf("asset committer: asset id is required")
	}
	result, err := db.ExecContext(ctx, `
		UPDATE media_assets
		SET admin_version = admin_version + 1
		WHERE id = ? AND admin_version = ?`, assetID, expectedVersion)
	if err != nil {
		return 0, false, fmt.Errorf("asset committer: increment admin version: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, false, fmt.Errorf("asset committer: admin version rows affected: %w", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT admin_version FROM media_assets WHERE id = ?`, assetID).Scan(&currentVersion); err != nil {
		if err == sql.ErrNoRows {
			return 0, false, fmt.Errorf("asset committer: asset not found")
		}
		return 0, false, fmt.Errorf("asset committer: read admin version: %w", err)
	}
	return currentVersion, affected == 1, nil
}

// UpdateMediaAssetContentHashesTx persists the paired content/binary digest
// projection for an existing asset inside a caller-owned transaction.

func UpdateMediaAssetContentHashesTx(ctx context.Context, tx *sql.Tx, assetID, contentSHA256, binarySHA256 string) error {
	return execAssetUpdate(ctx, tx, assetID, "content hash backfill", `UPDATE media_assets SET content_sha256 = ?, binary_sha256 = ? WHERE id = ?`, contentSHA256, binarySHA256, assetID)
}

// UpdateMediaAssetTaxonomyTx applies taxonomy dimensions in a caller-owned
// transaction. It is the bridge used by the media registry adapter.

func UpdateMediaAssetTaxonomyTx(ctx context.Context, tx *sql.Tx, taxonomy mediaregistry.AssetTaxonomy) error {
	return UpdateMediaAssetTaxonomy(ctx, tx, taxonomy)
}

// UpdateMediaAssetTaxonomyDB applies taxonomy through the canonical writer to
// a standalone database connection.

func UpdateMediaAssetTaxonomyDB(ctx context.Context, db *sql.DB, taxonomy mediaregistry.AssetTaxonomy) error {
	return UpdateMediaAssetTaxonomy(ctx, db, taxonomy)
}

// LinkMediaAssetContentTx links a content digest in a caller-owned
// transaction. It is the bridge used by the media registry adapter.

func LinkMediaAssetContentTx(ctx context.Context, tx *sql.Tx, assetID, contentSHA256 string) error {
	return execAssetUpdate(ctx, tx, assetID, "content link", `UPDATE media_assets SET content_sha256 = ? WHERE id = ?`, contentSHA256, assetID)
}

// LinkMediaAssetContentDB links a content digest through the canonical writer
// to a standalone database connection.

func LinkMediaAssetContentDB(ctx context.Context, db *sql.DB, assetID, contentSHA256 string) error {
	return execAssetUpdate(ctx, db, assetID, "content link", `UPDATE media_assets SET content_sha256 = ? WHERE id = ?`, contentSHA256, assetID)
}

// UpdateMediaAssetTaxonomyBackfill applies a complete taxonomy repair while
// keeping the conditional media_type normalization in the canonical writer.

func UpdateMediaAssetTaxonomyBackfill(ctx context.Context, db *sql.DB, assetID string, taxonomy mediaregistry.AssetTaxonomy, replacementMediaType string) error {
	if db == nil {
		return fmt.Errorf("asset committer: database is required")
	}
	if err := taxonomy.Validate(); err != nil {
		return fmt.Errorf("asset committer: taxonomy backfill: %w", err)
	}
	query := `UPDATE media_assets SET namespace=?, asset_kind=?, source_type=?`
	args := []any{taxonomy.Namespace, taxonomy.AssetKind, taxonomy.SourceType}
	if replacementMediaType != "" {
		query += `, media_type=?`
		args = append(args, replacementMediaType)
	}
	query += `, updated_at=datetime('now') WHERE id=?`
	args = append(args, assetID)
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("asset committer: taxonomy backfill %q: %w", assetID, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("asset committer: taxonomy backfill rows affected %q: %w", assetID, err)
	} else if affected == 0 {
		return fmt.Errorf("asset committer: taxonomy backfill asset %q not found", assetID)
	}
	return nil
}

// UpdateMediaAssetLifecycleIfNotInDeletionChain performs the first deletion
// transition while preserving the dispatcher's idempotency predicate.

func UpdateMediaAssetLifecycleIfNotInDeletionChain(ctx context.Context, tx *sql.Tx, assetID, newState, updatedAt string) (int64, error) {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	result, err := tx.ExecContext(ctx, `UPDATE media_assets SET lifecycle_state = ?, updated_at = ? WHERE id = ? AND lifecycle_state NOT IN ('DELETE_REQUESTED', 'DELETE_PENDING', 'DRIVE_DELETE_PENDING', 'INDEX_DELETE_PENDING', 'DELETED')`, newState, updatedAt, assetID)
	if err != nil {
		return 0, fmt.Errorf("asset committer: lifecycle deletion dispatch %q: %w", assetID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("asset committer: lifecycle deletion dispatch rows affected %q: %w", assetID, err)
	}
	return affected, nil
}

// UpdateMediaAssetLifecycleCAS performs the canonical expected-state guarded
// lifecycle transition used by outbox workers.

func UpdateMediaAssetLifecycleCAS(ctx context.Context, tx *sql.Tx, assetID, expectedState, newState, updatedAt string) (int64, error) {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	result, err := tx.ExecContext(ctx, `UPDATE media_assets SET lifecycle_state = ?, updated_at = ? WHERE id = ? AND lifecycle_state = ?`, newState, updatedAt, assetID, expectedState)
	if err != nil {
		return 0, fmt.Errorf("asset committer: lifecycle CAS %q: %w", assetID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("asset committer: lifecycle CAS rows affected %q: %w", assetID, err)
	}
	return affected, nil
}

// UpdateMediaAssetUpdatedAtTx refreshes the canonical timestamp in a caller
// owned transaction without exposing media_assets SQL to the caller package.

func UpdateMediaAssetUpdatedAtTx(ctx context.Context, tx *sql.Tx, assetID, updatedAt string) error {
	return UpdateMediaAssetUpdatedAt(ctx, tx, assetID, updatedAt)
}

// MarkMediaAssetOrphan persists the maintenance orphan marker through the
// canonical mutation boundary.

func MarkMediaAssetOrphan(ctx context.Context, db *sql.DB, assetID string, detectedAt time.Time, kind string) error {
	return UpdateMediaAssetOrphanMetadata(ctx, db, assetID, detectedAt, kind)
}

// DeleteMediaAssetRow deletes the parent row after dependent rows have been
// removed by HardDeleteTx. The SQL remains inside the canonical writer family.

func DeleteMediaAssetRow(ctx context.Context, tx *sql.Tx, assetID string) (int64, error) {
	result, err := tx.ExecContext(ctx, `DELETE FROM media_assets WHERE id = ?`, assetID)
	if err != nil {
		return 0, fmt.Errorf("asset committer: parent delete %q: %w", assetID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("asset committer: parent delete rows affected %q: %w", assetID, err)
	}
	return rows, nil
}

// RestoreMediaAssetTx restores the canonical lifecycle state in a caller-owned
// transaction. The SQL is deliberately delegated to the canonical mutation
// boundary rather than retained by the repository primitive.

func RestoreMediaAssetTx(ctx context.Context, tx *sql.Tx, assetID string) error {
	return UpdateMediaAssetLifecycle(ctx, tx, assetID, "ACTIVE", "", time.Now().UTC().Format(time.RFC3339))
}

// HardDeleteMediaAssetTx is the repository-shaped alias for the canonical
// tx-bound deletion primitive.

func HardDeleteMediaAssetTx(ctx context.Context, tx *sql.Tx, assetID string) error {
	return HardDeleteTx(ctx, tx, assetID)
}

func normalizeDriveLocationPatches(changes []persistence.DriveLocationPatch) ([]persistence.DriveLocationPatch, error) {
	seen := make(map[string]persistence.DriveLocationPatch, len(changes))
	out := make([]persistence.DriveLocationPatch, 0, len(changes))
	for _, change := range changes {
		change.AssetID = strings.TrimSpace(change.AssetID)
		change.DriveFileID = strings.TrimSpace(change.DriveFileID)
		change.DriveLink = strings.TrimSpace(change.DriveLink)
		change.DownloadURL = strings.TrimSpace(change.DownloadURL)
		if change.AssetID == "" {
			return nil, fmt.Errorf("asset mutator: drive location asset id is required")
		}
		if previous, exists := seen[change.AssetID]; exists {
			if previous != change {
				return nil, fmt.Errorf("asset mutator: conflicting drive changes for asset %q", change.AssetID)
			}
			continue
		}
		seen[change.AssetID] = change
		out = append(out, change)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AssetID < out[j].AssetID })
	return out, nil
}

func hasVerifiedAlternateLocation(ctx context.Context, tx *sql.Tx, assetID string) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM asset_locations
		WHERE asset_id = ? AND location_kind IN ('local','object_storage')
		  AND TRIM(COALESCE(uri,'')) <> ''
		  AND TRIM(COALESCE(legacy_file_md5,'')) <> ''
		  AND COALESCE(file_size_bytes,0) > 0`, assetID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("asset mutator: inspect alternate locations %q: %w", assetID, err)
	}
	return count > 0, nil
}

func canonicalDrivePrimary(ctx context.Context, tx *sql.Tx, assetID string) (bool, error) {
	var existing int
	err := tx.QueryRowContext(ctx, `SELECT is_primary FROM asset_locations WHERE asset_id=? AND location_kind='drive'`, assetID).Scan(&existing)
	if err == nil {
		return existing != 0, nil
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_locations WHERE asset_id=? AND is_primary=1`, assetID).Scan(&count); err != nil {
		return false, err
	}
	return count == 0, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
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
