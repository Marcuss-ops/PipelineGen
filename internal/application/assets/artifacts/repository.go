package artifacts

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SQLiteRepository persists artifact metadata in the unified media.db.sqlite.
type SQLiteRepository struct {
	db *sql.DB
}

// NewSQLiteRepository creates a new artifact repository.
func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{db: db}
}

// SchemaDDL returns the DDL for the artifacts table.
func SchemaDDL() string {
	return `
CREATE TABLE IF NOT EXISTS artifacts (
    id              TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL DEFAULT 'unknown',
    status          TEXT NOT NULL DEFAULT 'STAGING'
        CHECK (status IN ('STAGING','VERIFYING','READY','FAILED','QUARANTINED','DELETED')),
    storage_backend TEXT NOT NULL DEFAULT 'local',
    storage_key     TEXT NOT NULL DEFAULT '',
    sha256          TEXT NOT NULL DEFAULT '',
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    mime_type       TEXT NOT NULL DEFAULT '',
    duration_ms     INTEGER,
    width           INTEGER,
    height          INTEGER,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    verified_at     TEXT,
    last_accessed_at TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_artifacts_sha256 ON artifacts(sha256) WHERE sha256 != '';
CREATE INDEX IF NOT EXISTS idx_artifacts_job ON artifacts(job_id);
CREATE INDEX IF NOT EXISTS idx_artifacts_status ON artifacts(status);

CREATE TABLE IF NOT EXISTS artifact_sources (
    source_id         TEXT PRIMARY KEY,
    artifact_id       TEXT NOT NULL,
    source_type       TEXT NOT NULL DEFAULT '',
    source_reference  TEXT NOT NULL DEFAULT '',
    source_account_id TEXT NOT NULL DEFAULT '',
    imported_at       TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_artifact_sources_artifact ON artifact_sources(artifact_id);

CREATE TABLE IF NOT EXISTS job_artifacts (
    job_id      TEXT NOT NULL,
    artifact_id TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT '',
    ordinal     INTEGER NOT NULL DEFAULT 0,
    required    INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (job_id, artifact_id)
);
CREATE INDEX IF NOT EXISTS idx_job_artifacts_job ON job_artifacts(job_id);
`
}

// Create inserts a new artifact record.
func (r *SQLiteRepository) Create(ctx context.Context, a *Artifact) error {
	now := time.Now().UTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	a.UpdatedAt = now

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO artifacts (id, job_id, kind, status, storage_backend,
			storage_key, sha256, size_bytes, mime_type, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, a.ID, a.JobID, a.Kind, a.Status, a.StorageBackend,
		a.StorageKey, a.SHA256, a.SizeBytes, a.MimeType,
		a.CreatedAt.UTC().Format(time.RFC3339), a.UpdatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("artifacts: create %s: %w", a.ID, err)
	}
	return nil
}

// Get retrieves an artifact by ID.
func (r *SQLiteRepository) Get(ctx context.Context, id string) (*Artifact, error) {
	var a Artifact
	var createdAt, updatedAt string
	var verifiedAt sql.NullString

	err := r.db.QueryRowContext(ctx, `
		SELECT id, job_id, kind, status, storage_backend,
			storage_key, sha256, size_bytes, mime_type,
			created_at, updated_at, verified_at
		FROM artifacts WHERE id = ?
	`, id).Scan(
		&a.ID, &a.JobID, &a.Kind, &a.Status, &a.StorageBackend,
		&a.StorageKey, &a.SHA256, &a.SizeBytes, &a.MimeType,
		&createdAt, &updatedAt, &verifiedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("artifacts: get %s: %w", id, err)
	}

	a.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	a.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if verifiedAt.Valid {
		t, _ := time.Parse(time.RFC3339, verifiedAt.String)
		a.VerifiedAt = &t
	}
	return &a, nil
}

// GetBySHA256 retrieves an artifact by its SHA-256 hash.
func (r *SQLiteRepository) GetBySHA256(ctx context.Context, sha256 string) (*Artifact, error) {
	var a Artifact
	var createdAt, updatedAt string
	var verifiedAt sql.NullString

	err := r.db.QueryRowContext(ctx, `
		SELECT id, job_id, kind, status, storage_backend,
			storage_key, sha256, size_bytes, mime_type,
			created_at, updated_at, verified_at
		FROM artifacts WHERE sha256 = ? AND status != 'DELETED'
		LIMIT 1
	`, sha256).Scan(
		&a.ID, &a.JobID, &a.Kind, &a.Status, &a.StorageBackend,
		&a.StorageKey, &a.SHA256, &a.SizeBytes, &a.MimeType,
		&createdAt, &updatedAt, &verifiedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("artifacts: get by sha256: %w", err)
	}

	a.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	a.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if verifiedAt.Valid {
		t, _ := time.Parse(time.RFC3339, verifiedAt.String)
		a.VerifiedAt = &t
	}
	return &a, nil
}

