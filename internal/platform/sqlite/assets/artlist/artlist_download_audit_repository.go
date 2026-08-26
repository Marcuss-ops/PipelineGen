// Package assets — artlist_download_audit_repository.go
//
// SQLite concrete for the artlist download audit repository. Tracks every
// automatic Artlist download and supports daily per-account rate-limit
// queries.
package artlist

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// DownloadAuditStatus mirrors the application-layer status values.
type DownloadAuditStatus string

const (
	DownloadAuditStatusPending   DownloadAuditStatus = "pending"
	DownloadAuditStatusSucceeded DownloadAuditStatus = "succeeded"
	DownloadAuditStatusFailed    DownloadAuditStatus = "failed"
)

// DownloadAuditRecord is the local (pkg-internal) equivalent of the
// application-layer record. Fields map 1:1 onto the
// artlist_download_audit table columns.
type DownloadAuditRecord struct {
	AssetID      string
	ExternalURL  string
	AccountID    string
	Provider     string
	Status       DownloadAuditStatus
	DownloadedAt string
	LicenseID    string
	ReleaseID    string
	ProjectID    string
	DownloadedBy string
}

// DownloadAuditRow is a read-model for a single audit row, including the
// license/release/project tracking fields.
type DownloadAuditRow struct {
	ID           string
	Provider     string
	AccountID    string
	AssetID      string
	ExternalURL  string
	Status       DownloadAuditStatus
	DownloadedAt string
	LicenseID    string
	ReleaseID    string
	ProjectID    string
	DownloadedBy string
	CreatedAt    string
}

// DownloadAuditRepository is the LOCAL interface for the SQLite concrete.
// The application-layer artlist.DownloadAuditRepository port is bridged
// via the composition-root adapter.
type DownloadAuditRepository interface {
	RecordDownload(ctx context.Context, rec DownloadAuditRecord) (string, error)
	UpdateDownloadStatus(ctx context.Context, id string, status DownloadAuditStatus) error
	CountDailyDownloads(ctx context.Context, provider, accountID string) (int, error)
	ListByAsset(ctx context.Context, assetID string) ([]DownloadAuditRow, error)
	ListByLicense(ctx context.Context, licenseID string) ([]DownloadAuditRow, error)
	ListByRelease(ctx context.Context, releaseID string) ([]DownloadAuditRow, error)
	ListByProject(ctx context.Context, projectID string) ([]DownloadAuditRow, error)
	ListByDownloader(ctx context.Context, downloadedBy string) ([]DownloadAuditRow, error)
}

// ArtlistDownloadAuditRepository is the SQLite-backed implementation of
// DownloadAuditRepository.
type ArtlistDownloadAuditRepository struct {
	db  *sql.DB
	log *zap.Logger
}

