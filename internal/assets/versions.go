package assets

import (
	"context"
	"database/sql"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

type versionRepositoryAdapter struct {
	store *AssetStoreSQLite
}

func (a *versionRepositoryAdapter) GetCurrent(ctx context.Context, assetID string) (*Version, error) {
	return a.store.GetCurrentVersion(ctx, assetID)
}

func (a *versionRepositoryAdapter) List(ctx context.Context, assetID string) ([]Version, error) {
	return a.store.ListVersions(ctx, assetID)
}

func (a *versionRepositoryAdapter) Append(ctx context.Context, v *Version) error {
	return a.store.AppendVersion(ctx, v)
}

// VersionRepository returns the VersionRepository adapter for the store.
func (s *AssetStoreSQLite) VersionRepository() VersionRepository {
	return &versionRepositoryAdapter{store: s}
}

// GetCurrentVersion returns the latest version for an asset.
func (s *AssetStoreSQLite) GetCurrentVersion(ctx context.Context, assetID string) (*Version, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, asset_id, version_number, source_uri, file_hash, file_size_bytes, mime_type, metadata_json, created_at
		FROM asset_versions
		WHERE asset_id = ?
		ORDER BY version_number DESC LIMIT 1
	`, assetID)
	return scanVersion(row)
}

// ListVersions returns all versions for an asset.
func (s *AssetStoreSQLite) ListVersions(ctx context.Context, assetID string) ([]Version, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, asset_id, version_number, source_uri, file_hash, file_size_bytes, mime_type, metadata_json, created_at
		FROM asset_versions
		WHERE asset_id = ?
		ORDER BY version_number DESC
	`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Version
	for rows.Next() {
		ver, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ver)
	}
	return out, rows.Err()
}

func (s *AssetStoreSQLite) AppendVersion(ctx context.Context, v *Version) error {
	tx, err := s.db.BeginTx(ctx, nil)
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
		INSERT INTO asset_versions
			(asset_id, version_number, source_uri, file_hash, file_size_bytes, mime_type, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, v.AssetID, nextVer, v.SourceURI, v.FileHash, v.FileSizeBytes, v.MimeType, v.MetadataJSON, nowStr)
	if err != nil {
		return err
	}

	v.VersionNumber = nextVer
	return tx.Commit()
}

func scanVersion(scanner interface{ Scan(dest ...any) error }) (*Version, error) {
	var v Version
	var sourceURI, fileHash, mimeType, metaJSON, createdAtStr sql.NullString
	err := scanner.Scan(
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
