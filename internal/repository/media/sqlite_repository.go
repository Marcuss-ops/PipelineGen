package media

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	coremedia "velox/go-master/internal/core/media"
)

var normalizeRe = regexp.MustCompile(`[^a-z0-9_-]+`)

type SQLiteRepository struct {
	db *sql.DB
}

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{db: db}
}

func generateID() string {
	return uuid.New().String()
}

func normalizeName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ToLower(name)
	return normalizeRe.ReplaceAllString(name, "")
}

// coremedia.Item operations
func (r *SQLiteRepository) CreateItem(ctx context.Context, item *coremedia.Item) error {
	if item.ID == "" {
		item.ID = generateID()
	}
	now := time.Now()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}

	query := `
		INSERT INTO media_items (id, workspace_id, project_id, source_id, source_kind, media_type, status, title, description, external_id, external_url, duration_seconds, file_hash, metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := r.db.ExecContext(ctx, query,
		item.ID, item.WorkspaceID, item.ProjectID, item.SourceID,
		string(item.SourceKind), string(item.MediaType), string(item.Status),
		item.Title, item.Description, item.ExternalID, item.ExternalURL,
		item.DurationSecs, item.FileHash, item.MetadataJSON,
		item.CreatedAt.Format(time.RFC3339), item.UpdatedAt.Format(time.RFC3339))
	return err
}

func (r *SQLiteRepository) UpdateItem(ctx context.Context, item *coremedia.Item) error {
	item.UpdatedAt = time.Now()
	query := `
		UPDATE media_items SET workspace_id=?, project_id=?, source_id=?, source_kind=?, media_type=?, status=?, title=?, description=?, external_id=?, external_url=?, duration_seconds=?, file_hash=?, metadata_json=?, updated_at=?
		WHERE id=?
	`
	_, err := r.db.ExecContext(ctx, query,
		item.WorkspaceID, item.ProjectID, item.SourceID,
		string(item.SourceKind), string(item.MediaType), string(item.Status),
		item.Title, item.Description, item.ExternalID, item.ExternalURL,
		item.DurationSecs, item.FileHash, item.MetadataJSON,
		item.UpdatedAt.Format(time.RFC3339), item.ID)
	return err
}

func (r *SQLiteRepository) GetItem(ctx context.Context, id string) (*coremedia.Item, error) {
	query := `SELECT id, workspace_id, project_id, source_id, source_kind, media_type, status, title, description, external_id, external_url, duration_seconds, file_hash, metadata_json, created_at, updated_at FROM media_items WHERE id=?`
	row := r.db.QueryRowContext(ctx, query, id)
	item := &coremedia.Item{}
	var sourceKind, mediaType, status, createdAt, updatedAt string
	err := row.Scan(&item.ID, &item.WorkspaceID, &item.ProjectID, &item.SourceID, &sourceKind, &mediaType, &status, &item.Title, &item.Description, &item.ExternalID, &item.ExternalURL, &item.DurationSecs, &item.FileHash, &item.MetadataJSON, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	item.SourceKind = coremedia.SourceKind(sourceKind)
	item.MediaType = coremedia.MediaType(mediaType)
	item.Status = coremedia.MediaStatus(status)
	item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return item, nil
}

func (r *SQLiteRepository) DeleteItem(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM media_items WHERE id=?`, id)
	return err
}

