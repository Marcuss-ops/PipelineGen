// Package clips provides the repository for media assets (media_assets table).
//
// This repository manages:
//   - Video clips and their metadata
//   - Clip folders for organization
//   - Segment embeddings for timeline generation
//
// Database: media.db.sqlite (unified single database)
// Table: media_assets (unified schema with metadata_json for flexible fields)
//
// Note: All sources (youtube, artlist, stock) share the same unified database
// (media.db.sqlite) and are differentiated by the `source` column.
// Different Repository instances may be created for ergonomic source-filtering
// but they all wrap the same underlying DB.
package clips

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// mediaAssetColumns defines the columns selected from media_assets table.
// Extended fields are stored in metadata_json and parsed into Metadata map.
const (
	mediaAssetColumns = `id, COALESCE(source, '') AS source, COALESCE(name, '') AS name, COALESCE(tags, '[]') AS tags, COALESCE(embedding_json, '[]') AS embedding_json, COALESCE(duration_ms, 0) AS duration_ms, COALESCE(url, '') AS url, created_at, COALESCE(metadata_json, '{}') AS metadata_json, COALESCE(drive_folder_id, '') AS drive_folder_id, COALESCE(visual_embedding, '[]') AS visual_embedding, COALESCE(transcript_embedding, '[]') AS transcript_embedding`
	clipFolderColumns = `id, source, COALESCE(source_url, '') AS source_url, COALESCE(video_id, '') AS video_id, COALESCE(folder_id, '') AS folder_id, COALESCE(folder_path, '') AS folder_path, COALESCE(local_folder_path, '') AS local_folder_path, COALESCE(group_name, '') AS group_name, COALESCE(manifest_txt_path, '') AS manifest_txt_path, COALESCE(manifest_json_path, '') AS manifest_json_path, clip_count, processed_count, failed_count, skipped_count, COALESCE(last_error, '') AS last_error, COALESCE(metadata, '{}') AS metadata, created_at, updated_at`
)

// buildClipFolderQuery builds a SELECT query for clip_folders
func buildClipFolderQuery(source string) string {
	query := "SELECT " + clipFolderColumns + " FROM clip_folders"
	if source != "" && source != "all" && source != "unified" {
		query += " WHERE source = ?"
	}
	return query
}

// buildMediaAssetQuery builds a SELECT query using the media_assets table,
// excluding soft-deleted clips via the canonical lifecycle_state column.
func buildMediaAssetQuery(source string) string {
	query := "SELECT " + mediaAssetColumns + " FROM media_assets WHERE lifecycle_state != 'deleted'"
	if source != "" && source != "all" && source != "unified" {
		query += " AND source = ?"
	}
	return query
}

// Repository handles persistence for clips
type Repository struct {
	db  *sql.DB
	log *zap.Logger
}

// NewRepository creates a new clips repository
func NewRepository(db *sql.DB, log *zap.Logger) *Repository {
	return &Repository{db: db, log: log}
}

// Log returns the repository's logger
func (r *Repository) Log() *zap.Logger {
	return r.log
}

// DB returns the underlying database connection
func (r *Repository) DB() *sql.DB {
	return r.db
}

// BeginTx starts a new transaction
func (r *Repository) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return r.db.BeginTx(ctx, opts)
} // UpsertClip inserts or updates a media asset (media_assets table).
// Extended fields are stored in metadata_json as a JSON map.
//
// This is a thin wrapper around UpsertClipTx that opens its own transaction
// for atomicity. Callers that are already in a transaction (e.g. composing
// the upsert with a media_index_outbox INSERT under the outbox Dispatcher)
// should call UpsertClipTx directly to reuse the caller's tx.
func (r *Repository) UpsertClip(ctx context.Context, clip *models.MediaAsset) error {
	r.populateAssetMetadata(clip)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := r.UpsertClipTx(ctx, tx, clip); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			r.log.Warn("rollback failed", zap.Error(rbErr))
		}
		return err
	}
	return tx.Commit()
}

