package media

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"velox/go-master/internal/core/media"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) UpsertAsset(ctx context.Context, asset media.MediaAsset) error {
	now := time.Now().Format(time.RFC3339)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	metadataJSON := asset.MetadataJSON
	if metadataJSON == "" {
		metadataJSON = "{}"
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO media_items (id, workspace_id, project_id, source_id, source_kind, media_type, status,
		                        title, description, category, external_id, external_url, duration_secs, metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			workspace_id = excluded.workspace_id,
			project_id = excluded.project_id,
			source_kind = excluded.source_kind,
			media_type = excluded.media_type,
			status = excluded.status,
			title = excluded.title,
			description = excluded.description,
			category = excluded.category,
			external_id = excluded.external_id,
			external_url = excluded.external_url,
			duration_secs = excluded.duration_secs,
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at
	`, asset.ID, asset.WorkspaceID, asset.ProjectID, asset.SourceID, string(asset.SourceKind),
		string(asset.MediaType), string(asset.Status), asset.Title, asset.Description, asset.Category,
		asset.ExternalID, asset.ExternalURL, asset.DurationSecs, metadataJSON, now, now)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `DELETE FROM media_tags WHERE media_asset_id = ?`, asset.ID)
	if err != nil {
		return err
	}

	if len(asset.Tags) > 0 {
		for _, tag := range asset.Tags {
			_, err = tx.ExecContext(ctx, `INSERT INTO media_tags (media_asset_id, tag) VALUES (?, ?)`, asset.ID, tag)
			if err != nil {
				return err
			}
		}
	}

	_, err = tx.ExecContext(ctx, `DELETE FROM media_files WHERE media_asset_id = ?`, asset.ID)
	if err != nil {
		return err
	}

	if asset.PrimaryFile != nil {
		if err := r.insertFile(ctx, tx, asset.ID, asset.PrimaryFile); err != nil {
			return err
		}
	}
	for _, f := range asset.Files {
		if err := r.insertFile(ctx, tx, asset.ID, &f); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *Repository) insertFile(ctx context.Context, tx *sql.Tx, assetID string, f *media.MediaFile) error {
	now := time.Now().Format(time.RFC3339)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO media_files (id, media_asset_id, location_kind, uri, local_path, drive_link, download_link,
		                        mime_type, width, height, duration_secs, file_size_bytes, file_hash, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, f.ID, assetID, string(f.LocationKind), f.URI, f.LocalPath, f.DriveLink, f.DownloadLink,
		f.MimeType, f.Width, f.Height, f.DurationSecs, f.FileSizeBytes, f.FileHash, string(f.Status), now, now)
	return err
}

func (r *Repository) GetAsset(ctx context.Context, workspaceID, assetID string) (media.MediaAsset, error) {
	var asset media.MediaAsset
	var metadataJSON string

	err := r.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, project_id, source_id, source_kind, media_type, status,
		       title, description, category, external_id, external_url, duration_secs, metadata_json, created_at, updated_at
		FROM media_items
		WHERE id = ? AND workspace_id = ?
		LIMIT 1
	`, assetID, workspaceID).Scan(&asset.ID, &asset.WorkspaceID, &asset.ProjectID, &asset.SourceID, &asset.SourceKind,
		&asset.MediaType, &asset.Status, &asset.Title, &asset.Description, &asset.Category,
		&asset.ExternalID, &asset.ExternalURL, &asset.DurationSecs, &metadataJSON, &asset.CreatedAt, &asset.UpdatedAt)
	if err == sql.ErrNoRows {
		return media.MediaAsset{}, nil
	}
	if err != nil {
		return media.MediaAsset{}, err
	}

	asset.MetadataJSON = metadataJSON
	asset.Tags = r.loadTags(ctx, assetID)
	asset.PrimaryFile, asset.Files = r.loadFiles(ctx, assetID)

	return asset, nil
}

func (r *Repository) SearchAssets(ctx context.Context, query media.SearchQuery) ([]media.MediaAsset, error) {
	where := []string{"1=1"}
	args := []interface{}{}

	if query.WorkspaceID != "" {
		where = append(where, "workspace_id = ?")
		args = append(args, query.WorkspaceID)
	}
	if query.ProjectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, query.ProjectID)
	}
	if len(query.SourceKinds) > 0 {
		placeholders := strings.Repeat("?,", len(query.SourceKinds)-1) + "?"
		where = append(where, "source_kind IN ("+placeholders+")")
		for _, k := range query.SourceKinds {
			args = append(args, string(k))
		}
	}
	if query.Query != "" {
		where = append(where, "LOWER(title) LIKE ?")
		args = append(args, "%"+strings.ToLower(query.Query)+"%")
	}

	sql := `
		SELECT id, workspace_id, project_id, source_id, source_kind, media_type, status,
		       title, description, category, external_id, external_url, duration_secs, metadata_json, created_at, updated_at
		FROM media_items
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?`
	args = append(args, query.Limit, query.Offset)

	rows, err := r.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	assets := []media.MediaAsset{}
	for rows.Next() {
		var a media.MediaAsset
		var metadataJSON string
		err := rows.Scan(&a.ID, &a.WorkspaceID, &a.ProjectID, &a.SourceID, &a.SourceKind,
			&a.MediaType, &a.Status, &a.Title, &a.Description, &a.Category,
			&a.ExternalID, &a.ExternalURL, &a.DurationSecs, &metadataJSON, &a.CreatedAt, &a.UpdatedAt)
		if err != nil {
			return nil, err
		}
		a.MetadataJSON = metadataJSON
		a.Tags = r.loadTags(ctx, a.ID)
		a.PrimaryFile, a.Files = r.loadFiles(ctx, a.ID)
		assets = append(assets, a)
	}
	return assets, rows.Err()
}