// NewArtlistDownloadAuditRepository builds a SQLite-backed audit repository.
func NewArtlistDownloadAuditRepository(db *sql.DB, log *zap.Logger) (*ArtlistDownloadAuditRepository, error) {
	if db == nil {
		return nil, errors.New("artlist_download_audit_repository: sql.DB is nil")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &ArtlistDownloadAuditRepository{db: db, log: log}, nil
}

var _ DownloadAuditRepository = (*ArtlistDownloadAuditRepository)(nil)

// RecordDownload persists a download audit row and returns its ID.
func (r *ArtlistDownloadAuditRepository) RecordDownload(ctx context.Context, rec DownloadAuditRecord) (string, error) {
	if rec.AssetID == "" {
		return "", errors.New("artlist_download_audit_repository.RecordDownload: AssetID is required")
	}
	if rec.Provider == "" {
		rec.Provider = "artlist"
	}
	if rec.AccountID == "" {
		rec.AccountID = "default"
	}
	if rec.Status == "" {
		rec.Status = DownloadAuditStatusPending
	}

	id := uuid.New().String()
	downloadedAt := rec.DownloadedAt
	if downloadedAt == "" {
		downloadedAt = timeutil.FormatRFC3339(time.Now())
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO artlist_download_audit (id, provider, account_id, asset_id, external_url, status, downloaded_at, license_id, release_id, project_id, downloaded_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, rec.Provider, rec.AccountID, rec.AssetID, toNullString(rec.ExternalURL), string(rec.Status),
		downloadedAt, toNullString(rec.LicenseID), toNullString(rec.ReleaseID), toNullString(rec.ProjectID), toNullString(rec.DownloadedBy),
	)
	if err != nil {
		r.log.Error("artlist_download_audit_repository.RecordDownload failed",
			zap.String("asset_id", rec.AssetID),
			zap.String("account_id", rec.AccountID),
			zap.Error(err),
		)
		return "", fmt.Errorf("artlist_download_audit_repository.RecordDownload: %w", err)
	}
	return id, nil
}

// UpdateDownloadStatus updates the status of an existing audit row.
func (r *ArtlistDownloadAuditRepository) UpdateDownloadStatus(ctx context.Context, id string, status DownloadAuditStatus) error {
	if id == "" {
		return errors.New("artlist_download_audit_repository.UpdateDownloadStatus: id is required")
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE artlist_download_audit SET status = ? WHERE id = ?`,
		string(status), id,
	)
	if err != nil {
		r.log.Error("artlist_download_audit_repository.UpdateDownloadStatus failed",
			zap.String("id", id),
			zap.String("status", string(status)),
			zap.Error(err),
		)
		return fmt.Errorf("artlist_download_audit_repository.UpdateDownloadStatus: %w", err)
	}
	return nil
}

// ListByAsset returns all audit rows for a given asset.
func (r *ArtlistDownloadAuditRepository) ListByAsset(ctx context.Context, assetID string) ([]DownloadAuditRow, error) {
	if assetID == "" {
		return nil, errors.New("artlist_download_audit_repository.ListByAsset: assetID is required")
	}
	return r.listByAssetID(ctx, assetID)
}

// ListByLicense returns all audit rows linked to a given license.
func (r *ArtlistDownloadAuditRepository) ListByLicense(ctx context.Context, licenseID string) ([]DownloadAuditRow, error) {
	if licenseID == "" {
		return nil, errors.New("artlist_download_audit_repository.ListByLicense: licenseID is required")
	}
	return r.listByLicenseID(ctx, licenseID)
}

// ListByRelease returns all audit rows linked to a given release.
func (r *ArtlistDownloadAuditRepository) ListByRelease(ctx context.Context, releaseID string) ([]DownloadAuditRow, error) {
	if releaseID == "" {
		return nil, errors.New("artlist_download_audit_repository.ListByRelease: releaseID is required")
	}
	return r.listByReleaseID(ctx, releaseID)
}

// ListByProject returns all audit rows for a given project.
func (r *ArtlistDownloadAuditRepository) ListByProject(ctx context.Context, projectID string) ([]DownloadAuditRow, error) {
	if projectID == "" {
		return nil, errors.New("artlist_download_audit_repository.ListByProject: projectID is required")
	}
	return r.listByProjectID(ctx, projectID)
}

// ListByDownloader returns all audit rows for a given downloader principal.
func (r *ArtlistDownloadAuditRepository) ListByDownloader(ctx context.Context, downloadedBy string) ([]DownloadAuditRow, error) {
	if downloadedBy == "" {
		return nil, errors.New("artlist_download_audit_repository.ListByDownloader: downloadedBy is required")
	}
	return r.listByDownloadedBy(ctx, downloadedBy)
}

func (r *ArtlistDownloadAuditRepository) listByAssetID(ctx context.Context, assetID string) ([]DownloadAuditRow, error) {
	const query = `SELECT id, provider, account_id, asset_id, external_url, status, downloaded_at, license_id, release_id, project_id, downloaded_by, created_at FROM artlist_download_audit WHERE asset_id = ? ORDER BY created_at DESC`
	return r.scanRows(ctx, query, assetID)
}

func (r *ArtlistDownloadAuditRepository) listByLicenseID(ctx context.Context, licenseID string) ([]DownloadAuditRow, error) {
	const query = `SELECT id, provider, account_id, asset_id, external_url, status, downloaded_at, license_id, release_id, project_id, downloaded_by, created_at FROM artlist_download_audit WHERE license_id = ? ORDER BY created_at DESC`
	return r.scanRows(ctx, query, licenseID)
}

func (r *ArtlistDownloadAuditRepository) listByReleaseID(ctx context.Context, releaseID string) ([]DownloadAuditRow, error) {
	const query = `SELECT id, provider, account_id, asset_id, external_url, status, downloaded_at, license_id, release_id, project_id, downloaded_by, created_at FROM artlist_download_audit WHERE release_id = ? ORDER BY created_at DESC`
	return r.scanRows(ctx, query, releaseID)
}

func (r *ArtlistDownloadAuditRepository) listByProjectID(ctx context.Context, projectID string) ([]DownloadAuditRow, error) {
	const query = `SELECT id, provider, account_id, asset_id, external_url, status, downloaded_at, license_id, release_id, project_id, downloaded_by, created_at FROM artlist_download_audit WHERE project_id = ? ORDER BY created_at DESC`
	return r.scanRows(ctx, query, projectID)
}

func (r *ArtlistDownloadAuditRepository) listByDownloadedBy(ctx context.Context, downloadedBy string) ([]DownloadAuditRow, error) {
	const query = `SELECT id, provider, account_id, asset_id, external_url, status, downloaded_at, license_id, release_id, project_id, downloaded_by, created_at FROM artlist_download_audit WHERE downloaded_by = ? ORDER BY created_at DESC`
	return r.scanRows(ctx, query, downloadedBy)
}

func (r *ArtlistDownloadAuditRepository) scanRows(ctx context.Context, query string, value string) ([]DownloadAuditRow, error) {
	rows, err := r.db.QueryContext(ctx, query, value)
	if err != nil {
		return nil, fmt.Errorf("artlist_download_audit_repository.listBy: %w", err)
	}
	defer rows.Close()

	var out []DownloadAuditRow
	for rows.Next() {
		var row DownloadAuditRow
		var externalURL, licenseID, releaseID, projectID, downloadedBy sql.NullString
		if err := rows.Scan(
			&row.ID, &row.Provider, &row.AccountID, &row.AssetID, &externalURL,
			&row.Status, &row.DownloadedAt, &licenseID, &releaseID,
			&projectID, &downloadedBy, &row.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("artlist_download_audit_repository.listBy scan: %w", err)
		}
		row.ExternalURL = externalURL.String
		row.LicenseID = licenseID.String
		row.ReleaseID = releaseID.String
		row.ProjectID = projectID.String
		row.DownloadedBy = downloadedBy.String
		out = append(out, row)
	}
	return out, rows.Err()
}

// CountDailyDownloads returns the number of non-failed downloads
// recorded for the given provider/account on the current UTC day.
// Pending rows are counted so concurrent downloads cannot overshoot
// the quota; failed rows are excluded.
func (r *ArtlistDownloadAuditRepository) CountDailyDownloads(ctx context.Context, provider, accountID string) (int, error) {
	if provider == "" {
		provider = "artlist"
	}
	if accountID == "" {
		accountID = "default"
	}

	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM artlist_download_audit
		 WHERE provider = ? AND account_id = ? AND date(created_at) = date('now')
		 AND status IN (?, ?)`,
		provider, accountID, string(DownloadAuditStatusPending), string(DownloadAuditStatusSucceeded),
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("artlist_download_audit_repository.CountDailyDownloads: %w", err)
	}
	return count, nil
}
func toNullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
