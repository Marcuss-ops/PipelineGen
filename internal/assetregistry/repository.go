package assetregistry

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SQLiteRepository persists asset metadata in the unified media.db.sqlite.
type SQLiteRepository struct {
	db *sql.DB
}

// NewSQLiteRepository creates a new asset registry repository.
func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{db: db}
}

// SchemaDDL returns the DDL for the asset registry tables.
// This matches migration 054_asset_registry.sql exactly.
func SchemaDDL() string {
	return `
CREATE TABLE IF NOT EXISTS assets (
    asset_id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('voiceover','scene_image','stock_clip','music','font','subtitle','thumbnail')),
    status TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING','READY','FAILED','DELETED')),
    sha256 TEXT NOT NULL UNIQUE,
    storage_backend TEXT NOT NULL DEFAULT 'local',
    storage_key TEXT NOT NULL UNIQUE,
    mime_type TEXT,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER,
    width INTEGER,
    height INTEGER,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    verified_at TEXT,
    last_accessed_at TEXT,
    deleted_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_assets_sha256 ON assets(sha256);
CREATE INDEX IF NOT EXISTS idx_assets_kind ON assets(kind, status);
CREATE INDEX IF NOT EXISTS idx_assets_storage ON assets(storage_backend, storage_key);

CREATE TABLE IF NOT EXISTS asset_sources (
    source_id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL,
    source_type TEXT NOT NULL,
    source_reference TEXT NOT NULL,
    source_account_id TEXT,
    imported_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY(asset_id) REFERENCES assets(asset_id)
);

CREATE INDEX IF NOT EXISTS idx_asset_sources_asset ON asset_sources(asset_id);

CREATE TABLE IF NOT EXISTS job_assets (
    job_id TEXT NOT NULL,
    asset_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('voiceover','scene_image','stock_clip','music','font','subtitle','thumbnail')),
    ordinal INTEGER NOT NULL DEFAULT 0,
    required INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY(job_id, role, ordinal),
    FOREIGN KEY(job_id) REFERENCES jobs(job_id),
    FOREIGN KEY(asset_id) REFERENCES assets(asset_id)
);

CREATE INDEX IF NOT EXISTS idx_job_assets_asset ON job_assets(asset_id);
`
}

// ── Asset CRUD ────────────────────────────────────────────────────────

// CreateAsset inserts a new asset record.
func (r *SQLiteRepository) CreateAsset(ctx context.Context, a *Asset) error {
	now := time.Now().UTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO assets (asset_id, kind, status, sha256,
			storage_backend, storage_key, mime_type, size_bytes,
			duration_ms, width, height, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, a.AssetID, a.Kind, a.Status, a.SHA256,
		a.StorageBackend, a.StorageKey, a.MimeType, a.SizeBytes,
		a.DurationMs, a.Width, a.Height,
		a.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("assetregistry: create asset %s: %w", a.AssetID, err)
	}
	return nil
}

// GetAsset retrieves an asset by ID.
func (r *SQLiteRepository) GetAsset(ctx context.Context, assetID string) (*Asset, error) {
	var a Asset
	var createdAt, verifiedAt, lastAccessedAt, deletedAt sql.NullString

	err := r.db.QueryRowContext(ctx, `
		SELECT asset_id, kind, status, sha256,
			storage_backend, storage_key, mime_type, size_bytes,
			duration_ms, width, height,
			created_at, verified_at, last_accessed_at, deleted_at
		FROM assets WHERE asset_id = ?
	`, assetID).Scan(
		&a.AssetID, &a.Kind, &a.Status, &a.SHA256,
		&a.StorageBackend, &a.StorageKey, &a.MimeType, &a.SizeBytes,
		&a.DurationMs, &a.Width, &a.Height,
		&createdAt, &verifiedAt, &lastAccessedAt, &deletedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("assetregistry: get asset %s: %w", assetID, err)
	}

	a.CreatedAt, _ = time.Parse(time.RFC3339, createdAt.String)
	if verifiedAt.Valid {
		t, _ := time.Parse(time.RFC3339, verifiedAt.String)
		a.VerifiedAt = &t
	}
	if lastAccessedAt.Valid {
		t, _ := time.Parse(time.RFC3339, lastAccessedAt.String)
		a.LastAccessedAt = &t
	}
	if deletedAt.Valid {
		t, _ := time.Parse(time.RFC3339, deletedAt.String)
		a.DeletedAt = &t
	}
	return &a, nil
}

