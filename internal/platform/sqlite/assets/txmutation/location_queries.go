// Package assets — location SQL queries (Wave C: moved from
// internal/kernel/asset/location.go).
//
// The Location type itself stays in the domain package (domain/asset)
// because callers in application/ refer to it and don't want a type
// duplication. The SQL behaviours + adapter factory + adapter struct
// move to this infra file. The legacy domain struct no longer holds
// any of these receivers or the database/sql import they required.
package txmutation

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── SQL receivers (migrated from location.go) ──────────────────────

// UpsertLocation inserts or replaces a location record and populates loc.ID.
func (s *AssetStoreSQLite) UpsertLocation(ctx context.Context, loc *asset.Location) error {
	now := timeutil.FormatRFC3339(time.Now())
	isPrimary := 0
	if loc.IsPrimary {
		isPrimary = 1
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO asset_locations
			(asset_id, location_kind, uri, external_id, web_view_link, download_url,
			 mime_type, file_size_bytes, legacy_file_md5, is_primary, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(asset_id, location_kind) DO UPDATE SET
			uri = excluded.uri,
			external_id = excluded.external_id,
			web_view_link = excluded.web_view_link,
			download_url = excluded.download_url,
			mime_type = excluded.mime_type,
			file_size_bytes = excluded.file_size_bytes,
			legacy_file_md5 = excluded.legacy_file_md5,
			is_primary = excluded.is_primary,
			updated_at = excluded.updated_at
	`, loc.AssetID, string(loc.LocationKind), loc.URI, loc.ExternalID,
		loc.AccessURL, loc.DownloadURL,
		loc.MimeType, loc.FileSizeBytes, loc.LegacyFileMD5, isPrimary, now, now)
	if err != nil {
		return fmt.Errorf("assets.UpsertLocation(%s, %s): %w", loc.AssetID, loc.LocationKind, err)
	}
	// Populate the generated ID for new records. SQLite's last_insert_rowid
	// returns the row ID of the most recent insert; for UPDATE-via-CONFLICT
	// it returns the existing row ID when sqlite3_last_insert_rowid() is
	// called after the upsert, which is the behavior of the mattn driver.
	loc.ID, err = res.LastInsertId()
	if err != nil {
		return fmt.Errorf("assets.UpsertLocation(%s, %s): last insert id: %w", loc.AssetID, loc.LocationKind, err)
	}
	return nil
}

// GetPrimaryLocation returns the primary location for an asset.
func (s *AssetStoreSQLite) GetPrimaryLocation(ctx context.Context, assetID string) (*asset.Location, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, asset_id, location_kind, uri, external_id, web_view_link, download_url,
		       mime_type, file_size_bytes, legacy_file_md5, is_primary, created_at, updated_at
		FROM asset_locations
		WHERE asset_id = ? AND is_primary = 1
	`, assetID)
	return scanLocation(row)
}

// ListLocationsByAsset returns all locations for an asset.
func (s *AssetStoreSQLite) ListLocationsByAsset(ctx context.Context, assetID string) ([]*asset.Location, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, asset_id, location_kind, uri, external_id, web_view_link, download_url,
		       mime_type, file_size_bytes, legacy_file_md5, is_primary, created_at, updated_at
		FROM asset_locations
		WHERE asset_id = ?
		ORDER BY is_primary DESC, location_kind
	`, assetID)
	if err != nil {
		return nil, fmt.Errorf("assets.ListLocationsByAsset(%s): %w", assetID, err)
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

// SetPrimaryLocation sets the primary location kind for an asset.
func (s *AssetStoreSQLite) SetPrimaryLocation(ctx context.Context, assetID string, kind asset.LocationKind) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := timeutil.FormatRFC3339(time.Now())

	_, err = tx.ExecContext(ctx, `
		UPDATE asset_locations SET is_primary = 0, updated_at = ?
		WHERE asset_id = ? AND is_primary = 1
	`, now, assetID)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE asset_locations SET is_primary = 1, updated_at = ?
		WHERE asset_id = ? AND location_kind = ?
	`, now, assetID, string(kind))
	if err != nil {
		return err
	}

	return tx.Commit()
}

// DeleteLocation removes a location for an asset.
func (s *AssetStoreSQLite) DeleteLocation(ctx context.Context, assetID string, kind asset.LocationKind) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM asset_locations WHERE asset_id = ? AND location_kind = ?
	`, assetID, string(kind))
	return err
}

// DeleteAllLocations removes all locations for an asset.
func (s *AssetStoreSQLite) DeleteAllLocations(ctx context.Context, assetID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM asset_locations WHERE asset_id = ?`, assetID)
	return err
}

// scanLocation scans a single asset_locations row into a *Location.
func scanLocation(scanner interface{ Scan(dest ...any) error }) (*asset.Location, error) {
	var loc asset.Location
	var isPrimary int
	var createdAtStr, updatedAtStr string
	err := scanner.Scan(
		&loc.ID, &loc.AssetID, &loc.LocationKind, &loc.URI, &loc.ExternalID,
		&loc.AccessURL, &loc.DownloadURL, &loc.MimeType, &loc.FileSizeBytes,
		&loc.LegacyFileMD5, &isPrimary, &createdAtStr, &updatedAtStr,
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

// ── LocationRepository adapter (canonical Wave C surface) ────────────
//
// The asset.LocationRepository interface stays in domain (canonical
// contract). The concrete adapter struct + factory method migrate
// here so the domain stays free of database/sql responsibilities.

type locationRepositoryAdapter struct {
	store *AssetStoreSQLite
}

func (a *locationRepositoryAdapter) Upsert(ctx context.Context, loc *asset.Location) error {
	return a.store.UpsertLocation(ctx, loc)
}

func (a *locationRepositoryAdapter) GetPrimary(ctx context.Context, assetID string) (*asset.Location, error) {
	return a.store.GetPrimaryLocation(ctx, assetID)
}

func (a *locationRepositoryAdapter) ListByAsset(ctx context.Context, assetID string) ([]*asset.Location, error) {
	return a.store.ListLocationsByAsset(ctx, assetID)
}

func (a *locationRepositoryAdapter) SetPrimary(ctx context.Context, assetID string, kind asset.LocationKind) error {
	return a.store.SetPrimaryLocation(ctx, assetID, kind)
}

func (a *locationRepositoryAdapter) Delete(ctx context.Context, assetID string, kind asset.LocationKind) error {
	return a.store.DeleteLocation(ctx, assetID, kind)
}

func (a *locationRepositoryAdapter) DeleteAll(ctx context.Context, assetID string) error {
	return a.store.DeleteAllLocations(ctx, assetID)
}

// LocationRepository returns the LocationRepository adapter for the
// LOCAL AssetStoreSQLite. Caller-side promotion to legacy
// `*asset.AssetStoreSQLite` keeps backward
// compatibility for composition roots that haven't yet migrated.
func (s *AssetStoreSQLite) LocationRepository() asset.LocationRepository {
	return &locationRepositoryAdapter{store: s}
}
