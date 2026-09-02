// Package media — writer.go: the canonical transaction-bound clip writer
// and mutator on PostgreSQL.
//
// Mirrors the SQLite canonical writer family (imagesregistry
// canonical_clip_mutations.go + clips_transactions.go +
// asset_committer_mutations.go) statement-for-statement. The dispatcher
// owns the transaction and the outbox event; this writer only persists the
// asset projection inside the caller-owned transaction.
package media

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// ── CanonicalAssetWriter: dispatcher-facing tx-bound mutations ──────────

// UpsertClipTx is the canonical transaction-bound clip mutation. The caller
// owns the transaction and remains responsible for emitting the matching
// outbox event before committing it.
func (c *PostgresMediaCommitter) UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *asset.Asset) error {
	if c == nil {
		return fmt.Errorf("canonical clip writer: media committer is unavailable")
	}
	return commitClipTxThroughCanonical(ctx, tx, clip, c)
}

// SetIndexStateTx is the canonical transaction-bound index-state mutation.
// It deliberately uses the exact *sql.Tx supplied by the dispatcher.
func (c *PostgresMediaCommitter) SetIndexStateTx(ctx context.Context, tx *sql.Tx, assetID string, state asset.IndexState) error {
	if c == nil || c.assets == nil {
		return fmt.Errorf("canonical clip writer: asset committer is unavailable")
	}
	if tx == nil {
		return fmt.Errorf("canonical clip writer: transaction is required")
	}
	if assetID == "" {
		return fmt.Errorf("canonical clip writer: asset id is required")
	}
	if !state.Valid() {
		return fmt.Errorf("canonical clip writer: index state %q is invalid", state)
	}
	return UpdateMediaAssetIndexState(ctx, tx, assetID, string(state), "", "")
}

// ── AssetMutator ────────────────────────────────────────────────────────

