// finalize.go owns the terminal transitions of the queue: claiming a parent
// row for the finalizer, completing a job (with artifact persistence) and
// marking a job rendered for a publication-only retry.
package postgres

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// ClaimFinalization atomically claims a parent row for one finalizer.
func (r *Repository) ClaimFinalization(ctx context.Context, parentJobID, workerID string) (*model.Job, bool, error) {
	if parentJobID == "" || workerID == "" {
		return nil, false, fmt.Errorf("parent job id and worker id are required")
	}
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	res, err := r.db.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = `+stateLiteral(model.StateFinalizing)+`, current_worker_id = $2, started_at = now(),
		    lease_until = now() + make_interval(secs => $3)
		WHERE id = $1 AND state IN `+stateIn(model.StatePending, model.StateRunning), parentJobID, workerID, r.lease.Seconds())
	if err != nil {
		return nil, false, err
	}
	n, err := rowsAffected(res)
	if err != nil {
		return nil, false, err
	}
	if n == 0 {
		job, getErr := r.Get(ctx, parentJobID)
		if getErr != nil {
			return nil, false, getErr
		}
		if job.State == model.StateFinalizing || job.State == model.StateCompleted {
			return job, false, nil
		}
		return job, false, fmt.Errorf("parent job %s is in state %q", parentJobID, job.State)
	}
	job, err := r.Get(ctx, parentJobID)
	return job, true, err
}

// Complete marks a running job as completed and, when the artifact has a
// storage key, persists it and links it to the job.
func (r *Repository) Complete(ctx context.Context, id, workerID string, artifact model.Artifact) error {
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = `+stateLiteral(model.StateCompleted)+`, completed_at = now()
		WHERE id = $1 AND state IN `+stateIn(model.StateRunning, model.StateFinalizing)+` AND current_worker_id = $2`, id, workerID)
	if err != nil {
		return err
	}
	n, err := rowsAffected(res)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("job %s is not running or not owned by %s", id, workerID)
	}

	attempt, err := runningAttemptID(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := finishAttempt(ctx, tx, attempt, attemptStatusCompleted, "", ""); err != nil {
		return err
	}
	if err := recordEvent(ctx, tx, eventJobCompleted, id, attempt, workerID, nil); err != nil {
		return err
	}

	if artifact.StorageKey != "" {
		if artifact.ID == "" {
			artifact.ID = id // one artifact per job
		}
		if err := insertArtifact(ctx, tx, id, artifact); err != nil {
			return err
		}
	}
	if err := insertProcessingMetrics(ctx, tx, id, attempt, artifact.Metrics); err != nil {
		return err
	}
	if err := insertRenderTelemetry(ctx, tx, id, attempt, artifact.ChrononTelemetry); err != nil {
		return err
	}
	return tx.Commit()
}

// Rendered marks a running job as rendered: its artifact is durably stored in
// the object store but external publication (Google Drive) failed. The job is
// kept out of `completed` and becomes claimable again for a publication-only
// retry, so a flaky upload never wastes a GPU re-render.
func (r *Repository) Rendered(ctx context.Context, id, workerID string, artifact model.Artifact, reason string) error {
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = `+stateLiteral(model.StateRendered)+`, error_message = $2, queued_at = now(),
		    current_worker_id = NULL, lease_until = NULL
		WHERE id = $1 AND state = `+stateLiteral(model.StateRunning)+` AND current_worker_id = $3`, id, reason, workerID)
	if err != nil {
		return err
	}
	n, err := rowsAffected(res)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("job %s is not running or not owned by %s", id, workerID)
	}

	attempt, err := runningAttemptID(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := finishAttempt(ctx, tx, attempt, attemptStatusRendered, "drive_upload_failed", reason); err != nil {
		return err
	}
	if err := recordEvent(ctx, tx, eventJobRendered, id, attempt, workerID, map[string]any{"reason": reason}); err != nil {
		return err
	}

	if artifact.StorageKey != "" {
		if artifact.ID == "" {
			artifact.ID = id // one artifact per job
		}
		if err := insertArtifact(ctx, tx, id, artifact); err != nil {
			return err
		}
	}
	if err := insertProcessingMetrics(ctx, tx, id, attempt, artifact.Metrics); err != nil {
		return err
	}
	if err := insertRenderTelemetry(ctx, tx, id, attempt, artifact.ChrononTelemetry); err != nil {
		return err
	}
	return tx.Commit()
}
