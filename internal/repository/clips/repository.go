// Package clips provides the repository for YouTube clips (clips.db.sqlite).
//
// This repository manages:
//   - YouTube clips and their metadata
//   - Clip folders for organization
//   - Segment embeddings for timeline generation
//
// Database: clips.db.sqlite
// Migrations: internal/repository/clips/migrations/
//
// Note: Stock and Artlist clips use separate databases (stock.db, artlist.db)
// but share the same clips.Repository structure with different instances.
package clips

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/models"
	"velox/go-master/pkg/sqlutil"
)

// Clip column constants to avoid repetition
const (
	clipColumns = `id, COALESCE(name, '') AS name, COALESCE(filename, '') AS filename, COALESCE(folder_id, '') AS folder_id, COALESCE(parent_folder_id, '') AS parent_folder_id, COALESCE(depth, 0) AS depth, is_folder, COALESCE(folder_path, '') AS folder_path, COALESCE(group_name, '') AS group_name, COALESCE(media_type, '') AS media_type, COALESCE(drive_link, '') AS drive_link, COALESCE(drive_file_id, '') AS drive_file_id, COALESCE(download_link, '') AS download_link, COALESCE(tags, '[]') AS tags, source, COALESCE(category, '') AS category, COALESCE(external_url, '') AS external_url, COALESCE(duration, 0) AS duration, COALESCE(metadata, '{}') AS metadata, COALESCE(file_hash, '') AS file_hash, COALESCE(local_path, '') AS local_path, COALESCE(status, '') AS status, COALESCE(error, '') AS error, COALESCE(search_terms, '[]') AS search_terms, COALESCE(thumb_url, '') AS thumb_url, COALESCE(phash, '') AS phash, COALESCE(visual_embedding_json, '[]') AS visual_embedding_json, created_at, updated_at, deleted_at, (SELECT COUNT(*) FROM clips c2 WHERE c2.parent_folder_id = clips.id) AS child_count`
	clipFolderColumns = `id, source, COALESCE(source_url, '') AS source_url, COALESCE(video_id, '') AS video_id, COALESCE(folder_id, '') AS folder_id, COALESCE(folder_path, '') AS folder_path, COALESCE(local_folder_path, '') AS local_folder_path, COALESCE(group_name, '') AS group_name, COALESCE(manifest_txt_path, '') AS manifest_txt_path, COALESCE(manifest_json_path, '') AS manifest_json_path, clip_count, processed_count, failed_count, skipped_count, COALESCE(last_error, '') AS last_error, COALESCE(metadata, '{}') AS metadata, created_at, updated_at`
)

