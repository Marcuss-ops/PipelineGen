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

	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/pkg/sqlutil"
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

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO media_assets (id, source, name, tags, duration_ms, url, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source=excluded.source,
			name=excluded.name,
			tags=excluded.tags,
			duration_ms=excluded.duration_ms,
			url=excluded.url,
			metadata_json=excluded.metadata_json
		`, clip.ID, clip.Source, clip.Name, string(tagsJSON), clip.Duration, clip.ExternalURL, metadataJSON, now.Format(time.RFC3339))

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

// ListClips returns all clips, optionally filtered by source
func (r *Repository) ListClips(ctx context.Context, source string) ([]*models.MediaAsset, error) {
	query := buildMediaAssetQuery(source)
	args := []interface{}{}
	if source != "" {
		args = append(args, source)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// BulkAddTags adds a set of tags to multiple clips efficiently.
func (r *Repository) BulkAddTags(ctx context.Context, ids []string, tags []string) error {
	if len(ids) == 0 || len(tags) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, id := range ids {
		var currentTagsJSON string
		err := tx.QueryRowContext(ctx, "SELECT tags FROM media_assets WHERE id = ?", id).Scan(&currentTagsJSON)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return err
		}

		var currentTags []string
		if currentTagsJSON != "" && currentTagsJSON != "[]" {
			json.Unmarshal([]byte(currentTagsJSON), &currentTags)
		}

		tagMap := make(map[string]bool)
		for _, t := range currentTags {
			tagMap[t] = true
		}
		for _, t := range tags {
			tagMap[t] = true
		}

		newTags := make([]string, 0, len(tagMap))
		for t := range tagMap {
			newTags = append(newTags, t)
		}

		newTagsJSON, _ := json.Marshal(newTags)
		_, err = tx.ExecContext(ctx, "UPDATE media_assets SET tags = ? WHERE id = ?", string(newTagsJSON), id)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// BulkRemoveTags removes a set of tags from multiple clips.
func (r *Repository) BulkRemoveTags(ctx context.Context, ids []string, tags []string) error {
	if len(ids) == 0 || len(tags) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	toRemove := make(map[string]bool)
	for _, t := range tags {
		toRemove[t] = true
	}

	for _, id := range ids {
		var currentTagsJSON string
		err := tx.QueryRowContext(ctx, "SELECT tags FROM media_assets WHERE id = ?", id).Scan(&currentTagsJSON)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return err
		}

		var currentTags []string
		if currentTagsJSON != "" && currentTagsJSON != "[]" {
			json.Unmarshal([]byte(currentTagsJSON), &currentTags)
		}

		newTags := make([]string, 0)
		for _, t := range currentTags {
			if !toRemove[t] {
				newTags = append(newTags, t)
			}
		}

		newTagsJSON, _ := json.Marshal(newTags)
		_, err = tx.ExecContext(ctx, "UPDATE media_assets SET tags = ? WHERE id = ?", string(newTagsJSON), id)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// buildMediaAssetQuery builds a SELECT query using the standard media_asset columns,
// excluding deleted clips (those with '$.deleted_at' in metadata_json).
func buildMediaAssetQuery(source string) string {
	query := "SELECT " + mediaAssetColumns + " FROM media_assets WHERE json_extract(COALESCE(metadata_json,'{}'), '$.deleted_at') IS NULL"
	if source != "" && source != "all" && source != "unified" {
		query += " AND source = ?"
	}
	return query
}

// ListClipsPaged returns clips with pagination and optional search.
// If q is non-empty, performs a search; otherwise lists all clips with pagination.
func (r *Repository) ListClipsPaged(ctx context.Context, source string, limit, offset int, q string) ([]*models.MediaAsset, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 10000 {
		limit = 10000
	}
	if offset < 0 {
		offset = 0
	}

	if strings.TrimSpace(q) != "" {
		return r.SearchClips(ctx, source, q)
	}

	query := buildMediaAssetQuery(source) + " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args := []interface{}{}
	if source != "" && source != "all" && source != "unified" {
		args = append(args, source)
	}
	args = append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// SearchClips searches clips by tag or name using LIKE on the media_assets table.
func (r *Repository) SearchClips(ctx context.Context, source, tag string) ([]*models.MediaAsset, error) {
	columns := []string{"tags", "name"}
	keywords := strings.Fields(tag)
	if len(keywords) == 0 {
		keywords = []string{tag}
	}

	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*models.MediaAsset{}, nil
	}

	query := buildMediaAssetQuery(source) + " AND (" + conditionSQL + ")"
	finalArgs := []interface{}{}
	if source != "" && source != "all" && source != "unified" {
		finalArgs = append(finalArgs, source)
	}
	finalArgs = append(finalArgs, args...)

	rows, err := r.db.QueryContext(ctx, query, finalArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// SearchClipsByKeywords searches clips by keywords using LIKE on the media_assets table.
func (r *Repository) SearchClipsByKeywords(ctx context.Context, source string, keywords []string, limit int) ([]*models.MediaAsset, error) {
	if len(keywords) == 0 {
		return []*models.MediaAsset{}, nil
	}

	columns := []string{"name", "tags"}
	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*models.MediaAsset{}, nil
	}

	query := fmt.Sprintf(
		"%s AND (%s) LIMIT ?",
		buildMediaAssetQuery(source),
		conditionSQL,
	)
	finalArgs := []interface{}{}
	if source != "" && source != "all" && source != "unified" {
		finalArgs = append(finalArgs, source)
	}
	finalArgs = append(finalArgs, args...)
	finalArgs = append(finalArgs, limit)

	rows, err := r.db.QueryContext(ctx, query, finalArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// SearchStockByKeywords searches stock clips by keywords using LIKE on the media_assets table.
func (r *Repository) SearchStockByKeywords(ctx context.Context, keywords []string, limit int) ([]*models.MediaAsset, error) {
	if len(keywords) == 0 {
		return []*models.MediaAsset{}, nil
	}

	columns := []string{"name", "tags"}
	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*models.MediaAsset{}, nil
	}

	query := fmt.Sprintf(`
		SELECT %s
		FROM media_assets
		WHERE source = 'stock' AND json_extract(COALESCE(metadata_json,'{}'), '$.deleted_at') IS NULL AND (%s)
		LIMIT ?`,
		mediaAssetColumns,
		conditionSQL,
	)
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// scanMediaAssetRows scans a media asset from sql.Rows
func scanMediaAssetRows(rows *sql.Rows) (*models.MediaAsset, error) {
	var clip models.MediaAsset
	var tagsNull, metadataStr sql.NullString
	var sourceNull, nameNull, urlNull sql.NullString
	var createdAtStr sql.NullString
	var duration sql.NullInt64
	var embeddingJSON sql.NullString

	err := rows.Scan(
		&clip.ID, &sourceNull, &nameNull, &tagsNull, &embeddingJSON, &duration, &urlNull, &createdAtStr, &metadataStr,
	)
	if err != nil {
		return nil, err
	}

	clip.Source = sourceNull.String
	clip.Name = nameNull.String
	clip.ExternalURL = urlNull.String

	if embeddingJSON.Valid {
		clip.EmbeddingJSON = embeddingJSON.String
	}

	if duration.Valid {
		clip.Duration = int(duration.Int64)
	}

	if createdAtStr.Valid {
		if t, err := time.Parse(time.RFC3339, createdAtStr.String); err == nil {
			clip.CreatedAt = t
			clip.UpdatedAt = t
		}
	}

	// Parse tags
	if tagsNull.Valid && tagsNull.String != "" && tagsNull.String != "[]" {
		_ = json.Unmarshal([]byte(tagsNull.String), &clip.Tags)
	}

	// Parse metadata_json into Metadata map
	clip.SetMetadataJSON(metadataStr.String)

	// Extract common fields from Metadata map for convenience
	clip.FolderID = clip.GetMetadataString("folder_id")
	clip.ParentFolderID = clip.GetMetadataString("parent_folder_id")
	clip.FolderPath = clip.GetMetadataString("folder_path")
	clip.Group = clip.GetMetadataString("group_name")
	clip.MediaType = clip.GetMetadataString("media_type")
	clip.DriveLink = clip.GetMetadataString("drive_link")
	clip.DriveFileID = clip.GetMetadataString("drive_file_id")
	clip.DownloadLink = clip.GetMetadataString("download_link")
	clip.Category = clip.GetMetadataString("category")
	clip.FileHash = clip.GetMetadataString("file_hash")
	clip.LocalPath = clip.GetMetadataString("local_path")
	clip.Status = clip.GetMetadataString("status")
	clip.Error = clip.GetMetadataString("error")
	clip.ThumbURL = clip.GetMetadataString("thumb_url")
	clip.PHash = clip.GetMetadataString("phash")
	clip.Filename = clip.GetMetadataString("filename")
	clip.VisualEmbeddingJSON = clip.GetMetadataString("visual_embedding_json")
	clip.SearchText = clip.GetMetadataString("search_text")
	clip.SceneType = clip.GetMetadataString("scene_type")

	return &clip, nil
}

// scanMediaAssetRow scans a single media asset from sql.Row
func (r *Repository) scanMediaAssetRow(row *sql.Row) (*models.MediaAsset, error) {
	var clip models.MediaAsset
	var tagsNull, metadataStr sql.NullString
	var sourceNull, nameNull, urlNull sql.NullString
	var createdAtStr sql.NullString
	var duration sql.NullInt64
	var embeddingJSON sql.NullString

	err := row.Scan(
		&clip.ID, &sourceNull, &nameNull, &tagsNull, &embeddingJSON, &duration, &urlNull, &createdAtStr, &metadataStr,
	)
	if err != nil {
		return nil, err
	}

	clip.Source = sourceNull.String
	clip.Name = nameNull.String
	clip.ExternalURL = urlNull.String

	if embeddingJSON.Valid {
		clip.EmbeddingJSON = embeddingJSON.String
	}

	if duration.Valid {
		clip.Duration = int(duration.Int64)
	}

	if createdAtStr.Valid {
		if t, err := time.Parse(time.RFC3339, createdAtStr.String); err == nil {
			clip.CreatedAt = t
			clip.UpdatedAt = t
		}
	}

	// Parse tags
	if tagsNull.Valid && tagsNull.String != "" && tagsNull.String != "[]" {
		_ = json.Unmarshal([]byte(tagsNull.String), &clip.Tags)
	}

	// Parse metadata_json into Metadata map
	clip.SetMetadataJSON(metadataStr.String)

	// Extract common fields from Metadata map for convenience
	clip.FolderID = clip.GetMetadataString("folder_id")
	clip.ParentFolderID = clip.GetMetadataString("parent_folder_id")
	clip.FolderPath = clip.GetMetadataString("folder_path")
	clip.Group = clip.GetMetadataString("group_name")
	clip.MediaType = clip.GetMetadataString("media_type")
	clip.DriveLink = clip.GetMetadataString("drive_link")
	clip.DriveFileID = clip.GetMetadataString("drive_file_id")
	clip.DownloadLink = clip.GetMetadataString("download_link")
	clip.Category = clip.GetMetadataString("category")
	clip.FileHash = clip.GetMetadataString("file_hash")
	clip.LocalPath = clip.GetMetadataString("local_path")
	clip.Status = clip.GetMetadataString("status")
	clip.Error = clip.GetMetadataString("error")
	clip.ThumbURL = clip.GetMetadataString("thumb_url")
	clip.PHash = clip.GetMetadataString("phash")
	clip.Filename = clip.GetMetadataString("filename")
	clip.VisualEmbeddingJSON = clip.GetMetadataString("visual_embedding_json")
	clip.SearchText = clip.GetMetadataString("search_text")
	clip.SceneType = clip.GetMetadataString("scene_type")

	return &clip, nil
}

// GetClipByFolderAndFilename retrieves a clip by folder and filename (stored in metadata_json).
func (r *Repository) GetClipByFolderAndFilename(ctx context.Context, folderID, filename string) (*models.MediaAsset, error) {
	query := buildMediaAssetQuery("") + " AND json_extract(COALESCE(metadata_json,'{}'), '$.folder_id') = ? AND json_extract(COALESCE(metadata_json,'{}'), '$.filename') = ? LIMIT 1"
	row := r.db.QueryRowContext(ctx, query, folderID, filename)
	return r.scanMediaAssetRow(row)
}

// GetClip retrieves a clip by ID
func (r *Repository) GetClip(ctx context.Context, id string) (*models.MediaAsset, error) {
	query := buildMediaAssetQuery("") + " AND id = ? LIMIT 1"
	row := r.db.QueryRowContext(ctx, query, id)
	return r.scanMediaAssetRow(row)
}

// GetClipByDriveFileID finds a clip by Drive file ID (searches both drive_link and download_link in metadata_json).
// Returns nil, nil if not found.
func (r *Repository) GetClipByDriveFileID(ctx context.Context, fileID string) (*models.MediaAsset, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return nil, fmt.Errorf("drive file id is required")
	}

	query := buildMediaAssetQuery("") + " AND (json_extract(COALESCE(metadata_json,'{}'), '$.drive_link') LIKE ? OR json_extract(COALESCE(metadata_json,'{}'), '$.download_link') LIKE ?) LIMIT 1"
	pattern := "%" + fileID + "%"
	row := r.db.QueryRowContext(ctx, query, pattern, pattern)
	clip, err := r.scanMediaAssetRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return clip, err
}

// FindClipsByHash returns all clips with the given file hash (stored in metadata_json).
func (r *Repository) FindClipsByHash(ctx context.Context, hash string) ([]*models.MediaAsset, error) {
	query := buildMediaAssetQuery("") + " AND json_extract(COALESCE(metadata_json,'{}'), '$.file_hash') = ?"
	rows, err := r.db.QueryContext(ctx, query, hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// GetAllWithDriveFileID returns all clips that have a non-empty drive_file_id (stored in metadata_json).
func (r *Repository) GetAllWithDriveFileID(ctx context.Context) ([]*models.MediaAsset, error) {
	query := buildMediaAssetQuery("") + " AND json_extract(COALESCE(metadata_json,'{}'), '$.drive_file_id') IS NOT NULL AND json_extract(COALESCE(metadata_json,'{}'), '$.drive_file_id') != ''"
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// UpdateDriveFileID updates the drive_file_id for a clip (stored in metadata_json).
func (r *Repository) UpdateDriveFileID(ctx context.Context, clipID, fileID string) error {
	clipID = strings.TrimSpace(clipID)
	fileID = strings.TrimSpace(fileID)
	if clipID == "" {
		return fmt.Errorf("clip id is required")
	}

	_, err := r.db.ExecContext(ctx, "UPDATE media_assets SET metadata_json = json_set(COALESCE(metadata_json,'{}'), '$.drive_file_id', ?) WHERE id=?", fileID, clipID)
	return err
}

// CountClips returns the total number of clips (excluding soft-deleted).
func (r *Repository) CountClips(ctx context.Context) (int, error) {
	row := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_assets WHERE json_extract(COALESCE(metadata_json,'{}'), '$.deleted_at') IS NULL")
	var count int
	err := row.Scan(&count)
	return count, err
}

// UpdateFileHash updates the file_hash for a clip (stored in metadata_json).
func (r *Repository) UpdateFileHash(ctx context.Context, clipID, hash string) error {
	_, err := r.db.ExecContext(ctx, "UPDATE media_assets SET metadata_json = json_set(COALESCE(metadata_json,'{}'), '$.file_hash', ?) WHERE id=?", hash, clipID)
	return err
}

// LastUpdatedAtForTerm returns the most recent created_at value for clips matching a term.
// Uses LIKE search on tags.
func (r *Repository) LastUpdatedAtForTerm(ctx context.Context, term string) (*string, error) {
	term = strings.TrimSpace(term)

	var lastUpdated sql.NullString
	row := r.db.QueryRowContext(ctx, `
		SELECT MAX(created_at)
		FROM media_assets
		WHERE source = 'artlist' AND tags LIKE ?
	`, "%"+term+"%")

	if err := row.Scan(&lastUpdated); err != nil {
		return nil, err
	}
	if !lastUpdated.Valid || strings.TrimSpace(lastUpdated.String) == "" {
		return nil, nil
	}
	return &lastUpdated.String, nil
}

// UpsertClipFolder inserts or updates a clip folder
func (r *Repository) UpsertClipFolder(ctx context.Context, folder *models.ClipFolder) error {
	now := time.Now()
	// Compute search key: lowercase group + folder path, remove spaces
	searchKey := strings.ToLower(folder.Group + " " + folder.FolderPath)
	searchKey = strings.ReplaceAll(searchKey, " ", "")

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO clip_folders (id, source, source_url, video_id, folder_id, folder_path,
			local_folder_path, group_name, manifest_txt_path, manifest_json_path,
			clip_count, processed_count, failed_count, skipped_count, last_error, metadata, created_at, updated_at, search_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source=excluded.source, source_url=excluded.source_url, video_id=excluded.video_id,
			folder_id=excluded.folder_id, folder_path=excluded.folder_path,
			local_folder_path=excluded.local_folder_path, group_name=excluded.group_name,
			manifest_txt_path=excluded.manifest_txt_path, manifest_json_path=excluded.manifest_json_path,
			clip_count=excluded.clip_count, processed_count=excluded.processed_count,
			failed_count=excluded.failed_count, skipped_count=excluded.skipped_count,
			last_error=excluded.last_error, metadata=excluded.metadata, updated_at=excluded.updated_at,
			search_key=excluded.search_key
		`, folder.ID, folder.Source, folder.SourceURL, folder.VideoID, folder.FolderID, folder.FolderPath,
		folder.LocalFolderPath, folder.Group, folder.ManifestTXTPath, folder.ManifestJSONPath,
		folder.ClipCount, folder.ProcessedCount, folder.FailedCount, folder.SkippedCount, folder.LastError, folder.Metadata,
		folder.CreatedAt.Format(time.RFC3339), now.Format(time.RFC3339), searchKey)

	return err
}

