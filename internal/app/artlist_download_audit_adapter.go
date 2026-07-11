// Package app — artlist_download_audit_adapter.go
//
// Bridges the application-layer artlist.DownloadAuditRepository port to the
// SQLite concrete in internal/infrastructure/database/sqlite/assets.
package app

import (
	"context"

	artlist "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	sqliteassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// artlistDownloadAuditAdapter adapts sqlite/assets.DownloadAuditRepository to
// artlist.DownloadAuditRepository. The adapter lives in the composition root so
// the import graph stays acyclic.
type artlistDownloadAuditAdapter struct {
	concrete sqliteassets.DownloadAuditRepository
}

// NewArtlistDownloadAuditAdapter wraps the SQLite concrete.
func NewArtlistDownloadAuditAdapter(concrete sqliteassets.DownloadAuditRepository) artlist.DownloadAuditRepository {
	if concrete == nil {
		return nil
	}
	return &artlistDownloadAuditAdapter{concrete: concrete}
}

// Compile-time pin: adapter satisfies the application-layer port.
var _ artlist.DownloadAuditRepository = (*artlistDownloadAuditAdapter)(nil)

func (a *artlistDownloadAuditAdapter) RecordDownload(ctx context.Context, rec artlist.DownloadAuditRecord) (string, error) {
	return a.concrete.RecordDownload(ctx, sqliteassets.DownloadAuditRecord{
		AssetID:     rec.AssetID,
		ExternalURL: rec.ExternalURL,
		AccountID:   rec.AccountID,
		Provider:    rec.Provider,
		Status:      sqliteassets.DownloadAuditStatus(rec.Status),
	})
}

func (a *artlistDownloadAuditAdapter) UpdateDownloadStatus(ctx context.Context, id string, status artlist.DownloadAuditStatus) error {
	return a.concrete.UpdateDownloadStatus(ctx, id, sqliteassets.DownloadAuditStatus(status))
}

func (a *artlistDownloadAuditAdapter) CountDailyDownloads(ctx context.Context, provider, accountID string) (int, error) {
	return a.concrete.CountDailyDownloads(ctx, provider, accountID)
}
