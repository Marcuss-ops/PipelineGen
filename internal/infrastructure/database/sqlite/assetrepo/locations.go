package assetrepo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// LocationRepository implements asset.LocationRepository.
func (r *Repository) UpsertLocation(ctx context.Context, loc *asset.Location) error {
	now := timeutil.FormatRFC3339(time.Now())
	isPrimary := 0
	if loc.IsPrimary {
		isPrimary = 1
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO asset_locations
			(asset_id, location_kind, uri, external_id, web_view_link, download_url,
			 mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(asset_id, location_kind) DO UPDATE SET
			uri = excluded.uri, external_id = excluded.external_id,
			web_view_link = excluded.web_view_link, download_url = excluded.download_url,
			mime_type = excluded.mime_type, file_size_bytes = excluded.file_size_bytes,
			file_hash = excluded.file_hash, is_primary = excluded.is_primary,
			updated_at = excluded.updated_at
	`, loc.AssetID, string(loc.LocationKind), loc.URI, loc.ExternalID,
		loc.AccessURL, loc.DownloadURL,
		loc.MimeType, loc.FileSizeBytes, loc.FileHash, isPrimary, now, now)
	if err != nil {
		return fmt.Errorf("assetrepo.UpsertLocation(%s, %s): %w", loc.AssetID, loc.LocationKind, err)
	}
	return nil
}

func (r *Repository) GetPrimaryLocation(ctx context.Context, assetID string) (*asset.Location, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, asset_id, location_kind, uri, external_id, web_view_link, download_url,
		       mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at
		FROM asset_locations WHERE asset_id = ? AND is_primary = 1
	`, assetID)
	return scanLocation(row)
}

func (r *Repository) ListLocationsByAsset(ctx context.Context, assetID string) ([]*asset.Location, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, asset_id, location_kind, uri, external_id, web_view_link, download_url,
		       mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at
		FROM asset_locations WHERE asset_id = ? ORDER BY is_primary DESC, location_kind
	`, assetID)
	if err != nil {
		return nil, fmt.Errorf("assetrepo.ListLocationsByAsset(%s): %w", assetID, err)
	}
	defer rows.Close()

	var out []*asset.Location
	for rows.Next() {
		loc, err := scanLocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, loc)
	}
	return out, rows.Err()
}

func (r *Repository) SetPrimaryLocation(ctx context.Context, assetID string, kind asset.LocationKind) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := timeutil.FormatRFC3339(time.Now())
	_, err = tx.ExecContext(ctx, `UPDATE asset_locations SET is_primary = 0, updated_at = ? WHERE asset_id = ? AND is_primary = 1`, now, assetID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE asset_locations SET is_primary = 1, updated_at = ? WHERE asset_id = ? AND location_kind = ?`, now, assetID, string(kind))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) DeleteLocation(ctx context.Context, assetID string, kind asset.LocationKind) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM asset_locations WHERE asset_id = ? AND location_kind = ?`, assetID, string(kind))
	return err
}

func (r *Repository) DeleteAllLocations(ctx context.Context, assetID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM asset_locations WHERE asset_id = ?`, assetID)
	return err
}

func scanLocation(s scanner) (*asset.Location, error) {
	var loc asset.Location
	var isPrimary int
	var createdAtStr, updatedAtStr string
	err := s.Scan(
		&loc.ID, &loc.AssetID, &loc.LocationKind, &loc.URI, &loc.ExternalID,
		&loc.AccessURL, &loc.DownloadURL, &loc.MimeType, &loc.FileSizeBytes,
		&loc.FileHash, &isPrimary, &createdAtStr, &updatedAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	loc.IsPrimary = isPrimary == 1
	loc.CreatedAt = timeutil.ParseRFC3339(createdAtStr)
	loc.UpdatedAt = timeutil.ParseRFC3339(updatedAtStr)
	return &loc, nil
}
