// Package assetversions provides the read/write layer for the
// asset_versions table, which tracks version history for media assets.
// Each time an asset is re-processed or re-uploaded, a new version row
// is appended. The current version is the one with the highest version
// number for the asset.
//
// Version allocation is ATOMIC: use CreateNext() instead of calling
// NextVersion() + Create() separately to avoid race conditions between
// two concurrent workers.
package assetversions

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"velox/go-master/pkg/timeutil"
)

// Version represents a single asset_versions row.
type Version struct {
	AssetID       string
	Version       int
	ContentHash   string
	FileHash      string
	FileSizeBytes int64
	MimeType      string
	MetadataJSON  string
	CreatedBy     string
	CreatedAt     time.Time
}

// VersionInput holds the user-supplied fields for creating a new version.
type VersionInput struct {
	ContentHash   string
	FileHash      string
	FileSizeBytes int64
	MimeType      string
	MetadataJSON  string
	CreatedBy     string
}

// Repository wraps SQL access to the asset_versions table.
type Repository struct {
	db *sql.DB
}

// NewRepository creates a Repository backed by db.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Create inserts a new version. The version number should be computed by
// the caller (typically current max + 1). Returns error on duplicate
// (asset_id, version) tuple.
//
// Deprecated: use CreateNext() for atomic version allocation.
func (r *Repository) Create(ctx context.Context, v Version) error {
	if v.MetadataJSON != "" && v.MetadataJSON != "{}" && !json.Valid([]byte(v.MetadataJSON)) {
		return fmt.Errorf("assetversions.Create(%s, v%d): metadata_json is not valid JSON", v.AssetID, v.Version)
	}
	now := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO asset_versions
			(asset_id, version, content_hash, file_hash, file_size_bytes, mime_type, metadata_json, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, v.AssetID, v.Version, v.ContentHash, v.FileHash, v.FileSizeBytes, v.MimeType, v.MetadataJSON, v.CreatedBy, now)
	if err != nil {
		return fmt.Errorf("assetversions.Create(%s, v%d): %w", v.AssetID, v.Version, err)
	}
	return nil
}

// CreateNext atomically allocates the next version number and inserts the
// version in a single transaction. This is the SAFE alternative to calling
// NextVersion() + Create() separately — two concurrent callers will always
// produce different version numbers.
//
// Returns the created Version with the allocated version number populated.
func (r *Repository) CreateNext(ctx context.Context, assetID string, input VersionInput) (*Version, error) {
	if input.MetadataJSON != "" && input.MetadataJSON != "{}" && !json.Valid([]byte(input.MetadataJSON)) {
		return nil, fmt.Errorf("assetversions.CreateNext(%s): metadata_json is not valid JSON", assetID)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("assetversions.CreateNext(%s) begin tx: %w", assetID, err)
	}
	defer tx.Rollback()

	// Atomically allocate the next version inside the transaction.
	var nextVer int
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM asset_versions WHERE asset_id = ?`, assetID).Scan(&nextVer)
	if err != nil {
		return nil, fmt.Errorf("assetversions.CreateNext(%s) select max: %w", assetID, err)
	}

	now := timeutil.FormatRFC3339(time.Now())
	_, err = tx.ExecContext(ctx, `
		INSERT INTO asset_versions
			(asset_id, version, content_hash, file_hash, file_size_bytes, mime_type, metadata_json, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, assetID, nextVer, input.ContentHash, input.FileHash, input.FileSizeBytes, input.MimeType, input.MetadataJSON, input.CreatedBy, now)
	if err != nil {
		return nil, fmt.Errorf("assetversions.CreateNext(%s, v%d): %w", assetID, nextVer, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("assetversions.CreateNext(%s) commit: %w", assetID, err)
	}

	return &Version{
		AssetID:       assetID,
		Version:       nextVer,
		ContentHash:   input.ContentHash,
		FileHash:      input.FileHash,
		FileSizeBytes: input.FileSizeBytes,
		MimeType:      input.MimeType,
		MetadataJSON:  input.MetadataJSON,
		CreatedBy:     input.CreatedBy,
		CreatedAt:     time.Now(),
	}, nil
}

// GetCurrent returns the latest version for an asset (highest version number).
// Returns nil if no versions exist.
func (r *Repository) GetCurrent(ctx context.Context, assetID string) (*Version, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT asset_id, version, content_hash, file_hash, file_size_bytes, mime_type, metadata_json, created_by, created_at
		FROM asset_versions
		WHERE asset_id = ?
		ORDER BY version DESC LIMIT 1
	`, assetID)
	v, err := scanVersion(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("assetversions.GetCurrent(%s): %w", assetID, err)
	}
	return v, nil
}

// Get returns a specific version for an asset.
func (r *Repository) Get(ctx context.Context, assetID string, version int) (*Version, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT asset_id, version, content_hash, file_hash, file_size_bytes, mime_type, metadata_json, created_by, created_at
		FROM asset_versions
		WHERE asset_id = ? AND version = ?
	`, assetID, version)
	v, err := scanVersion(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("assetversions.Get(%s, v%d): %w", assetID, version, err)
	}
	return v, nil
}

// List returns all versions for an asset, ordered by version descending.
func (r *Repository) List(ctx context.Context, assetID string) ([]Version, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT asset_id, version, content_hash, file_hash, file_size_bytes, mime_type, metadata_json, created_by, created_at
		FROM asset_versions
		WHERE asset_id = ?
		ORDER BY version DESC
	`, assetID)
	if err != nil {
		return nil, fmt.Errorf("assetversions.List(%s): %w", assetID, err)
	}
	defer rows.Close()
	return scanVersions(rows)
}

// NextVersion returns the next version number for an asset.
// Returns 1 if the asset has no versions yet.
//
// Deprecated: use CreateNext() for atomic version allocation.
// NextVersion() + Create() has a race condition when two callers run
// concurrently.
func (r *Repository) NextVersion(ctx context.Context, assetID string) (int, error) {
	var maxVer sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT MAX(version) FROM asset_versions WHERE asset_id = ?`, assetID).Scan(&maxVer)
	if err != nil {
		return 0, fmt.Errorf("assetversions.NextVersion(%s): %w", assetID, err)
	}
	if maxVer.Valid {
		return int(maxVer.Int64) + 1, nil
	}
	return 1, nil
}

// Delete removes a specific version for an asset.
func (r *Repository) Delete(ctx context.Context, assetID string, version int) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM asset_versions WHERE asset_id = ? AND version = ?`, assetID, version)
	if err != nil {
		return fmt.Errorf("assetversions.Delete(%s, v%d): %w", assetID, version, err)
	}
	return nil
}

// DeleteAll removes all versions for an asset.
func (r *Repository) DeleteAll(ctx context.Context, assetID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM asset_versions WHERE asset_id = ?`, assetID)
	if err != nil {
		return fmt.Errorf("assetversions.DeleteAll(%s): %w", assetID, err)
	}
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────

func scanVersion(s interface{ Scan(dest ...any) error }) (*Version, error) {
	v := &Version{}
	var createdAtStr string
	err := s.Scan(&v.AssetID, &v.Version, &v.ContentHash, &v.FileHash, &v.FileSizeBytes, &v.MimeType, &v.MetadataJSON, &v.CreatedBy, &createdAtStr)
	if err != nil {
		return nil, err
	}
	if t := timeutil.ParseRFC3339(createdAtStr); !t.IsZero() {
		v.CreatedAt = t
	}
	return v, nil
}

func scanVersions(rows *sql.Rows) ([]Version, error) {
	var out []Version
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}