// DeleteClipFolder deletes a clip folder by its ID.
func (r *Repository) DeleteClipFolder(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("clip folder id is required")
	}

	_, err := r.db.ExecContext(ctx, "DELETE FROM clip_folders WHERE id = ?", id)
	return err
}

// GetClipFolder retrieves a clip folder by ID
func (r *Repository) GetClipFolder(ctx context.Context, id string) (*models.ClipFolder, error) {
	query := buildClipFolderQuery("") + " WHERE id = ? LIMIT 1"
	row := r.db.QueryRowContext(ctx, query, id)

	var folder models.ClipFolder
	var createdAt, updatedAt string
	err := row.Scan(&folder.ID, &folder.Source, &folder.SourceURL, &folder.VideoID, &folder.FolderID,
		&folder.FolderPath, &folder.LocalFolderPath, &folder.Group, &folder.ManifestTXTPath,
		&folder.ManifestJSONPath, &folder.ClipCount, &folder.ProcessedCount, &folder.FailedCount,
		&folder.SkippedCount, &folder.LastError, &folder.Metadata, &createdAt, &updatedAt)

	if err != nil {
		return nil, err
	}

	if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
		folder.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
		folder.UpdatedAt = t
	}

	return &folder, nil
}

// GetClipFolderByVideoID retrieves a clip folder by video ID
func (r *Repository) GetClipFolderByVideoID(ctx context.Context, videoID string) (*models.ClipFolder, error) {
	query := buildClipFolderQuery("") + " WHERE video_id = ? LIMIT 1"
	row := r.db.QueryRowContext(ctx, query, videoID)

	var folder models.ClipFolder
	var createdAt, updatedAt string
	err := row.Scan(&folder.ID, &folder.Source, &folder.SourceURL, &folder.VideoID, &folder.FolderID,
		&folder.FolderPath, &folder.LocalFolderPath, &folder.Group, &folder.ManifestTXTPath,
		&folder.ManifestJSONPath, &folder.ClipCount, &folder.ProcessedCount, &folder.FailedCount,
		&folder.SkippedCount, &folder.LastError, &folder.Metadata, &createdAt, &updatedAt)

	if err != nil {
		return nil, err
	}

	if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
		folder.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
		folder.UpdatedAt = t
	}

	return &folder, nil
}