// PatchAsset opens a transaction, applies the patch, and commits.
func (c *PostgresMediaCommitter) PatchAsset(ctx context.Context, patch persistence.AssetPatch) error {
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

// PatchAssetTx applies the typed partial-update contract inside the
// caller-owned transaction. A nil pointer field means "leave unchanged";
// the SQL is assembled dynamically but every column lives inside this
// canonical writer family — no producer may patch media_assets directly.
func (c *PostgresMediaCommitter) PatchAssetTx(ctx context.Context, tx persistence.Transaction, patch persistence.AssetPatch) error {
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
	// PostgreSQL uses ordinal placeholders; the next placeholder number is
	// len(args)+1 at every append.
	addString := func(column string, value *string) {
		if value == nil {
			return
		}
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)+1))
		args = append(args, *value)
	}
	addFloat := func(column string, value *float64) {
		if value == nil {
			return
		}
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)+1))
		args = append(args, *value)
	}
	addInt := func(column string, value *int) {
		if value == nil {
			return
		}
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)+1))
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
		sets = append(sets, fmt.Sprintf("index_state = $%d", len(args)+1))
		args = append(args, *patch.IndexState)
		updatedAt := time.Now().UTC()
		if patch.IndexStateUpdatedAt != nil {
			updatedAt = patch.IndexStateUpdatedAt.UTC()
		}
		sets = append(sets, fmt.Sprintf("index_state_updated_at = $%d", len(args)+1))
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
		sets = append(sets, fmt.Sprintf("updated_at = $%d", len(args)+1))
		args = append(args, updatedAt)
		args = append(args, patch.AssetID)
		res, err := sqlTx.ExecContext(ctx, "UPDATE media_assets SET "+strings.Join(sets, ", ")+" WHERE id = $"+fmt.Sprintf("%d", len(args)), args...)
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
		FROM media_assets WHERE id = $1`, patch.AssetID).
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

// ── Drive location reconciliation ───────────────────────────────────────

// ReconcileDriveLocations opens a transaction and applies the Drive
// projection patches inside it.
func (c *PostgresMediaCommitter) ReconcileDriveLocations(ctx context.Context, changes []persistence.DriveLocationPatch) error {
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

// ReconcileDriveLocationsTx applies the Drive projection patches inside the
// caller-owned transaction (SQLite mirror: reconcileOneDriveLocation —
// preserve durable Drive identity, upsert the drive location, patch the
// asset, and emit a location-suffixed index request).
func (c *PostgresMediaCommitter) ReconcileDriveLocationsTx(ctx context.Context, tx persistence.Transaction, changes []persistence.DriveLocationPatch) error {
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

func (c *PostgresMediaCommitter) reconcileOneDriveLocation(ctx context.Context, tx *sql.Tx, change persistence.DriveLocationPatch) error {
	var source, mediaType, sourceVersion, contentHash, existingFileID, existingLink, lifecycle string
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(source,''), COALESCE(media_type,''), COALESCE(source_version,''),
		       COALESCE(legacy_file_md5,''), COALESCE(drive_file_id,''),
		       COALESCE(drive_link,''), COALESCE(lifecycle_state,'ACTIVE')
		FROM media_assets WHERE id = $1`, change.AssetID).
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
		FROM asset_locations WHERE asset_id = $1 AND location_kind = 'drive'`, change.AssetID).
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
		VALUES ($1, 'drive', $2, $3, $4, $5, '', 0, '', $6, $7, $8)
		ON CONFLICT (asset_id, location_kind) DO UPDATE SET
			uri=excluded.uri, external_id=excluded.external_id,
			web_view_link=excluded.web_view_link, download_url=excluded.download_url,
			updated_at=excluded.updated_at`,
		change.AssetID, uri, change.DriveFileID, change.DriveLink, change.DownloadURL,
		pgBoolInt(primary), now, now); err != nil {
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
		WHERE asset_id = $1 AND location_kind IN ('local','object_storage')
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
	err := tx.QueryRowContext(ctx, `SELECT is_primary FROM asset_locations WHERE asset_id=$1 AND location_kind='drive'`, assetID).Scan(&existing)
	if err == nil {
		return existing != 0, nil
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_locations WHERE asset_id=$1 AND is_primary=1`, assetID).Scan(&count); err != nil {
		return false, err
	}
	return count == 0, nil
}

// ── Dispatcher clip mapping (SQLite mirror: clips_transactions.go) ──────

// commitClipTxThroughCanonical maps the domain asset to the canonical
// persistence request and routes it through the injected AssetCommitter.
func commitClipTxThroughCanonical(ctx context.Context, tx *sql.Tx, clip *asset.Asset, committer persistence.AssetCommitter) error {
	if clip == nil {
		return fmt.Errorf("upsert clip: asset is required")
	}
	if tx == nil {
		return fmt.Errorf("upsert clip: transaction is required")
	}
	if committer == nil {
		return fmt.Errorf("upsert clip: canonical AssetCommitter is required")
	}
	req, err := canonicalClipCommitRequest(clip)
	if err != nil {
		return err
	}
	if _, err := committer.CommitTx(ctx, tx, req); err != nil {
		return fmt.Errorf("upsert clip %s through canonical writer: %w", clip.ID, err)
	}
	return nil
}

func canonicalClipCommitRequest(clip *asset.Asset) (persistence.CommitRequest, error) {
	if clip.SourceURL != "" && clip.MetadataSourceURL() == "" {
		clip.SetMetadataSourceURL(clip.SourceURL)
	}
	taxonomy, err := resolveClipTaxonomy(clip)
	if err != nil {
		return persistence.CommitRequest{}, err
	}
	contentHash := clip.LegacyFileMD5()
	if contentHash == "" {
		contentHash = clip.ContentHash()
	}
	filename := clip.Filename
	if filename == "" {
		filename = clip.ID + ".asset"
	}
	name := clip.Name
	if name == "" {
		name = clip.ID
	}
	mediaType := string(clip.MediaType)
	if mediaType == "" || mediaType == "clip" {
		mediaType = "video"
	}
	lifecycle := string(clip.LifecycleState)
	if lifecycle == "" {
		lifecycle = string(asset.StateActive)
	}
	metadata := persistence.TypedMetadata{
		Title: clip.Title(), Description: clip.Description(),
		SourceVersion:  clip.GetMetadataString("source_version"),
		SourceProvider: clip.MetadataSourceProvider(), SourceVideoID: clip.MetadataSourceVideoID(),
		Tags: append([]string(nil), clip.Tags...), Category: clip.Category, Extra: clip.Metadata,
	}
	if metadata.Title == "" {
		metadata.Title = name
	}
	if metadata.SourceVersion == "" {
		metadata.SourceVersion = contentHash
	}
	return persistence.CommitRequest{
		AssetID: clip.ID, Source: string(clip.Source), Name: name, Filename: filename,
		MediaType: mediaType, Category: clip.Category, GroupName: clip.Group,
		DurationMs: clip.Duration.Milliseconds(), ContentHash: contentHash,
		Description: clip.Description(), SearchText: clip.SearchText, LifecycleState: lifecycle,
		IndexState: clip.GetMetadataString("index_state"), LocalPath: clip.LocalPath(),
		FolderID: clip.FolderID(), FolderPath: clip.FolderPath(), ThumbnailURL: clip.ThumbnailURL,
		SourceURL: clip.SourceURL, Title: metadata.Title,
		SourceProvider: clip.MetadataSourceProvider(), SourceVideoID: clip.MetadataSourceVideoID(),
		StartMs: int64(asset.MetadataFloat(clip.Metadata, "start_sec") * 1000),
		EndMs:   int64(asset.MetadataFloat(clip.Metadata, "end_sec") * 1000), Metadata: metadata,
		Taxonomy: taxonomy, Locations: clipLocationsForCanonicalCommit(clip, contentHash),
		EmitIndexEvent: false,
	}, nil
}

func clipLocationsForCanonicalCommit(clip *asset.Asset, contentHash string) []persistence.LocationCommit {
	locations := make([]persistence.LocationCommit, 0, 2)
	if localPath := clip.LocalPath(); localPath != "" {
		locations = append(locations, persistence.LocationCommit{
			Kind: "local", Provider: "local", URI: localPath,
			MimeType: string(clip.MediaType), LegacyFileMD5: contentHash,
		})
	}
	if fileID, link := clip.DriveFileID(), clip.DriveLink(); fileID != "" || link != "" {
		uri := link
		if fileID != "" {
			uri = "drive://" + fileID
		}
		locations = append(locations, persistence.LocationCommit{
			Kind: "drive", Provider: "drive", ExternalID: fileID,
			URI: uri, WebViewLink: link, DownloadURL: clip.DownloadLink(),
			MimeType: string(clip.MediaType), LegacyFileMD5: contentHash,
			IsPrimary: true,
		})
	}
	return locations
}

func resolveClipTaxonomy(clip *asset.Asset) (capregistry.AssetTaxonomy, error) {
	mediaType := capregistry.MediaType(string(clip.MediaType))
	if mediaType == "" || mediaType == "clip" {
		mediaType = capregistry.MediaVideo
		clip.MediaType = asset.MediaType(mediaType)
	}
	kind := capregistry.AssetKind(asset.MetadataString(clip.Metadata, "asset_kind"))
	role := asset.MetadataString(clip.Metadata, "semantic_role")
	taxonomy, err := capregistry.ResolveTaxonomy(capregistry.TaxonomyInput{
		AssetID: clip.ID, Provider: string(clip.Source), MediaType: mediaType,
		AssetKind: kind, SemanticRole: role,
	})
	if err != nil {
		return capregistry.AssetTaxonomy{}, fmt.Errorf("upsert clip %s: resolve taxonomy: %w", clip.ID, err)
	}
	return taxonomy, nil
}
