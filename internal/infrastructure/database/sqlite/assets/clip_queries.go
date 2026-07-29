// Package assets — clip/tag SQL queries (Wave C: moved from
// internal/kernel/asset/tags.go).
//
// After Wave C, the source `internal/kernel/asset/tags.go` is deleted
// (no types reside in it). The 8 SQL receivers migrate here.
package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── SQL receivers (migrated from tags.go) ────────────────────────────

// BulkAddTags adds a set of tags to multiple clips efficiently.
//
// QDRANT-002: THIS METHOD BYPASSES THE OUTBOX. Tag mutations affect
// search indexing (tags are part of the vector embedding input).
// Callers should route through the outbox.Dispatcher with a re-index
// event instead of mutating tags in isolation.
//
// Today this is called from the BulkTagsUseCase (API handler path);
// a future PR should replace it with a dispatcher.EnqueueAndIndex
// call that re-indexes the affected assets after the tag change.
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
// See BulkAddTags for the QDRANT-002 outbox bypass warning.
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

// GetClipByFolderAndFilename retrieves a clip by folder + filename.
func (s *AssetStoreSQLite) GetClipByFolderAndFilename(ctx context.Context, folderID, filename string) (*asset.Asset, error) {
	query := buildMediaAssetQuery("") + " AND folder_id = ? AND filename = ? LIMIT 1"
	row := s.db.QueryRowContext(ctx, query, folderID, filename)
	return ScanCanonicalAssetRowPublic(row)
}

// GetClip retrieves a clip by ID. Delegates to canonical Get.
func (s *AssetStoreSQLite) GetClip(ctx context.Context, id string) (*asset.Asset, error) {
	det, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if det == nil {
		return nil, nil
	}
	return det.Asset, nil
}

// GetClipByDriveFileID finds a clip by Drive file ID (searches
// canonical columns drive_file_id, drive_link, download_link).
// Returns nil, nil if not found.
func (s *AssetStoreSQLite) GetClipByDriveFileID(ctx context.Context, fileID string) (*asset.Asset, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return nil, fmt.Errorf("drive file id is required")
	}

	pattern := "%" + fileID + "%"
	query := buildMediaAssetQuery("") + " AND (drive_link LIKE ? OR download_link LIKE ? OR drive_file_id LIKE ?) LIMIT 1"
	row := s.db.QueryRowContext(ctx, query, pattern, pattern, pattern)
	clip, err := ScanCanonicalAssetRowPublic(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return clip, err
}

// FindClipsByHash returns all clips with the given file hash
// (canonical column after migration 059).
func (s *AssetStoreSQLite) FindClipsByHash(ctx context.Context, hash string) ([]*asset.Asset, error) {
	query := buildMediaAssetQuery("") + " AND file_hash = ?"
	rows, err := s.db.QueryContext(ctx, query, hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*asset.Asset
	for rows.Next() {
		clip, err := ScanCanonicalAssetRowsPublic(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// GetAllWithDriveFileID returns all clips that have a non-empty
// drive_file_id (canonical column).
func (s *AssetStoreSQLite) GetAllWithDriveFileID(ctx context.Context) ([]*asset.Asset, error) {
	query := buildMediaAssetQuery("") + " AND drive_file_id IS NOT NULL AND drive_file_id != ''"
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*asset.Asset
	for rows.Next() {
		clip, err := ScanCanonicalAssetRowsPublic(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// UpdateDriveFileID updates the drive_file_id for a clip (canonical
// column).
//
// QDRANT-002: THIS METHOD BYPASSES THE OUTBOX.
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
//
// QDRANT-002: THIS METHOD BYPASSES THE OUTBOX.
func (s *AssetStoreSQLite) UpdateFileHash(ctx context.Context, clipID, hash string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE media_assets SET file_hash = ? WHERE id=?", hash, clipID)
	return err
}
