package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"velox/go-master/pkg/timeutil"
)

type ListFilter struct {
	Status *JobStatus
	Type   *string
	Limit  int
	Offset int
}

type Store interface {
	Create(ctx context.Context, job *Job) error
	Get(ctx context.Context, id string) (*Job, error)
	List(ctx context.Context, filter ListFilter) ([]Job, error)
	MarkQueued(ctx context.Context, id string) error
	MarkRunning(ctx context.Context, id string) error
	MarkSucceeded(ctx context.Context, id string, result any) error
	MarkFailed(ctx context.Context, id string, err error) error
	MarkCancelled(ctx context.Context, id string) error
	MarkRetrying(ctx context.Context, id string) error
	IncrementAttempts(ctx context.Context, id string) error
	LeaseNext(ctx context.Context) (*Job, error)
	RecoverZombieJobs(ctx context.Context, timeout time.Duration) (int64, error)
}

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func (s *SQLiteStore) Create(ctx context.Context, job *Job) error {
	query := `
		INSERT INTO jobs_new (id, type, status, payload_json, result_json, error, attempts, max_attempts, created_at, started_at, finished_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	now := time.Now()
	job.CreatedAt = now
	job.UpdatedAt = now
	if job.Status == "" {
		job.Status = JobStatusCreated
	}

	_, err := s.db.ExecContext(ctx, query,
		job.ID, job.Type, job.Status,
		job.PayloadJSON, job.ResultJSON, job.Error,
		job.Attempts, job.MaxAttempts,
		timeutil.FormatRFC3339(job.CreatedAt),
		timeutil.FormatPtrRFC3339(job.StartedAt),
		timeutil.FormatPtrRFC3339(job.FinishedAt),
		timeutil.FormatRFC3339(job.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (*Job, error) {
	query := `
		SELECT id, type, status, payload_json, result_json, error, attempts, max_attempts, created_at, started_at, finished_at, updated_at
		FROM jobs_new WHERE id = ?
	`
	var job Job
	var status string
	var payloadJSON, resultJSON, errorMsg string
	var createdAt, updatedAt string
	var startedAt, finishedAt *string
	var attempts, maxAttempts int

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&job.ID, &job.Type, &status,
		&payloadJSON, &resultJSON, &errorMsg,
		&attempts, &maxAttempts,
		&createdAt, &startedAt, &finishedAt, &updatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("job not found: %s", id)
		}
		return nil, fmt.Errorf("failed to get job: %w", err)
	}

	job.Status = JobStatus(status)
	job.PayloadJSON = payloadJSON
	job.ResultJSON = resultJSON
	job.Error = errorMsg
	job.Attempts = attempts
	job.MaxAttempts = maxAttempts
	job.CreatedAt = timeutil.ParseRFC3339String(&createdAt)
	job.UpdatedAt = timeutil.ParseRFC3339String(&updatedAt)
	job.StartedAt = timeutil.ParseRFC3339PtrString(startedAt)
	job.FinishedAt = timeutil.ParseRFC3339PtrString(finishedAt)

	return &job, nil
}

func (s *SQLiteStore) List(ctx context.Context, filter ListFilter) ([]Job, error) {
	query := `
		SELECT id, type, status, payload_json, result_json, error, attempts, max_attempts, created_at, started_at, finished_at, updated_at
		FROM jobs_new WHERE 1=1
	`
	args := []interface{}{}

	if filter.Status != nil {
		query += ` AND status = ?`
		args = append(args, string(*filter.Status))
	}
	if filter.Type != nil {
		query += ` AND type = ?`
		args = append(args, *filter.Type)
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

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var job Job
		var status string
		var payloadJSON, resultJSON, errorMsg string
		var createdAt, updatedAt string
		var startedAt, finishedAt *string
		var attempts, maxAttempts int

		err := rows.Scan(
			&job.ID, &job.Type, &status,
			&payloadJSON, &resultJSON, &errorMsg,
			&attempts, &maxAttempts,
			&createdAt, &startedAt, &finishedAt, &updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}

		job.Status = JobStatus(status)
		job.PayloadJSON = payloadJSON
		job.ResultJSON = resultJSON
		job.Error = errorMsg
		job.Attempts = attempts
		job.MaxAttempts = maxAttempts
		job.CreatedAt = timeutil.ParseRFC3339String(&createdAt)
		job.UpdatedAt = timeutil.ParseRFC3339String(&updatedAt)
		job.StartedAt = timeutil.ParseRFC3339PtrString(startedAt)
		job.FinishedAt = timeutil.ParseRFC3339PtrString(finishedAt)

		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (s *SQLiteStore) MarkQueued(ctx context.Context, id string) error {
	query := `UPDATE jobs_new SET status = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, JobStatusQueued, timeutil.FormatRFC3339(time.Now()), id)
	if err != nil {
		return fmt.Errorf("failed to mark job queued: %w", err)
	}
	return nil
}

func (s *SQLiteStore) MarkRunning(ctx context.Context, id string) error {
	now := time.Now()
	query := `UPDATE jobs_new SET status = ?, started_at = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, JobStatusRunning, timeutil.FormatRFC3339(now), timeutil.FormatRFC3339(now), id)
	if err != nil {
		return fmt.Errorf("failed to mark job running: %w", err)
	}
	return nil
}

func (s *SQLiteStore) MarkSucceeded(ctx context.Context, id string, result any) error {
	resultJSON, _ := json.Marshal(result)
	if resultJSON == nil {
		resultJSON = []byte("{}")
	}
	now := time.Now()
	query := `UPDATE jobs_new SET status = ?, result_json = ?, finished_at = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, JobStatusSucceeded, string(resultJSON), timeutil.FormatRFC3339(now), timeutil.FormatRFC3339(now), id)
	if err != nil {
		return fmt.Errorf("failed to mark job succeeded: %w", err)
	}
	return nil
}