// buildClipFolderQuery builds a SELECT query for clip_folders
func buildClipFolderQuery(source string) string {
	query := "SELECT " + clipFolderColumns + " FROM clip_folders"
	if source != "" {
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

// UpsertClip inserts or updates a clip
func (r *Repository) UpsertClip(ctx context.Context, clip *models.Clip) error {
	tagsJSON, err := json.Marshal(clip.Tags)
	if err != nil {
		return fmt.Errorf("failed to marshal tags: %w", err)
	}
	searchTermsJSON, err := json.Marshal(clip.SearchTerms)
	if err != nil {
		return fmt.Errorf("failed to marshal search_terms: %w", err)
	}
	now := time.Now()

	var deletedAt interface{}
	if clip.DeletedAt != nil {
		deletedAt = clip.DeletedAt.Format(time.RFC3339)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO clips (id, name, filename, folder_id, parent_folder_id, depth, is_folder, folder_path, group_name, media_type,
			drive_link, drive_file_id, download_link, tags, source, category, external_url, duration, metadata,
			file_hash, local_path, status, error, search_terms, phash, visual_embedding_json, created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, filename=excluded.filename, folder_id=excluded.folder_id,
			parent_folder_id=excluded.parent_folder_id, depth=excluded.depth, is_folder=excluded.is_folder,
			folder_path=excluded.folder_path, group_name=excluded.group_name,
			media_type=excluded.media_type, drive_link=excluded.drive_link,
			drive_file_id=excluded.drive_file_id, download_link=excluded.download_link, tags=excluded.tags,
			source=excluded.source, category=excluded.category,
			external_url=excluded.external_url, duration=excluded.duration,
			metadata=excluded.metadata, file_hash=excluded.file_hash,
			local_path=excluded.local_path, status=excluded.status, error=excluded.error,
			search_terms=excluded.search_terms, phash=excluded.phash,
			visual_embedding_json=excluded.visual_embedding_json,
			updated_at=excluded.updated_at, deleted_at=excluded.deleted_at
		`, clip.ID, clip.Name, clip.Filename, clip.FolderID, clip.ParentFolderID, clip.Depth, clip.IsFolder, clip.FolderPath, clip.Group,
		clip.MediaType, clip.DriveLink, clip.DriveFileID, clip.DownloadLink, string(tagsJSON), clip.Source,
		clip.Category, clip.ExternalURL, clip.Duration, clip.Metadata, clip.FileHash,
		clip.LocalPath, clip.Status, clip.Error, string(searchTermsJSON), clip.PHash, clip.VisualEmbeddingJSON, clip.CreatedAt.Format(time.RFC3339), now.Format(time.RFC3339), deletedAt)

	return err
}

// DeleteClip soft-deletes a clip by its ID.
func (r *Repository) DeleteClip(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("clip id is required")
	}

	_, err := r.db.ExecContext(ctx, "UPDATE clips SET deleted_at = ?, updated_at = ? WHERE id = ?", time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339), id)
	return err
}

// RestoreClip restores a soft-deleted clip by its ID.
func (r *Repository) RestoreClip(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("clip id is required")
	}

	_, err := r.db.ExecContext(ctx, "UPDATE clips SET deleted_at = NULL, updated_at = ? WHERE id = ?", time.Now().Format(time.RFC3339), id)
	return err
}

// HardDeleteClip permanently deletes a clip by its ID.
func (r *Repository) HardDeleteClip(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("clip id is required")
	}

	_, err := r.db.ExecContext(ctx, "DELETE FROM clips WHERE id = ?", id)
	return err
}

// DeleteClipByDriveLink soft-deletes a clip by its Drive link (checks both drive_link and download_link).
func (r *Repository) DeleteClipByDriveLink(ctx context.Context, driveLink string) error {
	driveLink = strings.TrimSpace(driveLink)
	if driveLink == "" {
		return fmt.Errorf("drive link is required")
	}

	now := time.Now().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx, "UPDATE clips SET deleted_at = ?, updated_at = ? WHERE drive_link = ? OR download_link = ?", now, now, driveLink, driveLink)
	return err
}

// ListClips returns all clips, optionally filtered by source
func (r *Repository) ListClips(ctx context.Context, source string) ([]*models.Clip, error) {
	query := buildClipQuery(source)
	args := []interface{}{}
	if source != "" {
		args = append(args, source)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.Clip
	for rows.Next() {
		clip, err := scanClipRows(rows)
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
		err := tx.QueryRowContext(ctx, "SELECT tags FROM clips WHERE id = ?", id).Scan(&currentTagsJSON)
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
		_, err = tx.ExecContext(ctx, "UPDATE clips SET tags = ?, updated_at = ? WHERE id = ?", string(newTagsJSON), time.Now().Format(time.RFC3339), id)
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
		err := tx.QueryRowContext(ctx, "SELECT tags FROM clips WHERE id = ?", id).Scan(&currentTagsJSON)
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
		_, err = tx.ExecContext(ctx, "UPDATE clips SET tags = ?, updated_at = ? WHERE id = ?", string(newTagsJSON), time.Now().Format(time.RFC3339), id)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}


// buildClipQuery builds a SELECT query using the standard clip columns, excluding deleted clips.
func buildClipQuery(source string) string {
	query := "SELECT " + clipColumns + " FROM clips WHERE deleted_at IS NULL"
	if source != "" {
		query += " AND source = ?"
	}
	return query
}

// ListClipsPaged returns clips with pagination and optional search.
// If q is non-empty, performs a search; otherwise lists all clips with pagination.
func (r *Repository) ListClipsPaged(ctx context.Context, limit, offset int, q string) ([]*models.Clip, error) {
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
		return r.SearchClips(ctx, q)
	}

	query := `SELECT ` + clipColumns + `
		FROM clips
		WHERE deleted_at IS NULL
		ORDER BY updated_at DESC
		LIMIT ? OFFSET ?`

	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.Clip
	for rows.Next() {
		clip, err := scanClipRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// SearchClips searches clips by tag or group_name using FTS5.
// Falls back to LIKE search if FTS5 table doesn't exist.
func (r *Repository) SearchClips(ctx context.Context, tag string) ([]*models.Clip, error) {
	// Try FTS5 search first
	query := `
		SELECT c.` + clipColumns + `
		FROM clips_fts fts
		JOIN clips c ON c.id = fts.clip_id
		WHERE clips_fts MATCH ?
		ORDER BY rank`
	matchExpr := "(tags:\"" + tag + "*\" OR group_name:\"" + tag + "*\")"
	rows, err := r.db.QueryContext(ctx, query, matchExpr)
	if err == nil {
		defer rows.Close()
		var clips []*models.Clip
		for rows.Next() {
			clip, err := scanClipRows(rows)
			if err != nil {
				return nil, err
			}
			clips = append(clips, clip)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		// Fallback to LIKE if FTS5 returned 0 results
		if len(clips) > 0 {
			return clips, nil
		}
		// If we get here, FTS5 succeeded but returned 0 results - fall back to LIKE
	}
	r.log.Warn("FTS5 search failed or returned 0 results, falling back to LIKE", zap.Error(err))
	// Fallback to LIKE search on multiple fields
	columns := []string{"tags", "group_name", "name", "category"}
	keywords := strings.Fields(tag)
	if len(keywords) == 0 {
		keywords = []string{tag}
	}
	
	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*models.Clip{}, nil
	}

	query = buildClipQuery("") + " AND " + conditionSQL
	rows, err = r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.Clip
	for rows.Next() {
		clip, err := scanClipRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// SearchClipsByKeywords searches clips by keywords using FTS5.
// Falls back to LIKE search if FTS5 table doesn't exist.
func (r *Repository) SearchClipsByKeywords(ctx context.Context, keywords []string, limit int) ([]*models.Clip, error) {
	if len(keywords) == 0 {
		return []*models.Clip{}, nil
	}

	// Build FTS5 match expression
	matchExpr := ""
	for i, kw := range keywords {
		if i > 0 {
			matchExpr += " OR "
		}
		matchExpr += "(name:\"" + kw + "*\" OR tags:\"" + kw + "*\" OR folder_path:\"" + kw + "*\" OR group_name:\"" + kw + "*\" OR category:\"" + kw + "*\")"
	}

	query := fmt.Sprintf(`
		SELECT c.%s
		FROM clips_fts fts
		JOIN clips c ON c.id = fts.clip_id
		WHERE clips_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, clipColumns)
	rows, err := r.db.QueryContext(ctx, query, matchExpr, limit)
	if err == nil {
		defer rows.Close()
		var clips []*models.Clip
		for rows.Next() {
			clip, err := scanClipRows(rows)
			if err != nil {
				return nil, err
			}
			clips = append(clips, clip)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return clips, nil
	}
	r.log.Warn("FTS5 search failed, falling back to LIKE", zap.Error(err))

	// Fallback to LIKE search
	columns := []string{"name", "tags", "folder_path", "group_name", "category"}
	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*models.Clip{}, nil
	}

	query = fmt.Sprintf(
		"%s AND (%s) LIMIT ?",
		buildClipQuery(""),
		conditionSQL,
	)
	args = append(args, limit)

	rows, err = r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.Clip
	for rows.Next() {
		clip, err := scanClipRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// SearchStockByKeywords searches stock clips by keywords using FTS5.
// Falls back to LIKE search if FTS5 table doesn't exist.
func (r *Repository) SearchStockByKeywords(ctx context.Context, keywords []string, limit int) ([]*models.Clip, error) {
	if len(keywords) == 0 {
		return []*models.Clip{}, nil
	}

	// Build FTS5 match expression
	matchExpr := ""
	for i, kw := range keywords {
		if i > 0 {
			matchExpr += " OR "
		}
		matchExpr += "(name:\"" + kw + "*\" OR tags:\"" + kw + "*\" OR folder_path:\"" + kw + "*\" OR group_name:\"" + kw + "*\" OR category:\"" + kw + "*\")"
	}

	query := fmt.Sprintf(`
		SELECT c.%s
		FROM clips_fts fts
		JOIN clips c ON c.id = fts.clip_id
		WHERE c.source = 'stock' AND clips_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, clipColumns)
	rows, err := r.db.QueryContext(ctx, query, matchExpr, limit)
	if err == nil {
		defer rows.Close()
		var clips []*models.Clip
		for rows.Next() {
			clip, err := scanClipRows(rows)
			if err != nil {
				return nil, err
			}
			clips = append(clips, clip)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return clips, nil
	}
	r.log.Warn("FTS5 search failed, falling back to LIKE", zap.Error(err))

	// Fallback to LIKE search
	columns := []string{"name", "tags"}
	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*models.Clip{}, nil
	}

	query = fmt.Sprintf(`
		SELECT %s
		FROM clips
		WHERE source = 'stock' AND (%s)
		LIMIT ?`,
		clipColumns,
		conditionSQL,
	)
	args = append(args, limit)

	rows, err = r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.Clip
	for rows.Next() {
		clip, err := scanClipRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// scanClipRows scans a clip from sql.Rows
func scanClipRows(rows *sql.Rows) (*models.Clip, error) {
	var clip models.Clip
	var tagsNull, searchTermsNull, metadataNull, createdAtNull, updatedAtNull, deletedAtNull sql.NullString
	var nameNull, filenameNull, folderIDNull, parentFolderIDNull, folderPathNull sql.NullString
	var groupNull, mediaTypeNull, driveLinkNull, driveFileIDNull, downloadLinkNull sql.NullString
	var sourceNull, categoryNull, externalURLNull, fileHashNull, localPathNull sql.NullString
	var statusNull, errorNull, thumbURLNull, phashNull, visualEmbNull sql.NullString

	err := rows.Scan(
		&clip.ID, &nameNull, &filenameNull, &folderIDNull, &parentFolderIDNull, &clip.Depth, &clip.IsFolder, &folderPathNull,
		&groupNull, &mediaTypeNull, &driveLinkNull, &driveFileIDNull, &downloadLinkNull, &tagsNull, &sourceNull,
		&categoryNull, &externalURLNull, &clip.Duration, &metadataNull, &fileHashNull, &localPathNull, &statusNull, &errorNull, &searchTermsNull, &thumbURLNull,
		&phashNull, &visualEmbNull, &createdAtNull, &updatedAtNull, &deletedAtNull, &clip.ChildCount)

	if err != nil {
		fmt.Printf("DEBUG: Scan error clip_id=%v err=%v\n", clip.ID, err)
		return nil, err
	}

	clip.Name = nameNull.String
	clip.Filename = filenameNull.String
	clip.FolderID = folderIDNull.String
	clip.ParentFolderID = parentFolderIDNull.String
	clip.FolderPath = folderPathNull.String
	clip.Group = groupNull.String
	clip.MediaType = mediaTypeNull.String
	clip.DriveLink = driveLinkNull.String
	clip.DriveFileID = driveFileIDNull.String
	clip.DownloadLink = downloadLinkNull.String
	clip.Source = sourceNull.String
	clip.Category = categoryNull.String
	clip.ExternalURL = externalURLNull.String
	clip.FileHash = fileHashNull.String
	clip.LocalPath = localPathNull.String
	clip.Status = statusNull.String
	clip.Error = errorNull.String
	clip.ThumbURL = thumbURLNull.String
	clip.Metadata = metadataNull.String
	clip.PHash = phashNull.String
	clip.VisualEmbeddingJSON = visualEmbNull.String

	if tagsNull.Valid && tagsNull.String != "" && tagsNull.String != "[]" {
		_ = json.Unmarshal([]byte(tagsNull.String), &clip.Tags)
	}
	if searchTermsNull.Valid && searchTermsNull.String != "" && searchTermsNull.String != "[]" {
		_ = json.Unmarshal([]byte(searchTermsNull.String), &clip.SearchTerms)
	}

	if createdAtNull.Valid {
		if t, err := time.Parse(time.RFC3339, createdAtNull.String); err == nil {
			clip.CreatedAt = t
		}
	}
	if updatedAtNull.Valid {
		if t, err := time.Parse(time.RFC3339, updatedAtNull.String); err == nil {
			clip.UpdatedAt = t
		}
	}
	if deletedAtNull.Valid && deletedAtNull.String != "" {
		if t, err := time.Parse(time.RFC3339, deletedAtNull.String); err == nil {
			clip.DeletedAt = &t
		}
	}

	return &clip, nil
}
func (r *Repository) scanClipRow(row *sql.Row) (*models.Clip, error) {
	var clip models.Clip
	var tagsNull, searchTermsNull, metadataNull, createdAtNull, updatedAtNull, deletedAtNull sql.NullString
	var nameNull, filenameNull, folderIDNull, parentFolderIDNull, folderPathNull sql.NullString
	var groupNull, mediaTypeNull, driveLinkNull, driveFileIDNull, downloadLinkNull sql.NullString
	var sourceNull, categoryNull, externalURLNull, fileHashNull, localPathNull sql.NullString
	var statusNull, errorNull, thumbURLNull, phashNull, visualEmbNull sql.NullString

	err := row.Scan(
		&clip.ID, &nameNull, &filenameNull, &folderIDNull, &parentFolderIDNull, &clip.Depth, &clip.IsFolder, &folderPathNull,
		&groupNull, &mediaTypeNull, &driveLinkNull, &driveFileIDNull, &downloadLinkNull, &tagsNull, &sourceNull,
		&categoryNull, &externalURLNull, &clip.Duration, &metadataNull, &fileHashNull, &localPathNull, &statusNull, &errorNull, &searchTermsNull, &thumbURLNull,
		&phashNull, &visualEmbNull, &createdAtNull, &updatedAtNull, &deletedAtNull, &clip.ChildCount)

	if err != nil {
		return nil, err
	}

	clip.Name = nameNull.String
	clip.Filename = filenameNull.String
	clip.FolderID = folderIDNull.String
	clip.ParentFolderID = parentFolderIDNull.String
	clip.FolderPath = folderPathNull.String
	clip.Group = groupNull.String
	clip.MediaType = mediaTypeNull.String
	clip.DriveLink = driveLinkNull.String
	clip.DriveFileID = driveFileIDNull.String
	clip.DownloadLink = downloadLinkNull.String
	clip.Source = sourceNull.String
	clip.Category = categoryNull.String
	clip.ExternalURL = externalURLNull.String
	clip.FileHash = fileHashNull.String
	clip.LocalPath = localPathNull.String
	clip.Status = statusNull.String
	clip.Error = errorNull.String
	clip.ThumbURL = thumbURLNull.String
	clip.Metadata = metadataNull.String

	if tagsNull.Valid && tagsNull.String != "" && tagsNull.String != "[]" {
		_ = json.Unmarshal([]byte(tagsNull.String), &clip.Tags)
	}
	if searchTermsNull.Valid && searchTermsNull.String != "" && searchTermsNull.String != "[]" {
		_ = json.Unmarshal([]byte(searchTermsNull.String), &clip.SearchTerms)
	}

	if createdAtNull.Valid {
		if t, err := time.Parse(time.RFC3339, createdAtNull.String); err == nil {
			clip.CreatedAt = t
		}
	}
	if updatedAtNull.Valid {
		if t, err := time.Parse(time.RFC3339, updatedAtNull.String); err == nil {
			clip.UpdatedAt = t
		}
	}
	if deletedAtNull.Valid && deletedAtNull.String != "" {
		if t, err := time.Parse(time.RFC3339, deletedAtNull.String); err == nil {
			clip.DeletedAt = &t
		}
	}

	return &clip, nil
}

// GetClipByFolderAndFilename retrieves a clip by folder and filename
func (r *Repository) GetClipByFolderAndFilename(ctx context.Context, folderID, filename string) (*models.Clip, error) {
	query := buildClipQuery("") + " AND folder_id = ? AND filename = ? LIMIT 1"
	row := r.db.QueryRowContext(ctx, query, folderID, filename)
	return r.scanClipRow(row)
}

// GetClip retrieves a clip by ID
func (r *Repository) GetClip(ctx context.Context, id string) (*models.Clip, error) {
	query := buildClipQuery("") + " AND id = ? LIMIT 1"
	row := r.db.QueryRowContext(ctx, query, id)
	return r.scanClipRow(row)
}

// GetClipByDriveFileID finds a clip by Drive file ID (searches both drive_link and download_link).
// Returns nil, nil if not found.
func (r *Repository) GetClipByDriveFileID(ctx context.Context, fileID string) (*models.Clip, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return nil, fmt.Errorf("drive file id is required")
	}

	query := buildClipQuery("") + " AND (drive_link LIKE ? OR download_link LIKE ?) LIMIT 1"
	pattern := "%" + fileID + "%"
	row := r.db.QueryRowContext(ctx, query, pattern, pattern)
	clip, err := r.scanClipRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return clip, err
}

// FindClipsByHash returns all clips with the given file hash.
func (r *Repository) FindClipsByHash(ctx context.Context, hash string) ([]*models.Clip, error) {
	query := buildClipQuery("") + " AND file_hash = ?"
	rows, err := r.db.QueryContext(ctx, query, hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.Clip
	for rows.Next() {
		clip, err := scanClipRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// GetAllWithDriveFileID returns all clips that have a non-empty drive_file_id
func (r *Repository) GetAllWithDriveFileID(ctx context.Context) ([]*models.Clip, error) {
	query := buildClipQuery("") + " AND drive_file_id IS NOT NULL AND drive_file_id != ''"
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.Clip
	for rows.Next() {
		clip, err := scanClipRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// UpdateDriveFileID updates the drive_file_id for a clip
func (r *Repository) UpdateDriveFileID(ctx context.Context, clipID, fileID string) error {
	clipID = strings.TrimSpace(clipID)
	fileID = strings.TrimSpace(fileID)
	if clipID == "" {
		return fmt.Errorf("clip id is required")
	}

	_, err := r.db.ExecContext(ctx, "UPDATE clips SET drive_file_id=?, updated_at=? WHERE id=?", fileID, time.Now().Format(time.RFC3339), clipID)
	return err
}

// CountClips returns the total number of clips
func (r *Repository) CountClips(ctx context.Context) (int, error) {
	row := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM clips")
	var count int
	err := row.Scan(&count)
	return count, err
}

// UpdateFileHash updates the file_hash for a clip.
func (r *Repository) UpdateFileHash(ctx context.Context, clipID, hash string) error {
	_, err := r.db.ExecContext(ctx, "UPDATE clips SET file_hash=? WHERE id=?", hash, clipID)
	return err
}

// LastUpdatedAtForTerm returns the most recent updated_at value for clips matching a term.
// Uses FTS5 if available, falls back to LIKE.
func (r *Repository) LastUpdatedAtForTerm(ctx context.Context, term string) (*string, error) {
	// Try FTS5 first
	query := `
		SELECT MAX(c.updated_at)
		FROM clips_fts fts
		JOIN clips c ON c.id = fts.clip_id
		WHERE c.source = 'artlist' AND clips_fts MATCH ?
	`
	term = strings.TrimSpace(term)
	row := r.db.QueryRowContext(ctx, query, "tags:\""+term+"*\"")
	var lastUpdated sql.NullString
	err := row.Scan(&lastUpdated)
	if err == nil && lastUpdated.Valid && strings.TrimSpace(lastUpdated.String) != "" {
		return &lastUpdated.String, nil
	}

	// Fallback to LIKE
	row = r.db.QueryRowContext(ctx, `
		SELECT MAX(updated_at)
		FROM clips
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

// ListClipsByFolderID returns all clips for a given folder ID
func (r *Repository) ListClipsByFolderID(ctx context.Context, folderID string) ([]*models.Clip, error) {
	query := buildClipQuery("") + " AND folder_id = ? ORDER BY created_at ASC"
	rows, err := r.db.QueryContext(ctx, query, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.Clip
	for rows.Next() {
		clip, err := scanClipRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// ListClipsByFolderPath returns all clips for a given folder path
func (r *Repository) ListClipsByFolderPath(ctx context.Context, folderPath string) ([]*models.Clip, error) {
	query := buildClipQuery("") + " AND folder_path = ? ORDER BY created_at ASC"
	rows, err := r.db.QueryContext(ctx, query, folderPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.Clip
	for rows.Next() {
		clip, err := scanClipRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// CountClipsByFolderID returns the number of clips in a folder
func (r *Repository) CountClipsByFolderID(ctx context.Context, folderID string) (int, error) {
	row := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM clips WHERE folder_id = ?", folderID)
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
// Uses FTS5 if available, falls back to LIKE.
func (r *Repository) SearchClipFolders(ctx context.Context, keyword string) ([]*models.ClipFolder, error) {
	// Try FTS5 first (if clip_folders_fts exists)
	query := `
		SELECT f.id, f.source, f.source_url, f.video_id, f.folder_id, f.folder_path,
		       f.local_folder_path, f.group_name, f.manifest_txt_path, f.manifest_json_path,
		       f.clip_count, f.processed_count, f.failed_count, f.skipped_count,
		       f.last_error, f.metadata, f.created_at, f.updated_at
		FROM clip_folders_fts fts
		JOIN clip_folders f ON f.id = fts.folder_id
		WHERE clip_folders_fts MATCH ?
		ORDER BY rank`
	rows, err := r.db.QueryContext(ctx, query, keyword+"*")
	if err == nil {
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
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return folders, nil
	}

	// Fallback to LIKE
	r.log.Warn("FTS5 for clip_folders not available, falling back to LIKE", zap.Error(err))
	columns := []string{"source_url", "video_id", "group_name", "folder_path"}
	keywords := strings.Fields(keyword)
	if len(keywords) == 0 {
		keywords = []string{keyword}
	}

	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*models.ClipFolder{}, nil
	}

	query = buildClipFolderQuery("") + " WHERE " + conditionSQL + " ORDER BY updated_at DESC"
	rows, err = r.db.QueryContext(ctx, query, args...)
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
// Pass an empty string to get root folders.
func (r *Repository) GetFolderChildren(ctx context.Context, parentID string) ([]*models.Clip, error) {
	query := `SELECT ` + clipColumns + `
		FROM clips
		WHERE parent_folder_id = ? AND deleted_at IS NULL
		ORDER BY is_folder DESC, name ASC`

	rows, err := r.db.QueryContext(ctx, query, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.Clip
	for rows.Next() {
		clip, err := scanClipRows(rows)
		if err != nil {
			r.log.Error("failed to scan clip", zap.Error(err))
			continue
		}
		clips = append(clips, clip)
	}

	return clips, rows.Err()
}

// FindByPHash searches for a clip with the given perceptual hash.
// Returns the clip ID if found, empty string if not.
func (r *Repository) FindByPHash(ctx context.Context, phash string) (string, error) {
	if phash == "" {
		return "", nil
	}
	var id string
	err := r.db.QueryRowContext(ctx,
		`SELECT id FROM clips WHERE phash = ? AND phash != '' AND deleted_at IS NULL LIMIT 1`, phash,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("FindByPHash: %w", err)
	}
	return id, nil
}
