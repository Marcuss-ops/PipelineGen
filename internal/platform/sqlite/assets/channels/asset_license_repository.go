// Package assets — asset_license_repository.go
//
// SQLite concrete for the asset license repository.
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

// AssetLicenseRepository is the SQLite-backed implementation of
// asset.LicenseRepository.
type AssetLicenseRepository struct {
	db  *sql.DB
	log *zap.Logger
}

// NewAssetLicenseRepository builds a SQLite-backed license repository.
func NewAssetLicenseRepository(db *sql.DB, log *zap.Logger) (*AssetLicenseRepository, error) {
	if db == nil {
		return nil, errors.New("asset_license_repository: sql.DB is nil")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetLicenseRepository{db: db, log: log}, nil
}

var _ asset.LicenseRepository = (*AssetLicenseRepository)(nil)

// Create persists a new AssetLicense and returns its ID.
func (r *AssetLicenseRepository) Create(ctx context.Context, license *asset.AssetLicense) (string, error) {
	if license == nil {
		return "", errors.New("asset_license_repository.Create: license is nil")
	}
	if license.AssetID == "" {
		return "", errors.New("asset_license_repository.Create: AssetID is required")
	}
	if license.Provider == "" {
		license.Provider = "artlist"
	}
	if license.AccountID == "" {
		license.AccountID = "default"
	}
	if license.LicenseType == "" {
		license.LicenseType = asset.LicenseTypeStandard
	}
	if !isValidLicenseType(license.LicenseType) {
		return "", fmt.Errorf("asset_license_repository.Create: invalid LicenseType %q", license.LicenseType)
	}

	id := uuid.New().String()
	now := time.Now()
	license.ID = id
	license.CreatedAt = now
	license.UpdatedAt = now

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO asset_licenses (
			id, provider, account_id, project_id, asset_id, license_type, license_name,
			license_url, license_terms, receipt_url, receipt_path, certificate_url,
			certificate_path, valid_from, valid_until, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, license.Provider, license.AccountID, license.ProjectID, license.AssetID,
		string(license.LicenseType), license.LicenseName, license.LicenseURL,
		license.LicenseTerms, license.ReceiptURL, license.ReceiptPath,
		license.CertificateURL, license.CertificatePath,
		timeutil.FormatPtrRFC3339(license.ValidFrom), timeutil.FormatPtrRFC3339(license.ValidUntil),
		timeutil.FormatRFC3339(license.CreatedAt), timeutil.FormatRFC3339(license.UpdatedAt),
	)
	if err != nil {
		r.log.Error("asset_license_repository.Create failed",
			zap.String("asset_id", license.AssetID),
			zap.Error(err),
		)
		return "", fmt.Errorf("asset_license_repository.Create: %w", err)
	}
	return id, nil
}

// Get retrieves an AssetLicense by ID.
func (r *AssetLicenseRepository) Get(ctx context.Context, id string) (*asset.AssetLicense, error) {
	if id == "" {
		return nil, errors.New("asset_license_repository.Get: id is required")
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT id, provider, account_id, project_id, asset_id, license_type, license_name,
			license_url, license_terms, receipt_url, receipt_path, certificate_url,
			certificate_path, valid_from, valid_until, created_at, updated_at
		 FROM asset_licenses WHERE id = ?`,
		id,
	)
	return scanAssetLicense(row)
}

// ListByAsset lists all licenses for a given asset.
func (r *AssetLicenseRepository) ListByAsset(ctx context.Context, assetID string) ([]*asset.AssetLicense, error) {
	if assetID == "" {
		return nil, errors.New("asset_license_repository.ListByAsset: assetID is required")
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, provider, account_id, project_id, asset_id, license_type, license_name,
			license_url, license_terms, receipt_url, receipt_path, certificate_url,
			certificate_path, valid_from, valid_until, created_at, updated_at
		 FROM asset_licenses WHERE asset_id = ? ORDER BY created_at DESC`,
		assetID,
	)
	if err != nil {
		return nil, fmt.Errorf("asset_license_repository.ListByAsset: %w", err)
	}
	defer rows.Close()

	var out []*asset.AssetLicense
	for rows.Next() {
		l, err := scanAssetLicense(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ListByProject lists all licenses for a given project.
func (r *AssetLicenseRepository) ListByProject(ctx context.Context, projectID string) ([]*asset.AssetLicense, error) {
	if projectID == "" {
		return nil, errors.New("asset_license_repository.ListByProject: projectID is required")
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, provider, account_id, project_id, asset_id, license_type, license_name,
			license_url, license_terms, receipt_url, receipt_path, certificate_url,
			certificate_path, valid_from, valid_until, created_at, updated_at
		 FROM asset_licenses WHERE project_id = ? ORDER BY created_at DESC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("asset_license_repository.ListByProject: %w", err)
	}
	defer rows.Close()

	var out []*asset.AssetLicense
	for rows.Next() {
		l, err := scanAssetLicense(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Update modifies an existing AssetLicense.
func (r *AssetLicenseRepository) Update(ctx context.Context, license *asset.AssetLicense) error {
	if license == nil || license.ID == "" {
		return errors.New("asset_license_repository.Update: license and ID are required")
	}
	if !isValidLicenseType(license.LicenseType) {
		return fmt.Errorf("asset_license_repository.Update: invalid LicenseType %q", license.LicenseType)
	}
	license.UpdatedAt = time.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE asset_licenses SET
			provider = ?, account_id = ?, project_id = ?, asset_id = ?, license_type = ?,
			license_name = ?, license_url = ?, license_terms = ?, receipt_url = ?,
			receipt_path = ?, certificate_url = ?, certificate_path = ?, valid_from = ?,
			valid_until = ?, updated_at = ?
		 WHERE id = ?`,
		license.Provider, license.AccountID, license.ProjectID, license.AssetID,
		string(license.LicenseType), license.LicenseName, license.LicenseURL,
		license.LicenseTerms, license.ReceiptURL, license.ReceiptPath,
		license.CertificateURL, license.CertificatePath,
		timeutil.FormatPtrRFC3339(license.ValidFrom), timeutil.FormatPtrRFC3339(license.ValidUntil),
		timeutil.FormatRFC3339(license.UpdatedAt), license.ID,
	)
	if err != nil {
		r.log.Error("asset_license_repository.Update failed",
			zap.String("id", license.ID),
			zap.Error(err),
		)
		return fmt.Errorf("asset_license_repository.Update: %w", err)
	}
	return nil
}

// Delete removes an AssetLicense by ID.
func (r *AssetLicenseRepository) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("asset_license_repository.Delete: id is required")
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM asset_licenses WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("asset_license_repository.Delete: %w", err)
	}
	return nil
}

func isValidLicenseType(lt asset.LicenseType) bool {
	switch lt {
	case asset.LicenseTypeStandard, asset.LicenseTypeExtended, asset.LicenseTypeRoyaltyFree,
		asset.LicenseTypeCC0, asset.LicenseTypeCreativeCommons, asset.LicenseTypeCustom:
		return true
	}
	return false
}

func scanAssetLicense(row interface {
	Scan(dest ...any) error
}) (*asset.AssetLicense, error) {
	var l asset.AssetLicense
	var validFrom, validUntil sql.NullString
	var createdAt, updatedAt string

	err := row.Scan(
		&l.ID, &l.Provider, &l.AccountID, &l.ProjectID, &l.AssetID,
		&l.LicenseType, &l.LicenseName, &l.LicenseURL, &l.LicenseTerms,
		&l.ReceiptURL, &l.ReceiptPath, &l.CertificateURL, &l.CertificatePath,
		&validFrom, &validUntil, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scanAssetLicense: %w", err)
	}

	l.ValidFrom = timeutil.ParseRFC3339Ptr(validFrom.String)
	l.ValidUntil = timeutil.ParseRFC3339Ptr(validUntil.String)
	l.CreatedAt = timeutil.ParseRFC3339(createdAt)
	l.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
	return &l, nil
}
