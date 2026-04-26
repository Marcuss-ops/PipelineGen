package clips

import (
	"context"
	"database/sql"
	"encoding/json"
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

// UpsertClip inserts or updates a clip
func (r *Repository) UpsertClip(ctx context.Context, clip *models.Clip) error {
	tagsJSON, _ := json.Marshal(clip.Tags)
	
	query := `
		INSERT INTO clips (
			id, name, filename, folder_id, folder_path, group_name, 
			media_type, drive_link, download_link, tags, 
			source, category, external_url, duration, metadata,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			filename = excluded.filename,
			folder_id = excluded.folder_id,
			folder_path = excluded.folder_path,
			group_name = excluded.group_name,
			media_type = excluded.media_type,
			drive_link = excluded.drive_link,
			download_link = excluded.download_link,
			tags = excluded.tags,
			source = excluded.source,
			category = excluded.category,
			external_url = excluded.external_url,
			duration = excluded.duration,
			metadata = excluded.metadata,
			updated_at = datetime('now')
	`
	
	_, err := r.db.ExecContext(ctx, query,
		clip.ID, clip.Name, clip.Filename, clip.FolderID, clip.FolderPath, clip.Group,
		clip.MediaType, clip.DriveLink, clip.DownloadLink, string(tagsJSON),
		clip.Source, clip.Category, clip.ExternalURL, clip.Duration, clip.Metadata,
	)
	
	return err
}

// GetClipByID retrieves a clip by its ID
func (r *Repository) GetClipByID(ctx context.Context, id string) (*models.Clip, error) {
	query := `SELECT id, name, filename, folder_id, folder_path, group_name, media_type, drive_link, download_link, tags, source, category, external_url, duration, metadata, created_at, updated_at FROM clips WHERE id = ?`
	
	var clip models.Clip
	var tagsJSON string
	var createdAtStr, updatedAtStr string
	
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&clip.ID, &clip.Name, &clip.Filename, &clip.FolderID, &clip.FolderPath, &clip.Group,
		&clip.MediaType, &clip.DriveLink, &clip.DownloadLink, &tagsJSON,
		&clip.Source, &clip.Category, &clip.ExternalURL, &clip.Duration, &clip.Metadata,
		&createdAtStr, &updatedAtStr,
	)
	
	if err != nil {
		return nil, err
	}
	
	json.Unmarshal([]byte(tagsJSON), &clip.Tags)
	clip.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAtStr)
	clip.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAtStr)
	
	return &clip, nil
}

// ListClips lists clips with optional filtering
func (r *Repository) ListClips(ctx context.Context, group string) ([]*models.Clip, error) {
	query := `SELECT id, name, filename, folder_id, folder_path, group_name, media_type, drive_link, download_link, tags, source, category, external_url, duration, metadata, created_at, updated_at FROM clips`
	var args []interface{}
	
	if group != "" {
		query += " WHERE group_name = ?"
		args = append(args, group)
	}
	
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var clips []*models.Clip
	for rows.Next() {
		var clip models.Clip
		var tagsJSON string
		var createdAtStr, updatedAtStr string
		
		err := rows.Scan(
			&clip.ID, &clip.Name, &clip.Filename, &clip.FolderID, &clip.FolderPath, &clip.Group,
			&clip.MediaType, &clip.DriveLink, &clip.DownloadLink, &tagsJSON,
			&clip.Source, &clip.Category, &clip.ExternalURL, &clip.Duration, &clip.Metadata,
			&createdAtStr, &updatedAtStr,
		)
		if err != nil {
			return nil, err
		}
		
		json.Unmarshal([]byte(tagsJSON), &clip.Tags)
		clip.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAtStr)
		clip.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAtStr)
		
		clips = append(clips, &clip)
	}
	
	return clips, nil
}