// GetAssetBySHA256 retrieves an asset by its SHA-256 content hash.
func (r *SQLiteRepository) GetAssetBySHA256(ctx context.Context, sha256 string) (*Asset, error) {
	if sha256 == "" {
		return nil, nil
	}

	var a Asset
	var createdAt, verifiedAt, lastAccessedAt, deletedAt sql.NullString

	err := r.db.QueryRowContext(ctx, `
		SELECT asset_id, kind, status, sha256,
			storage_backend, storage_key, mime_type, size_bytes,
			duration_ms, width, height,
			created_at, verified_at, last_accessed_at, deleted_at
		FROM assets WHERE sha256 = ? AND status != 'DELETED'
		LIMIT 1
	`, sha256).Scan(
		&a.AssetID, &a.Kind, &a.Status, &a.SHA256,
		&a.StorageBackend, &a.StorageKey, &a.MimeType, &a.SizeBytes,
		&a.DurationMs, &a.Width, &a.Height,
		&createdAt, &verifiedAt, &lastAccessedAt, &deletedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("assetregistry: get asset by sha256: %w", err)
	}

	a.CreatedAt, _ = time.Parse(time.RFC3339, createdAt.String)
	if verifiedAt.Valid {
		t, _ := time.Parse(time.RFC3339, verifiedAt.String)
		a.VerifiedAt = &t
	}
	if lastAccessedAt.Valid {
		t, _ := time.Parse(time.RFC3339, lastAccessedAt.String)
		a.LastAccessedAt = &t
	}
	if deletedAt.Valid {
		t, _ := time.Parse(time.RFC3339, deletedAt.String)
		a.DeletedAt = &t
	}
	return &a, nil
}

// UpdateStatus transitions an asset to a new status.
func (r *SQLiteRepository) UpdateStatus(ctx context.Context, assetID string, status Status) error {
	now := time.Now().UTC().Format(time.RFC3339)

	// Use CASE expression to set timestamp columns based on target status
	_, err := r.db.ExecContext(ctx, `
		UPDATE assets
		SET status = ?,
			verified_at = CASE WHEN ? = 'READY' THEN ? ELSE verified_at END,
			deleted_at = CASE WHEN ? = 'DELETED' THEN ? ELSE deleted_at END
		WHERE asset_id = ?
	`, status, status, now, status, now, assetID)
	if err != nil {
		return fmt.Errorf("assetregistry: update status %s: %w", assetID, err)
	}

	// Touch last_accessed_at for READY assets
	if status == StatusReady {
		r.db.ExecContext(ctx, `UPDATE assets SET last_accessed_at = ? WHERE asset_id = ?`, now, assetID)
	}
	return nil
}

// TouchAccess updates last_accessed_at for an asset.
func (r *SQLiteRepository) TouchAccess(ctx context.Context, assetID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx, `UPDATE assets SET last_accessed_at = ? WHERE asset_id = ?`, now, assetID)
	return err
}

// ── AssetSource CRUD ───────────────────────────────────────────────────

// CreateSource inserts a new asset source provenance record.
func (r *SQLiteRepository) CreateSource(ctx context.Context, s *AssetSource) error {
	now := time.Now().UTC()
	if s.ImportedAt.IsZero() {
		s.ImportedAt = now
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO asset_sources (source_id, asset_id, source_type,
			source_reference, source_account_id, imported_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, s.SourceID, s.AssetID, s.SourceType,
		s.SourceReference, s.SourceAccountID,
		s.ImportedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("assetregistry: create source %s: %w", s.SourceID, err)
	}
	return nil
}

// ── JobAsset CRUD ──────────────────────────────────────────────────────

// UpsertJobAsset inserts or replaces a job-asset link.
func (r *SQLiteRepository) UpsertJobAsset(ctx context.Context, ja *JobAsset) error {
	now := time.Now().UTC()
	if ja.CreatedAt.IsZero() {
		ja.CreatedAt = now
	}
	required := 0
	if ja.Required {
		required = 1
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO job_assets (job_id, asset_id, role, ordinal, required, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id, role, ordinal) DO UPDATE SET
			asset_id = excluded.asset_id,
			required = excluded.required
	`, ja.JobID, ja.AssetID, ja.Role, ja.Ordinal, required,
		ja.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("assetregistry: upsert job_asset: %w", err)
	}
	return nil
}

// ListJobAssets returns all asset links for a job.
func (r *SQLiteRepository) ListJobAssets(ctx context.Context, jobID string) ([]JobAsset, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT job_id, asset_id, role, ordinal, required, created_at
		FROM job_assets WHERE job_id = ? ORDER BY ordinal
	`, jobID)
	if err != nil {
		return nil, fmt.Errorf("assetregistry: list job_assets %s: %w", jobID, err)
	}
	defer rows.Close()

	var items []JobAsset
	for rows.Next() {
		var ja JobAsset
		var required int
		var createdAt string
		if err := rows.Scan(&ja.JobID, &ja.AssetID, &ja.Role, &ja.Ordinal, &required, &createdAt); err != nil {
			return nil, fmt.Errorf("assetregistry: scan job_asset: %w", err)
		}
		ja.Required = required != 0
		ja.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		items = append(items, ja)
	}
	return items, rows.Err()
}

// GetJobAsset retrieves a specific job-asset link.
func (r *SQLiteRepository) GetJobAsset(ctx context.Context, jobID, assetID string) (*JobAsset, error) {
	var ja JobAsset
	var required int
	var createdAt string

	err := r.db.QueryRowContext(ctx, `
		SELECT job_id, asset_id, role, ordinal, required, created_at
		FROM job_assets WHERE job_id = ? AND asset_id = ?
		LIMIT 1
	`, jobID, assetID).Scan(&ja.JobID, &ja.AssetID, &ja.Role, &ja.Ordinal, &required, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("assetregistry: get job_asset %s/%s: %w", jobID, assetID, err)
	}
	ja.Required = required != 0
	ja.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &ja, nil
}

// Compile-time check
var _ AssetRepository = (*SQLiteRepository)(nil)