// UpsertClipTx is the tx-aware variant of UpsertClip. Use this when the
// caller is already inside a *sql.Tx and the upsert must be atomic with
// other writes (e.g. media_index_outbox enqueue by outbox.Dispatcher).
//
// MUST keep SQL, metadata serialization, and tag normalization in lockstep
// with UpsertClip — the two diverge by call surface only.
func (r *Repository) UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *models.MediaAsset) error {
	r.populateAssetMetadata(clip)
	tagsJSON, err := json.Marshal(clip.Tags)
	if err != nil {
		return fmt.Errorf("failed to marshal tags: %w", err)
	}
	metadataJSON := clip.MetadataJSON()
	tagsNorm := normalizeTags(clip.Tags)
	nowStr := timeutil.FormatRFC3339(time.Now())

	_, err = tx.ExecContext(ctx, `
		INSERT INTO media_assets (id, source, name, tags, tags_norm, duration_ms, url, media_type, status, local_path, relative_path, drive_file_id, drive_folder_id, drive_link, download_link, file_hash, embedding_json, metadata_json, visual_embedding, transcript_embedding, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source=excluded.source,
			name=excluded.name,
			tags=excluded.tags,
			tags_norm=excluded.tags_norm,
			duration_ms=excluded.duration_ms,
			url=excluded.url,
			media_type=excluded.media_type,
			status=excluded.status,
			local_path=excluded.local_path,
			relative_path=excluded.relative_path,
			drive_file_id=excluded.drive_file_id,
			drive_folder_id=excluded.drive_folder_id,
			drive_link=excluded.drive_link,
			download_link=excluded.download_link,
			file_hash=excluded.file_hash,
			embedding_json=excluded.embedding_json,
			metadata_json=excluded.metadata_json,
			visual_embedding=excluded.visual_embedding,
			transcript_embedding=excluded.transcript_embedding,
			updated_at=excluded.updated_at
	`, clip.ID, clip.Source, clip.Name, string(tagsJSON), tagsNorm,
		clip.Duration, clip.ExternalURL,
		clip.MediaType, clip.Status, clip.LocalPath, clip.LocalPath,
		clip.DriveFileID, clip.FolderID, clip.DriveLink, clip.DownloadLink,
		clip.FileHash, clip.EmbeddingJSON,
		metadataJSON, clip.VisualEmbedding, clip.TranscriptEmbedding, nowStr, nowStr)

	return err
}

