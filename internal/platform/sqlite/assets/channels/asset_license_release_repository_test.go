// Package assets — asset_license_release_repository_test.go
//
// Pins the canonical round-trip contracts for asset_licenses and
// asset_releases tables introduced in migrations 138 and 139.
package channels

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newLicenseReleaseTestDB opens a fresh in-memory SQLite database and
// creates the minimal media_assets, asset_licenses and asset_releases
// schema needed by the repositories.
func newLicenseReleaseTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err, "open in-memory sqlite")
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			source TEXT,
			name TEXT,
			tags TEXT,
			tags_norm TEXT,
			duration_ms INTEGER,
			url TEXT,
			media_type TEXT,
			status TEXT,
			local_path TEXT,
			relative_path TEXT,
			drive_file_id TEXT,
			drive_folder_id TEXT,
			drive_link TEXT,
			download_link TEXT,
			legacy_file_md5 TEXT,
			embedding_json TEXT,
			metadata_json TEXT,
			visual_embedding TEXT,
			transcript_embedding TEXT,
			created_at TEXT,
			updated_at TEXT,
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    lifecycle_state TEXT NOT NULL DEFAULT '',
    index_state TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    source_provider TEXT NOT NULL DEFAULT '',
    source_video_id TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    start_ms INTEGER NOT NULL DEFAULT 0,
    end_ms INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '');

		CREATE TABLE asset_licenses (
			id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			account_id TEXT NOT NULL DEFAULT 'default',
			project_id TEXT,
			asset_id TEXT NOT NULL,
			license_type TEXT NOT NULL DEFAULT 'standard',
			license_name TEXT,
			license_url TEXT,
			license_terms TEXT,
			receipt_url TEXT,
			receipt_path TEXT,
			certificate_url TEXT,
			certificate_path TEXT,
			valid_from TEXT,
			valid_until TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now')),
			FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
		);

		CREATE TABLE asset_releases (
			id TEXT PRIMARY KEY,
			asset_id TEXT NOT NULL,
			release_type TEXT NOT NULL CHECK (release_type IN ('model', 'property', 'both')),
			model_release_url TEXT,
			model_release_path TEXT,
			property_release_url TEXT,
			property_release_path TEXT,
			certificate_url TEXT,
			certificate_path TEXT,
			receipt_url TEXT,
			receipt_path TEXT,
			status TEXT DEFAULT 'pending',
			verified_at TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now')),
			FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
		);

		CREATE TABLE artlist_download_audit (
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
		);
	`)
	require.NoError(t, err, "create test schema")
	return db
}

// seedLicenseReleaseMediaAsset inserts a minimal media_assets row so FK
// constraints are satisfied.
func seedLicenseReleaseMediaAsset(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO media_assets (id, source, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		id, "test", "Test Asset", time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339),
	)
	require.NoError(t, err, "seed media_assets row")
}

func TestAssetLicenseRepository_CreateRoundTrip(t *testing.T) {
	db := newLicenseReleaseTestDB(t)
	ctx := context.Background()

	repo, err := NewAssetLicenseRepository(db, zap.NewNop())
	require.NoError(t, err)

	seedLicenseReleaseMediaAsset(t, db, "asset-001")

	validFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	validUntil := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	license := &asset.AssetLicense{
		Provider:        "artlist",
		AccountID:       "account-001",
		ProjectID:       "project-001",
		AssetID:         "asset-001",
		LicenseType:     asset.LicenseTypeExtended,
		LicenseName:     "Extended License",
		LicenseURL:      "https://example.com/license",
		LicenseTerms:    "{\"seats\": 1}",
		ReceiptURL:      "https://example.com/receipt",
		ReceiptPath:     "/receipts/r1.pdf",
		CertificateURL:  "https://example.com/cert",
		CertificatePath: "/certs/c1.pdf",
		ValidFrom:       &validFrom,
		ValidUntil:      &validUntil,
	}

	id, err := repo.Create(ctx, license)
	require.NoError(t, err)
	require.NotEmpty(t, id)

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, id, got.ID)
	assert.Equal(t, license.Provider, got.Provider)
	assert.Equal(t, license.AccountID, got.AccountID)
	assert.Equal(t, license.ProjectID, got.ProjectID)
	assert.Equal(t, license.AssetID, got.AssetID)
	assert.Equal(t, license.LicenseType, got.LicenseType)
	assert.Equal(t, license.LicenseName, got.LicenseName)
	assert.Equal(t, license.LicenseURL, got.LicenseURL)
	assert.Equal(t, license.LicenseTerms, got.LicenseTerms)
	assert.Equal(t, license.ReceiptURL, got.ReceiptURL)
	assert.Equal(t, license.ReceiptPath, got.ReceiptPath)
	assert.Equal(t, license.CertificateURL, got.CertificateURL)
	assert.Equal(t, license.CertificatePath, got.CertificatePath)
	assert.NotNil(t, got.ValidFrom)
	assert.WithinDuration(t, validFrom, *got.ValidFrom, 0)
	assert.NotNil(t, got.ValidUntil)
	assert.WithinDuration(t, validUntil, *got.ValidUntil, 0)
	assert.False(t, got.CreatedAt.IsZero())
	assert.False(t, got.UpdatedAt.IsZero())
}

func TestAssetLicenseRepository_ListByAsset(t *testing.T) {
	db := newLicenseReleaseTestDB(t)
	ctx := context.Background()

	repo, err := NewAssetLicenseRepository(db, zap.NewNop())
	require.NoError(t, err)

	seedLicenseReleaseMediaAsset(t, db, "asset-002")

	_, err = repo.Create(ctx, &asset.AssetLicense{
		Provider: "artlist", AssetID: "asset-002", LicenseType: asset.LicenseTypeStandard,
	})
	require.NoError(t, err)
	_, err = repo.Create(ctx, &asset.AssetLicense{
		Provider: "pexels", AssetID: "asset-002", LicenseType: asset.LicenseTypeCC0,
	})
	require.NoError(t, err)

	licenses, err := repo.ListByAsset(ctx, "asset-002")
	require.NoError(t, err)
	assert.Len(t, licenses, 2)
}

func TestAssetLicenseRepository_ListByProject(t *testing.T) {
	db := newLicenseReleaseTestDB(t)
	ctx := context.Background()

	repo, err := NewAssetLicenseRepository(db, zap.NewNop())
	require.NoError(t, err)

	seedLicenseReleaseMediaAsset(t, db, "asset-003")

	_, err = repo.Create(ctx, &asset.AssetLicense{
		Provider: "artlist", AssetID: "asset-003", ProjectID: "project-x", LicenseType: asset.LicenseTypeStandard,
	})
	require.NoError(t, err)

	licenses, err := repo.ListByProject(ctx, "project-x")
	require.NoError(t, err)
	assert.Len(t, licenses, 1)
	assert.Equal(t, "project-x", licenses[0].ProjectID)
}

func TestAssetLicenseRepository_UpdateAndDelete(t *testing.T) {
	db := newLicenseReleaseTestDB(t)
	ctx := context.Background()

	repo, err := NewAssetLicenseRepository(db, zap.NewNop())
	require.NoError(t, err)

	seedLicenseReleaseMediaAsset(t, db, "asset-004")

	id, err := repo.Create(ctx, &asset.AssetLicense{
		Provider: "artlist", AssetID: "asset-004", LicenseType: asset.LicenseTypeStandard,
	})
	require.NoError(t, err)

	license, err := repo.Get(ctx, id)
	require.NoError(t, err)
	license.LicenseName = "Updated Name"
	require.NoError(t, repo.Update(ctx, license))

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Updated Name", got.LicenseName)

	require.NoError(t, repo.Delete(ctx, id))
	got, err = repo.Get(ctx, id)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestAssetReleaseRepository_CreateRoundTrip(t *testing.T) {
	db := newLicenseReleaseTestDB(t)
	ctx := context.Background()

	repo, err := NewAssetReleaseRepository(db, zap.NewNop())
	require.NoError(t, err)

	seedLicenseReleaseMediaAsset(t, db, "asset-005")

	verifiedAt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	release := &asset.AssetRelease{
		AssetID:             "asset-005",
		ReleaseType:         asset.ReleaseTypeBoth,
		ModelReleaseURL:     "https://example.com/model",
		ModelReleasePath:    "/releases/model.pdf",
		PropertyReleaseURL:  "https://example.com/property",
		PropertyReleasePath: "/releases/property.pdf",
		CertificateURL:      "https://example.com/cert",
		CertificatePath:     "/certs/cert.pdf",
		ReceiptURL:          "https://example.com/receipt",
		ReceiptPath:         "/receipts/receipt.pdf",
		Status:              asset.ReleaseStatusVerified,
		VerifiedAt:          &verifiedAt,
	}

	id, err := repo.Create(ctx, release)
	require.NoError(t, err)
	require.NotEmpty(t, id)

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, id, got.ID)
	assert.Equal(t, release.AssetID, got.AssetID)
	assert.Equal(t, release.ReleaseType, got.ReleaseType)
	assert.Equal(t, release.ModelReleaseURL, got.ModelReleaseURL)
	assert.Equal(t, release.ModelReleasePath, got.ModelReleasePath)
	assert.Equal(t, release.PropertyReleaseURL, got.PropertyReleaseURL)
	assert.Equal(t, release.PropertyReleasePath, got.PropertyReleasePath)
	assert.Equal(t, release.CertificateURL, got.CertificateURL)
	assert.Equal(t, release.CertificatePath, got.CertificatePath)
	assert.Equal(t, release.ReceiptURL, got.ReceiptURL)
	assert.Equal(t, release.ReceiptPath, got.ReceiptPath)
	assert.Equal(t, release.Status, got.Status)
	assert.NotNil(t, got.VerifiedAt)
	assert.WithinDuration(t, verifiedAt, *got.VerifiedAt, 0)
}

func TestAssetReleaseRepository_ListByAsset(t *testing.T) {
	db := newLicenseReleaseTestDB(t)
	ctx := context.Background()

	repo, err := NewAssetReleaseRepository(db, zap.NewNop())
	require.NoError(t, err)

	seedLicenseReleaseMediaAsset(t, db, "asset-006")

	_, err = repo.Create(ctx, &asset.AssetRelease{
		AssetID: "asset-006", ReleaseType: asset.ReleaseTypeModel, Status: asset.ReleaseStatusPending,
	})
	require.NoError(t, err)
	_, err = repo.Create(ctx, &asset.AssetRelease{
		AssetID: "asset-006", ReleaseType: asset.ReleaseTypeProperty, Status: asset.ReleaseStatusVerified,
	})
	require.NoError(t, err)

	releases, err := repo.ListByAsset(ctx, "asset-006")
	require.NoError(t, err)
	assert.Len(t, releases, 2)
}

func TestArtlistDownloadAuditRepository_RecordDownloadWithLicenseRelease(t *testing.T) {
	db := newLicenseReleaseTestDB(t)
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

func TestAssetReleaseRepository_UpdateAndDelete(t *testing.T) {
	db := newLicenseReleaseTestDB(t)
	ctx := context.Background()

	repo, err := NewAssetReleaseRepository(db, zap.NewNop())
	require.NoError(t, err)

	seedLicenseReleaseMediaAsset(t, db, "asset-007")

	id, err := repo.Create(ctx, &asset.AssetRelease{
		AssetID: "asset-007", ReleaseType: asset.ReleaseTypeBoth, Status: asset.ReleaseStatusPending,
	})
	require.NoError(t, err)

	release, err := repo.Get(ctx, id)
	require.NoError(t, err)
	release.Status = asset.ReleaseStatusVerified
	require.NoError(t, repo.Update(ctx, release))

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, asset.ReleaseStatusVerified, got.Status)

	require.NoError(t, repo.Delete(ctx, id))
	got, err = repo.Get(ctx, id)
	require.NoError(t, err)
	assert.Nil(t, got)
}
