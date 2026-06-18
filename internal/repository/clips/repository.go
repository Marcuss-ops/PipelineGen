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
	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assetrepo"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// mediaAssetColumns defines the columns selected from media_assets table.
const mediaAssetColumns = `
	id,
	COALESCE(source, '') AS source,
	COALESCE(name, '') AS name,
	COALESCE(tags, '[]') AS tags,
	COALESCE(tags_norm, '') AS tags_norm,
	COALESCE(embedding_json, '[]') AS embedding_json,
	COALESCE(duration_ms, 0) AS duration_ms,
	COALESCE(url, '') AS url,
	COALESCE(media_type, '') AS media_type,
	COALESCE(status, '') AS status,
	COALESCE(local_path, '') AS local_path,
	COALESCE(relative_path, '') AS relative_path,
	COALESCE(drive_file_id, '') AS drive_file_id,
	COALESCE(folder_id, '') AS drive_folder_id,
	COALESCE(drive_link, '') AS drive_link,
	COALESCE(download_link, '') AS download_link,
	COALESCE(file_hash, '') AS file_hash,
	COALESCE(metadata_json, '{}') AS metadata_json,
	COALESCE(visual_embedding, '[]') AS visual_embedding,
	COALESCE(transcript_embedding, '[]') AS transcript_embedding,
	created_at,
	COALESCE(updated_at, '') AS updated_at,
	COALESCE(width, 0) AS width,
	COALESCE(height, 0) AS height,
	COALESCE(lifecycle_state, 'ready') AS lifecycle_state,
	COALESCE(deleted_at, '') AS deleted_at,
	COALESCE(folder_id, '') AS folder_id,
	COALESCE(parent_folder_id, '') AS parent_folder_id,
	COALESCE(folder_path, '') AS folder_path,
	COALESCE(category, '') AS category,
	COALESCE(filename, '') AS filename,
	COALESCE(error, '') AS error,
	COALESCE(thumb_url, '') AS thumb_url,
	COALESCE(phash, '') AS phash,
	COALESCE(search_text, '') AS search_text,
	COALESCE(scene_type, '') AS scene_type,
	COALESCE(quality_score, 0.0) AS quality_score,
	COALESCE(reuse_count, 0) AS reuse_count,
	COALESCE(last_used_at, '') AS last_used_at`

const (
	clipFolderColumns = `id, source, COALESCE(source_url, '') AS source_url, COALESCE(video_id, '') AS video_id, COALESCE(folder_id, '') AS folder_id, COALESCE(folder_path, '') AS folder_path, COALESCE(local_folder_path, '') AS local_folder_path, COALESCE(group_name, '') AS group_name, COALESCE(manifest_txt_path, '') AS manifest_txt_path, COALESCE(manifest_json_path, '') AS manifest_json_path, clip_count, processed_count, failed_count, skipped_count, COALESCE(last_error, '') AS last_error, COALESCE(metadata, '{}') AS metadata, created_at, updated_at`
)

func buildClipFolderQuery(source string) string {
	query := "SELECT " + clipFolderColumns + " FROM clip_folders"
	if source != "" && source != "all" && source != "unified" {
		query += " WHERE source = ?"
	}
	return query
}

func (r *Repository) buildMediaAssetQuery(source string) string {
	query := "SELECT " + mediaAssetColumns + " FROM media_assets WHERE " + r.SoftDeleteFilter()
	if source != "" && source != "all" && source != "unified" {
		query += " AND source = ?"
	}
	return query
}

// Repository handles persistence for clips.
//
// PR1: Repository optionally satisfies asset.Repository by delegating to a
// canonical assetrepo.Repository. When canonical is non-nil, the
// asset.Repository methods delegate to it.
type Repository struct {
	db  *sql.DB
	log *zap.Logger
	canonical *assetrepo.Repository
}

func NewRepository(db *sql.DB, log *zap.Logger) *Repository {
	r := &Repository{db: db, log: log}
	if log != nil {
		log.Info("clips repository: canonical lifecycle_state column in use")
	}
	return r
}

func NewRepositoryCanonical(db *sql.DB, log *zap.Logger, canonical *assetrepo.Repository) *Repository {
	if canonical == nil {
		panic("clips.NewRepositoryCanonical: canonical assetrepo.Repository is required")
	}
	r := &Repository{db: db, log: log, canonical: canonical}
	if log != nil {
		log.Info("clips repository: canonical lifecycle_state column in use",
			zap.Bool("canonical_wired", true))
	}
	return r
}

// ── asset.Repository interface (PR1: canonical delegation) ──────────────

func (r *Repository) Upsert(ctx context.Context, clip *asset.MediaAsset) error {
	if r.canonical != nil {
		return r.canonical.Upsert(ctx, clip)
	}
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
	if err := r.UpsertTx(ctx, tx, clip); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			r.log.Warn("rollback failed", zap.Error(rbErr))
		}
		return err
	}
	return tx.Commit()
}

