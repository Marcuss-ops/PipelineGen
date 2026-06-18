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
// PR1: Repository always delegates canonical CRUD (Upsert/Get/List/
// SoftDelete/Restore/HardDelete) to an embedded assetrepo.Repository,
// which is auto-created by NewRepository. Callers that previously wired
// a separate canonical via NewRepositoryCanonical get graceful overlap
// (the explicitly-passed canonical is used). Custom queries (SearchClips,
// folders, dedup, scoring) remain on this type.
type Repository struct {
	db        *sql.DB
	log       *zap.Logger
	canonical *assetrepo.Repository
}

// NewRepository creates a Repository backed by db, with an auto-created
// canonical assetrepo.Repository. All callers get canonical delegation
// for free — no migration needed.
func NewRepository(db *sql.DB, log *zap.Logger) *Repository {
	r := &Repository{
		db:        db,
		log:       log,
		canonical: assetrepo.New(db, log),
	}
	if log != nil {
		log.Info("clips repository: canonical assetrepo auto-wired (PR1)")
	}
	return r
}

// NewRepositoryCanonical wraps an existing canonical assetrepo.Repository.
// For backward compatibility with callers that previously created the
// canonical externally (e.g., compose_core.go). When nil, auto-creates one.
func NewRepositoryCanonical(db *sql.DB, log *zap.Logger, canonical *assetrepo.Repository) *Repository {
	if canonical == nil {
		canonical = assetrepo.New(db, log)
	}
	r := &Repository{db: db, log: log, canonical: canonical}
	if log != nil {
		log.Info("clips repository: canonical lifecycle_state column in use",
			zap.Bool("canonical_wired", true))
	}
	return r
}

// ── asset.Repository interface (PR1: always delegated to canonical) ────

func (r *Repository) Upsert(ctx context.Context, m *asset.MediaAsset) error {
	return r.canonical.Upsert(ctx, m)
}

func (r *Repository) Get(ctx context.Context, id string) (*asset.MediaAsset, error) {
	return r.canonical.Get(ctx, id)
}

func (r *Repository) List(ctx context.Context, filter asset.Filter) ([]*asset.MediaAsset, error) {
	return r.canonical.List(ctx, filter)
}

func (r *Repository) Count(ctx context.Context, filter asset.Filter) (int64, error) {
	return r.canonical.Count(ctx, filter)
}

func (r *Repository) SoftDelete(ctx context.Context, id string) error {
	return r.canonical.SoftDelete(ctx, id)
}

func (r *Repository) Restore(ctx context.Context, id string) error {
	return r.canonical.Restore(ctx, id)
}

func (r *Repository) HardDelete(ctx context.Context, id string) error {
	return r.canonical.HardDelete(ctx, id)
}

// Canonical returns the embedded assetrepo.Repository for callers that
// need direct access (e.g., WithTx, UpsertTx).
func (r *Repository) Canonical() *assetrepo.Repository {
	return r.canonical
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

// UpsertClip inserts or updates a media asset. PR1: always delegates to
// the canonical assetrepo.Repository. Same semantics as before.
func (r *Repository) UpsertClip(ctx context.Context, clip *asset.MediaAsset) error {
	return r.canonical.Upsert(ctx, clip)
}

// GetByDriveFileID is a legacy alias for GetClipByDriveFileID.
func (r *Repository) GetByDriveFileID(ctx context.Context, fileID string) (*asset.MediaAsset, error) {
	return r.GetClipByDriveFileID(ctx, fileID)
}

// GetClipFolderByVideoID is a legacy alias for GetFolderByVideoID.
func (r *Repository) GetClipFolderByVideoID(ctx context.Context, videoID string) (*models.ClipFolder, error) {
	return r.GetFolderByVideoID(ctx, videoID)
}



// UpsertClipTx is the tx-aware variant of UpsertClip. PR1: delegates to
// the canonical assetrepo.Repository.UpsertTx. The caller owns the
// transaction lifecycle and outbox emission.
func (r *Repository) UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *asset.MediaAsset) error {
	return r.canonical.UpsertTx(ctx, tx, clip)
}

// DeleteClip soft-deletes a clip by its ID. PR1: delegates to canonical.
func (r *Repository) DeleteClip(ctx context.Context, id string) error {
	return r.canonical.SoftDelete(ctx, id)
}

// RestoreClip restores a soft-deleted clip. PR1: delegates to canonical.
func (r *Repository) RestoreClip(ctx context.Context, id string) error {
	return r.canonical.Restore(ctx, id)
}

// HardDeleteClip permanently deletes a clip. PR1: delegates to canonical.
func (r *Repository) HardDeleteClip(ctx context.Context, id string) error {
	return r.canonical.HardDelete(ctx, id)
}

// DeleteClipByDriveLink deletes a clip by its drive_link or download_link.
func (r *Repository) DeleteClipByDriveLink(ctx context.Context, driveLink string) error {
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