func (r *SQLiteRepository) ListItems(ctx context.Context, query coremedia.SearchQuery) ([]*coremedia.Item, error) {
	sqlQuery := `SELECT id, workspace_id, project_id, source_id, source_kind, media_type, status, title, description, external_id, external_url, duration_seconds, file_hash, metadata_json, created_at, updated_at FROM media_items WHERE 1=1`
	args := []interface{}{}

	if query.WorkspaceID != "" {
		sqlQuery += ` AND workspace_id=?`
		args = append(args, query.WorkspaceID)
	}
	if query.ProjectID != "" {
		sqlQuery += ` AND project_id=?`
		args = append(args, query.ProjectID)
	}
	if len(query.SourceKinds) > 0 {
		sqlQuery += ` AND source_kind IN (?`
		for i := 1; i < len(query.SourceKinds); i++ {
			sqlQuery += `,?`
		}
		sqlQuery += `)`
		for _, k := range query.SourceKinds {
			args = append(args, string(k))
		}
	}
	if len(query.MediaTypes) > 0 {
		sqlQuery += ` AND media_type IN (?`
		for i := 1; i < len(query.MediaTypes); i++ {
			sqlQuery += `,?`
		}
		sqlQuery += `)`
		for _, t := range query.MediaTypes {
			args = append(args, string(t))
		}
	}
	if len(query.Statuses) > 0 {
		sqlQuery += ` AND status IN (?`
		for i := 1; i < len(query.Statuses); i++ {
			sqlQuery += `,?`
		}
		sqlQuery += `)`
		for _, s := range query.Statuses {
			args = append(args, string(s))
		}
	}
	if query.Query != "" {
		sqlQuery += ` AND (title LIKE ? OR description LIKE ?)`
		like := "%" + query.Query + "%"
		args = append(args, like, like)
	}
	sqlQuery += ` ORDER BY created_at DESC`
	if query.Limit > 0 {
		sqlQuery += ` LIMIT ?`
		args = append(args, query.Limit)
	}
	if query.Offset > 0 {
		sqlQuery += ` OFFSET ?`
		args = append(args, query.Offset)
	}

	rows, err := r.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*coremedia.Item
	for rows.Next() {
		item := &coremedia.Item{}
		var sourceKind, mediaType, status, createdAt, updatedAt string
		err := rows.Scan(&item.ID, &item.WorkspaceID, &item.ProjectID, &item.SourceID, &sourceKind, &mediaType, &status, &item.Title, &item.Description, &item.ExternalID, &item.ExternalURL, &item.DurationSecs, &item.FileHash, &item.MetadataJSON, &createdAt, &updatedAt)
		if err != nil {
			return nil, err
		}
		item.SourceKind = coremedia.SourceKind(sourceKind)
		item.MediaType = coremedia.MediaType(mediaType)
		item.Status = coremedia.MediaStatus(status)
		item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

// coremedia.File operations
func (r *SQLiteRepository) CreateFile(ctx context.Context, file *coremedia.File) error {
	if file.ID == "" {
		file.ID = generateID()
	}
	now := time.Now()
	if file.CreatedAt.IsZero() {
		file.CreatedAt = now
	}
	if file.UpdatedAt.IsZero() {
		file.UpdatedAt = now
	}

	query := `
		INSERT INTO media_files (id, media_item_id, location_kind, uri, mime_type, width, height, duration_seconds, file_size_bytes, file_hash, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := r.db.ExecContext(ctx, query,
		file.ID, file.MediaItemID, string(file.LocationKind), file.URI, file.MimeType,
		file.Width, file.Height, file.DurationSecs, file.FileSizeBytes, file.FileHash,
		string(file.Status), file.CreatedAt.Format(time.RFC3339), file.UpdatedAt.Format(time.RFC3339))
	return err
}

func (r *SQLiteRepository) UpdateFile(ctx context.Context, file *coremedia.File) error {
	file.UpdatedAt = time.Now()
	query := `
		UPDATE media_files SET media_item_id=?, location_kind=?, uri=?, mime_type=?, width=?, height=?, duration_seconds=?, file_size_bytes=?, file_hash=?, status=?, updated_at=?
		WHERE id=?
	`
	_, err := r.db.ExecContext(ctx, query,
		file.MediaItemID, string(file.LocationKind), file.URI, file.MimeType,
		file.Width, file.Height, file.DurationSecs, file.FileSizeBytes, file.FileHash,
		string(file.Status), file.UpdatedAt.Format(time.RFC3339), file.ID)
	return err
}

func (r *SQLiteRepository) ListFiles(ctx context.Context, mediaItemID string) ([]*coremedia.File, error) {
	query := `SELECT id, media_item_id, location_kind, uri, mime_type, width, height, duration_seconds, file_size_bytes, file_hash, status, created_at, updated_at FROM media_files WHERE media_item_id=?`
	rows, err := r.db.QueryContext(ctx, query, mediaItemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []*coremedia.File
	for rows.Next() {
		file := &coremedia.File{}
		var locationKind, status, createdAt, updatedAt string
		err := rows.Scan(&file.ID, &file.MediaItemID, &locationKind, &file.URI, &file.MimeType, &file.Width, &file.Height, &file.DurationSecs, &file.FileSizeBytes, &file.FileHash, &status, &createdAt, &updatedAt)
		if err != nil {
			return nil, err
		}
		file.LocationKind = coremedia.LocationKind(locationKind)
		file.Status = coremedia.MediaStatus(status)
		file.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		file.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		files = append(files, file)
	}
	return files, rows.Err()
}

// coremedia.Source operations
func (r *SQLiteRepository) CreateSource(ctx context.Context, source *coremedia.Source) error {
	if source.ID == "" {
		source.ID = generateID()
	}
	now := time.Now()
	if source.CreatedAt.IsZero() {
		source.CreatedAt = now
	}
	if source.UpdatedAt.IsZero() {
		source.UpdatedAt = now
	}

	query := `
		INSERT INTO media_sources (id, workspace_id, kind, name, external_id, external_url, metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := r.db.ExecContext(ctx, query,
		source.ID, source.WorkspaceID, string(source.Kind), source.Name,
		source.ExternalID, source.ExternalURL, source.MetadataJSON,
		source.CreatedAt.Format(time.RFC3339), source.UpdatedAt.Format(time.RFC3339))
	return err
}

func (r *SQLiteRepository) GetSource(ctx context.Context, id string) (*coremedia.Source, error) {
	query := `SELECT id, workspace_id, kind, name, external_id, external_url, metadata_json, created_at, updated_at FROM media_sources WHERE id=?`
	row := r.db.QueryRowContext(ctx, query, id)
	source := &coremedia.Source{}
	var kind, createdAt, updatedAt string
	err := row.Scan(&source.ID, &source.WorkspaceID, &kind, &source.Name, &source.ExternalID, &source.ExternalURL, &source.MetadataJSON, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	source.Kind = coremedia.SourceKind(kind)
	source.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	source.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return source, nil
}

func (r *SQLiteRepository) ListSources(ctx context.Context, workspaceID string) ([]*coremedia.Source, error) {
	query := `SELECT id, workspace_id, kind, name, external_id, external_url, metadata_json, created_at, updated_at FROM media_sources WHERE workspace_id=?`
	rows, err := r.db.QueryContext(ctx, query, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []*coremedia.Source
	for rows.Next() {
		source := &coremedia.Source{}
		var kind, createdAt, updatedAt string
		err := rows.Scan(&source.ID, &source.WorkspaceID, &kind, &source.Name, &source.ExternalID, &source.ExternalURL, &source.MetadataJSON, &createdAt, &updatedAt)
		if err != nil {
			return nil, err
		}
		source.Kind = coremedia.SourceKind(kind)
		source.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		source.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

// coremedia.Tag operations
func (r *SQLiteRepository) AddTags(ctx context.Context, mediaItemID string, tagNames []string) error {
	for _, name := range tagNames {
		normalized := normalizeName(name)

		var tagID string
		err := r.db.QueryRowContext(ctx, `SELECT id FROM media_tags WHERE workspace_id=(SELECT workspace_id FROM media_items WHERE id=?) AND normalized_name=?`, mediaItemID, normalized).Scan(&tagID)
		if err != nil {
			tagID = generateID()
			_, err = r.db.ExecContext(ctx, `INSERT INTO media_tags (id, workspace_id, name, normalized_name, created_at) VALUES (?, (SELECT workspace_id FROM media_items WHERE id=?), ?, ?, ?)`,
				tagID, mediaItemID, name, normalized, time.Now().Format(time.RFC3339))
			if err != nil {
				return err
			}
		}

		_, err = r.db.ExecContext(ctx, `INSERT OR IGNORE INTO media_item_tags (media_item_id, tag_id) VALUES (?, ?)`, mediaItemID, tagID)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *SQLiteRepository) RemoveTags(ctx context.Context, mediaItemID string, tagNames []string) error {
	for _, name := range tagNames {
		normalized := normalizeName(name)
		_, err := r.db.ExecContext(ctx, `DELETE FROM media_item_tags WHERE media_item_id=? AND tag_id=(SELECT id FROM media_tags WHERE workspace_id=(SELECT workspace_id FROM media_items WHERE id=?) AND normalized_name=?)`, mediaItemID, mediaItemID, normalized)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *SQLiteRepository) ListTags(ctx context.Context, workspaceID string) ([]*coremedia.Tag, error) {
	query := `SELECT id, workspace_id, name, normalized_name, created_at FROM media_tags WHERE workspace_id=?`
	rows, err := r.db.QueryContext(ctx, query, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []*coremedia.Tag
	for rows.Next() {
		tag := &coremedia.Tag{}
		var createdAt string
		err := rows.Scan(&tag.ID, &tag.WorkspaceID, &tag.Name, &tag.NormalizedName, &createdAt)
		if err != nil {
			return nil, err
		}
		tag.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

// coremedia.Usage operations
func (r *SQLiteRepository) RecordUsage(ctx context.Context, usage *coremedia.Usage) error {
	if usage.ID == "" {
		usage.ID = generateID()
	}
	query := `
		INSERT INTO media_usage (id, media_item_id, project_id, script_id, usage_kind, used_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	_, err := r.db.ExecContext(ctx, query,
		usage.ID, usage.MediaItemID, usage.ProjectID, usage.ScriptID,
		string(usage.UsageKind), usage.UsedAt.Format(time.RFC3339))
	return err
}

func (r *SQLiteRepository) ListUsage(ctx context.Context, mediaItemID string) ([]*coremedia.Usage, error) {
	query := `SELECT id, media_item_id, project_id, script_id, usage_kind, used_at FROM media_usage WHERE media_item_id=?`
	rows, err := r.db.QueryContext(ctx, query, mediaItemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var usages []*coremedia.Usage
	for rows.Next() {
		usage := &coremedia.Usage{}
		var usageKind, usedAt string
		err := rows.Scan(&usage.ID, &usage.MediaItemID, &usage.ProjectID, &usage.ScriptID, &usageKind, &usedAt)
		if err != nil {
			return nil, err
		}
		usage.UsageKind = coremedia.UsageKind(usageKind)
		usage.UsedAt, _ = time.Parse(time.RFC3339, usedAt)
		usages = append(usages, usage)
	}
	return usages, rows.Err()
}
