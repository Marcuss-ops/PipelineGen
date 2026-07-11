// Package assets — artlist_download_audit_repository.go
//
// SQLite concrete for the artlist download audit repository. Tracks every
// automatic Artlist download and supports daily per-account rate-limit
// queries.
package assets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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
	AssetID     string
	ExternalURL string
	AccountID   string
	Provider    string
	Status      DownloadAuditStatus
}

// DownloadAuditRepository is the LOCAL interface for the SQLite concrete.
// The application-layer artlist.DownloadAuditRepository port is bridged
// via the composition-root adapter.
type DownloadAuditRepository interface {
	RecordDownload(ctx context.Context, rec DownloadAuditRecord) (string, error)
	UpdateDownloadStatus(ctx context.Context, id string, status DownloadAuditStatus) error
	CountDailyDownloads(ctx context.Context, provider, accountID string) (int, error)
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
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO artlist_download_audit (id, provider, account_id, asset_id, external_url, status)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, rec.Provider, rec.AccountID, rec.AssetID, rec.ExternalURL, string(rec.Status),
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
