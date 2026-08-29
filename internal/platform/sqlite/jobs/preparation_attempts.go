package jobs

import (
	"context"
	"fmt"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/google/uuid"
)

// PreparationAttemptInput describes one execution attempt. The database
// remains the durable source of truth for attempt lifecycle and metrics.
type PreparationAttemptInput struct {
	UnitFingerprint   string
	TriggerJobID      string
	WorkerID          string
	Host              string
	ExecutionMode     string
	ResourceClass     string
	SchedulerPriority float64
	ExpectedWorkMS    int64
	WorkloadDimension string
	WorkloadAmount    float64
	Unit              *job.PreparationUnit
}

// PreparationAttempt is the durable execution record returned by the store.
type PreparationAttempt struct {
	AttemptID       string
	UnitFingerprint string
	Status          string
	ExecutionMode   string
	WorkerID        string
	StartedAt       time.Time
	FinishedAt      *time.Time
	WallMS          int64
}

// StartPreparationAttempt atomically records a RUNNING attempt. The caller
// should first acquire the unit lease; WorkerID is retained for diagnostics.
func (r *SQLiteStore) StartPreparationAttempt(ctx context.Context, input PreparationAttemptInput) (string, error) {
	if input.UnitFingerprint == "" || input.ExecutionMode == "" || input.ResourceClass == "" || input.WorkerID == "" {
		return "", fmt.Errorf("start preparation attempt requires fingerprint, mode, resource class, and worker")
	}
	if input.Unit != nil {
		driver := input.Unit.Driver()
		if input.WorkloadDimension == "" {
			input.WorkloadDimension = string(driver.Dimension)
		}
		if input.WorkloadAmount <= 0 {
			input.WorkloadAmount = driver.Amount
		}
	}
	attemptID := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.ExecContext(ctx, `INSERT INTO preparation_attempts
		(attempt_id, unit_fingerprint, trigger_job_id, worker_id, host, execution_mode,
		 resource_class, scheduler_priority, status, expected_work_ms, workload_dimension, workload_amount, started_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'RUNNING', ?, ?, ?, ?, ?)`,
		attemptID, input.UnitFingerprint, input.TriggerJobID, input.WorkerID, input.Host,
		input.ExecutionMode, input.ResourceClass, input.SchedulerPriority,
		input.ExpectedWorkMS, input.WorkloadDimension, input.WorkloadAmount, now, now)
	if err != nil {
		return "", fmt.Errorf("start preparation attempt: %w", err)
	}
	return attemptID, nil
}

// FinishPreparationAttempt closes an attempt, fenced by worker and RUNNING
// status. It records successful, failed, cancelled, preempted, or HIT work.
func (r *SQLiteStore) FinishPreparationAttempt(ctx context.Context, attemptID, workerID, status, errorCode, errorMessage string, wallMS int64) error {
	if attemptID == "" || workerID == "" {
		return fmt.Errorf("finish preparation attempt requires attempt id and worker")
	}
	switch status {
	case "READY", "FAILED", "CANCELLED", "PREEMPTED", "HIT":
	default:
		return fmt.Errorf("invalid preparation attempt terminal status %q", status)
	}
	finished := time.Now().UTC()
	res, err := r.db.ExecContext(ctx, `UPDATE preparation_attempts
		SET status = ?, finished_at = ?, wall_ms = ?, error_code = ?, error_message = ?
		WHERE attempt_id = ? AND worker_id = ? AND status = 'RUNNING'`,
		status, finished.Format(time.RFC3339Nano), wallMS, errorCode, errorMessage, attemptID, workerID)
	if err != nil {
		return fmt.Errorf("finish preparation attempt: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("finish preparation attempt: ownership lost or attempt is not running")
	}
	return nil
}

// CancelPreparationAttempt marks a running attempt cancelled. This is the
// scheduler's explicit preemption path; the executor should also observe ctx.
func (r *SQLiteStore) CancelPreparationAttempt(ctx context.Context, attemptID, workerID, reason string) error {
	return r.FinishPreparationAttempt(ctx, attemptID, workerID, "CANCELLED", "PREEMPTED", reason, 0)
}