// populateAssetMetadata moves extended fields from the typed MediaAsset
// columns into the Metadata map (serialized to metadata_json). Kept as a
// separate helper so UpsertClip and UpsertClipTx stay byte-for-byte
// equivalent on column writes. Returns nothing (no fallible work).
//
// DEPRECATED (Blocco 3, June 2026): This function duplicates canonical
// typed columns (local_path, drive_file_id, drive_link, download_link,
// file_hash, status, media_type) into metadata_json. After all readers
// have been migrated to use the canonical columns, this function should
// be removed and only non-canonical metadata (prompt, mood, subjects,
// model_parameters, provider_raw_metadata, creative_annotations) should
// be written to metadata_json.
//
// Migration plan:
//   1. Migrate all READ paths from json_extract(metadata_json, '$.<key>')
//      to the canonical typed columns.
//   2. Backfill: ensure every row has both column AND JSON key values.
//   3. Compare: verify column == json_extract(...) for all rows.
//   4. Stop writing duplicate keys (remove this function's column copies).
//   5. Remove duplicate keys from existing metadata_json records.
//   6. Delete this function.
func (r *Repository) populateAssetMetadata(clip *models.MediaAsset) {
	if clip.FolderID != "" {
		clip.SetMetadataString("folder_id", clip.FolderID)
	}
	if clip.DriveLink != "" {
		clip.SetMetadataString("drive_link", clip.DriveLink)
	}
	if clip.DownloadLink != "" {
		clip.SetMetadataString("download_link", clip.DownloadLink)
	}
	if clip.DriveFileID != "" {
		clip.SetMetadataString("drive_file_id", clip.DriveFileID)
	}
	if clip.FileHash != "" {
		clip.SetMetadataString("file_hash", clip.FileHash)
	}
	if clip.LocalPath != "" {
		clip.SetMetadataString("local_path", clip.LocalPath)
	}
	if clip.Status != "" {
		clip.SetMetadataString("status", clip.Status)
	}
	if clip.MediaType != "" {
		clip.SetMetadataString("media_type", clip.MediaType)
	}
	if clip.Group != "" {
		clip.SetMetadataString("group_name", clip.Group)
	}
	if clip.Category != "" {
		clip.SetMetadataString("category", clip.Category)
	}
	if clip.Filename != "" {
		clip.SetMetadataString("filename", clip.Filename)
	}
	if clip.ParentFolderID != "" {
		clip.SetMetadataString("parent_folder_id", clip.ParentFolderID)
	}
	if clip.FolderPath != "" {
		clip.SetMetadataString("folder_path", clip.FolderPath)
	}
	if clip.Error != "" {
		clip.SetMetadataString("error", clip.Error)
	}
	if clip.ThumbURL != "" {
		clip.SetMetadataString("thumb_url", clip.ThumbURL)
	}
	if clip.PHash != "" {
		clip.SetMetadataString("phash", clip.PHash)
	}
	if clip.VisualEmbeddingJSON != "" {
		clip.SetMetadataString("visual_embedding_json", clip.VisualEmbeddingJSON)
	}
	if clip.SearchText != "" {
		clip.SetMetadataString("search_text", clip.SearchText)
	}
	if clip.SceneType != "" {
		clip.SetMetadataString("scene_type", clip.SceneType)
	}
	if clip.QualityScore != 0 {
		clip.SetMetadataString("quality_score", fmt.Sprintf("%f", clip.QualityScore))
	}
	if clip.ReuseCount != 0 {
		clip.SetMetadataString("reuse_count", fmt.Sprintf("%d", clip.ReuseCount))
	}
	if clip.LastUsedAt != "" {
		clip.SetMetadataString("last_used_at", clip.LastUsedAt)
	}
	if len(clip.UsableFor) > 0 {
		b, _ := json.Marshal(clip.UsableFor)
		clip.SetMetadataString("usable_for", string(b))
	}
	if len(clip.AvoidFor) > 0 {
		b, _ := json.Marshal(clip.AvoidFor)
		clip.SetMetadataString("avoid_for", string(b))
	}
	if clip.EmbeddingJSON != "" {
		clip.SetMetadataString("embedding_json", clip.EmbeddingJSON)
	}
}

// DeleteClip soft-deletes a clip by its ID.
// Uses both lifecycle_state + deleted_at columns (canonical) and
// metadata_json.deleted_at (legacy, for backward compatibility during
// dual-read migration phase). After Blocco 3 migration 037 completes,
// the metadata_json write can be removed.
func (r *Repository) DeleteClip(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("clip id is required")
	}
	now := timeutil.FormatRFC3339(time.Now())

	_, err := r.db.ExecContext(ctx,
		`UPDATE media_assets
		 SET lifecycle_state = 'deleted',
		     deleted_at = ?,
		     metadata_json = json_set(COALESCE(metadata_json,'{}'), '$.deleted_at', ?)
		 WHERE id = ?`, now, now, id)
	return err
}

// RestoreClip restores a soft-deleted clip by its ID.
// Uses both lifecycle_state + deleted_at columns (canonical) and
// metadata_json.deleted_at (legacy compat).
func (r *Repository) RestoreClip(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("clip id is required")
	}

	_, err := r.db.ExecContext(ctx,
		`UPDATE media_assets
		 SET lifecycle_state = 'ready',
		     deleted_at = '',
		     metadata_json = json_remove(COALESCE(metadata_json,'{}'), '$.deleted_at')
		 WHERE id = ?`, id)
	return err
}

