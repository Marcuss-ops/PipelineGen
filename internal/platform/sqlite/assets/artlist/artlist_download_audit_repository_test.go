// Package assets — artlist_download_audit_repository_test.go
//
// Pins the canonical round-trip contract for the artlist_download_audit
// table: RecordDownload persists a row with the license/release/project
// tracking fields and ListByAsset returns it unchanged.
package artlist

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// auditTestDB opens a file-backed in-memory SQLite database with the
// minimal artlist_download_audit schema required by the repository.
func auditTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "artlist_audit_test.db") + "?cache=shared&_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err, "open sqlite")
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS artlist_download_audit (
	id TEXT PRIMARY KEY,
	provider TEXT NOT NULL DEFAULT 'artlist',
	account_id TEXT NOT NULL DEFAULT 'default',
	asset_id TEXT NOT NULL,
	external_url TEXT,
	status TEXT NOT NULL DEFAULT 'pending',
	created_at TEXT DEFAULT (datetime('now')),
	downloaded_at TEXT,
	license_id TEXT,
	release_id TEXT,
	project_id TEXT,
	downloaded_by TEXT
);`)
	require.NoError(t, err, "CREATE TABLE artlist_download_audit")
	return db
}

func TestArtlistDownloadAuditRepository_RecordDownloadWithLicenseRelease(t *testing.T) {
	db := auditTestDB(t)
	ctx := context.Background()

	repo, err := NewArtlistDownloadAuditRepository(db, zap.NewNop())
	require.NoError(t, err)

	id, err := repo.RecordDownload(ctx, DownloadAuditRecord{
		AssetID:      "asset-007",
		ExternalURL:  "https://example.com/clip.mp4",
		AccountID:    "account-007",
		Provider:     "artlist",
		Status:       DownloadAuditStatusSucceeded,
		DownloadedAt: "2026-07-10T12:00:00Z",
		LicenseID:    "license-007",
		ReleaseID:    "release-007",
		ProjectID:    "project-007",
		DownloadedBy: "user-007",
	})
	require.NoError(t, err)
	require.NotEmpty(t, id)

	rows, err := repo.ListByAsset(ctx, "asset-007")
	require.NoError(t, err)
	require.Len(t, rows, 1)

	row := rows[0]
	assert.Equal(t, "asset-007", row.AssetID)
	assert.Equal(t, "https://example.com/clip.mp4", row.ExternalURL)
	assert.Equal(t, "account-007", row.AccountID)
	assert.Equal(t, "artlist", row.Provider)
	assert.Equal(t, DownloadAuditStatusSucceeded, row.Status)
	assert.Equal(t, "2026-07-10T12:00:00Z", row.DownloadedAt)
	assert.Equal(t, "license-007", row.LicenseID)
	assert.Equal(t, "release-007", row.ReleaseID)
	assert.Equal(t, "project-007", row.ProjectID)
	assert.Equal(t, "user-007", row.DownloadedBy)
	assert.NotEmpty(t, row.CreatedAt)
}
