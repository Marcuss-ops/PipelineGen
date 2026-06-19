package assetrepo

import (
	"context"
	"database/sql"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// VersionRepository implements asset.VersionRepository.

func (r *Repository) GetCurrentVersion(ctx context.Context, assetID string) (*asset.Version, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, asset_id, version_number, source_uri, file_hash, file_size_bytes, mime_type, metadata_json, created_at
		FROM asset_versions WHERE asset_id = ? ORDER BY version_number DESC LIMIT 1
	`, assetID)
	return scanVersion(row)
}

func (r *Repository) ListVersions(ctx context.Context, assetID string) ([]asset.Version, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, asset_id, version_number, source_uri, file_hash, file_size_bytes, mime_type, metadata_json, created_at
		FROM asset_versions WHERE asset_id = ? ORDER BY version_number DESC
	`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []asset.Version
	for rows.Next() {
		ver, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ver)
	}
	return out, rows.Err()
}

func (r *Repository) AppendVersion(ctx context.Context, v *asset.Version) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var nextVer int
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_number), 0) + 1 FROM asset_versions WHERE asset_id = ?`, v.AssetID).Scan(&nextVer)
	if err != nil {
		return err
	}

	nowStr := timeutil.FormatRFC3339(time.Now())
	_, err = tx.ExecContext(ctx, `
		INSERT INTO asset_versions (asset_id, version_number, source_uri, file_hash, file_size_bytes, mime_type, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, v.AssetID, nextVer, v.SourceURI, v.FileHash, v.FileSizeBytes, v.MimeType, v.MetadataJSON, nowStr)
	if err != nil {
		return err
	}

	v.VersionNumber = nextVer
	return tx.Commit()
}

func scanVersion(s scanner) (*asset.Version, error) {
	var v asset.Version
	var sourceURI, fileHash, mimeType, metaJSON, createdAtStr sql.NullString
	err := s.Scan(
		&v.ID, &v.AssetID, &v.VersionNumber, &sourceURI, &fileHash, &v.FileSizeBytes, &mimeType, &metaJSON, &createdAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v.SourceURI = sourceURI.String
	v.FileHash = fileHash.String
	v.MimeType = mimeType.String
	v.MetadataJSON = metaJSON.String
	v.CreatedAt = timeutil.ParseRFC3339(createdAtStr.String)
	return &v, nil
}
