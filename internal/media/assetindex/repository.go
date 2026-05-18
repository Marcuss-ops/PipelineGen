package assetindex

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"velox/go-master/internal/pkg/timeutil"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Upsert(ctx context.Context, rec *AssetRecord) error {
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = time.Now().UTC()
	}

	query := `
        INSERT INTO asset_index (
            asset_id, asset_type, source, source_id, operation_key,
            group_name, subfolder, local_path, drive_link, download_link,
            file_hash, content_hash, status, metadata_json, created_at, updated_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(asset_id) DO UPDATE SET
            asset_type = excluded.asset_type,
            source = excluded.source,
            source_id = excluded.source_id,
            operation_key = excluded.operation_key,
            group_name = excluded.group_name,
            subfolder = excluded.subfolder,
            local_path = excluded.local_path,
            drive_link = excluded.drive_link,
            download_link = excluded.download_link,
            file_hash = excluded.file_hash,
            content_hash = excluded.content_hash,
            status = excluded.status,
            metadata_json = excluded.metadata_json,
            updated_at = excluded.updated_at
    `

	_, err := r.db.ExecContext(ctx, query,
		rec.AssetID,
		rec.AssetType,
		rec.Source,
		rec.SourceID,
		rec.OperationKey,
		rec.GroupName,
		rec.Subfolder,
		rec.LocalPath,
		rec.DriveLink,
		rec.DownloadLink,
		rec.FileHash,
		rec.ContentHash,
		rec.Status,
		rec.Metadata,
		rec.CreatedAt.Format(time.RFC3339),
		rec.UpdatedAt.Format(time.RFC3339),
	)

	return err
}

