package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
	"velox/go-master/pkg/metrics"
	"velox/go-master/pkg/timeutil"
)

type Repository struct {
	db      *sql.DB
	log     *zap.Logger
	claimMu sync.Mutex
}

// jobColumns is the canonical list of column names read by Get, List and
// FindActiveByKey and written by Create. Kept in one place so adding a
// new tracked column is a one-line change.
const jobColumns = `id, type, status, priority, project, video_name, active_key,
	correlation_id, payload_json, result_json, progress, error, retry_count, max_retries,
	worker_id, lease_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at, revision`

func NewRepository(db *sql.DB, log *zap.Logger) *Repository {
	return &Repository{db: db, log: log}
}

func (r *Repository) Create(ctx context.Context, job *models.Job) error {
	query := `
		INSERT INTO jobs (id, type, status, priority, project, video_name, active_key,
			correlation_id, payload_json, result_json, progress, error, retry_count, max_retries,
			worker_id, lease_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at, revision)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	payloadJSON, _ := json.Marshal(job.Payload)
	if payloadJSON == nil {
		payloadJSON = []byte("{}")
	}
	resultJSON, _ := json.Marshal(job.Result)
	if resultJSON == nil {
		resultJSON = []byte("{}")
	}

	// Revision is per-row monotonic counter; on creation it is 1 (the
	// first revision before any transition fired).
	revision := job.Revision
	if revision <= 0 {
		revision = 1
	}

	_, err := r.db.ExecContext(ctx, query,
		job.ID, job.Type, job.Status, job.Priority, job.Project, job.VideoName, job.ActiveKey,
		job.CorrelationID,
		string(payloadJSON), string(resultJSON), job.Progress, job.Error,
		job.RetryCount, job.MaxRetries, job.WorkerID, job.LeaseID,
		timeutil.FormatPtrRFC3339(job.LeaseExpiry),
		timeutil.FormatRFC3339(job.CreatedAt), timeutil.FormatRFC3339(job.UpdatedAt),
		timeutil.FormatPtrRFC3339(job.StartedAt), timeutil.FormatPtrRFC3339(job.CompletedAt), nil, revision)
	if err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}

	return nil
}

// scanJobColumns reads the canonical job column list into job, then
// unmarshals the JSON payload/result and parses the time columns. Used by
// Get, List, and FindActiveByKey.
func scanJobColumns(s scanner, job *models.Job) error {
	var payloadJSON, resultJSON string
	var leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt *string
	if	err := s.Scan(
		&job.ID, &job.Type, &job.Status, &job.Priority, &job.Project, &job.VideoName, &job.ActiveKey,
		&job.CorrelationID,
		&payloadJSON, &resultJSON, &job.Progress, &job.Error, &job.RetryCount, &job.MaxRetries,
		&job.WorkerID, &job.LeaseID, &leaseExpiry, &createdAt, &updatedAt,
		&startedAt, &completedAt, &cancelledAt, &job.Revision,
	); err != nil {
		return err
	}
	unmarshalJobFields(job, payloadJSON, resultJSON, leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt)
	return nil
}

// scanner is the minimum surface of *sql.Row and *sql.Rows that scanJobColumns
// needs. Defined here so we can share the same code between single-row and
// multi-row reads.
type scanner interface {
	Scan(dest ...any) error
}

func (r *Repository) Get(ctx context.Context, id string) (*models.Job, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE id = ?`
	job := &models.Job{}
	if err := scanJobColumns(r.db.QueryRowContext(ctx, query, id), job); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get job: %w", err)
	}
	return job, nil
}

