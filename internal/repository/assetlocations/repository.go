// Package assetlocations provides the read/write layer for the
// asset_locations table, which records where an asset physically lives.
// Each asset can have multiple locations (local file, Drive, object storage)
// with one designated as primary.
//
// This table normalises what was previously inlined in media_assets
// (drive_link, local_path, download_link, drive_file_id). The old columns
// remain for backward compatibility and will be deprecated in a follow-up.
package assetlocations

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// LocationKind categorises where an asset physically lives.
type LocationKind string

const (
	LocationLocal         LocationKind = "local"
	LocationDrive         LocationKind = "drive"
	LocationObjectStorage LocationKind = "object_storage"
)

// AssetLocation represents a single asset_locations row.
type AssetLocation struct {
	ID            int64
	AssetID       string
	LocationKind  LocationKind
	URI           string // local path, drive_file_id, s3://...
	MimeType      string
	FileSizeBytes int64
	FileHash      string
	IsPrimary     bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Repository wraps SQL access to the asset_locations table.
type Repository struct {
	db *sql.DB
}

// NewRepository creates a Repository backed by db.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Upsert inserts or replaces a location record. The (asset_id, location_kind)
// unique constraint ensures at most one record per location kind per asset.
// On conflict, all non-PK fields are updated.
func (r *Repository) Upsert(ctx context.Context, loc AssetLocation) error {
	now := timeutil.FormatRFC3339(time.Now())
	isPrimary := 0
	if loc.IsPrimary {
		isPrimary = 1
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO asset_locations
			(asset_id, location_kind, uri, mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(asset_id, location_kind) DO UPDATE SET
			uri = excluded.uri,
			mime_type = excluded.mime_type,
			file_size_bytes = excluded.file_size_bytes,
			file_hash = excluded.file_hash,
			is_primary = excluded.is_primary,
			updated_at = excluded.updated_at
	`, loc.AssetID, string(loc.LocationKind), loc.URI, loc.MimeType,
		loc.FileSizeBytes, loc.FileHash, isPrimary, now, now)
	if err != nil {
		return fmt.Errorf("assetlocations.Upsert(%s, %s): %w", loc.AssetID, loc.LocationKind, err)
	}
	return nil
}

// GetByAssetID returns all location records for an asset, ordered by
// is_primary DESC so the primary location comes first.
func (r *Repository) GetByAssetID(ctx context.Context, assetID string) ([]AssetLocation, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, asset_id, location_kind, uri, mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at
		FROM asset_locations
		WHERE asset_id = ?
		ORDER BY is_primary DESC, location_kind
	`, assetID)
	if err != nil {
		return nil, fmt.Errorf("assetlocations.GetByAssetID(%s): %w", assetID, err)
	}
	defer rows.Close()
	return scanLocations(rows)
}

// GetPrimary returns the primary location for an asset (is_primary=1), or nil
// if the asset has no primary location.
func (r *Repository) GetPrimary(ctx context.Context, assetID string) (*AssetLocation, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, asset_id, location_kind, uri, mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at
		FROM asset_locations
		WHERE asset_id = ? AND is_primary = 1
	`, assetID)
	loc, err := scanLocation(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("assetlocations.GetPrimary(%s): %w", assetID, err)
	}
	return loc, nil
}

// SetPrimary designates a location as the primary for its asset, unmarking
// any previous primary. Runs in a transaction so there's never a moment
// where the asset has 0 or 2 primary locations.
func (r *Repository) SetPrimary(ctx context.Context, assetID string, kind LocationKind) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assetlocations.SetPrimary(%s, %s) begin: %w", assetID, kind, err)
	}
	defer tx.Rollback()

	now := timeutil.FormatRFC3339(time.Now())

	// Unset any existing primary.
	if _, err := tx.ExecContext(ctx, `
		UPDATE asset_locations SET is_primary = 0, updated_at = ?
		WHERE asset_id = ? AND is_primary = 1
	`, now, assetID); err != nil {
		return fmt.Errorf("assetlocations.SetPrimary unset: %w", err)
	}

	// Set the new primary.
	result, err := tx.ExecContext(ctx, `
		UPDATE asset_locations SET is_primary = 1, updated_at = ?
		WHERE asset_id = ? AND location_kind = ?
	`, now, assetID, string(kind))
	if err != nil {
		return fmt.Errorf("assetlocations.SetPrimary set: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("assetlocations.SetPrimary(%s, %s): location not found", assetID, kind)
	}

	return tx.Commit()
}

// Delete removes a location record for an asset.
func (r *Repository) Delete(ctx context.Context, assetID string, kind LocationKind) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM asset_locations WHERE asset_id = ? AND location_kind = ?
	`, assetID, string(kind))
	if err != nil {
		return fmt.Errorf("assetlocations.Delete(%s, %s): %w", assetID, kind, err)
	}
	return nil
}

// DeleteAll removes all location records for an asset.
func (r *Repository) DeleteAll(ctx context.Context, assetID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM asset_locations WHERE asset_id = ?`, assetID)
	if err != nil {
		return fmt.Errorf("assetlocations.DeleteAll(%s): %w", assetID, err)
	}
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────

func scanLocation(s interface{ Scan(dest ...any) error }) (*AssetLocation, error) {
	loc := &AssetLocation{}
	var isPrimary int
	var createdAtStr, updatedAtStr string
	err := s.Scan(&loc.ID, &loc.AssetID, (*string)(&loc.LocationKind), &loc.URI,
		&loc.MimeType, &loc.FileSizeBytes, &loc.FileHash, &isPrimary,
		&createdAtStr, &updatedAtStr)
	if err != nil {
		return nil, err
	}
	loc.IsPrimary = isPrimary == 1
	loc.CreatedAt = timeutil.ParseRFC3339(createdAtStr)
	loc.UpdatedAt = timeutil.ParseRFC3339(updatedAtStr)
	return loc, nil
}

func scanLocations(rows *sql.Rows) ([]AssetLocation, error) {
	var out []AssetLocation
	for rows.Next() {
		loc, err := scanLocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *loc)
	}
	return out, rows.Err()
}
