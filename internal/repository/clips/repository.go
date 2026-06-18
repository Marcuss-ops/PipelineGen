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
// All canonical fields are typed columns (migration 059); only true
// non-canonical data lives in metadata_json (clipindexer state, transcript
// search helpers, provider raw metadata).
const (
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
// excluding soft-deleted clips via softDeleteFilter() (which uses the
// canonical lifecycle_state column when available, falling back to
// metadata_json extraction for pre-migration databases).
func (r *Repository) buildMediaAssetQuery(source string) string {
	query := "SELECT " + mediaAssetColumns + " FROM media_assets WHERE " + r.SoftDeleteFilter()
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

// NewRepository creates a new clips repository.
// Migration 059 makes lifecycle_state a canonical column on every supported
// media.db.sqlite, so the dual-read fallback to metadata_json.deleted_at is
// no longer required.
func NewRepository(db *sql.DB, log *zap.Logger) *Repository {
	r := &Repository{db: db, log: log}
	if log != nil {
		log.Info("clips repository: canonical lifecycle_state column in use")
	}
	return r
}

// SoftDeleteFilter returns the SQL WHERE fragment that excludes soft-deleted
// clips. Always uses the canonical lifecycle_state column (migration 059).
//
// Exported so callers outside the clips package (e.g. sweepers) can compose
// ad-hoc queries that respect the same filter.
func (r *Repository) SoftDeleteFilter() string {
	return "lifecycle_state != 'deleted'"
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
// Canonical columns are written directly from the typed MediaAsset struct;
// metadata_json keeps ONLY non-canonical data (clipindexer state machine,
// transcript-derivable search helpers, prompt/mood/subjects, provider
// raw metadata, creative annotations).
//
// This is a thin wrapper around UpsertClipTx that opens its own transaction
// for atomicity. Callers that are already in a transaction (e.g. composing
// the upsert with an outbox_events INSERT under the outbox Dispatcher)
// should call UpsertClipTx directly to reuse the caller's tx.
func (r *Repository) UpsertClip(ctx context.Context, clip *models.MediaAsset) error {
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
// other writes (e.g. outbox_events enqueue by outbox.Dispatcher).
//
// MUST keep SQL, metadata serialization, and tag normalization in lockstep
// with UpsertClip — the two diverge by call surface only.
func (r *Repository) UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *models.MediaAsset) error {
	tagsJSON, err := json.Marshal(clip.Tags)
	if err != nil {
		return fmt.Errorf("failed to marshal tags: %w", err)
	}
	metadataJSON := clip.MetadataJSON()
	tagsNorm := normalizeTags(clip.Tags)
	nowStr := timeutil.FormatRFC3339(time.Now())

	lifecycle := clip.LifecycleStateOrDefault()
	qualityScore := clip.QualityScore
	reuseCount := clip.ReuseCount

	_, err = tx.ExecContext(ctx, `
		INSERT INTO media_assets (id, source, name, tags, tags_norm, duration_ms, url, media_type, status, local_path, relative_path, drive_file_id, drive_folder_id, drive_link, download_link, file_hash, embedding_json, metadata_json, visual_embedding, transcript_embedding, lifecycle_state, deleted_at, folder_id, parent_folder_id, folder_path, category, filename, error, thumb_url, phash, search_text, scene_type, quality_score, reuse_count, last_used_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		clip.Duration, clip.ExternalURL,
		clip.MediaType, clip.Status, clip.LocalPath, clip.LocalPath,
		clip.DriveFileID, clip.FolderID, clip.DriveLink, clip.DownloadLink,
		clip.FileHash, clip.EmbeddingJSON,
		metadataJSON, clip.VisualEmbedding, clip.TranscriptEmbedding,
		lifecycle, clip.DeletedAtString(), clip.FolderID, clip.ParentFolderID,
		clip.FolderPath, clip.Category, clip.Filename, clip.Error,
		clip.ThumbURL, clip.PHash, clip.SearchText, clip.SceneType,
		qualityScore, reuseCount, clip.LastUsedAt,
		nowStr, nowStr)

	return err
}

// DeleteClip soft-deletes a clip by its ID (migration 059: only the
// canonical columns are touched; metadata_json stays untouched).
func (r *Repository) DeleteClip(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("clip id is required")
	}
	now := timeutil.FormatRFC3339(time.Now())

	_, err := r.db.ExecContext(ctx,
		`UPDATE media_assets
		 SET lifecycle_state = 'deleted',
		     deleted_at = ?
		 WHERE id = ?`, now, id)
	return err
}

// RestoreClip restores a soft-deleted clip by its ID (migration 059).
func (r *Repository) RestoreClip(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("clip id is required")
	}

	_, err := r.db.ExecContext(ctx,
		`UPDATE media_assets
		 SET lifecycle_state = 'ready',
		     deleted_at = ''
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
		"SELECT COUNT(*) FROM media_assets WHERE "+r.SoftDeleteFilter(),
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
		 WHERE `+r.SoftDeleteFilter()+`
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
		 WHERE `+r.SoftDeleteFilter()+`
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
