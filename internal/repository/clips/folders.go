package clips

import (
	"context"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/media/models"
	"velox/go-master/internal/pkg/sqlutil"
)

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
	query := "SELECT COUNT(*) FROM media_assets WHERE json_extract(COALESCE(metadata_json,'{}'), '$.folder_id') = ?"
	row := r.db.QueryRowContext(ctx, query, folderID)
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
