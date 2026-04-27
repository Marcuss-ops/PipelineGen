package harvester

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Job represents a harvester cron job
type Job struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Query     string    `json:"query"`
	Channel   string    `json:"channel,omitempty"`
	Interval  string    `json:"interval"` // e.g., "hourly", "daily", "weekly"
	Enabled   bool      `json:"enabled"`
	LastRunAt *time.Time `json:"last_run_at,omitempty"`
	NextRunAt *time.Time `json:"next_run_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Repository handles persistence for harvester jobs
type Repository struct {
	db  *sql.DB
	log *zap.Logger
}

// NewRepository creates a new harvester jobs repository
func NewRepository(db *sql.DB, log *zap.Logger) *Repository {
	return &Repository{db: db, log: log}
}

// CreateJob creates a new harvester job
func (r *Repository) CreateJob(ctx context.Context, job *Job) error {
	query := `
		INSERT INTO harvester_jobs (id, name, query, channel, interval, enabled, last_run_at, next_run_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := r.db.ExecContext(ctx, query,
		job.ID, job.Name, job.Query, job.Channel, job.Interval, job.Enabled,
		formatTime(job.LastRunAt), formatTime(job.NextRunAt))
	if err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}

	return nil
}

// GetJob retrieves a job by ID
func (r *Repository) GetJob(ctx context.Context, id string) (*Job, error) {
	query := `
		SELECT id, name, query, channel, interval, enabled, last_run_at, next_run_at, created_at, updated_at
		FROM harvester_jobs WHERE id = ?
	`

	var job Job
	var lastRunAt, nextRunAt, createdAt, updatedAt string

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&job.ID, &job.Name, &job.Query, &job.Channel, &job.Interval, &job.Enabled,
		&lastRunAt, &nextRunAt, &createdAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to get job: %w", err)
	}

	parseJobTimes(&job, lastRunAt, nextRunAt, createdAt, updatedAt)
	return &job, nil
}

// ListJobs lists all harvester jobs
func (r *Repository) ListJobs(ctx context.Context) ([]*Job, error) {
	query := `
		SELECT id, name, query, channel, interval, enabled, last_run_at, next_run_at, created_at, updated_at
		FROM harvester_jobs ORDER BY created_at DESC
	`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*Job
	for rows.Next() {
		var job Job
		var lastRunAt, nextRunAt, createdAt, updatedAt string

		err := rows.Scan(
			&job.ID, &job.Name, &job.Query, &job.Channel, &job.Interval, &job.Enabled,
			&lastRunAt, &nextRunAt, &createdAt, &updatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}

		parseJobTimes(&job, lastRunAt, nextRunAt, createdAt, updatedAt)
		jobs = append(jobs, &job)
	}

	return jobs, nil
}

// UpdateJob updates an existing job
func (r *Repository) UpdateJob(ctx context.Context, job *Job) error {
	query := `
		UPDATE harvester_jobs
		SET name = ?, query = ?, channel = ?, interval = ?, enabled = ?, last_run_at = ?, next_run_at = ?, updated_at = datetime('now')
		WHERE id = ?
	`

	_, err := r.db.ExecContext(ctx, query,
		job.Name, job.Query, job.Channel, job.Interval, job.Enabled,
		formatTime(job.LastRunAt), formatTime(job.NextRunAt), job.ID)
	if err != nil {
		return fmt.Errorf("failed to update job: %w", err)
	}

	return nil
}

// DeleteJob deletes a job by ID
func (r *Repository) DeleteJob(ctx context.Context, id string) error {
	query := `DELETE FROM harvester_jobs WHERE id = ?`
	_, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete job: %w", err)
	}
	return nil
}

// ToggleJob enables or disables a job
func (r *Repository) ToggleJob(ctx context.Context, id string) error {
	query := `UPDATE harvester_jobs SET enabled = NOT enabled, updated_at = datetime('now') WHERE id = ?`
	_, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to toggle job: %w", err)
	}
	return nil
}

// GetJobsToRun returns enabled jobs that are due to run
func (r *Repository) GetJobsToRun(ctx context.Context) ([]*Job, error) {
	query := `
		SELECT id, name, query, channel, interval, enabled, last_run_at, next_run_at, created_at, updated_at
		FROM harvester_jobs
		WHERE enabled = 1 AND (next_run_at IS NULL OR next_run_at <= datetime('now'))
		ORDER BY next_run_at ASC
	`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get jobs to run: %w", err)
	}
	defer rows.Close()

	var jobs []*Job
	for rows.Next() {
		var job Job
		var lastRunAt, nextRunAt, createdAt, updatedAt string

		err := rows.Scan(
			&job.ID, &job.Name, &job.Query, &job.Channel, &job.Interval, &job.Enabled,
			&lastRunAt, &nextRunAt, &createdAt, &updatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}

		parseJobTimes(&job, lastRunAt, nextRunAt, createdAt, updatedAt)
		jobs = append(jobs, &job)
	}

	return jobs, nil
}

// UpdateJobRunTimes updates the last_run_at and calculates next_run_at
func (r *Repository) UpdateJobRunTimes(ctx context.Context, id string) error {
	job, err := r.GetJob(ctx, id)
	if err != nil {
		return err
	}

	now := time.Now()
	job.LastRunAt = &now
	job.NextRunAt = calculateNextRun(&now, job.Interval)

	return r.UpdateJob(ctx, job)
}

// Helper functions

func formatTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339)
}

func parseJobTimes(job *Job, lastRunAt, nextRunAt, createdAt, updatedAt string) {
	if lastRunAt != "" {
		t, _ := time.Parse(time.RFC3339, lastRunAt)
		job.LastRunAt = &t
	}
	if nextRunAt != "" {
		t, _ := time.Parse(time.RFC3339, nextRunAt)
		job.NextRunAt = &t
	}
	job.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	job.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
}

func calculateNextRun(lastRun *time.Time, interval string) *time.Time {
	if lastRun == nil {
		now := time.Now()
		lastRun = &now
	}

	var next time.Time
	switch interval {
	case "hourly":
		next = lastRun.Add(1 * time.Hour)
	case "daily":
		next = lastRun.Add(24 * time.Hour)
	case "weekly":
		next = lastRun.Add(7 * 24 * time.Hour)
	default:
		next = lastRun.Add(24 * time.Hour) // default to daily
	}

	return &next
}

// ToJSON converts job to JSON string
func (j *Job) ToJSON() string {
	data, _ := json.Marshal(j)
	return string(data)
}
