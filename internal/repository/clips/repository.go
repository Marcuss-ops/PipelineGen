// Package clips provides the repository for media assets (media_assets table).
//
// This repository manages:
//   - Video clips and their metadata
//   - Clip folders for organization
//   - Segment embeddings for timeline generation
//
// Database: clips.db.sqlite / artlist.db.sqlite / stock.db.sqlite
// Table: media_assets (unified schema with metadata_json for flexible fields)
//
// Note: Stock and Artlist clips use separate databases (stock.db, artlist.db)
// but share the same Repository structure with different instances.
package clips

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/media/models"
	"go.uber.org/zap"
)

// mediaAssetColumns defines the columns selected from media_assets table.
// Extended fields are stored in metadata_json and parsed into Metadata map.
const (
	mediaAssetColumns = `id, COALESCE(source, '') AS source, COALESCE(name, '') AS name, COALESCE(tags, '[]') AS tags, COALESCE(embedding_json, '[]') AS embedding_json, COALESCE(duration_ms, 0) AS duration_ms, COALESCE(url, '') AS url, created_at, COALESCE(metadata_json, '{}') AS metadata_json`
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
// excluding deleted clips (those with '$.deleted_at' in metadata_json).
func buildMediaAssetQuery(source string) string {
	query := "SELECT " + mediaAssetColumns + " FROM media_assets WHERE json_extract(COALESCE(metadata_json,'{}'), '$.deleted_at') IS NULL"
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
}

// UpsertClip inserts or updates a media asset (media_assets table).
// Extended fields are stored in metadata_json as a JSON map.
func (r *Repository) UpsertClip(ctx context.Context, clip *models.MediaAsset) error {
	tagsJSON, err := json.Marshal(clip.Tags)
	if err != nil {
		return fmt.Errorf("failed to marshal tags: %w", err)
	}
	now := time.Now()

	// Store extended fields in Metadata map
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
	// Embedding is stored directly, also save to metadata for consistency
	if clip.EmbeddingJSON != "" {
		clip.SetMetadataString("embedding_json", clip.EmbeddingJSON)
	}

	// Serialize Metadata map to JSON for the metadata_json column
	metadataJSON := clip.MetadataJSON()

	// Normalizza tags per ricerca full-text
	tagsNorm := normalizeTags(clip.Tags)

	nowStr := now.Format(time.RFC3339)
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO media_assets (id, source, name, tags, tags_norm, duration_ms, url, media_type, status, local_path, relative_path, drive_file_id, drive_link, download_link, file_hash, embedding_json, metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			drive_link=excluded.drive_link,
			download_link=excluded.download_link,
			file_hash=excluded.file_hash,
			embedding_json=excluded.embedding_json,
			metadata_json=excluded.metadata_json,
			updated_at=excluded.updated_at
		`, clip.ID, clip.Source, clip.Name, string(tagsJSON), tagsNorm,
		clip.Duration, clip.ExternalURL,
		clip.MediaType, clip.Status, clip.LocalPath, clip.LocalPath,
		clip.DriveFileID, clip.DriveLink, clip.DownloadLink,
		clip.FileHash, clip.EmbeddingJSON,
		metadataJSON, nowStr, nowStr)

	return err
}

// DeleteClip soft-deletes a clip by its ID.
func (r *Repository) DeleteClip(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("clip id is required")
	}

	_, err := r.db.ExecContext(ctx, "UPDATE media_assets SET metadata_json = json_set(COALESCE(metadata_json,'{}'), '$.deleted_at', ?) WHERE id = ?", time.Now().Format(time.RFC3339), id)
	return err
}

// RestoreClip restores a soft-deleted clip by its ID.
func (r *Repository) RestoreClip(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("clip id is required")
	}

	_, err := r.db.ExecContext(ctx, "UPDATE media_assets SET metadata_json = json_remove(COALESCE(metadata_json,'{}'), '$.deleted_at') WHERE id = ?", id)
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

// DeleteClipByDriveLink deletes a clip by its Drive link (stored in metadata_json).
func (r *Repository) DeleteClipByDriveLink(ctx context.Context, driveLink string) error {
	driveLink = strings.TrimSpace(driveLink)
	if driveLink == "" {
		return fmt.Errorf("drive link is required")
	}

	now := time.Now().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx, "UPDATE media_assets SET metadata_json = json_set(COALESCE(metadata_json,'{}'), '$.deleted_at', ?) WHERE json_extract(metadata_json, '$.drive_link') = ? OR json_extract(metadata_json, '$.download_link') = ?", now, driveLink, driveLink)
	return err
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