// ListClipsByFolderID returns all clips for a given folder ID (stored in metadata_json).
func (r *Repository) ListClipsByFolderID(ctx context.Context, folderID string) ([]*models.MediaAsset, error) {
	query := buildMediaAssetQuery("") + " AND json_extract(COALESCE(metadata_json,'{}'), '$.folder_id') = ? ORDER BY created_at ASC"
	rows, err := r.db.QueryContext(ctx, query, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// ListClipsByFolderPath returns all clips for a given folder path (stored in metadata_json).
func (r *Repository) ListClipsByFolderPath(ctx context.Context, folderPath string) ([]*models.MediaAsset, error) {
	query := buildMediaAssetQuery("") + " AND json_extract(COALESCE(metadata_json,'{}'), '$.folder_path') = ? ORDER BY created_at ASC"
	rows, err := r.db.QueryContext(ctx, query, folderPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// CountClipsByFolderID returns the number of clips in a folder (folder_id stored in metadata_json).
func (r *Repository) CountClipsByFolderID(ctx context.Context, folderID string) (int, error) {
	row := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_assets WHERE json_extract(COALESCE(metadata_json,'{}'), '$.folder_id') = ?", folderID)
	var count int
	err := row.Scan(&count)
	return count, err
}

// ListClipFolders returns all clip folders, optionally filtered by source
func (r *Repository) ListClipFolders(ctx context.Context, source string) ([]*models.ClipFolder, error) {
	query := buildClipFolderQuery(source)
	if source != "" {
		query += " ORDER BY updated_at DESC"
	} else {
		query += " ORDER BY updated_at DESC"
	}
	args := []interface{}{}
	if source != "" {
		args = append(args, source)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []*models.ClipFolder
	for rows.Next() {
		var folder models.ClipFolder
		var createdAt, updatedAt string
		err := rows.Scan(&folder.ID, &folder.Source, &folder.SourceURL, &folder.VideoID,
			&folder.FolderID, &folder.FolderPath, &folder.LocalFolderPath, &folder.Group,
			&folder.ManifestTXTPath, &folder.ManifestJSONPath, &folder.ClipCount,
			&folder.ProcessedCount, &folder.FailedCount, &folder.SkippedCount,
			&folder.LastError, &folder.Metadata, &createdAt, &updatedAt)
		if err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			folder.CreatedAt = t
		}
		if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
			folder.UpdatedAt = t
		}
		folders = append(folders, &folder)
	}
	return folders, rows.Err()
}

// SearchClipFolders searches clip folders by keyword in source_url, video_id, group_name, or folder_path
// Uses LIKE search.
func (r *Repository) SearchClipFolders(ctx context.Context, keyword string) ([]*models.ClipFolder, error) {
	columns := []string{"source_url", "video_id", "group_name", "folder_path"}
	keywords := strings.Fields(keyword)
	if len(keywords) == 0 {
		keywords = []string{keyword}
	}

	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*models.ClipFolder{}, nil
	}

	query := buildClipFolderQuery("") + " WHERE " + conditionSQL + " ORDER BY updated_at DESC"
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []*models.ClipFolder
	for rows.Next() {
		var folder models.ClipFolder
		var createdAt, updatedAt string
		err := rows.Scan(&folder.ID, &folder.Source, &folder.SourceURL, &folder.VideoID,
			&folder.FolderID, &folder.FolderPath, &folder.LocalFolderPath, &folder.Group,
			&folder.ManifestTXTPath, &folder.ManifestJSONPath, &folder.ClipCount,
			&folder.ProcessedCount, &folder.FailedCount, &folder.SkippedCount,
			&folder.LastError, &folder.Metadata, &createdAt, &updatedAt)
		if err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			folder.CreatedAt = t
		}
		if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
			folder.UpdatedAt = t
		}
		folders = append(folders, &folder)
	}
	return folders, rows.Err()
}

