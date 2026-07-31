// Package processmetrics provides the canonical SQLite adapter for durable
// process phase metrics.
package processmetrics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// Metric is the persistence model for one completed or in-flight process
// phase. Timestamps are stored as RFC3339Nano TEXT.
type Metric struct {
	ID          int64
	ProcessType string
	JobID       string
	ParentJobID string

	Phase    string
	Language string
	Provider string

	StartedAt   time.Time
	DurationMs  int64
	QueueWaitMs int64

	Status    string
	ErrorCode string

	ItemsIn  int64
	ItemsOut int64
	BytesIn  int64
	BytesOut int64

	RetryCount int64
	CreatedAt  time.Time
	Details    map[string]any
}

// Repository is the persistence surface used by process-metrics recorders.
type Repository interface {
	Insert(ctx context.Context, metric *Metric) (int64, error)
	Update(ctx context.Context, metric *Metric) error
	GetByID(ctx context.Context, id int64) (*Metric, error)
	ListByJob(ctx context.Context, jobID string, limit int) ([]Metric, error)
	ListByParentJob(ctx context.Context, parentJobID string, limit int) ([]Metric, error)
}

// SQLiteRepository persists process phase metrics in SQLite.
type SQLiteRepository struct {
	db *sql.DB
}

// NewSQLiteRepository constructs the repository.
func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	if db == nil {
		panic("processmetrics.NewSQLiteRepository: nil *sql.DB")
	}
	return &SQLiteRepository{db: db}
}

var _ Repository = (*SQLiteRepository)(nil)

const metricColumns = `id, process_type, job_id, parent_job_id,
	phase, language, provider, started_at, duration_ms, queue_wait_ms,
	status, error_code, items_in, items_out, bytes_in, bytes_out,
	retry_count, created_at, details_json`

func validateMetric(metric *Metric) error {
	if metric == nil {
		return errors.New("processmetrics: metric is required")
	}
	if metric.ProcessType == "" {
		return errors.New("processmetrics: process_type is required")
	}
	if metric.JobID == "" {
		return errors.New("processmetrics: job_id is required")
	}
	if metric.Phase == "" {
		return errors.New("processmetrics: phase is required")
	}
	if metric.StartedAt.IsZero() {
		return errors.New("processmetrics: started_at is required")
	}
	if metric.Status == "" {
		return errors.New("processmetrics: status is required")
	}
	if metric.DurationMs < 0 || metric.QueueWaitMs < 0 || metric.ItemsIn < 0 ||
		metric.ItemsOut < 0 || metric.BytesIn < 0 || metric.BytesOut < 0 || metric.RetryCount < 0 {
		return errors.New("processmetrics: numeric metric values must be non-negative")
	}
	return nil
}