func (s *SQLiteStore) MarkFailed(ctx context.Context, id string, err error) error {
	now := time.Now()
	query := `UPDATE jobs_new SET status = ?, error = ?, finished_at = ?, updated_at = ? WHERE id = ?`
	_, dbErr := s.db.ExecContext(ctx, query, JobStatusFailed, err.Error(), timeutil.FormatRFC3339(now), timeutil.FormatRFC3339(now), id)
	if dbErr != nil {
		return fmt.Errorf("failed to mark job failed: %w", dbErr)
	}
	return nil
}

func (s *SQLiteStore) MarkCancelled(ctx context.Context, id string) error {
	now := time.Now()
	query := `UPDATE jobs_new SET status = ?, finished_at = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, JobStatusCancelled, timeutil.FormatRFC3339(now), timeutil.FormatRFC3339(now), id)
	if err != nil {
		return fmt.Errorf("failed to mark job cancelled: %w", err)
	}
	return nil
}

func (s *SQLiteStore) LeaseNext(ctx context.Context) (*Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()
	zombieTimeout := now.Add(-15 * time.Minute)
	recoveryTimeStr := timeutil.FormatRFC3339(now)

	recoverQuery := `
		UPDATE jobs_new
		SET status = CASE
			WHEN attempts < max_attempts THEN ?
			ELSE ?
		END,
		started_at = NULL,
		updated_at = ?,
		error = CASE
			WHEN attempts < max_attempts THEN 'Job recovered from zombie state, retrying'
			ELSE 'Job failed: zombie timeout exceeded max attempts'
		END
		WHERE status = ?
		AND started_at IS NOT NULL
		AND datetime(started_at) < datetime(?)
	`
	_, err = tx.ExecContext(ctx, recoverQuery,
		JobStatusQueued, JobStatusFailed,
		recoveryTimeStr,
		JobStatusRunning,
		timeutil.FormatRFC3339(zombieTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to recover zombie jobs: %w", err)
	}

	query := `
		SELECT id, type, status, payload_json, result_json, error, attempts, max_attempts, created_at, started_at, finished_at, updated_at
		FROM jobs_new
		WHERE status = ?
		ORDER BY created_at ASC
		LIMIT 1
	`
	var job Job
	var status string
	var payloadJSON, resultJSON, errorMsg string
	var createdAt, updatedAt string
	var startedAt, finishedAt *string
	var attempts, maxAttempts int

	err = tx.QueryRowContext(ctx, query, JobStatusQueued, recoveryTimeStr).Scan(
		&job.ID, &job.Type, &status,
		&payloadJSON, &resultJSON, &errorMsg,
		&attempts, &maxAttempts,
		&createdAt, &startedAt, &finishedAt, &updatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to lease next job: %w", err)
	}

	job.Status = JobStatus(status)
	job.PayloadJSON = payloadJSON
	job.ResultJSON = resultJSON
	job.Error = errorMsg
	job.Attempts = attempts
	job.MaxAttempts = maxAttempts
	job.CreatedAt = timeutil.ParseRFC3339String(&createdAt)
	job.UpdatedAt = timeutil.ParseRFC3339String(&updatedAt)
	job.StartedAt = timeutil.ParseRFC3339PtrString(startedAt)
	job.FinishedAt = timeutil.ParseRFC3339PtrString(finishedAt)

	now = time.Now()
	updateQuery := `UPDATE jobs_new SET status = ?, started_at = ?, updated_at = ? WHERE id = ?`
	_, err = tx.ExecContext(ctx, updateQuery, JobStatusRunning, timeutil.FormatRFC3339(now), timeutil.FormatRFC3339(now), job.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to update leased job: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	job.Status = JobStatusRunning
	job.StartedAt = &now
	job.UpdatedAt = now

	return &job, nil
}

func (s *SQLiteStore) MarkRetrying(ctx context.Context, id string) error {
	now := time.Now()
	query := `UPDATE jobs_new SET status = ?, started_at = NULL, updated_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, JobStatusRetrying, timeutil.FormatRFC3339(now), id)
	if err != nil {
		return fmt.Errorf("failed to mark job retrying: %w", err)
	}
	return nil
}

func (s *SQLiteStore) IncrementAttempts(ctx context.Context, id string) error {
	query := `UPDATE jobs_new SET attempts = attempts + 1, updated_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, timeutil.FormatRFC3339(time.Now()), id)
	if err != nil {
		return fmt.Errorf("failed to increment attempts: %w", err)
	}
	return nil
}

func (s *SQLiteStore) RecoverZombieJobs(ctx context.Context, timeout time.Duration) (int64, error) {
	now := time.Now()
	zombieThreshold := now.Add(-timeout)

	recoverQuery := `
		UPDATE jobs_new
		SET status = CASE
			WHEN attempts < max_attempts THEN ?
			ELSE ?
		END,
		started_at = NULL,
		updated_at = ?,
		error = CASE
			WHEN attempts < max_attempts THEN 'Job recovered from zombie state, retrying'
			ELSE 'Job failed: zombie timeout exceeded max attempts'
		END
		WHERE status = ?
		AND started_at IS NOT NULL
		AND datetime(started_at) < datetime(?)
	`
	result, err := s.db.ExecContext(ctx, recoverQuery,
		JobStatusQueued, JobStatusFailed,
		timeutil.FormatRFC3339(now),
		JobStatusRunning,
		timeutil.FormatRFC3339(zombieThreshold),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to recover zombie jobs: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rowsAffected, nil
}