// GetFolderChildren returns all clips that are children of the given parent_folder_id.
// parent_folder_id is stored in metadata_json.
// Pass an empty string to get root folders.
func (r *Repository) GetFolderChildren(ctx context.Context, parentID string) ([]*models.MediaAsset, error) {
	query := `SELECT ` + mediaAssetColumns + `
		FROM media_assets
		WHERE json_extract(COALESCE(metadata_json,'{}'), '$.parent_folder_id') = ? AND json_extract(COALESCE(metadata_json,'{}'), '$.deleted_at') IS NULL
		ORDER BY name ASC`

	rows, err := r.db.QueryContext(ctx, query, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
		if err != nil {
			r.log.Error("failed to scan clip", zap.Error(err))
			continue
		}
		clips = append(clips, clip)
	}

	return clips, rows.Err()
}

// FindByPHash searches for a clip with the given perceptual hash (stored in metadata_json).
// Returns the clip ID if found, empty string if not.
func (r *Repository) FindByPHash(ctx context.Context, phash string) (string, error) {
	if phash == "" {
		return "", nil
	}
	var id string
	err := r.db.QueryRowContext(ctx,
		`SELECT id FROM media_assets WHERE json_extract(COALESCE(metadata_json,'{}'), '$.phash') = ? AND json_extract(COALESCE(metadata_json,'{}'), '$.deleted_at') IS NULL LIMIT 1`, phash,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("FindByPHash: %w", err)
	}
	return id, nil
}
