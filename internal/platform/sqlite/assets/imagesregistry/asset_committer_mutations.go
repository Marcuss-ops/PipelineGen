package imagesregistry

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

var _ persistence.AssetMutator = (*SQLiteMediaCommitter)(nil)
var _ persistence.CanonicalAssetWriter = (*SQLiteMediaCommitter)(nil)

func (c *SQLiteMediaCommitter) PatchAsset(ctx context.Context, patch persistence.AssetPatch) error {
	if c == nil || c.db == nil {
		return fmt.Errorf("asset mutator: canonical writer is unavailable")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("asset mutator: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := c.PatchAssetTx(ctx, tx, patch); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("asset mutator: commit: %w", err)
	}
	committed = true
	return nil
}

func (c *SQLiteMediaCommitter) PatchAssetTx(ctx context.Context, tx persistence.Transaction, patch persistence.AssetPatch) error {
	if c == nil || c.box == nil {
		return fmt.Errorf("asset mutator: canonical writer is unavailable")
	}
	if strings.TrimSpace(patch.AssetID) == "" {
		return fmt.Errorf("asset mutator: asset id is required")
	}
	sqlTx, ok := tx.(*sql.Tx)
	if !ok || sqlTx == nil {
		return fmt.Errorf("asset mutator: expected *sql.Tx, got %T", tx)
	}

	sets := make([]string, 0, 24)
	args := make([]any, 0, 28)
	addString := func(column string, value *string) {
		if value == nil {
			return
		}
		sets = append(sets, column+" = ?")
		args = append(args, *value)
	}
	addFloat := func(column string, value *float64) {
		if value == nil {
			return
		}
		sets = append(sets, column+" = ?")
		args = append(args, *value)
	}
	addInt := func(column string, value *int) {
		if value == nil {
			return
		}
		sets = append(sets, column+" = ?")
		args = append(args, *value)
	}
	addString("name", patch.Name)
	addString("category", patch.Category)
	addString("group_name", patch.Group)
	addString("folder_id", patch.FolderID)
	addString("folder_path", patch.FolderPath)
	addString("deleted_at", patch.DeletedAt)
	addString("search_text", patch.SearchText)
	addString("lifecycle_state", patch.LifecycleState)
	addString("enrich_state", patch.EnrichState)
	addString("metadata_json", patch.MetadataJSON)
	addString("embedding_json", patch.EmbeddingJSON)
	addString("visual_embedding", patch.VisualEmbedding)
	addString("transcript_embedding", patch.TranscriptEmbedding)
	addString("collection_version", patch.Collection)
	addString("scene_type", patch.SceneType)
	addString("phash", patch.PHash)
	addString("last_used_at", patch.LastUsedAt)
	addFloat("quality_score", patch.QualityScore)
	addInt("reuse_count", patch.ReuseCount)
	addString("drive_file_id", patch.DriveFileID)
	addString("drive_link", patch.DriveLink)
	addString("download_link", patch.DownloadLink)
	addString("local_path", patch.LocalPath)
	if patch.IndexState != nil {
		sets = append(sets, "index_state = ?")
		args = append(args, *patch.IndexState)
		updatedAt := time.Now().UTC()
		if patch.IndexStateUpdatedAt != nil {
			updatedAt = patch.IndexStateUpdatedAt.UTC()
		}
		sets = append(sets, "index_state_updated_at = ?")
		args = append(args, updatedAt.Format(time.RFC3339Nano))
	}

	if patch.MetadataPatchJSON != nil {
		if strings.TrimSpace(*patch.MetadataPatchJSON) == "" {
			return fmt.Errorf("asset mutator: metadata patch for %q is empty", patch.AssetID)
		}
		updatedAt := ""
		if patch.UpdatedAt != nil {
			updatedAt = *patch.UpdatedAt
		}
		if err := PatchMediaAssetMetadataJSON(ctx, sqlTx, patch.AssetID, *patch.MetadataPatchJSON, updatedAt); err != nil {
			return err
		}
	}

	if len(sets) > 0 {
		updatedAt := time.Now().UTC().Format(time.RFC3339Nano)
		if patch.UpdatedAt != nil {
			updatedAt = *patch.UpdatedAt
		}
		sets = append(sets, "updated_at = ?")
		args = append(args, updatedAt)
		args = append(args, patch.AssetID)
		res, err := sqlTx.ExecContext(ctx, "UPDATE media_assets SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
		if err != nil {
			return fmt.Errorf("asset mutator: patch %q: %w", patch.AssetID, err)
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("asset mutator: inspect patch %q: %w", patch.AssetID, err)
		}
		if rows == 0 {
			return fmt.Errorf("asset mutator: asset %q not found", patch.AssetID)
		}
	}

	if patch.RequestIndex {
		source, mediaType, sourceVersion, err := resolveMutationIndexIdentity(ctx, sqlTx, patch)
		if err != nil {
			return err
		}
		if _, err := CommitIndexRequestTx(ctx, sqlTx, c.box, IndexRequest{
			AssetID: patch.AssetID, Source: source, MediaType: mediaType,
			SourceVersion: sourceVersion, RequestedAt: time.Now().UTC(),
			Priority: patch.IndexPriority, EventKeySuffix: patch.EventKeySuffix,
		}); err != nil {
			return fmt.Errorf("asset mutator: enqueue index request for %q: %w", patch.AssetID, err)
		}
	}
	return nil
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

func (c *SQLiteMediaCommitter) ReconcileDriveLocations(ctx context.Context, changes []persistence.DriveLocationPatch) error {
	if c == nil || c.db == nil {
		return fmt.Errorf("asset mutator: canonical writer is unavailable")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("asset mutator: begin drive reconciliation tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := c.ReconcileDriveLocationsTx(ctx, tx, changes); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("asset mutator: commit drive reconciliation: %w", err)
	}
	committed = true
	return nil
}

func (c *SQLiteMediaCommitter) ReconcileDriveLocationsTx(ctx context.Context, tx persistence.Transaction, changes []persistence.DriveLocationPatch) error {
	sqlTx, ok := tx.(*sql.Tx)
	if !ok || sqlTx == nil {
		return fmt.Errorf("asset mutator: expected *sql.Tx, got %T", tx)
	}
	normalized, err := normalizeDriveLocationPatches(changes)
	if err != nil {
		return err
	}
	for _, change := range normalized {
		if err := c.reconcileOneDriveLocation(ctx, sqlTx, change); err != nil {
			return err
		}
	}
	return nil
}

func (c *SQLiteMediaCommitter) reconcileOneDriveLocation(ctx context.Context, tx *sql.Tx, change persistence.DriveLocationPatch) error {
	var source, mediaType, sourceVersion, contentHash, existingFileID, existingLink, lifecycle string
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(source,''), COALESCE(media_type,''), COALESCE(source_version,''),
		       COALESCE(legacy_file_md5,''), COALESCE(drive_file_id,''),
		       COALESCE(drive_link,''), COALESCE(lifecycle_state,'ACTIVE')
		FROM media_assets WHERE id = ?`, change.AssetID).
		Scan(&source, &mediaType, &sourceVersion, &contentHash, &existingFileID, &existingLink, &lifecycle); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("asset mutator: drive reconciliation asset %q not found", change.AssetID)
		}
		return fmt.Errorf("asset mutator: read drive asset %q: %w", change.AssetID, err)
	}
	if sourceVersion == "" {
		sourceVersion = contentHash
	}
	if sourceVersion == "" {
		return fmt.Errorf("asset mutator: drive reconciliation asset %q has no source version", change.AssetID)
	}

	var locationFileID, locationLink string
	locationErr := tx.QueryRowContext(ctx, `
		SELECT COALESCE(external_id,''), COALESCE(web_view_link,'')
		FROM asset_locations WHERE asset_id = ? AND location_kind = 'drive'`, change.AssetID).
		Scan(&locationFileID, &locationLink)
	if locationErr != nil && locationErr != sql.ErrNoRows {
		return fmt.Errorf("asset mutator: read drive location %q: %w", change.AssetID, locationErr)
	}
	if change.DriveFileID == "" {
		change.DriveFileID = existingFileID
		if change.DriveFileID == "" && locationErr == nil {
			change.DriveFileID = locationFileID
		}
	}
	if change.DriveLink != "" && change.DriveFileID == "" {
		return fmt.Errorf("asset mutator: asset %q has Drive link without Drive file id", change.AssetID)
	}

	nextLifecycle := lifecycle
	if change.DriveLink == "" {
		hasAlternate, err := hasVerifiedAlternateLocation(ctx, tx, change.AssetID)
		if err != nil {
			return err
		}
		if !hasAlternate && (lifecycle == "" || lifecycle == "ACTIVE" || lifecycle == "PUBLISHED") {
			nextLifecycle = "ERROR"
		}
	}
	if existingFileID == change.DriveFileID && existingLink == change.DriveLink &&
		locationErr == nil && locationFileID == change.DriveFileID && locationLink == change.DriveLink &&
		nextLifecycle == lifecycle && change.DownloadURL == "" {
		return nil
	}

	primary, err := canonicalDrivePrimary(ctx, tx, change.AssetID)
	if err != nil {
		return fmt.Errorf("asset mutator: resolve drive primary %q: %w", change.AssetID, err)
	}
	uri := strings.TrimSpace(change.DriveLink)
	if change.DriveFileID != "" {
		uri = "drive://" + change.DriveFileID
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO asset_locations
			(asset_id, location_kind, uri, external_id, web_view_link, download_url,
			 mime_type, file_size_bytes, legacy_file_md5, is_primary, created_at, updated_at)
		VALUES (?, 'drive', ?, ?, ?, ?, '', 0, '', ?, ?, ?)
		ON CONFLICT(asset_id, location_kind) DO UPDATE SET
			uri=excluded.uri, external_id=excluded.external_id,
			web_view_link=excluded.web_view_link, download_url=excluded.download_url,
			updated_at=excluded.updated_at`,
		change.AssetID, uri, change.DriveFileID, change.DriveLink, change.DownloadURL,
		boolInt(primary), now, now); err != nil {
		return fmt.Errorf("asset mutator: upsert drive location %q: %w", change.AssetID, err)
	}

	driveFileID, driveLink, lifecycleValue := change.DriveFileID, change.DriveLink, nextLifecycle
	patch := persistence.AssetPatch{
		AssetID:        change.AssetID,
		DriveFileID:    &driveFileID,
		DriveLink:      &driveLink,
		LifecycleState: &lifecycleValue,
		RequestIndex:   true,
		Source:         source,
		MediaType:      mediaType,
		SourceVersion:  sourceVersion,
	}
	if change.DownloadURL != "" {
		downloadURL := change.DownloadURL
		patch.DownloadLink = &downloadURL
	}
	suffixHash := digest.SHA256Bytes([]byte(change.DriveFileID + "|" + change.DriveLink + "|" + change.DownloadURL))
	patch.EventKeySuffix = ":location:" + suffixHash[:16]
	return c.PatchAssetTx(ctx, tx, patch)
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
