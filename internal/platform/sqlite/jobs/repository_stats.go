// Package jobs — repository_stats.go: aggregated job statistics + metrics refresh.
//
// Extracted from repository.go per AGENTS.md Pattern 5 (PR-REPO-SPLIT, July 2026).

package jobs

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// JobStats remains a compatibility alias for callers that historically
// imported the SQLite adapter's DTO. The canonical contract is owned by the
// kernel package; keeping this alias preserves the existing JSON shape and
// source compatibility while removing the concrete type from application
// ports.
type JobStats = job.JobStats

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
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE status IN ('LEASED', 'RUNNING', 'FINALIZING') AND lease_expiry < datetime('now')`).Scan(&staleCount); err != nil {
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
		job.StatusQueued, job.StatusLeased, job.StatusRunning, job.StatusFinalizing,
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
