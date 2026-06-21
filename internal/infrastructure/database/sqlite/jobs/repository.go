// Package jobs provides the canonical job repository with atomic CAS operations
// for the 7-state job lifecycle.
//
// States: queued → leased → running → completed / retry_wait / failed / cancelled
//
// Implements Repository from internal/core/domain/job directly,
// without conversion through legacy model types (PR4: job.Job SSOT).
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

type SQLiteStore struct {
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

func NewSQLiteStore(db *sql.DB, log *zap.Logger) *SQLiteStore {
	return &SQLiteStore{db: db, log: log}
}

// DB returns the underlying *sql.DB for direct query access in tests + migrations.
func (r *SQLiteStore) DB() *sql.DB { return r.db }

// Compile-time check: SQLiteStore satisfies the canonical job.Store contract.
var _ job.Store = (*SQLiteStore)(nil)

func (r *SQLiteStore) Create(ctx context.Context, j *job.Job) error {
	query := `
		INSERT INTO jobs (id, type, status, priority, project, video_name, active_key,
			correlation_id, payload_json, result_json, progress, error, retry_count, max_retries,
			worker_id, lease_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at, revision)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	payloadJSON := string(j.Payload)
	if payloadJSON == "" || payloadJSON == "null" {
		payloadJSON = "{}"
	}
	resultJSON := string(j.Result)
	if resultJSON == "" || resultJSON == "null" {
		resultJSON = "{}"
	}

	// Revision is per-row monotonic counter; on creation it is 1 (the
	// first revision before any transition fired).
	revision := j.Revision
	if revision <= 0 {
		revision = 1
	}

	_, err := r.db.ExecContext(ctx, query,
		j.ID, j.Type, j.Status, j.Priority, j.Project, j.VideoName, j.ActiveKey,
		j.CorrelationID,
		payloadJSON, resultJSON, j.Progress, j.Error,
		j.RetryCount, j.MaxRetries, j.WorkerID, j.LeaseID,
		timeutil.FormatPtrRFC3339(j.LeaseExpiry),
		timeutil.FormatRFC3339(j.CreatedAt), timeutil.FormatRFC3339(j.UpdatedAt),
		timeutil.FormatPtrRFC3339(j.StartedAt), timeutil.FormatPtrRFC3339(j.CompletedAt), nil, revision)
	if err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}

	return nil
}

// scanJobColumns reads the canonical job column list into job, then
// unmarshals the JSON payload/result and parses the time columns. Used by
// Get, List, and FindActiveByKey.
func scanJobColumns(s scanner, j *job.Job) error {
	var payloadJSON, resultJSON string
	var leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt *string
	if err := s.Scan(
		&j.ID, &j.Type, &j.Status, &j.Priority, &j.Project, &j.VideoName, &j.ActiveKey,
		&j.CorrelationID,
		&payloadJSON, &resultJSON, &j.Progress, &j.Error, &j.RetryCount, &j.MaxRetries,
		&j.WorkerID, &j.LeaseID, &leaseExpiry, &createdAt, &updatedAt,
		&startedAt, &completedAt, &cancelledAt, &j.Revision,
	); err != nil {
		return err
	}
	unmarshalJobFields(j, payloadJSON, resultJSON, leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt)
	return nil
}

// scanner is the minimum surface of *sql.Row and *sql.Rows that scanJobColumns
// needs. Defined here so we can share the same code between single-row and
// multi-row reads.
type scanner interface {
	Scan(dest ...any) error
}

func (r *SQLiteStore) Get(ctx context.Context, id string) (*job.Job, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE id = ?`
	j := &job.Job{}
	if err := scanJobColumns(r.db.QueryRowContext(ctx, query, id), j); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get job: %w", err)
	}
	return j, nil
}

func (r *SQLiteStore) List(ctx context.Context, filter job.Filter) ([]job.Job, error) {
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

	var out []job.Job
	for rows.Next() {
		j := &job.Job{}
		if err := scanJobColumns(rows, j); err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}
		out = append(out, *j)
	}

	return out, nil
}

