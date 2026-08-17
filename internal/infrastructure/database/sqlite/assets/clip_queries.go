// Package assets — clip/tag SQL queries (Wave C: moved from
// internal/kernel/asset/tags.go).
//
// After Wave C, the source `internal/kernel/asset/tags.go` is deleted
// (no types reside in it). The 8 SQL receivers migrate here.
package assets

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

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
