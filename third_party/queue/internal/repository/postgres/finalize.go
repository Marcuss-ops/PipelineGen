// finalize.go owns the terminal transitions of the queue: claiming a parent
// row for the finalizer, completing a job (with artifact persistence) and
// marking a job rendered for a publication-only retry.
package postgres

import (
	"context"
	"database/sql"
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
	return r.withTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE render_jobs
			SET state = `+stateLiteral(model.StateCompleted)+`, completed_at = now()
			WHERE id = $1 AND state IN `+stateIn(model.StateRunning, model.StateFinalizing)+` AND current_worker_id = $2`, id, workerID)
		if err != nil {
			return err
		}
		attempt, err := closeRunningAttempt(ctx, tx, res, id, workerID, attemptStatusCompleted, "", "")
		if err != nil {
			return err
		}
		if err := recordEvent(ctx, tx, eventJobCompleted, id, attempt, workerID, nil); err != nil {
			return err
		}
		return persistArtifactTx(ctx, tx, id, attempt, artifact)
	})
}

// closeRunningAttempt validates the conditional row transition and closes the
// running attempt with the given terminal status. Every terminal transition
// performs these three steps in this order, so they live here once.
func closeRunningAttempt(ctx context.Context, tx *sql.Tx, res sql.Result, id, workerID, status, failureCode, reason string) (string, error) {
	if err := requireTransitionRows(res, id, workerID); err != nil {
		return "", err
	}
	attempt, err := runningAttemptID(ctx, tx, id)
	if err != nil {
		return "", err
	}
	if err := finishAttempt(ctx, tx, attempt, status, failureCode, reason); err != nil {
		return "", err
	}
	return attempt, nil
}

// persistArtifactTx links the artifact and its metrics/telemetry to the job
// inside the transition transaction. Complete and Rendered write the identical
// tail (one artifact per job, artifact id defaults to the job id), so the shape
// lives here rather than twice.
func persistArtifactTx(ctx context.Context, tx *sql.Tx, jobID, attempt string, artifact model.Artifact) error {
	if artifact.StorageKey != "" {
		if artifact.ID == "" {
			artifact.ID = jobID
		}
		if err := insertArtifact(ctx, tx, jobID, artifact); err != nil {
			return err
		}
	}
	if err := insertProcessingMetrics(ctx, tx, jobID, attempt, artifact.Metrics); err != nil {
		return err
	}
	return insertRenderTelemetry(ctx, tx, jobID, attempt, artifact.ChrononTelemetry)
}

// Rendered marks a running job as rendered: its artifact is durably stored in
// the object store but external publication (Google Drive) failed. The job is
// kept out of `completed` and becomes claimable again for a publication-only
// retry, so a flaky upload never wastes a GPU re-render.
func (r *Repository) Rendered(ctx context.Context, id, workerID string, artifact model.Artifact, reason string) error {
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	return r.withTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE render_jobs
			SET state = `+stateLiteral(model.StateRendered)+`, error_message = $2, queued_at = now(),
			    current_worker_id = NULL, lease_until = NULL
			WHERE id = $1 AND state = `+stateLiteral(model.StateRunning)+` AND current_worker_id = $3`, id, reason, workerID)
		if err != nil {
			return err
		}
		attempt, err := closeRunningAttempt(ctx, tx, res, id, workerID, attemptStatusRendered, "drive_upload_failed", reason)
		if err != nil {
			return err
		}
		if err := recordEvent(ctx, tx, eventJobRendered, id, attempt, workerID, map[string]any{"reason": reason}); err != nil {
			return err
		}
		return persistArtifactTx(ctx, tx, id, attempt, artifact)
	})
}