func (r *Repository) List(ctx context.Context, filter models.JobFilter) ([]*models.Job, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE 1=1`
	args := []any{}

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

	var out []*models.Job
	for rows.Next() {
		job := &models.Job{}
		if err := scanJobColumns(rows, job); err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}
		out = append(out, job)
	}

	return out, nil
}

// JobStats holds aggregated job statistics.
type JobStats struct {
	Total      int                                         `json:"total"`
	ByStatus   map[models.JobStatus]int                    `json:"by_status"`
	ByType     map[models.JobType]map[models.JobStatus]int `json:"by_type"`
	DurationMs struct {
		Overall float64 `json:"overall_ms"`
		ByType  map[models.JobType]struct {
			Count           int     `json:"count"`
			AvgDurationMs   float64 `json:"avg_duration_ms"`
			ImagesGenerated int     `json:"images_generated,omitempty"`
			Errors          int     `json:"errors,omitempty"`
		} `json:"by_type"`
	} `json:"durations"`
	StaleRunning int `json:"stale_running"` // running jobs with expired lease (zombie)
	Recent24h    struct {
		Completed       int `json:"completed"`
		Failed          int `json:"failed"`
		ImagesGenerated int `json:"images_generated"`
	} `json:"recent_24h"`
}

// GetStats returns aggregated job statistics for monitoring.
func (r *Repository) GetStats(ctx context.Context) (*JobStats, error) {
	stats := &JobStats{
		ByStatus: make(map[models.JobStatus]int),
		ByType:   make(map[models.JobType]map[models.JobStatus]int),
	}
	stats.DurationMs.ByType = make(map[models.JobType]struct {
		Count           int     `json:"count"`
		AvgDurationMs   float64 `json:"avg_duration_ms"`
		ImagesGenerated int     `json:"images_generated,omitempty"`
		Errors          int     `json:"errors,omitempty"`
	})

	// 1. Count by status
	rows, err := r.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM jobs GROUP BY status`)
	if err != nil {
		r.log.Warn("getStats: by-status query failed", zap.Error(err))
	} else {
		for rows.Next() {
			var status models.JobStatus
			var cnt int
			if err := rows.Scan(&status, &cnt); err != nil {
				r.log.Warn("getStats: scan by-status row", zap.Error(err))
			} else {
				stats.ByStatus[status] = cnt
				stats.Total += cnt
			}
		}
		rows.Close()
	}

	// 2. Count by type + status
	rows, err = r.db.QueryContext(ctx, `SELECT type, status, COUNT(*) FROM jobs GROUP BY type, status ORDER BY type, status`)
	if err != nil {
		r.log.Warn("getStats: by-type query failed", zap.Error(err))
	} else {
		for rows.Next() {
			var jt models.JobType
			var js models.JobStatus
			var cnt int
			if err := rows.Scan(&jt, &js, &cnt); err != nil {
				r.log.Warn("getStats: scan by-type row", zap.Error(err))
			} else {
				if _, ok := stats.ByType[jt]; !ok {
					stats.ByType[jt] = make(map[models.JobStatus]int)
				}
				stats.ByType[jt][js] = cnt
			}
		}
		rows.Close()
	}

	// 3. Overall average duration for completed jobs
	var overallAvg sql.NullFloat64
	if err := r.db.QueryRowContext(ctx, `SELECT AVG((julianday(COALESCE(completed_at, updated_at)) - julianday(started_at)) * 86400.0) FROM jobs WHERE status = 'completed' AND started_at IS NOT NULL`).Scan(&overallAvg); err != nil {
		r.log.Warn("getStats: avg-duration query failed", zap.Error(err))
	} else if overallAvg.Valid {
		stats.DurationMs.Overall = overallAvg.Float64
	}

	// 4. Per-type: avg duration, images_generated, errors
	typeRow, err := r.db.QueryContext(ctx, `
		SELECT type,
			COUNT(*) as cnt,
			AVG((julianday(COALESCE(completed_at, updated_at)) - julianday(started_at)) * 86400.0) as avg_ms,
			COALESCE(SUM(CAST(json_extract(result_json, '$.stats.images_generated') AS INTEGER)), 0) as imgs_gen,
			SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) as errs
		FROM jobs
		WHERE status IN ('SUCCEEDED', 'FAILED')
		GROUP BY type
		ORDER BY cnt DESC
	`)
	if err != nil {
		r.log.Warn("getStats: per-type query failed", zap.Error(err))
	} else {
		for typeRow.Next() {
			var jt models.JobType
			var cnt int
			var avgMs, imgsGen, errs sql.NullFloat64
			if err := typeRow.Scan(&jt, &cnt, &avgMs, &imgsGen, &errs); err != nil {
				r.log.Warn("getStats: scan per-type row", zap.Error(err))
			} else {
				entry := stats.DurationMs.ByType[jt]
				entry.Count = cnt
				if avgMs.Valid {
					entry.AvgDurationMs = avgMs.Float64
				}
				if imgsGen.Valid {
					entry.ImagesGenerated = int(imgsGen.Float64)
				}
				if errs.Valid {
					entry.Errors = int(errs.Float64)
				}
				stats.DurationMs.ByType[jt] = entry
			}
		}
		typeRow.Close()
	}

	// 5. Stale/zombie active jobs (status=LEASED or RUNNING but lease_expiry in past)
	var staleCount sql.NullInt64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE status IN ('LEASED', 'RUNNING') AND lease_expiry < datetime('now')`).Scan(&staleCount); err != nil {
		r.log.Warn("getStats: stale-running query failed", zap.Error(err))
	} else if staleCount.Valid {
		stats.StaleRunning = int(staleCount.Int64)
	}

	// 6. Recent 24h stats
	if err := r.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN status = 'SUCCEEDED' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'FAILED' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CAST(json_extract(result_json, '$.stats.images_generated') AS INTEGER)), 0)
		FROM jobs
		WHERE created_at > datetime('now', '-1 day')
	`).Scan(&stats.Recent24h.Completed, &stats.Recent24h.Failed, &stats.Recent24h.ImagesGenerated); err != nil {
		r.log.Warn("getStats: recent-24h query failed", zap.Error(err))
	}

	return stats, nil
}

