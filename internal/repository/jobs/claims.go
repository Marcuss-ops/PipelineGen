package jobs

import (
	"context"
	"strings"
	"time"

	"velox/go-master/internal/media/models"
	"velox/go-master/internal/pkg/timeutil"
)

func (r *Repository) ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration, types []models.JobType) (*models.Job, error) {
	r.claimMu.Lock()
	defer r.claimMu.Unlock()

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	query := `SELECT id, type, status, priority, project, video_name, active_key,
		payload_json, result_json, progress, error, retry_count, max_retries,
		worker_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at
		FROM jobs
		WHERE status IN ('queued', 'retrying')
		AND (lease_expiry IS NULL OR lease_expiry < ?)`
	args := []interface{}{time.Now().Format(time.RFC3339)}

	if len(types) > 0 {
		placeholders := make([]string, len(types))
		for i, t := range types {
			placeholders[i] = "?"
			args = append(args, t)
		}
		query += ` AND type IN (` + strings.Join(placeholders, ",") + `)`
	}

	query += ` ORDER BY priority DESC, created_at ASC LIMIT 1`

	var job models.Job
	var payloadJSON, resultJSON string
	var leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt *string

	err = tx.QueryRowContext(ctx, query, args...).Scan(
		&job.ID, &job.Type, &job.Status, &job.Priority, &job.Project, &job.VideoName, &job.ActiveKey,
		&payloadJSON, &resultJSON, &job.Progress, &job.Error, &job.RetryCount, &job.MaxRetries,
		&job.WorkerID, &leaseExpiry, &createdAt, &updatedAt,
		&startedAt, &completedAt, &cancelledAt)
	if err != nil {
		return nil, nil
	}

	unmarshalJobFields(&job, payloadJSON, resultJSON, leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt)

	leaseExpiryTime := time.Now().Add(leaseTTL)
	now := time.Now()

	updateQuery := `UPDATE jobs SET status = 'running', worker_id = ?, lease_expiry = ?, updated_at = ?`
	updateArgs := []interface{}{workerID, leaseExpiryTime.Format(time.RFC3339), now.Format(time.RFC3339)}

	if job.StartedAt == nil {
		updateQuery += `, started_at = ?`
		updateArgs = append(updateArgs, now.Format(time.RFC3339))
	}
	updateQuery += ` WHERE id = ?`
	updateArgs = append(updateArgs, job.ID)

	_, err = tx.ExecContext(ctx, updateQuery, updateArgs...)
	if err != nil {
		return nil, err
	}

	job.Status = models.StatusRunning
	job.WorkerID = workerID
	job.LeaseExpiry = &leaseExpiryTime
	if job.StartedAt == nil {
		job.StartedAt = &now
	}
	job.UpdatedAt = now

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &job, nil
}

func (r *Repository) RenewLease(ctx context.Context, jobID string, workerID string, leaseTTL time.Duration) error {
	leaseExpiry := time.Now().Add(leaseTTL)
	query := `UPDATE jobs SET lease_expiry = ?, updated_at = ? WHERE id = ? AND worker_id = ? AND status = 'running'`
	_, err := r.db.ExecContext(ctx, query, leaseExpiry.Format(time.RFC3339), time.Now().Format(time.RFC3339), jobID, workerID)
	return err
}

func (r *Repository) SetStatusRunning(ctx context.Context, jobID string) error {
	now := time.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'running', started_at = ?, updated_at = ? WHERE id = ?`,
		timeutil.FormatRFC3339(now), timeutil.FormatRFC3339(now), jobID)
	return err
}

func (r *Repository) RequeueExpiredLeases(ctx context.Context) error {
	now := time.Now()
	query := `UPDATE jobs
		SET status = 'queued', worker_id = '', lease_expiry = NULL, updated_at = ?
		WHERE status = 'running' AND lease_expiry < ?`
	_, err := r.db.ExecContext(ctx, query, timeutil.FormatRFC3339(now), timeutil.FormatRFC3339(now))
	return err
}

func (r *Repository) MarkRunningJobsOlderThanFailed(ctx context.Context, cutoff time.Time, reason string) (int, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, error = ?, updated_at = ? WHERE status = ? AND updated_at < ?`,
		models.StatusFailed, reason, time.Now().UTC().Format(time.RFC3339),
		models.StatusRunning, cutoff.Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}
