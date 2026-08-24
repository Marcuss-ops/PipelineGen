// Package assets — asset_release_repository.go
//
// SQLite concrete for the asset release repository.
package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// AssetReleaseRepository is the SQLite-backed implementation of
// asset.ReleaseRepository.
type AssetReleaseRepository struct {
	db  *sql.DB
	log *zap.Logger
}

// NewAssetReleaseRepository builds a SQLite-backed release repository.
func NewAssetReleaseRepository(db *sql.DB, log *zap.Logger) (*AssetReleaseRepository, error) {
	if db == nil {
		return nil, errors.New("asset_release_repository: sql.DB is nil")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetReleaseRepository{db: db, log: log}, nil
}

var _ asset.ReleaseRepository = (*AssetReleaseRepository)(nil)

// Create persists a new AssetRelease and returns its ID.
func (r *AssetReleaseRepository) Create(ctx context.Context, release *asset.AssetRelease) (string, error) {
	if release == nil {
		return "", errors.New("asset_release_repository.Create: release is nil")
	}
	if release.AssetID == "" {
		return "", errors.New("asset_release_repository.Create: AssetID is required")
	}
	if release.ReleaseType == "" {
		release.ReleaseType = asset.ReleaseTypeBoth
	}
	if !isValidReleaseType(release.ReleaseType) {
		return "", fmt.Errorf("asset_release_repository.Create: invalid ReleaseType %q", release.ReleaseType)
	}
	if release.Status == "" {
		release.Status = asset.ReleaseStatusPending
	}
	if !isValidReleaseStatus(release.Status) {
		return "", fmt.Errorf("asset_release_repository.Create: invalid ReleaseStatus %q", release.Status)
	}

	id := uuid.New().String()
	now := time.Now()
	release.ID = id
	release.CreatedAt = now
	release.UpdatedAt = now

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO asset_releases (
			id, asset_id, release_type, model_release_url, model_release_path,
			property_release_url, property_release_path, certificate_url,
			certificate_path, receipt_url, receipt_path, status, verified_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, release.AssetID, string(release.ReleaseType),
		release.ModelReleaseURL, release.ModelReleasePath,
		release.PropertyReleaseURL, release.PropertyReleasePath,
		release.CertificateURL, release.CertificatePath,
		release.ReceiptURL, release.ReceiptPath,
		string(release.Status), timeutil.FormatPtrRFC3339(release.VerifiedAt),
		timeutil.FormatRFC3339(release.CreatedAt), timeutil.FormatRFC3339(release.UpdatedAt),
	)
	if err != nil {
		r.log.Error("asset_release_repository.Create failed",
			zap.String("asset_id", release.AssetID),
			zap.Error(err),
		)
		return "", fmt.Errorf("asset_release_repository.Create: %w", err)
	}
	return id, nil
}

// Get retrieves an AssetRelease by ID.
func (r *AssetReleaseRepository) Get(ctx context.Context, id string) (*asset.AssetRelease, error) {
	if id == "" {
		return nil, errors.New("asset_release_repository.Get: id is required")
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT id, asset_id, release_type, model_release_url, model_release_path,
			property_release_url, property_release_path, certificate_url,
			certificate_path, receipt_url, receipt_path, status, verified_at,
			created_at, updated_at
		 FROM asset_releases WHERE id = ?`,
		id,
	)
	return scanAssetRelease(row)
}

// ListByAsset lists all releases for a given asset.
func (r *AssetReleaseRepository) ListByAsset(ctx context.Context, assetID string) ([]*asset.AssetRelease, error) {
	if assetID == "" {
		return nil, errors.New("asset_release_repository.ListByAsset: assetID is required")
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, asset_id, release_type, model_release_url, model_release_path,
			property_release_url, property_release_path, certificate_url,
			certificate_path, receipt_url, receipt_path, status, verified_at,
			created_at, updated_at
		 FROM asset_releases WHERE asset_id = ? ORDER BY created_at DESC`,
		assetID,
	)
	if err != nil {
		return nil, fmt.Errorf("asset_release_repository.ListByAsset: %w", err)
	}
	defer rows.Close()

	var out []*asset.AssetRelease
	for rows.Next() {
		rel, err := scanAssetRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rel)
	}
	return out, rows.Err()
}

// Update modifies an existing AssetRelease.
func (r *AssetReleaseRepository) Update(ctx context.Context, release *asset.AssetRelease) error {
	if release == nil || release.ID == "" {
		return errors.New("asset_release_repository.Update: release and ID are required")
	}
	if !isValidReleaseType(release.ReleaseType) {
		return fmt.Errorf("asset_release_repository.Update: invalid ReleaseType %q", release.ReleaseType)
	}
	if !isValidReleaseStatus(release.Status) {
		return fmt.Errorf("asset_release_repository.Update: invalid ReleaseStatus %q", release.Status)
	}
	release.UpdatedAt = time.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE asset_releases SET
			asset_id = ?, release_type = ?, model_release_url = ?, model_release_path = ?,
			property_release_url = ?, property_release_path = ?, certificate_url = ?,
			certificate_path = ?, receipt_url = ?, receipt_path = ?, status = ?,
			verified_at = ?, updated_at = ?
		 WHERE id = ?`,
		release.AssetID, string(release.ReleaseType),
		release.ModelReleaseURL, release.ModelReleasePath,
		release.PropertyReleaseURL, release.PropertyReleasePath,
		release.CertificateURL, release.CertificatePath,
		release.ReceiptURL, release.ReceiptPath,
		string(release.Status), timeutil.FormatPtrRFC3339(release.VerifiedAt),
		timeutil.FormatRFC3339(release.UpdatedAt), release.ID,
	)
	if err != nil {
		r.log.Error("asset_release_repository.Update failed",
			zap.String("id", release.ID),
			zap.Error(err),
		)
		return fmt.Errorf("asset_release_repository.Update: %w", err)
	}
	return nil
}

// Delete removes an AssetRelease by ID.
func (r *AssetReleaseRepository) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("asset_release_repository.Delete: id is required")
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM asset_releases WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("asset_release_repository.Delete: %w", err)
	}
	return nil
}

func isValidReleaseType(rt asset.ReleaseType) bool {
	switch rt {
	case asset.ReleaseTypeModel, asset.ReleaseTypeProperty, asset.ReleaseTypeBoth:
		return true
	}
	return false
}

func isValidReleaseStatus(rs asset.ReleaseStatus) bool {
	switch rs {
	case asset.ReleaseStatusPending, asset.ReleaseStatusVerified, asset.ReleaseStatusRejected, asset.ReleaseStatusNotRequired:
		return true
	}
	return false
}

func scanAssetRelease(row interface {
	Scan(dest ...any) error
}) (*asset.AssetRelease, error) {
	var rel asset.AssetRelease
	var verifiedAt sql.NullString
	var createdAt, updatedAt string

	err := row.Scan(
		&rel.ID, &rel.AssetID, &rel.ReleaseType,
		&rel.ModelReleaseURL, &rel.ModelReleasePath,
		&rel.PropertyReleaseURL, &rel.PropertyReleasePath,
		&rel.CertificateURL, &rel.CertificatePath,
		&rel.ReceiptURL, &rel.ReceiptPath,
		&rel.Status, &verifiedAt, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scanAssetRelease: %w", err)
	}

	rel.VerifiedAt = timeutil.ParseRFC3339Ptr(verifiedAt.String)
	rel.CreatedAt = timeutil.ParseRFC3339(createdAt)
	rel.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
	return &rel, nil
}