func (r *Repository) FindActiveByKey(ctx context.Context, activeKey string) (*models.Job, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE active_key = ? AND active_key != '' AND status IN ('PENDING', 'LEASED', 'RUNNING') ORDER BY started_at DESC LIMIT 1`
	job := &models.Job{}
	if err := scanJobColumns(r.db.QueryRowContext(ctx, query, activeKey), job); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find job by active key: %w", err)
	}
	return job, nil
}

// FindByTypeAndCorrelation returns the most recent job matching the
// (type, correlation_id) pair regardless of status. Used by Service.Enqueue
// to satisfy idempotency after a UNIQUE-constraint collision on the
// idx_jobs_type_correlation index (see migrations/sqlite/036_job_idempotency.sql).
//
// Returns (nil, nil) when correlation_id is empty — callers should short
// circuit before calling in that case so we never SELECT with a known-empty
// value (the index excludes empty strings, but a SELECT with ” would match
// no row anyway; the early return saves the round-trip).
func (r *Repository) FindByTypeAndCorrelation(ctx context.Context, jobType models.JobType, correlationID string) (*models.Job, error) {
	if correlationID == "" {
		return nil, nil
	}
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE type = ? AND correlation_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`
	job := &models.Job{}
	if err := scanJobColumns(r.db.QueryRowContext(ctx, query, jobType, correlationID), job); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find job by type+correlation: %w", err)
	}
	return job, nil
}

// RefreshMetrics recomputes queue depth / oldest-pending seconds gauges
// from the jobs table. Intended to be called periodically (e.g. every 30s)
// by the worker pool so Prometheus has fresh snapshots.
//
// Side-effects: writes to metrics.JobQueueDepth and metrics.JobOldestPendingSeconds.
// All known (active type) × (every status) combos are explicitly Set() to 0
// when missing, so drained queues don't leave stale non-zero gauge values.
func (r *Repository) RefreshMetrics(ctx context.Context) error {
	// Enumerate types active in the last 7 days so we know the labels to reset.
	activeTypes, err := r.db.QueryContext(ctx,
		`SELECT DISTINCT type FROM jobs WHERE created_at > datetime('now', '-7 days')`)
	if err != nil {
		return fmt.Errorf("active types query: %w", err)
	}
	types := make(map[models.JobType]bool)
	for activeTypes.Next() {
		var jt models.JobType
		if err := activeTypes.Scan(&jt); err != nil {
			activeTypes.Close()
			return fmt.Errorf("active types scan: %w", err)
		}
		types[jt] = true
	}
	activeTypes.Close()

	// Set depth for currently-observed type × status combinations.
	allStatuses := []models.JobStatus{
		models.StatusPending, models.StatusLeased, models.StatusRunning,
		models.StatusRetryWait,
		models.StatusFailed, models.StatusSucceeded, models.StatusCancelled,
	}
	depthSeen := make(map[string]bool)
	rows, err := r.db.QueryContext(ctx, `SELECT type, status, COUNT(*) FROM jobs GROUP BY type, status`)
	if err != nil {
		return fmt.Errorf("queue depth query: %w", err)
	}
	for rows.Next() {
		var jt models.JobType
		var js models.JobStatus
		var cnt int
		if err := rows.Scan(&jt, &js, &cnt); err != nil {
			rows.Close()
			return fmt.Errorf("queue depth scan: %w", err)
		}
		metrics.JobQueueDepth.WithLabelValues(string(jt), string(js)).Set(float64(cnt))
		depthSeen[string(jt)+"|"+string(js)] = true
	}
	rows.Close()

	// Reset gauges for combos that disappeared this tick.
	for jt := range types {
		for _, js := range allStatuses {
			if depthSeen[string(jt)+"|"+string(js)] {
				continue
			}
			metrics.JobQueueDepth.WithLabelValues(string(jt), string(js)).Set(0)
		}
	}

	// Oldest queued/retrying job per type (seconds since its created_at, or 0).
	oldest, err := r.db.QueryContext(ctx, `
		SELECT type, COALESCE(MAX((julianday('now') - julianday(created_at)) * 86400.0), 0)
		FROM jobs WHERE status = 'PENDING' GROUP BY type`)
	if err != nil {
		return fmt.Errorf("oldest pending query: %w", err)
	}
	oldestSeen := make(map[models.JobType]bool)
	for oldest.Next() {
		var jt models.JobType
		var secs float64
		if err := oldest.Scan(&jt, &secs); err != nil {
			oldest.Close()
			return fmt.Errorf("oldest pending scan: %w", err)
		}
		metrics.JobOldestPendingSeconds.WithLabelValues(string(jt)).Set(secs)
		oldestSeen[jt] = true
	}
	oldest.Close()
	for jt := range types {
		if !oldestSeen[jt] {
			metrics.JobOldestPendingSeconds.WithLabelValues(string(jt)).Set(0)
		}
	}
	return nil
}
