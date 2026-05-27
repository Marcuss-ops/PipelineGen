package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/pkg/timeutil"
)

type Repository struct {
	db      *sql.DB
	log     *zap.Logger
	claimMu sync.Mutex
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

	unmarshalJobFields(&job, payloadJSON, resultJSON, leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt)

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

		unmarshalJobFields(&job, payloadJSON, resultJSON, leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt)

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

	unmarshalJobFields(&job, payloadJSON, resultJSON, leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt)

	return &job, nil
}