// UpdateStatus transitions an artifact to a new status and updates its
// storage metadata atomically. Uses parameterized SQL for verified_at.
func (r *SQLiteRepository) UpdateStatus(ctx context.Context, id string, status Status, sha256 string, sizeBytes int64) error {
	now := time.Now().UTC().Format(time.RFC3339)

	// Set verified_at via CASE expression for READY status
	_, err := r.db.ExecContext(ctx, `
		UPDATE artifacts
		SET status = ?, sha256 = ?, size_bytes = ?, updated_at = ?,
			storage_key = CASE
				WHEN ? = 'READY' AND length(?) = 64
				THEN 'sha256/' || substr(?, 1, 2) || '/' || ?
				ELSE storage_key
			END,
			verified_at = CASE WHEN ? = 'READY' THEN ? ELSE verified_at END
		WHERE id = ?
	`, status, sha256, sizeBytes, now, status, sha256, sha256, sha256, status, now, id)
	if err != nil {
		return fmt.Errorf("artifacts: update status %s: %w", id, err)
	}
	return nil
}

// ListByJob returns all artifacts associated with a job.
func (r *SQLiteRepository) ListByJob(ctx context.Context, jobID string) ([]Artifact, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, job_id, kind, status, storage_backend,
			storage_key, sha256, size_bytes, mime_type,
			created_at, updated_at, verified_at
		FROM artifacts WHERE job_id = ? ORDER BY created_at
	`, jobID)
	if err != nil {
		return nil, fmt.Errorf("artifacts: list by job %s: %w", jobID, err)
	}
	defer rows.Close()

	var artifacts []Artifact
	for rows.Next() {
		var a Artifact
		var createdAt, updatedAt string
		var verifiedAt sql.NullString
		if err := rows.Scan(
			&a.ID, &a.JobID, &a.Kind, &a.Status, &a.StorageBackend,
			&a.StorageKey, &a.SHA256, &a.SizeBytes, &a.MimeType,
			&createdAt, &updatedAt, &verifiedAt,
		); err != nil {
			return nil, fmt.Errorf("artifacts: scan: %w", err)
		}
		a.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		a.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		if verifiedAt.Valid {
			t, _ := time.Parse(time.RFC3339, verifiedAt.String)
			a.VerifiedAt = &t
		}
		artifacts = append(artifacts, a)
	}
	return artifacts, rows.Err()
}

// ── Provenance (PR3) ─────────────────────────────────────────────────

// CreateSource inserts an artifact source record.
func (r *SQLiteRepository) CreateSource(ctx context.Context, s *ArtifactSource) error {
	if s.ImportedAt.IsZero() {
		s.ImportedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO artifact_sources (source_id, artifact_id, source_type,
			source_reference, source_account_id, imported_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, s.SourceID, s.ArtifactID, s.SourceType,
		s.SourceReference, s.SourceAccountID,
		s.ImportedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("artifacts: create source %s: %w", s.SourceID, err)
	}
	return nil
}

// ── Job-artifact linking (PR3) ──────────────────────────────────────

// UpsertJobArtifact inserts or updates a job-artifact link.
func (r *SQLiteRepository) UpsertJobArtifact(ctx context.Context, ja *JobArtifact) error {
	if ja.CreatedAt.IsZero() {
		ja.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO job_artifacts (job_id, artifact_id, role, ordinal, required, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id, artifact_id) DO UPDATE SET
			role = excluded.role, ordinal = excluded.ordinal,
			required = excluded.required
	`, ja.JobID, ja.ArtifactID, ja.Role, ja.Ordinal, ja.Required,
		ja.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("artifacts: upsert job artifact %s/%s: %w", ja.JobID, ja.ArtifactID, err)
	}
	return nil
}

// ListJobArtifacts returns all artifact links for a job.
func (r *SQLiteRepository) ListJobArtifacts(ctx context.Context, jobID string) ([]JobArtifact, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT job_id, artifact_id, role, ordinal, required, created_at
		FROM job_artifacts WHERE job_id = ? ORDER BY ordinal
	`, jobID)
	if err != nil {
		return nil, fmt.Errorf("artifacts: list job artifacts %s: %w", jobID, err)
	}
	defer rows.Close()

	var artifacts []JobArtifact
	for rows.Next() {
		var ja JobArtifact
		var createdAt string
		if err := rows.Scan(&ja.JobID, &ja.ArtifactID, &ja.Role, &ja.Ordinal, &ja.Required, &createdAt); err != nil {
			return nil, fmt.Errorf("artifacts: scan job artifact: %w", err)
		}
		ja.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		artifacts = append(artifacts, ja)
	}
	return artifacts, rows.Err()
}

// GetJobArtifact retrieves a single job-artifact link.
func (r *SQLiteRepository) GetJobArtifact(ctx context.Context, jobID, artifactID string) (*JobArtifact, error) {
	var ja JobArtifact
	var createdAt string

	err := r.db.QueryRowContext(ctx, `
		SELECT job_id, artifact_id, role, ordinal, required, created_at
		FROM job_artifacts WHERE job_id = ? AND artifact_id = ?
	`, jobID, artifactID).Scan(&ja.JobID, &ja.ArtifactID, &ja.Role, &ja.Ordinal, &ja.Required, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("artifacts: get job artifact %s/%s: %w", jobID, artifactID, err)
	}
	ja.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &ja, nil
}

// TouchAccess updates last_accessed_at for an artifact (PR3: from assetregistry).
func (r *SQLiteRepository) TouchAccess(ctx context.Context, artifactID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx, `UPDATE artifacts SET last_accessed_at = ? WHERE id = ?`, now, artifactID)
	return err
}

// Compile-time check
var _ Repository = (*SQLiteRepository)(nil)
