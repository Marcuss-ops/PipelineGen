// internal/infrastructure/database/sqlite/artifact_stages/repository_artifacts.go —
// artifact CRUD reads/writes (Insert / GetByID / ListByJob / ListByState).
// Extracted from repository.go; no behavior change.
package artifactstages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	artifact "github.com/Marcuss-ops/PipelineGen/internal/domain/artifact"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── Insert ──────────────────────────────────────────────────────────────

// Insert appends a new ArtifactStage row. State is forced to STAGED
// (the initial state of the saga); callers MAY supply a non-STAGED
// value but the repository will surface ErrInvalidArtifactStageState
// unless the state is in the canonical 4-value set.
func (r *Repository) Insert(ctx context.Context, stage *artifact.ArtifactStage) error {
	if err := validateForWrite(stage); err != nil {
		return err
	}
	now := r.now()
	if stage.CreatedAt.IsZero() {
		stage.CreatedAt = now
	}
	stage.UpdatedAt = now

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO artifact_stages (`+selectColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		stage.ID, stage.JobID, stage.LocalPath, stage.Hash, stage.Size, stage.Mime,
		string(stage.Requirement), stage.Destination, string(stage.State),
		stage.AttemptCount, stage.LastError, stage.PublishedLocation,
		timeutil.FormatPtrRFC3339Nano(stage.PublishedAt),
		timeutil.FormatRFC3339Nano(stage.CreatedAt),
		timeutil.FormatRFC3339Nano(stage.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("artifact_stages.Insert (id=%s): %w", stage.ID, err)
	}
	return nil
}

// ── GetByID ──────────────────────────────────────────────────────────────

func (r *Repository) GetByID(ctx context.Context, id string) (*artifact.ArtifactStage, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+selectColumns+` FROM artifact_stages WHERE id = ?`, id)
	stage, err := scanRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, artifact.WrapArtifactStageNotFound(id)
		}
		return nil, fmt.Errorf("artifact_stages.GetByID (id=%s): %w", id, err)
	}
	return &stage, nil
}

// ── ListByJob ───────────────────────────────────────────────────────────

func (r *Repository) ListByJob(ctx context.Context, jobID string) ([]artifact.ArtifactStage, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+selectColumns+` FROM artifact_stages WHERE job_id = ? ORDER BY created_at ASC`,
		jobID)
	if err != nil {
		return nil, fmt.Errorf("artifact_stages.ListByJob (job_id=%s): %w", jobID, err)
	}
	defer rows.Close()
	var out []artifact.ArtifactStage
	for rows.Next() {
		stage, scanErr := scanRow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("artifact_stages.ListByJob: scan: %w", scanErr)
		}
		out = append(out, stage)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("artifact_stages.ListByJob: rows: %w", err)
	}
	return out, nil
}

// ── ListByState ─────────────────────────────────────────────────────────

func (r *Repository) ListByState(ctx context.Context, state artifact.ArtifactStageState, limit int) ([]artifact.ArtifactStage, error) {
	if !state.IsValid() {
		return nil, fmt.Errorf("%w: %q", artifact.ErrInvalidArtifactStageState, state)
	}
	if limit <= 0 {
		limit = 100 // safe default
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+selectColumns+` FROM artifact_stages WHERE state = ? ORDER BY created_at ASC LIMIT ?`,
		string(state), limit)
	if err != nil {
		return nil, fmt.Errorf("artifact_stages.ListByState (state=%s): %w", state, err)
	}
	defer rows.Close()
	var out []artifact.ArtifactStage
	for rows.Next() {
		stage, scanErr := scanRow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("artifact_stages.ListByState: scan: %w", scanErr)
		}
		out = append(out, stage)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("artifact_stages.ListByState: rows: %w", err)
	}
	return out, nil
}