// JobStats holds aggregated job statistics.
type JobStats struct {
	Total      int                           `json:"total"`
	ByStatus   map[job.Status]int            `json:"by_status"`
	ByType     map[string]map[job.Status]int `json:"by_type"`
	DurationMs struct {
		Overall float64 `json:"overall_ms"`
		ByType  map[string]struct {
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
func (r *SQLiteStore) GetStats(ctx context.Context) (*JobStats, error) {
	stats := &JobStats{
		ByStatus: make(map[job.Status]int),
		ByType:   make(map[string]map[job.Status]int),
	}
	stats.DurationMs.ByType = make(map[string]struct {
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
			var status job.Status
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
			var jt string
			var js job.Status
			var cnt int
			if err := rows.Scan(&jt, &js, &cnt); err != nil {
				r.log.Warn("getStats: scan by-type row", zap.Error(err))
			} else {
				if _, ok := stats.ByType[jt]; !ok {
					stats.ByType[jt] = make(map[job.Status]int)
				}
				stats.ByType[jt][js] = cnt
			}
		}
		rows.Close()
	}

	// 3. Overall average duration for completed jobs
	var overallAvg sql.NullFloat64
	if err := r.db.QueryRowContext(ctx, `SELECT AVG((julianday(COALESCE(completed_at, updated_at)) - julianday(started_at)) * 86400.0) FROM jobs WHERE status = 'SUCCEEDED' AND started_at IS NOT NULL`).Scan(&overallAvg); err != nil {
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
			SUM(CASE WHEN status = 'FAILED' THEN 1 ELSE 0 END) as errs
		FROM jobs
		WHERE status IN ('SUCCEEDED', 'FAILED')
		GROUP BY type
		ORDER BY cnt DESC
	`)
	if err != nil {
		r.log.Warn("getStats: per-type query failed", zap.Error(err))
	} else {
		for typeRow.Next() {
			var jt string
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

	// 5. Stale/zombie active jobs (status=leased or running but lease_expiry in past)
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

func (r *SQLiteStore) FindActiveByKey(ctx context.Context, activeKey string) (*job.Job, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE active_key = ? AND active_key != '' AND status IN ('QUEUED', 'LEASED', 'RUNNING') ORDER BY started_at DESC LIMIT 1`
	j := &job.Job{}
	if err := scanJobColumns(r.db.QueryRowContext(ctx, query, activeKey), j); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find job by active key: %w", err)
	}
	return j, nil
}

// FindByTypeAndCorrelation returns the most recent job matching the
// (type, correlation_id) pair regardless of status. Used by Service.Enqueue
// to satisfy idempotency after a UNIQUE-constraint collision on the
// idx_jobs_type_correlation index.
func (r *SQLiteStore) FindByTypeAndCorrelation(ctx context.Context, jobType string, correlationID string) (*job.Job, error) {
	if correlationID == "" {
		return nil, nil
	}
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE type = ? AND correlation_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`
	j := &job.Job{}
	if err := scanJobColumns(r.db.QueryRowContext(ctx, query, jobType, correlationID), j); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find job by type+correlation: %w", err)
	}
	return j, nil
}

// RefreshMetrics recomputes queue depth / oldest-pending seconds gauges
// from the jobs table. Intended to be called periodically (e.g. every 30s)
// by the worker pool so Prometheus has fresh snapshots.
func (r *SQLiteStore) RefreshMetrics(ctx context.Context) error {
	// Enumerate types active in the last 7 days so we know the labels to reset.
	activeTypes, err := r.db.QueryContext(ctx,
		`SELECT DISTINCT type FROM jobs WHERE created_at > datetime('now', '-7 days')`)
	if err != nil {
		return fmt.Errorf("active types query: %w", err)
	}
	types := make(map[string]bool)
	for activeTypes.Next() {
		var jt string
		if err := activeTypes.Scan(&jt); err != nil {
			activeTypes.Close()
			return fmt.Errorf("active types scan: %w", err)
		}
		types[jt] = true
	}
	activeTypes.Close()

	// Set depth for currently-observed type × status combinations.
	allStatuses := []job.Status{
		job.StatusQueued, job.StatusLeased, job.StatusRunning,
		job.StatusRetryWait,
		job.StatusFailed, job.StatusSucceeded, job.StatusCancelled,
	}
	depthSeen := make(map[string]bool)
	rows, err := r.db.QueryContext(ctx, `SELECT type, status, COUNT(*) FROM jobs GROUP BY type, status`)
	if err != nil {
		return fmt.Errorf("queue depth query: %w", err)
	}
	for rows.Next() {
		var jt string
		var js job.Status
		var cnt int
		if err := rows.Scan(&jt, &js, &cnt); err != nil {
			rows.Close()
			return fmt.Errorf("queue depth scan: %w", err)
		}
		metrics.JobQueueDepth.WithLabelValues(jt, string(js)).Set(float64(cnt))
		depthSeen[jt+"|"+string(js)] = true
	}
	rows.Close()

	// Reset gauges for combos that disappeared this tick.
	for jt := range types {
		for _, js := range allStatuses {
			if depthSeen[jt+"|"+string(js)] {
				continue
			}
			metrics.JobQueueDepth.WithLabelValues(jt, string(js)).Set(0)
		}
	}

	// Oldest queued/retrying job per type (seconds since its created_at, or 0).
	oldest, err := r.db.QueryContext(ctx, `
		SELECT type, COALESCE(MAX((julianday('now') - julianday(created_at)) * 86400.0), 0)
		FROM jobs WHERE status = 'QUEUED' GROUP BY type`)
	if err != nil {
		return fmt.Errorf("oldest pending query: %w", err)
	}
	oldestSeen := make(map[string]bool)
	for oldest.Next() {
		var jt string
		var secs float64
		if err := oldest.Scan(&jt, &secs); err != nil {
			oldest.Close()
			return fmt.Errorf("oldest pending scan: %w", err)
		}
		metrics.JobOldestPendingSeconds.WithLabelValues(jt).Set(secs)
		oldestSeen[jt] = true
	}
	oldest.Close()
	for jt := range types {
		if !oldestSeen[jt] {
			metrics.JobOldestPendingSeconds.WithLabelValues(jt).Set(0)
		}
	}
	return nil
}

// ListEvents returns all events for a given job.
func (r *SQLiteStore) ListEvents(ctx context.Context, jobID string) ([]job.Event, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, job_id, type, message, data_json, created_at FROM job_events WHERE job_id = ? ORDER BY created_at ASC`, jobID)
	if err != nil {
		return nil, fmt.Errorf("listEvents: %w", err)
	}
	defer rows.Close()

	var events []job.Event
	for rows.Next() {
		var evt job.Event
		var dataJSON string
		if err := rows.Scan(&evt.ID, &evt.JobID, &evt.Type, &evt.Message, &dataJSON, &evt.CreatedAt); err != nil {
			return nil, fmt.Errorf("listEvents: scan: %w", err)
		}
		if len(dataJSON) > 0 {
			json.Unmarshal([]byte(dataJSON), &evt.Data)
		}
		events = append(events, evt)
	}
	return events, nil
}