func (r *Repository) FindByContentHash(ctx context.Context, hash string) (*AssetRecord, error) {
	query := `
        SELECT asset_id, asset_type, source, source_id, operation_key,
               group_name, subfolder, local_path, drive_link, download_link,
               file_hash, content_hash, status, metadata_json, created_at, updated_at
        FROM asset_index
        WHERE content_hash = ?
        LIMIT 1
    `

	rec := &AssetRecord{}
	var createdAtStr, updatedAtStr string
	err := r.db.QueryRowContext(ctx, query, hash).Scan(
		&rec.AssetID,
		&rec.AssetType,
		&rec.Source,
		&rec.SourceID,
		&rec.OperationKey,
		&rec.GroupName,
		&rec.Subfolder,
		&rec.LocalPath,
		&rec.DriveLink,
		&rec.DownloadLink,
		&rec.FileHash,
		&rec.ContentHash,
		&rec.Status,
		&rec.Metadata,
		&createdAtStr,
		&updatedAtStr,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	rec.CreatedAt = timeutil.ParseRFC3339(createdAtStr)
	rec.UpdatedAt = timeutil.ParseRFC3339(updatedAtStr)

	return rec, nil
}

func (r *Repository) FindReadyByGroup(ctx context.Context, group, subfolder string) ([]*AssetRecord, error) {
	var conditions []string
	var args []interface{}

	conditions = append(conditions, "status = ?")
	args = append(args, "ready")

	if group != "" {
		conditions = append(conditions, "group_name = ?")
		args = append(args, group)
	}

	if subfolder != "" {
		conditions = append(conditions, "subfolder = ?")
		args = append(args, subfolder)
	}

	query := `
        SELECT asset_id, asset_type, source, source_id, operation_key,
               group_name, subfolder, local_path, drive_link, download_link,
               file_hash, content_hash, status, metadata_json, created_at, updated_at
        FROM asset_index
        WHERE ` + strings.Join(conditions, " AND ") + `
        ORDER BY created_at DESC
    `

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*AssetRecord
	for rows.Next() {
		rec := &AssetRecord{}
		var createdAtStr, updatedAtStr string
		err := rows.Scan(
			&rec.AssetID,
			&rec.AssetType,
			&rec.Source,
			&rec.SourceID,
			&rec.OperationKey,
			&rec.GroupName,
			&rec.Subfolder,
			&rec.LocalPath,
			&rec.DriveLink,
			&rec.DownloadLink,
			&rec.FileHash,
			&rec.ContentHash,
			&rec.Status,
			&rec.Metadata,
			&createdAtStr,
			&updatedAtStr,
		)
		if err != nil {
			return nil, err
		}
		rec.CreatedAt = timeutil.ParseRFC3339(createdAtStr)
		rec.UpdatedAt = timeutil.ParseRFC3339(updatedAtStr)
		results = append(results, rec)
	}

	return results, nil
}

func (r *Repository) FindBySource(ctx context.Context, source, sourceID string) (*AssetRecord, error) {
	query := `
        SELECT asset_id, asset_type, source, source_id, operation_key,
               group_name, subfolder, local_path, drive_link, download_link,
               file_hash, content_hash, status, metadata_json, created_at, updated_at
        FROM asset_index
        WHERE source = ? AND source_id = ?
        LIMIT 1
    `
	rec := &AssetRecord{}
	var createdAtStr, updatedAtStr string
	err := r.db.QueryRowContext(ctx, query, source, sourceID).Scan(
		&rec.AssetID,
		&rec.AssetType,
		&rec.Source,
		&rec.SourceID,
		&rec.OperationKey,
		&rec.GroupName,
		&rec.Subfolder,
		&rec.LocalPath,
		&rec.DriveLink,
		&rec.DownloadLink,
		&rec.FileHash,
		&rec.ContentHash,
		&rec.Status,
		&rec.Metadata,
		&createdAtStr,
		&updatedAtStr,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	rec.CreatedAt = timeutil.ParseRFC3339(createdAtStr)
	rec.UpdatedAt = timeutil.ParseRFC3339(updatedAtStr)

	return rec, nil
}

func (r *Repository) UpdateStatus(ctx context.Context, assetID, status string) error {
	query := `UPDATE asset_index SET status = ?, updated_at = ? WHERE asset_id = ?`
	_, err := r.db.ExecContext(ctx, query, status, time.Now().UTC().Format(time.RFC3339), assetID)
	return err
}

func (r *Repository) Delete(ctx context.Context, assetID string) error {
	query := `DELETE FROM asset_index WHERE asset_id = ?`
	_, err := r.db.ExecContext(ctx, query, assetID)
	return err
}

type Stats struct {
	Total    int            `json:"total"`
	ByType   map[string]int `json:"by_type"`
	ByStatus map[string]int `json:"by_status"`
}

func (r *Repository) GetStats(ctx context.Context) (*Stats, error) {
	stats := &Stats{
		ByType:   make(map[string]int),
		ByStatus: make(map[string]int),
	}

	// Get total count
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_index`).Scan(&stats.Total)
	if err != nil {
		return nil, err
	}

	// Get count by type
	rows, err := r.db.QueryContext(ctx, `SELECT asset_type, COUNT(*) as cnt FROM asset_index GROUP BY asset_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var assetType string
		var cnt int
		if err := rows.Scan(&assetType, &cnt); err != nil {
			return nil, err
		}
		stats.ByType[assetType] = cnt
	}

	// Get count by status
	rows, err = r.db.QueryContext(ctx, `SELECT status, COUNT(*) as cnt FROM asset_index GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var cnt int
		if err := rows.Scan(&status, &cnt); err != nil {
			return nil, err
		}
		stats.ByStatus[status] = cnt
	}

	return stats, nil
}

func (r *Repository) ListAll(ctx context.Context) ([]*AssetRecord, error) {
	query := `
        SELECT asset_id, asset_type, source, source_id, operation_key,
               group_name, subfolder, local_path, drive_link, download_link,
               file_hash, content_hash, status, metadata_json, created_at, updated_at
        FROM asset_index
    `

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*AssetRecord
	for rows.Next() {
		rec := &AssetRecord{}
		var createdAtStr, updatedAtStr string
		err := rows.Scan(
			&rec.AssetID,
			&rec.AssetType,
			&rec.Source,
			&rec.SourceID,
			&rec.OperationKey,
			&rec.GroupName,
			&rec.Subfolder,
			&rec.LocalPath,
			&rec.DriveLink,
			&rec.DownloadLink,
			&rec.FileHash,
			&rec.ContentHash,
			&rec.Status,
			&rec.Metadata,
			&createdAtStr,
			&updatedAtStr,
		)
		if err != nil {
			return nil, err
		}
		rec.CreatedAt = timeutil.ParseRFC3339(createdAtStr)
		rec.UpdatedAt = timeutil.ParseRFC3339(updatedAtStr)
		results = append(results, rec)
	}

	return results, nil
}