func (r *Repository) Get(ctx context.Context, id string) (*asset.MediaAsset, error) {
	if r.canonical != nil {
		return r.canonical.Get(ctx, id)
	}
	query := r.buildMediaAssetQuery("") + " AND id = ? LIMIT 1"
	row := r.db.QueryRowContext(ctx, query, id)
	return r.scanCanonicalAssetRow(row)
}

func (r *Repository) List(ctx context.Context, filter asset.Filter) ([]*asset.MediaAsset, error) {
	if r.canonical != nil {
		return r.canonical.List(ctx, filter)
	}
	return nil, fmt.Errorf("clips.Repository.List: canonical repo not wired")
}

func (r *Repository) Count(ctx context.Context, filter asset.Filter) (int64, error) {
	if r.canonical != nil {
		return r.canonical.Count(ctx, filter)
	}
	return 0, fmt.Errorf("clips.Repository.Count: canonical repo not wired")
}

func (r *Repository) SoftDelete(ctx context.Context, id string) error {
	if r.canonical != nil {
		return r.canonical.SoftDelete(ctx, id)
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("clip id is required")
	}
	now := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx,
		`UPDATE media_assets SET lifecycle_state = 'deleted', deleted_at = ? WHERE id = ?`, now, id)
	return err
}

func (r *Repository) Restore(ctx context.Context, id string) error {
	if r.canonical != nil {
		return r.canonical.Restore(ctx, id)
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("clip id is required")
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE media_assets SET lifecycle_state = 'ready', deleted_at = '' WHERE id = ?`, id)
	return err
}

func (r *Repository) HardDelete(ctx context.Context, id string) error {
	if r.canonical != nil {
		return r.canonical.HardDelete(ctx, id)
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("clip id is required")
	}
	_, err := r.db.ExecContext(ctx, "DELETE FROM media_assets WHERE id = ?", id)
	return err
}

// ── Legacy method aliases (PR2: bridge for callers not yet migrated) ─────

// GetClip is a legacy alias for Get.
func (r *Repository) GetClip(ctx context.Context, id string) (*asset.MediaAsset, error) {
	return r.Get(ctx, id)
}

// UpsertClip is a legacy alias for Upsert.
func (r *Repository) UpsertClip(ctx context.Context, clip *asset.MediaAsset) error {
	return r.Upsert(ctx, clip)
}

// DeleteClip is a legacy alias for SoftDelete.
func (r *Repository) DeleteClip(ctx context.Context, id string) error {
	return r.SoftDelete(ctx, id)
}

// HardDeleteClip is a legacy alias for HardDelete.
func (r *Repository) HardDeleteClip(ctx context.Context, id string) error {
	return r.HardDelete(ctx, id)
}

// RestoreClip is a legacy alias for Restore.
func (r *Repository) RestoreClip(ctx context.Context, id string) error {
	return r.Restore(ctx, id)
}

// DeleteClipByDriveLink is a legacy alias for DeleteByDriveLink.
func (r *Repository) DeleteClipByDriveLink(ctx context.Context, driveLink string) error {
	return r.DeleteByDriveLink(ctx, driveLink)
}

// UpsertClipTx is a legacy alias for UpsertTx.
func (r *Repository) UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *asset.MediaAsset) error {
	return r.UpsertTx(ctx, tx, clip)
}

// GetClipByDriveFileID fetches a clip by its Google Drive file ID.
func (r *Repository) GetClipByDriveFileID(ctx context.Context, driveFileID string) (*asset.MediaAsset, error) {
	query := r.buildMediaAssetQuery("") + " AND drive_file_id = ? LIMIT 1"
	row := r.db.QueryRowContext(ctx, query, driveFileID)
	return r.scanCanonicalAssetRow(row)
}

// ── Legacy folder method aliases ─────────────────────────────────

// UpsertClipFolder is a legacy alias for UpsertFolder.
func (r *Repository) UpsertClipFolder(ctx context.Context, folder *models.ClipFolder) error {
	return r.UpsertFolder(ctx, folder)
}

// ListClipFolders is a legacy alias for ListFolders.
func (r *Repository) ListClipFolders(ctx context.Context, source string) ([]*models.ClipFolder, error) {
	return r.ListFolders(ctx, source)
}

// DeleteClipFolder is a legacy alias for DeleteFolder.
func (r *Repository) DeleteClipFolder(ctx context.Context, id string) error {
	return r.DeleteFolder(ctx, id)
}

// GetClipFolder is a legacy alias for GetFolder.
func (r *Repository) GetClipFolder(ctx context.Context, id string) (*models.ClipFolder, error) {
	return r.GetFolder(ctx, id)
}

// GetClipFolderByVideoID is a legacy alias for GetFolderByVideoID.
func (r *Repository) GetClipFolderByVideoID(ctx context.Context, videoID string) (*models.ClipFolder, error) {
	return r.GetFolderByVideoID(ctx, videoID)
}

// ── Legacy write methods (PR2: converted to canonical *asset.MediaAsset) ─

// SoftDeleteFilter returns the SQL WHERE fragment that excludes soft-deleted clips.
func (r *Repository) SoftDeleteFilter() string {
	return "lifecycle_state != 'deleted'"
}

func (r *Repository) Log() *zap.Logger { return r.log }
func (r *Repository) DB() *sql.DB      { return r.db }

func (r *Repository) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return r.db.BeginTx(ctx, opts)
}

// UpsertTx is the tx-aware variant of Upsert. Now accepts *asset.MediaAsset.
func (r *Repository) UpsertTx(ctx context.Context, tx *sql.Tx, clip *asset.MediaAsset) error {
	tagsJSON, err := json.Marshal(clip.Tags)
	if err != nil {
		return fmt.Errorf("failed to marshal tags: %w", err)
	}
	metadataJSON := clip.MetadataJSON()
	tagsNorm := normalizeTags(clip.Tags)
	nowStr := timeutil.FormatRFC3339(time.Now())

	lifecycle := string(clip.LifecycleState)
	if lifecycle == "" {
		lifecycle = "ready"
	}
	deletedAtStr := ""
	if clip.DeletedAt != nil {
		deletedAtStr = clip.DeletedAt.UTC().Format(time.RFC3339)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO media_assets (id, source, name, tags, tags_norm, duration_ms, url, media_type, status, local_path, relative_path, drive_file_id, drive_folder_id, drive_link, download_link, file_hash, embedding_json, metadata_json, visual_embedding, transcript_embedding, lifecycle_state, deleted_at, folder_id, parent_folder_id, folder_path, category, filename, error, thumb_url, phash, search_text, scene_type, quality_score, reuse_count, last_used_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			lifecycle_state=excluded.lifecycle_state,
			deleted_at=excluded.deleted_at,
			folder_id=excluded.folder_id,
			parent_folder_id=excluded.parent_folder_id,
			folder_path=excluded.folder_path,
			category=excluded.category,
			filename=excluded.filename,
			error=excluded.error,
			thumb_url=excluded.thumb_url,
			phash=excluded.phash,
			search_text=excluded.search_text,
			scene_type=excluded.scene_type,
			quality_score=excluded.quality_score,
			reuse_count=excluded.reuse_count,
			last_used_at=excluded.last_used_at,
			updated_at=excluded.updated_at
	`, clip.ID, clip.Source, clip.Name, string(tagsJSON), tagsNorm,
		clip.DurationMs, clip.SourceURL,
		clip.MediaType, "", clip.LocalPath, clip.LocalPath,
		clip.DriveFileID, clip.FolderID, clip.DriveLink, clip.DownloadLink,
		clip.FileHash, clip.EmbeddingJSON,
		metadataJSON, clip.VisualEmbedding, clip.TranscriptEmbedding,
		lifecycle, deletedAtStr, clip.FolderID, clip.ParentFolderID,
		clip.FolderPath, clip.Category, clip.Filename, "",
		clip.ThumbnailURL, clip.PHash, clip.SearchText, clip.SceneType,
		clip.QualityScore, clip.ReuseCount, clip.LastUsedAt,
		nowStr, nowStr)

	return err
}