// HardDeleteClip permanently deletes a clip by its ID.
func (r *Repository) HardDeleteClip(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("clip id is required")
	}

	_, err := r.db.ExecContext(ctx, "DELETE FROM media_assets WHERE id = ?", id)
	return err
}

// DeleteClipByDriveLink deletes a clip by its canonical drive_link or
// download_link column.
func (r *Repository) DeleteClipByDriveLink(ctx context.Context, driveLink string) error {
	driveLink = strings.TrimSpace(driveLink)
	if driveLink == "" {
		return fmt.Errorf("drive link is required")
	}

	now := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx,
		`UPDATE media_assets
		 SET lifecycle_state = 'deleted',
		     deleted_at = ?
		 WHERE drive_link = ?
		    OR download_link = ?`,
		now, driveLink, driveLink)
	return err
}

// CountAll returns the total row count of media_assets (excluding soft-deleted
// rows). Used by the PR3-5b IndexHealth cross-check as the canonical SQLite
// asset count.
func (r *Repository) CountAll(ctx context.Context) (int64, error) {
	if r.db == nil {
		return 0, fmt.Errorf("clips.Repository: db is nil")
	}
	var n int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM media_assets WHERE lifecycle_state != 'deleted'",
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("clips.CountAll: %w", err)
	}
	return n, nil
}

// CountIndexed returns the count of media_assets rows whose embedding_json is
// populated and not the empty/placeholder. The predicate matches the
// /api/media/index-health handler at internal/api/handlers/sources/
// index_health_handler.go so the cross-check number matches what the HTTP
// endpoint surfaces to operators.
//
// "Indexed" means: a non-empty vector has been written by clipindexer.Service
// or the seeding scripts. Folders and metadata-only rows are NOT counted.
func (r *Repository) CountIndexed(ctx context.Context) (int64, error) {
	if r.db == nil {
		return 0, fmt.Errorf("clips.Repository: db is nil")
	}
	var n int64
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets
		 WHERE lifecycle_state != 'deleted'
		   AND embedding_json IS NOT NULL
		   AND embedding_json != ''
		   AND embedding_json != '[]'`,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("clips.CountIndexed: %w", err)
	}
	return n, nil
}

// ListIndexedIDs returns up to `limit` distinct assetIDs whose
// embedding_json is populated (same predicate as CountIndexed). The cap is
// required because PR3-5b uses it to sample the cross-check diff against
// Qdrant — a naïve SELECT id would scan the entire table for no benefit.
//
// limit <= 0 returns an empty slice (not an error). Soft-deleted rows are
// excluded. ORDER is intentionally undefined: the caller's diff is symmetric
// over the result, so picking a deterministic order is wasted work.
func (r *Repository) ListIndexedIDs(ctx context.Context, limit int) ([]string, error) {
	if r.db == nil {
		return nil, fmt.Errorf("clips.Repository: db is nil")
	}
	if limit <= 0 {
		return []string{}, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id FROM media_assets
		 WHERE lifecycle_state != 'deleted'
		   AND embedding_json IS NOT NULL
		   AND embedding_json != ''
		   AND embedding_json != '[]'
		 LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("clips.ListIndexedIDs: %w", err)
	}
	defer rows.Close()

	out := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("clips.ListIndexedIDs scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clips.ListIndexedIDs rows: %w", err)
	}
	return out, nil
}

// normalizeTags converte una lista di tag in stringa normalizzata per ricerca full-text.
func normalizeTags(tags []string) string {
	var b strings.Builder
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		low := strings.ToLower(t)
		low = strings.NewReplacer(
			"à", "a", "è", "e", "é", "e", "ì", "i", "ò", "o", "ù", "u",
		).Replace(low)
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(low)
	}
	return b.String()
}

// ListClips returns all clips, optionally filtered by source