func (r *Repository) ListAssets(ctx context.Context, workspaceID, projectID string, limit, offset int) ([]media.MediaAsset, error) {
	where := []string{"1=1"}
	args := []interface{}{}

	if workspaceID != "" {
		where = append(where, "workspace_id = ?")
		args = append(args, workspaceID)
	}
	if projectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, projectID)
	}

	sql := `
		SELECT id, workspace_id, project_id, source_id, source_kind, media_type, status,
		       title, description, category, external_id, external_url, duration_secs, metadata_json, created_at, updated_at
		FROM media_items
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	assets := []media.MediaAsset{}
	for rows.Next() {
		var a media.MediaAsset
		var metadataJSON string
		err := rows.Scan(&a.ID, &a.WorkspaceID, &a.ProjectID, &a.SourceID, &a.SourceKind,
			&a.MediaType, &a.Status, &a.Title, &a.Description, &a.Category,
			&a.ExternalID, &a.ExternalURL, &a.DurationSecs, &metadataJSON, &a.CreatedAt, &a.UpdatedAt)
		if err != nil {
			return nil, err
		}
		a.MetadataJSON = metadataJSON
		a.Tags = r.loadTags(ctx, a.ID)
		a.PrimaryFile, a.Files = r.loadFiles(ctx, a.ID)
		assets = append(assets, a)
	}
	return assets, rows.Err()
}

func (r *Repository) loadTags(ctx context.Context, assetID string) []string {
	rows, err := r.db.QueryContext(ctx, `SELECT tag FROM media_tags WHERE media_asset_id = ?`, assetID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	tags := []string{}
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err == nil {
			tags = append(tags, tag)
		}
	}
	return tags
}

func (r *Repository) loadFiles(ctx context.Context, assetID string) (*media.MediaFile, []media.MediaFile) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, location_kind, uri, local_path, drive_link, download_link,
		       mime_type, width, height, duration_secs, file_size_bytes, file_hash, status
		FROM media_files
		WHERE media_asset_id = ?
		ORDER BY created_at ASC
	`, assetID)
	if err != nil {
		return nil, nil
	}
	defer rows.Close()

	var primary *media.MediaFile
	files := []media.MediaFile{}
	for rows.Next() {
		var f media.MediaFile
		err := rows.Scan(&f.ID, &f.LocationKind, &f.URI, &f.LocalPath, &f.DriveLink, &f.DownloadLink,
			&f.MimeType, &f.Width, &f.Height, &f.DurationSecs, &f.FileSizeBytes, &f.FileHash, &f.Status)
		if err != nil {
			continue
		}
		f.MediaAssetID = assetID
		files = append(files, f)
		if primary == nil {
			primary = &files[len(files)-1]
		}
	}
	return primary, files
}