// DeleteByDriveLink deletes a clip by its drive_link or download_link.
func (r *Repository) DeleteByDriveLink(ctx context.Context, driveLink string) error {
	driveLink = strings.TrimSpace(driveLink)
	if driveLink == "" {
		return fmt.Errorf("drive link is required")
	}
	now := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx,
		`UPDATE media_assets SET lifecycle_state = 'deleted', deleted_at = ? WHERE drive_link = ? OR download_link = ?`,
		now, driveLink, driveLink)
	return err
}

// CountAll returns the total row count excluding soft-deleted.
func (r *Repository) CountAll(ctx context.Context) (int64, error) {
	if r.db == nil {
		return 0, fmt.Errorf("clips.Repository: db is nil")
	}
	var n int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM media_assets WHERE "+r.SoftDeleteFilter()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("clips.CountAll: %w", err)
	}
	return n, nil
}

// CountIndexed returns count of rows with populated embedding_json.
func (r *Repository) CountIndexed(ctx context.Context) (int64, error) {
	if r.db == nil {
		return 0, fmt.Errorf("clips.Repository: db is nil")
	}
	var n int64
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets WHERE `+r.SoftDeleteFilter()+`
		   AND embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]'`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("clips.CountIndexed: %w", err)
	}
	return n, nil
}

// ListIndexedIDs returns up to limit asset IDs with populated embedding_json.
func (r *Repository) ListIndexedIDs(ctx context.Context, limit int) ([]string, error) {
	if r.db == nil {
		return nil, fmt.Errorf("clips.Repository: db is nil")
	}
	if limit <= 0 {
		return []string{}, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id FROM media_assets WHERE `+r.SoftDeleteFilter()+`
		   AND embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]' LIMIT ?`, limit)
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
	return out, rows.Err()
}

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
