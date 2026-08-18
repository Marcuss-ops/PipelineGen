// Package checkpoint persists the durable per-unit checkpoint registry in
// SQLite (table job_checkpoints, migration 216). The adapter is the single
// store for every workflow stage — never per-stage stores.
package checkpoint

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"
)

var ErrNotWired = errors.New("checkpoint sqlite adapter: not wired")

type Store struct{ db *sql.DB }

// New constructs the adapter. Fail-closed: a nil database is a construction
// error.
func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, ErrNotWired
	}
	return &Store{db: db}, nil
}

var _ capcheckpoint.Store = (*Store)(nil)

// Get returns the unit's checkpoint, or (nil, nil) when the unit has no
// checkpoint (missing and invalidated are indistinguishable for resume).
// Fail-closed: empty identity is an error, never a silent miss.
func (s *Store) Get(ctx context.Context, jobID, stage, unitID string) (*capcheckpoint.Checkpoint, error) {
	if err := validateIdentity(jobID, stage, unitID); err != nil {
		return nil, err
	}
	var c capcheckpoint.Checkpoint
	var completedAt string
	err := s.db.QueryRowContext(ctx, `SELECT job_id, stage, unit_id, input_fingerprint, status, artifact_sha256, artifact_uri, processor_version, completed_at
		FROM job_checkpoints WHERE job_id = ? AND stage = ? AND unit_id = ?`, jobID, stage, unitID).
		Scan(&c.JobID, &c.Stage, &c.UnitID, &c.InputFingerprint, &c.Status, &c.ArtifactSHA256, &c.ArtifactURI, &c.ProcessorVersion, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get checkpoint %s/%s/%s: %w", jobID, stage, unitID, err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, completedAt)
	if err != nil {
		return nil, fmt.Errorf("get checkpoint %s/%s/%s: invalid completed_at %q: %w", jobID, stage, unitID, completedAt, err)
	}
	c.CompletedAt = parsed
	return &c, nil
}

// Complete upserts the unit's completion record. Re-completing the same
// unit converges on the same row (idempotent: a crash between render and
// Complete never leaves a duplicate). Fail-closed: an invalid checkpoint is
// never persisted.
func (s *Store) Complete(ctx context.Context, c capcheckpoint.Checkpoint) error {
	if err := c.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO job_checkpoints (job_id, stage, unit_id, input_fingerprint, status, artifact_sha256, artifact_uri, processor_version, completed_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(job_id, stage, unit_id) DO UPDATE SET
			input_fingerprint = excluded.input_fingerprint,
			status = excluded.status,
			artifact_sha256 = excluded.artifact_sha256,
			artifact_uri = excluded.artifact_uri,
			processor_version = excluded.processor_version,
			completed_at = excluded.completed_at`,
		c.JobID, c.Stage, c.UnitID, c.InputFingerprint, c.Status, c.ArtifactSHA256, c.ArtifactURI, c.ProcessorVersion, c.CompletedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("complete checkpoint %s/%s/%s: %w", c.JobID, c.Stage, c.UnitID, err)
	}
	return nil
}

// Invalidate removes the unit's checkpoint so resume treats the unit as not
// completed (recompute). Idempotent: invalidating a missing checkpoint is a
// no-op, never an error.
func (s *Store) Invalidate(ctx context.Context, jobID, stage, unitID string) error {
	if err := validateIdentity(jobID, stage, unitID); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM job_checkpoints WHERE job_id = ? AND stage = ? AND unit_id = ?`, jobID, stage, unitID); err != nil {
		return fmt.Errorf("invalidate checkpoint %s/%s/%s: %w", jobID, stage, unitID, err)
	}
	return nil
}

func validateIdentity(jobID, stage, unitID string) error {
	if strings.TrimSpace(jobID) == "" || strings.TrimSpace(stage) == "" || strings.TrimSpace(unitID) == "" {
		return fmt.Errorf("%w: job_id, stage and unit_id are required", capcheckpoint.ErrInvalidCheckpoint)
	}
	return nil
}
