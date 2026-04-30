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

// SearchStockByKeywords searches clips by keywords in name or tags
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
