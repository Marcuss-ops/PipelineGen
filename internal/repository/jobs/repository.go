package jobs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/models"
	"velox/go-master/pkg/timeutil"
)

type Repository struct {
	db  *sql.DB
	log *zap.Logger
}

func NewRepository(db *sql.DB, log *zap.Logger) *Repository {
	return &Repository{db: db, log: log}
}

func (r *Repository) Create(ctx context.Context, job *models.Job) error {
	query := `
		INSERT INTO jobs (id, type, status, priority, project, video_name, active_key,
			payload_json, result_json, progress, error, retry_count, max_retries,
			worker_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	payloadJSON, _ := json.Marshal(job.Payload)
	if payloadJSON == nil {
		payloadJSON = []byte("{}")
	}
	resultJSON, _ := json.Marshal(job.Result)
	if resultJSON == nil {
		resultJSON = []byte("{}")
	}

	_, err := r.db.ExecContext(ctx, query,
		job.ID, job.Type, job.Status, job.Priority, job.Project, job.VideoName, job.ActiveKey,
		string(payloadJSON), string(resultJSON), job.Progress, job.Error,
		job.RetryCount, job.MaxRetries, job.WorkerID, timeutil.FormatPtrRFC3339(job.LeaseExpiry),
		timeutil.FormatRFC3339(job.CreatedAt), timeutil.FormatRFC3339(job.UpdatedAt),
		timeutil.FormatPtrRFC3339(job.StartedAt), timeutil.FormatPtrRFC3339(job.CompletedAt), nil)
	if err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}

	return nil
}

func (r *Repository) Get(ctx context.Context, id string) (*models.Job, error) {
	query := `SELECT id, type, status, priority, project, video_name, active_key,
		payload_json, result_json, progress, error, retry_count, max_retries,
		worker_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at
		FROM jobs WHERE id = ?`

	var job models.Job
	var payloadJSON, resultJSON string
	var leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt *string

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&job.ID, &job.Type, &job.Status, &job.Priority, &job.Project, &job.VideoName, &job.ActiveKey,
		&payloadJSON, &resultJSON, &job.Progress, &job.Error, &job.RetryCount, &job.MaxRetries,
		&job.WorkerID, &leaseExpiry, &createdAt, &updatedAt,
		&startedAt, &completedAt, &cancelledAt)
	if err != nil {
		return nil, fmt.Errorf("failed to get job: %w", err)
	}

	json.Unmarshal([]byte(payloadJSON), &job.Payload)
	json.Unmarshal([]byte(resultJSON), &job.Result)
	job.LeaseExpiry = timeutil.ParseRFC3339PtrString(leaseExpiry)
	job.CreatedAt = timeutil.ParseRFC3339String(createdAt)
	job.UpdatedAt = timeutil.ParseRFC3339String(updatedAt)
	job.StartedAt = timeutil.ParseRFC3339PtrString(startedAt)
	job.CompletedAt = timeutil.ParseRFC3339PtrString(completedAt)
	job.CancelledAt = timeutil.ParseRFC3339PtrString(cancelledAt)

	return &job, nil
}

func (r *Repository) List(ctx context.Context, filter models.JobFilter) ([]*models.Job, error) {
	query := `SELECT id, type, status, priority, project, video_name, active_key,
		payload_json, result_json, progress, error, retry_count, max_retries,
		worker_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at
		FROM jobs WHERE 1=1`
	args := []interface{}{}

	if filter.Status != nil {
		query += ` AND status = ?`
		args = append(args, *filter.Status)
	}
	if filter.Type != nil {
		query += ` AND type = ?`
		args = append(args, *filter.Type)
	}
	if filter.WorkerID != "" {
		query += ` AND worker_id = ?`
		args = append(args, filter.WorkerID)
	}

	query += ` ORDER BY created_at DESC`
	if filter.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, filter.Limit)
	}
	if filter.Offset > 0 {
		query += ` OFFSET ?`
		args = append(args, filter.Offset)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*models.Job
	for rows.Next() {
		var job models.Job
		var payloadJSON, resultJSON string
		var leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt *string

		err := rows.Scan(
			&job.ID, &job.Type, &job.Status, &job.Priority, &job.Project, &job.VideoName, &job.ActiveKey,
			&payloadJSON, &resultJSON, &job.Progress, &job.Error, &job.RetryCount, &job.MaxRetries,
			&job.WorkerID, &leaseExpiry, &createdAt, &updatedAt,
			&startedAt, &completedAt, &cancelledAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}

		json.Unmarshal([]byte(payloadJSON), &job.Payload)
		json.Unmarshal([]byte(resultJSON), &job.Result)
		job.LeaseExpiry = timeutil.ParseRFC3339PtrString(leaseExpiry)
		job.CreatedAt = timeutil.ParseRFC3339String(createdAt)
		job.UpdatedAt = timeutil.ParseRFC3339String(updatedAt)
		job.StartedAt = timeutil.ParseRFC3339PtrString(startedAt)
		job.CompletedAt = timeutil.ParseRFC3339PtrString(completedAt)
		job.CancelledAt = timeutil.ParseRFC3339PtrString(cancelledAt)

		jobs = append(jobs, &job)
	}

	return jobs, nil
}

func (r *Repository) FindActiveByKey(ctx context.Context, activeKey string) (*models.Job, error) {
	query := `SELECT id, type, status, priority, project, video_name, active_key,
		payload_json, result_json, progress, error, retry_count, max_retries,
		worker_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at
		FROM jobs WHERE active_key = ? AND active_key != '' AND status IN ('queued', 'running', 'retrying') ORDER BY started_at DESC LIMIT 1`

	var job models.Job
	var payloadJSON, resultJSON string
	var leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt *string

	err := r.db.QueryRowContext(ctx, query, activeKey).Scan(
		&job.ID, &job.Type, &job.Status, &job.Priority, &job.Project, &job.VideoName, &job.ActiveKey,
		&payloadJSON, &resultJSON, &job.Progress, &job.Error, &job.RetryCount, &job.MaxRetries,
		&job.WorkerID, &leaseExpiry, &createdAt, &updatedAt,
		&startedAt, &completedAt, &cancelledAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find job by active key: %w", err)
	}

	json.Unmarshal([]byte(payloadJSON), &job.Payload)
	json.Unmarshal([]byte(resultJSON), &job.Result)
	job.LeaseExpiry = timeutil.ParseRFC3339PtrString(leaseExpiry)
	job.CreatedAt = timeutil.ParseRFC3339String(createdAt)
	job.UpdatedAt = timeutil.ParseRFC3339String(updatedAt)
	job.StartedAt = timeutil.ParseRFC3339PtrString(startedAt)
	job.CompletedAt = timeutil.ParseRFC3339PtrString(completedAt)
	job.CancelledAt = timeutil.ParseRFC3339PtrString(cancelledAt)

	return &job, nil
}

func (r *Repository) ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration, types []models.JobType) (*models.Job, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
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
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to claim next job: %w", err)
	}

	json.Unmarshal([]byte(payloadJSON), &job.Payload)
	json.Unmarshal([]byte(resultJSON), &job.Result)
	job.LeaseExpiry = timeutil.ParseRFC3339PtrString(leaseExpiry)
	job.CreatedAt = timeutil.ParseRFC3339String(createdAt)
	job.UpdatedAt = timeutil.ParseRFC3339String(updatedAt)
	job.StartedAt = timeutil.ParseRFC3339PtrString(startedAt)
	job.CompletedAt = timeutil.ParseRFC3339PtrString(completedAt)
	job.CancelledAt = timeutil.ParseRFC3339PtrString(cancelledAt)

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
		return nil, fmt.Errorf("failed to update claimed job: %w", err)
	}

	job.Status = models.StatusRunning
	job.WorkerID = workerID
	job.LeaseExpiry = &leaseExpiryTime
	if job.StartedAt == nil {
		job.StartedAt = &now
	}
	job.UpdatedAt = now

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &job, nil
}

func (r *Repository) RenewLease(ctx context.Context, jobID string, workerID string, leaseTTL time.Duration) error {
	leaseExpiry := time.Now().Add(leaseTTL)
	query := `UPDATE jobs SET lease_expiry = ?, updated_at = ? WHERE id = ? AND worker_id = ? AND status = 'running'`
	_, err := r.db.ExecContext(ctx, query, leaseExpiry.Format(time.RFC3339), time.Now().Format(time.RFC3339), jobID, workerID)
	if err != nil {
		return fmt.Errorf("failed to renew lease: %w", err)
	}
	return nil
}

func (r *Repository) SetProgress(ctx context.Context, jobID string, progress int, message string) error {
	query := `UPDATE jobs SET progress = ?, updated_at = ? WHERE id = ?`
	_, err := r.db.ExecContext(ctx, query, progress, time.Now().Format(time.RFC3339), jobID)
	if err != nil {
		return fmt.Errorf("failed to set progress: %w", err)
	}

	if message != "" {
		_ = r.AddEvent(ctx, jobID, "progress", message, map[string]interface{}{"progress": progress})
	}

	return nil
}

func (r *Repository) Complete(ctx context.Context, jobID string, result map[string]any) error {
	resultJSON, _ := json.Marshal(result)
	if resultJSON == nil {
		resultJSON = []byte("{}")
	}

	now := time.Now()
	query := `UPDATE jobs SET status = 'completed', result_json = ?, progress = 100, completed_at = ?, updated_at = ?, active_key = '' WHERE id = ?`
	_, err := r.db.ExecContext(ctx, query, string(resultJSON), now.Format(time.RFC3339), now.Format(time.RFC3339), jobID)
	if err != nil {
		return fmt.Errorf("failed to complete job: %w", err)
	}

	_ = r.AddEvent(ctx, jobID, "completed", "Job completed successfully", nil)
	return nil
}

func (r *Repository) Fail(ctx context.Context, jobID string, errMsg string) error {
	now := time.Now()
	query := `UPDATE jobs SET status = 'failed', error = ?, completed_at = ?, updated_at = ?, active_key = '' WHERE id = ?`
	_, err := r.db.ExecContext(ctx, query, errMsg, now.Format(time.RFC3339), now.Format(time.RFC3339), jobID)
	if err != nil {
		return fmt.Errorf("failed to fail job: %w", err)
	}

	_ = r.AddEvent(ctx, jobID, "failed", errMsg, nil)
	return nil
}

func (r *Repository) Cancel(ctx context.Context, jobID string) error {
	now := time.Now()
	query := `UPDATE jobs SET status = 'cancelled', cancelled_at = ?, updated_at = ?, active_key = '' WHERE id = ?`
	_, err := r.db.ExecContext(ctx, query, now.Format(time.RFC3339), now.Format(time.RFC3339), jobID)
	if err != nil {
		return fmt.Errorf("failed to cancel job: %w", err)
	}

	_ = r.AddEvent(ctx, jobID, "cancelled", "Job cancelled by user", nil)
	return nil
}

func (r *Repository) Retry(ctx context.Context, jobID string) (*models.Job, error) {
	query := `UPDATE jobs
		SET status = 'queued', retry_count = retry_count + 1, error = '', progress = 0,
			worker_id = '', lease_expiry = NULL, updated_at = ?
		WHERE id = ? AND status IN ('failed', 'cancelled') AND retry_count < max_retries`

	now := time.Now()
	result, err := r.db.ExecContext(ctx, query, now.Format(time.RFC3339), jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to retry job: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return nil, fmt.Errorf("job not eligible for retry")
	}

	return r.Get(ctx, jobID)
}

func (r *Repository) AddEvent(ctx context.Context, jobID string, eventType string, message string, data map[string]any) error {
	id := fmt.Sprintf("evt_%d_%s", time.Now().UnixNano(), randomString(6))

	dataJSON, _ := json.Marshal(data)
	if dataJSON == nil {
		dataJSON = []byte("{}")
	}

	query := `INSERT INTO job_events (id, job_id, type, message, data_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`

	_, err := r.db.ExecContext(ctx, query, id, jobID, eventType, message, string(dataJSON), time.Now().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("failed to add event: %w", err)
	}

	return nil
}

func (r *Repository) ListEvents(ctx context.Context, jobID string) ([]models.JobEvent, error) {
	query := `SELECT id, job_id, type, message, created_at FROM job_events WHERE job_id = ? ORDER BY created_at ASC`

	rows, err := r.db.QueryContext(ctx, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to list events: %w", err)
	}
	defer rows.Close()

	var events []models.JobEvent
	for rows.Next() {
		var evt models.JobEvent
		var createdAt string
		err := rows.Scan(&evt.ID, &evt.JobID, &evt.Type, &evt.Message, &createdAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan event: %w", err)
		}
		evt.Timestamp, _ = time.Parse(time.RFC3339, createdAt)
		events = append(events, evt)
	}

	return events, nil
}

func (r *Repository) RequeueExpiredLeases(ctx context.Context) error {
	now := time.Now()
	query := `UPDATE jobs
		SET status = 'queued', worker_id = '', lease_expiry = NULL, updated_at = ?
		WHERE status = 'running' AND lease_expiry < ?`

	_, err := r.db.ExecContext(ctx, query, timeutil.FormatRFC3339(now), timeutil.FormatRFC3339(now))
	if err != nil {
		return fmt.Errorf("failed to requeue expired leases: %w", err)
	}
	return nil
}

func (r *Repository) MarkRunningJobsOlderThanFailed(ctx context.Context, cutoff time.Time, reason string) (int, error) {
	query := `
		UPDATE jobs
		SET status = ?, error = ?, updated_at = ?
		WHERE status = ?
		  AND updated_at < ?
	`

	result, err := r.db.ExecContext(ctx, query,
		models.StatusFailed,
		reason,
		time.Now().UTC().Format(time.RFC3339),
		models.StatusRunning,
		cutoff.Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to mark stale jobs failed: %w", err)
	}

	n, _ := result.RowsAffected()
	return int(n), nil
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%0*x", n, time.Now().UnixNano())
	}
	return hex.EncodeToString(b)[:n]
}
