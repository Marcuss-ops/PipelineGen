package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// Attempt statuses recorded in render_attempts. The vocabulary lives once in
// model.AttemptStatuses() (pinned to the SQL CHECK constraint by
// queue/internal/model/state_schema_test.go); these names are only the local
// spelling used by the statements below.
const (
	attemptStatusRunning      = string(model.AttemptRunning)
	attemptStatusCompleted    = string(model.AttemptCompleted)
	attemptStatusFailed       = string(model.AttemptFailed)
	attemptStatusLeaseExpired = string(model.AttemptLeaseExpired)
	attemptStatusRendered     = string(model.AttemptRendered)
	attemptStatusCancelled    = string(model.AttemptCancelled)
)

// attemptID builds the deterministic, readable ID for an attempt.
func attemptID(jobID string, n int) string {
	return fmt.Sprintf("%s#%d", jobID, n)
}

// createAttempt inserts a new running attempt when a job is claimed.
func createAttempt(ctx context.Context, tx *sql.Tx, jobID string, n int, workerID string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO render_attempts (id, job_id, attempt_number, worker_id, status, started_at, lease_started_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		attemptID(jobID, n), jobID, n, workerID, attemptStatusRunning, now)
	return err
}

// runningAttemptID returns the ID of the job's currently running attempt, or
// an empty string if none exists.
func runningAttemptID(ctx context.Context, tx *sql.Tx, jobID string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `
		SELECT id FROM render_attempts
		WHERE job_id = $1 AND status = $2
		ORDER BY attempt_number DESC
		LIMIT 1`, jobID, attemptStatusRunning).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// finishAttempt closes an attempt with a terminal status, preserving the
// attempt's error details.
func finishAttempt(ctx context.Context, tx *sql.Tx, id, status, errorCode, errorMessage string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE render_attempts
		SET status = $2, finished_at = now(), error_code = $3, error_message = $4
		WHERE id = $1`,
		id, status, nullIfEmpty(errorCode), nullIfEmpty(errorMessage))
	return err
}
