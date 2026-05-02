package clips

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"velox/go-master/pkg/models"
)

// Repository handles persistence for clips
type Repository struct {
	db *sql.DB
}

// NewRepository creates a new clips repository
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// BeginTx starts a new transaction
func (r *Repository) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return r.db.BeginTx(ctx, opts)
}

// UpsertClip inserts or updates a clip
func (r *Repository) UpsertClip(ctx context.Context, clip *models.Clip) error {
	tagsJSON, _ := json.Marshal(clip.Tags)
	now := time.Now()

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO clips (id, name, filename, folder_id, folder_path, group_name, media_type,
			drive_link, download_link, tags, source, category, external_url, duration, metadata,
			file_hash, local_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, filename=excluded.filename, folder_id=excluded.folder_id,
			folder_path=excluded.folder_path, group_name=excluded.group_name,
			media_type=excluded.media_type, drive_link=excluded.drive_link,
			download_link=excluded.download_link, tags=excluded.tags,
			source=excluded.source, category=excluded.category,
			external_url=excluded.external_url, duration=excluded.duration,
			metadata=excluded.metadata, file_hash=excluded.file_hash,
			local_path=excluded.local_path, updated_at=excluded.updated_at
	`, clip.ID, clip.Name, clip.Filename, clip.FolderID, clip.FolderPath, clip.Group,
		clip.MediaType, clip.DriveLink, clip.DownloadLink, string(tagsJSON), clip.Source,
		clip.Category, clip.ExternalURL, clip.Duration, clip.Metadata, clip.FileHash,
		clip.LocalPath, clip.CreatedAt.Format(time.RFC3339), now.Format(time.RFC3339))

	return err
}

// ListClips returns all clips, optionally filtered by source
func (r *Repository) ListClips(ctx context.Context, source string) ([]*models.Clip, error) {
	query, extended, err := r.clipSelectQuery(ctx, source)
	if err != nil {
		return nil, err
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

	var clips []*models.Clip
	for rows.Next() {
		clip, err := scanClipRows(rows, extended)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// SearchClips searches clips by tag (searches in tags JSON field)
func (r *Repository) SearchClips(ctx context.Context, tag string) ([]*models.Clip, error) {
	query, extended, err := r.clipSelectQuery(ctx, "")
	if err != nil {
		return nil, err
	}
	query += " WHERE tags LIKE ?"
	rows, err := r.db.QueryContext(ctx, query, "%"+tag+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.Clip
	for rows.Next() {
		clip, err := scanClipRows(rows, extended)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// SearchClipsByKeywords searches clips by keywords in name, tags, folder path, or group.
func (r *Repository) SearchClipsByKeywords(ctx context.Context, keywords []string, limit int) ([]*models.Clip, error) {
	if len(keywords) == 0 {
		return []*models.Clip{}, nil
	}

	var conditions []string
	var args []interface{}
	for _, kw := range keywords {
		conditions = append(conditions, "(name LIKE ? OR tags LIKE ? OR folder_path LIKE ? OR group_name LIKE ? OR category LIKE ?)")
		args = append(args, "%"+kw+"%", "%"+kw+"%", "%"+kw+"%", "%"+kw+"%", "%"+kw+"%")
	}

	query, extended, err := r.clipSelectQuery(ctx, "")
	if err != nil {
		return nil, err
	}
	query = fmt.Sprintf(
		"%s WHERE (%s) LIMIT ?",
		query,
		strings.Join(conditions, " OR "),
	)
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.Clip
	for rows.Next() {
		clip, err := scanClipRows(rows, extended)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// SearchStockByKeywords searches stock clips by keywords in name or tags.
func (r *Repository) SearchStockByKeywords(ctx context.Context, keywords []string, limit int) ([]*models.Clip, error) {
	if len(keywords) == 0 {
		return []*models.Clip{}, nil
	}

	var conditions []string
	var args []interface{}
	for _, kw := range keywords {
		conditions = append(conditions, "(name LIKE ? OR tags LIKE ?)")
		args = append(args, "%"+kw+"%", "%"+kw+"%")
	}

	query, extended, err := r.clipSelectQuery(ctx, "stock")
	if err != nil {
		return nil, err
	}
	query = fmt.Sprintf(
		"%s AND (%s) LIMIT ?",
		query,
		strings.Join(conditions, " OR "),
	)
	args = append([]interface{}{"stock"}, args...)
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.Clip
	for rows.Next() {
		clip, err := scanClipRows(rows, extended)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

func (r *Repository) clipSelectQuery(ctx context.Context, source string) (string, bool, error) {
	extended := true
	if ok, err := r.hasClipCompatColumns(ctx); err != nil {
		return "", false, err
	} else if !ok {
		extended = false
	}

	if extended {
		query := "SELECT id, name, filename, folder_id, folder_path, group_name, media_type, drive_link, download_link, tags, source, category, external_url, duration, metadata, file_hash, local_path, created_at, updated_at FROM clips"
		if source != "" {
			query += " WHERE source = ?"
		}
		return query, true, nil
	}

	query := "SELECT id, name, filename, folder_id, folder_path, group_name, media_type, drive_link, download_link, tags, source, category, external_url, duration, metadata, created_at, updated_at FROM clips"
	if source != "" {
		query += " WHERE source = ?"
	}
	return query, false, nil
}

func (r *Repository) hasClipCompatColumns(ctx context.Context) (bool, error) {
	rows, err := r.db.QueryContext(ctx, "PRAGMA table_info(clips)")
	if err != nil {
		return false, err
	}
	defer rows.Close()

	hasFileHash := false
	hasLocalPath := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		switch name {
		case "file_hash":
			hasFileHash = true
		case "local_path":
			hasLocalPath = true
		}
	}
	return hasFileHash && hasLocalPath, rows.Err()
}

func scanClipRows(rows *sql.Rows, extended bool) (*models.Clip, error) {
	var clip models.Clip
	var tagsJSON string
	var createdAt, updatedAt string

	if extended {
		var fileHash, localPath string
		err := rows.Scan(&clip.ID, &clip.Name, &clip.Filename, &clip.FolderID, &clip.FolderPath,
			&clip.Group, &clip.MediaType, &clip.DriveLink, &clip.DownloadLink, &tagsJSON,
			&clip.Source, &clip.Category, &clip.ExternalURL, &clip.Duration, &clip.Metadata,
			&fileHash, &localPath, &createdAt, &updatedAt)
		if err != nil {
			return nil, err
		}
		clip.FileHash = fileHash
		clip.LocalPath = localPath
	} else {
		err := rows.Scan(&clip.ID, &clip.Name, &clip.Filename, &clip.FolderID, &clip.FolderPath,
			&clip.Group, &clip.MediaType, &clip.DriveLink, &clip.DownloadLink, &tagsJSON,
			&clip.Source, &clip.Category, &clip.ExternalURL, &clip.Duration, &clip.Metadata,
			&createdAt, &updatedAt)
		if err != nil {
			return nil, err
		}
	}

	json.Unmarshal([]byte(tagsJSON), &clip.Tags)
	clip.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	clip.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	return &clip, nil
}

func scanClipRow(row *sql.Row) (*models.Clip, error) {
	var clip models.Clip
	var tagsJSON string
	var createdAt, updatedAt string

	err := row.Scan(&clip.ID, &clip.Name, &clip.Filename, &clip.FolderID, &clip.FolderPath,
		&clip.Group, &clip.MediaType, &clip.DriveLink, &clip.DownloadLink, &tagsJSON,
		&clip.Source, &clip.Category, &clip.ExternalURL, &clip.Duration, &clip.Metadata,
		&clip.FileHash, &clip.LocalPath, &createdAt, &updatedAt)

	if err != nil {
		return nil, err
	}

	json.Unmarshal([]byte(tagsJSON), &clip.Tags)
	clip.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	clip.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	return &clip, nil
}

// GetClipByFolderAndFilename retrieves a clip by folder and filename
func (r *Repository) GetClipByFolderAndFilename(ctx context.Context, folderID, filename string) (*models.Clip, error) {
	query := "SELECT id, name, filename, folder_id, folder_path, group_name, media_type, drive_link, download_link, tags, source, category, external_url, duration, metadata, file_hash, local_path, created_at, updated_at FROM clips WHERE folder_id = ? AND filename = ? LIMIT 1"
	row := r.db.QueryRowContext(ctx, query, folderID, filename)
	return scanClipRow(row)
}

// GetClip retrieves a clip by ID
func (r *Repository) GetClip(ctx context.Context, id string) (*models.Clip, error) {
	query := "SELECT id, name, filename, folder_id, folder_path, group_name, media_type, drive_link, download_link, tags, source, category, external_url, duration, metadata, file_hash, local_path, created_at, updated_at FROM clips WHERE id = ? LIMIT 1"
	row := r.db.QueryRowContext(ctx, query, id)
	return scanClipRow(row)
}

// CountClips returns the total number of clips
func (r *Repository) CountClips(ctx context.Context) (int, error) {
	row := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM clips")
	var count int
	err := row.Scan(&count)
	return count, err
}

// LastUpdatedAtForTerm returns the most recent updated_at value for clips matching a term.
func (r *Repository) LastUpdatedAtForTerm(ctx context.Context, term string) (*string, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT MAX(updated_at)
		FROM clips
		WHERE source = 'artlist' AND tags LIKE ?
	`, "%"+strings.TrimSpace(term)+"%")

	var lastUpdated sql.NullString
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

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO clip_folders (id, source, source_url, video_id, folder_id, folder_path,
			local_folder_path, group_name, manifest_txt_path, manifest_json_path,
			clip_count, processed_count, failed_count, skipped_count, last_error, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source=excluded.source, source_url=excluded.source_url, video_id=excluded.video_id,
			folder_id=excluded.folder_id, folder_path=excluded.folder_path,
			local_folder_path=excluded.local_folder_path, group_name=excluded.group_name,
			manifest_txt_path=excluded.manifest_txt_path, manifest_json_path=excluded.manifest_json_path,
			clip_count=excluded.clip_count, processed_count=excluded.processed_count,
			failed_count=excluded.failed_count, skipped_count=excluded.skipped_count,
			last_error=excluded.last_error, metadata=excluded.metadata, updated_at=excluded.updated_at
	`, folder.ID, folder.Source, folder.SourceURL, folder.VideoID, folder.FolderID, folder.FolderPath,
		folder.LocalFolderPath, folder.Group, folder.ManifestTXTPath, folder.ManifestJSONPath,
		folder.ClipCount, folder.ProcessedCount, folder.FailedCount, folder.SkippedCount, folder.LastError, folder.Metadata,
		folder.CreatedAt.Format(time.RFC3339), now.Format(time.RFC3339))

	return err
}

// GetClipFolder retrieves a clip folder by ID
func (r *Repository) GetClipFolder(ctx context.Context, id string) (*models.ClipFolder, error) {
	query := "SELECT id, source, source_url, video_id, folder_id, folder_path, local_folder_path, group_name, manifest_txt_path, manifest_json_path, clip_count, processed_count, failed_count, skipped_count, last_error, metadata, created_at, updated_at FROM clip_folders WHERE id = ? LIMIT 1"
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

	folder.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	folder.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	return &folder, nil
}

// GetClipFolderByVideoID retrieves a clip folder by video ID
func (r *Repository) GetClipFolderByVideoID(ctx context.Context, videoID string) (*models.ClipFolder, error) {
	query := "SELECT id, source, source_url, video_id, folder_id, folder_path, local_folder_path, group_name, manifest_txt_path, manifest_json_path, clip_count, processed_count, failed_count, skipped_count, last_error, metadata, created_at, updated_at FROM clip_folders WHERE video_id = ? LIMIT 1"
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

	folder.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	folder.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	return &folder, nil
}

// ListClipsByFolderID returns all clips for a given folder ID
func (r *Repository) ListClipsByFolderID(ctx context.Context, folderID string) ([]*models.Clip, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, filename, folder_id, folder_path, group_name, media_type, drive_link, download_link,
			tags, source, category, external_url, duration, metadata, file_hash, local_path, created_at, updated_at
		FROM clips WHERE folder_id = ? ORDER BY created_at ASC
	`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.Clip
	for rows.Next() {
		clip, err := scanClipRows(rows, true)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// ListClipsByFolderPath returns all clips for a given folder path
func (r *Repository) ListClipsByFolderPath(ctx context.Context, folderPath string) ([]*models.Clip, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, filename, folder_id, folder_path, group_name, media_type, drive_link, download_link,
			tags, source, category, external_url, duration, metadata, file_hash, local_path, created_at, updated_at
		FROM clips WHERE folder_path = ? ORDER BY created_at ASC
	`, folderPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.Clip
	for rows.Next() {
		clip, err := scanClipRows(rows, true)
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
	query := "SELECT id, source, source_url, video_id, folder_id, folder_path, local_folder_path, group_name, manifest_txt_path, manifest_json_path, clip_count, processed_count, failed_count, skipped_count, last_error, metadata, created_at, updated_at FROM clip_folders"
	args := []interface{}{}
	if source != "" {
		query += " WHERE source = ?"
		args = append(args, source)
	}
	query += " ORDER BY updated_at DESC"

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
		folder.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		folder.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		folders = append(folders, &folder)
	}
	return folders, rows.Err()
}

// SearchClipFolders searches clip folders by keyword in source_url, video_id, group_name, or folder_path
func (r *Repository) SearchClipFolders(ctx context.Context, keyword string) ([]*models.ClipFolder, error) {
	query := "SELECT id, source, source_url, video_id, folder_id, folder_path, local_folder_path, group_name, manifest_txt_path, manifest_json_path, clip_count, processed_count, failed_count, skipped_count, last_error, metadata, created_at, updated_at FROM clip_folders WHERE source_url LIKE ? OR video_id LIKE ? OR group_name LIKE ? OR folder_path LIKE ? ORDER BY updated_at DESC"
	searchTerm := "%" + keyword + "%"
	rows, err := r.db.QueryContext(ctx, query, searchTerm, searchTerm, searchTerm, searchTerm)
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
		folder.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		folder.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		folders = append(folders, &folder)
	}
	return folders, rows.Err()
}
