package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	
)

// BulkAddTags adds a set of tags to multiple clips efficiently.
func (s *AssetStoreSQLite) BulkAddTags(ctx context.Context, ids []string, tags []string) error {
	if len(ids) == 0 || len(tags) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
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
func (s *AssetStoreSQLite) BulkRemoveTags(ctx context.Context, ids []string, tags []string) error {
	if len(ids) == 0 || len(tags) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
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

// GetClipByFolderAndFilename retrieves a clip by folder and filename (canonical columns after migration 059).
func (s *AssetStoreSQLite) GetClipByFolderAndFilename(ctx context.Context, folderID, filename string) (*Asset, error) {
	query := buildMediaAssetQuery("") + " AND folder_id = ? AND filename = ? LIMIT 1"
	row := s.db.QueryRowContext(ctx, query, folderID, filename)
	return s.scanCanonicalAssetRow(row)
}

// GetClip retrieves a clip by ID. PR1: delegates to canonical assetrepo,
// which returns (nil, assets.ErrSoftDeleted) for soft-deleted assets.
func (s *AssetStoreSQLite) GetClip(ctx context.Context, id string) (*Asset, error) {
	return s.Get(ctx, id)
}

// GetClipByDriveFileID finds a clip by Drive file ID (searches canonical columns drive_file_id, drive_link, download_link).
// Returns nil, nil if not found.
func (s *AssetStoreSQLite) GetClipByDriveFileID(ctx context.Context, fileID string) (*Asset, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return nil, fmt.Errorf("drive file id is required")
	}

	pattern := "%" + fileID + "%"
	query := buildMediaAssetQuery("") + " AND (drive_link LIKE ? OR download_link LIKE ? OR drive_file_id LIKE ?) LIMIT 1"
	row := s.db.QueryRowContext(ctx, query, pattern, pattern, pattern)
	clip, err := s.scanCanonicalAssetRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return clip, err
}

// FindClipsByHash returns all clips with the given file hash (canonical column after migration 059).
func (s *AssetStoreSQLite) FindClipsByHash(ctx context.Context, hash string) ([]*Asset, error) {
	query := buildMediaAssetQuery("") + " AND file_hash = ?"
	rows, err := s.db.QueryContext(ctx, query, hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*Asset
	for rows.Next() {
		clip, err := scanCanonicalAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// GetAllWithDriveFileID returns all clips that have a non-empty drive_file_id (canonical column).
func (s *AssetStoreSQLite) GetAllWithDriveFileID(ctx context.Context) ([]*Asset, error) {
	query := buildMediaAssetQuery("") + " AND drive_file_id IS NOT NULL AND drive_file_id != ''"
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*Asset
	for rows.Next() {
		clip, err := scanCanonicalAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// UpdateDriveFileID updates the drive_file_id for a clip (canonical column).
func (s *AssetStoreSQLite) UpdateDriveFileID(ctx context.Context, clipID, fileID string) error {
	clipID = strings.TrimSpace(clipID)
	fileID = strings.TrimSpace(fileID)
	if clipID == "" {
		return fmt.Errorf("clip id is required")
	}

	_, err := s.db.ExecContext(ctx, "UPDATE media_assets SET drive_file_id = ? WHERE id=?", fileID, clipID)
	return err
}

// UpdateFileHash updates the file_hash for a clip (canonical column).
func (s *AssetStoreSQLite) UpdateFileHash(ctx context.Context, clipID, hash string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE media_assets SET file_hash = ? WHERE id=?", hash, clipID)
	return err
}

