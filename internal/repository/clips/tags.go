package clips

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"velox/go-master/internal/media/models"
)

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

	pattern := "%" + fileID + "%"
	query := buildMediaAssetQuery("") + " AND (json_extract(COALESCE(metadata_json,'{}'), '$.drive_link') LIKE ? OR json_extract(COALESCE(metadata_json,'{}'), '$.download_link') LIKE ? OR json_extract(COALESCE(metadata_json,'{}'), '$.drive_file_id') LIKE ?) LIMIT 1"
	row := r.db.QueryRowContext(ctx, query, pattern, pattern, pattern)
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

// UpdateFileHash updates the file_hash for a clip (stored in metadata_json).
func (r *Repository) UpdateFileHash(ctx context.Context, clipID, hash string) error {
	_, err := r.db.ExecContext(ctx, "UPDATE media_assets SET metadata_json = json_set(COALESCE(metadata_json,'{}'), '$.file_hash', ?) WHERE id=?", hash, clipID)
	return err
}