// Insert stores a metric and returns its row ID. CreatedAt is filled when absent.
func (r *SQLiteRepository) Insert(ctx context.Context, metric *Metric) (int64, error) {
	if err := validateMetric(metric); err != nil {
		return 0, err
	}
	if metric.CreatedAt.IsZero() {
		metric.CreatedAt = timeutil.Now()
	}
	details, err := encodeDetails(metric.Details)
	if err != nil {
		return 0, fmt.Errorf("processmetrics.Insert details_json: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO process_phase_metrics (
			process_type, job_id, parent_job_id, phase, language, provider,
			started_at, duration_ms, queue_wait_ms, status, error_code,
			items_in, items_out, bytes_in, bytes_out, retry_count, created_at, details_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, metric.ProcessType, metric.JobID, metric.ParentJobID, metric.Phase,
		metric.Language, metric.Provider, timeutil.FormatRFC3339Nano(metric.StartedAt),
		metric.DurationMs, metric.QueueWaitMs, metric.Status, metric.ErrorCode,
		metric.ItemsIn, metric.ItemsOut, metric.BytesIn, metric.BytesOut,
		metric.RetryCount, timeutil.FormatRFC3339Nano(metric.CreatedAt), details)
	if err != nil {
		return 0, fmt.Errorf("processmetrics.Insert: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("processmetrics.Insert last insert id: %w", err)
	}
	metric.ID = id
	return id, nil
}

// Update replaces mutable metric fields, including details.
func (r *SQLiteRepository) Update(ctx context.Context, metric *Metric) error {
	if metric == nil || metric.ID <= 0 {
		return errors.New("processmetrics: a positive metric id is required")
	}
	if err := validateMetric(metric); err != nil {
		return err
	}
	details, err := encodeDetails(metric.Details)
	if err != nil {
		return fmt.Errorf("processmetrics.Update details_json: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE process_phase_metrics SET
			process_type = ?, job_id = ?, parent_job_id = ?, phase = ?,
			language = ?, provider = ?, duration_ms = ?, queue_wait_ms = ?,
			status = ?, error_code = ?, items_in = ?, items_out = ?,
			bytes_in = ?, bytes_out = ?, retry_count = ?, details_json = ?
		WHERE id = ?
	`, metric.ProcessType, metric.JobID, metric.ParentJobID, metric.Phase,
		metric.Language, metric.Provider, metric.DurationMs, metric.QueueWaitMs,
		metric.Status, metric.ErrorCode, metric.ItemsIn, metric.ItemsOut,
		metric.BytesIn, metric.BytesOut, metric.RetryCount, details, metric.ID)
	if err != nil {
		return fmt.Errorf("processmetrics.Update: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("processmetrics.Update rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("processmetrics.Update: metric %d not found", metric.ID)
	}
	return nil
}

// GetByID returns one metric or sql.ErrNoRows wrapped when absent.
func (r *SQLiteRepository) GetByID(ctx context.Context, id int64) (*Metric, error) {
	if id <= 0 {
		return nil, errors.New("processmetrics.GetByID: a positive metric id is required")
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT `+metricColumns+` FROM process_phase_metrics WHERE id = ?`, id)
	metric, err := scanMetric(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("processmetrics.GetByID: metric %d not found: %w", id, sql.ErrNoRows)
		}
		return nil, fmt.Errorf("processmetrics.GetByID: %w", err)
	}
	return metric, nil
}

// ListByJob returns newest metrics for a job. Non-positive limits use 100.
func (r *SQLiteRepository) ListByJob(ctx context.Context, jobID string, limit int) ([]Metric, error) {
	if jobID == "" {
		return nil, errors.New("processmetrics.ListByJob: job_id is required")
	}
	return r.list(ctx, `WHERE job_id = ?`, jobID, limit)
}

// ListByParentJob returns newest metrics for a parent job. Non-positive limits use 100.
func (r *SQLiteRepository) ListByParentJob(ctx context.Context, parentJobID string, limit int) ([]Metric, error) {
	if parentJobID == "" {
		return nil, errors.New("processmetrics.ListByParentJob: parent_job_id is required")
	}
	return r.list(ctx, `WHERE parent_job_id = ?`, parentJobID, limit)
}

func (r *SQLiteRepository) list(ctx context.Context, predicate, value string, limit int) ([]Metric, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+metricColumns+` FROM process_phase_metrics `+predicate+` ORDER BY started_at DESC, id DESC LIMIT ?`, value, limit)
	if err != nil {
		return nil, fmt.Errorf("processmetrics.list: %w", err)
	}
	defer rows.Close()

	metrics := make([]Metric, 0)
	for rows.Next() {
		metric, scanErr := scanMetric(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("processmetrics.list scan: %w", scanErr)
		}
		metrics = append(metrics, *metric)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("processmetrics.list rows: %w", err)
	}
	return metrics, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanMetric(row scanner) (*Metric, error) {
	metric := &Metric{}
	var startedAt, createdAt, detailsJSON string
	if err := row.Scan(
		&metric.ID, &metric.ProcessType, &metric.JobID, &metric.ParentJobID,
		&metric.Phase, &metric.Language, &metric.Provider, &startedAt,
		&metric.DurationMs, &metric.QueueWaitMs, &metric.Status, &metric.ErrorCode,
		&metric.ItemsIn, &metric.ItemsOut, &metric.BytesIn, &metric.BytesOut,
		&metric.RetryCount, &createdAt, &detailsJSON,
	); err != nil {
		return nil, err
	}
	metric.StartedAt = timeutil.ParseRFC3339(startedAt)
	if metric.StartedAt.IsZero() {
		return nil, fmt.Errorf("processmetrics: invalid started_at %q", startedAt)
	}
	metric.CreatedAt = timeutil.ParseRFC3339(createdAt)
	if metric.CreatedAt.IsZero() {
		return nil, fmt.Errorf("processmetrics: invalid created_at %q", createdAt)
	}
	if detailsJSON != "" {
		if err := json.Unmarshal([]byte(detailsJSON), &metric.Details); err != nil {
			return nil, fmt.Errorf("processmetrics: invalid details_json: %w", err)
		}
	}
	return metric, nil
}

func encodeDetails(details map[string]any) (string, error) {
	if len(details) == 0 {
		return "{}", nil
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