// SearchClips searches clips by tags or name
func (r *Repository) SearchClips(ctx context.Context, searchTerm string) ([]*models.Clip, error) {
	query := `SELECT id, name, filename, folder_id, folder_path, group_name, media_type, drive_link, download_link, tags, source, category, external_url, duration, metadata, created_at, updated_at 
	          FROM clips 
	          WHERE name LIKE ? OR tags LIKE ?`
	
	pattern := "%" + searchTerm + "%"
	rows, err := r.db.QueryContext(ctx, query, pattern, pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var clips []*models.Clip
	for rows.Next() {
		var clip models.Clip
		var tagsJSON string
		var createdAtStr, updatedAtStr string
		
		err := rows.Scan(
			&clip.ID, &clip.Name, &clip.Filename, &clip.FolderID, &clip.FolderPath, &clip.Group,
			&clip.MediaType, &clip.DriveLink, &clip.DownloadLink, &tagsJSON,
			&clip.Source, &clip.Category, &clip.ExternalURL, &clip.Duration, &clip.Metadata,
			&createdAtStr, &updatedAtStr,
		)
		if err != nil {
			return nil, err
		}
		
		json.Unmarshal([]byte(tagsJSON), &clip.Tags)
		clip.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAtStr)
		clip.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAtStr)
		
		clips = append(clips, &clip)
	}
	
	return clips, nil
}

// SearchStockByKeywords searches for stock assets matching multiple keyword tokens
func (r *Repository) SearchStockByKeywords(ctx context.Context, keywords []string, limit int) ([]*models.Clip, error) {
	if len(keywords) == 0 {
		return nil, nil
	}

	// Tokenize all keywords
	var tokens []string
	for _, kw := range keywords {
		parts := strings.Fields(strings.ToLower(kw))
		for _, p := range parts {
			if len(p) > 2 {
				tokens = append(tokens, p)
			}
		}
	}

	if len(tokens) == 0 {
		return nil, nil
	}

	queryBase := `
		SELECT id, name, filename, folder_id, folder_path, group_name, media_type, drive_link, download_link, tags, source, category, external_url, duration, metadata, created_at, updated_at 
		FROM clips
		WHERE media_type = 'stock' AND (`

	var conditions []string
	var args []interface{}
	for _, token := range tokens {
		conditions = append(conditions, "(name LIKE ? OR tags LIKE ? OR group_name LIKE ?)")
		pattern := "%" + token + "%"
		args = append(args, pattern, pattern, pattern)
	}

	query := queryBase + strings.Join(conditions, " OR ") + ") LIMIT ?"
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.Clip
	for rows.Next() {
		var clip models.Clip
		var tagsJSON string
		var createdAtStr, updatedAtStr string
		
		err := rows.Scan(
			&clip.ID, &clip.Name, &clip.Filename, &clip.FolderID, &clip.FolderPath, &clip.Group,
			&clip.MediaType, &clip.DriveLink, &clip.DownloadLink, &tagsJSON,
			&clip.Source, &clip.Category, &clip.ExternalURL, &clip.Duration, &clip.Metadata,
			&createdAtStr, &updatedAtStr,
		)
		if err != nil {
			return nil, err
		}
		
		json.Unmarshal([]byte(tagsJSON), &clip.Tags)
		clip.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAtStr)
		clip.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAtStr)
		
		clips = append(clips, &clip)
	}
	
	return clips, nil
}

// uniqueStrings returns a slice of unique strings
func uniqueStrings(input []string) []string {
	keys := make(map[string]bool)
	var list []string
	for _, entry := range input {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}

// SaveCheckpoint saves an indexing checkpoint
func (r *Repository) SaveCheckpoint(ctx context.Context, cp *models.IndexingCheckpoint) error {
	query := `
		INSERT INTO indexing_checkpoints (id, path, last_indexed_at, metadata)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			path = excluded.path,
			last_indexed_at = excluded.last_indexed_at,
			metadata = excluded.metadata
	`
	_, err := r.db.ExecContext(ctx, query, cp.ID, cp.Path, cp.LastIndexedAt.Format(time.RFC3339), cp.Metadata)
	return err
}

// GetCheckpoint retrieves a checkpoint
func (r *Repository) GetCheckpoint(ctx context.Context, id string) (*models.IndexingCheckpoint, error) {
	query := `SELECT id, path, last_indexed_at, metadata FROM indexing_checkpoints WHERE id = ?`
	var cp models.IndexingCheckpoint
	var lastIndexedStr string
	
	err := r.db.QueryRowContext(ctx, query, id).Scan(&cp.ID, &cp.Path, &lastIndexedStr, &cp.Metadata)
	if err != nil {
		return nil, err
	}
	
	cp.LastIndexedAt, _ = time.Parse(time.RFC3339, lastIndexedStr)
	return &cp, nil
}
