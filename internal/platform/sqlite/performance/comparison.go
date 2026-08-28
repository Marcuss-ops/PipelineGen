package performance

import (
	"context"
	"errors"
	"fmt"
	"strings"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
)

func (r *ReportReader) CompareRuns(ctx context.Context, runIDs []string) ([]capperformance.RunComparison, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("performance comparison: not configured")
	}
	if len(runIDs) == 0 {
		return []capperformance.RunComparison{}, nil
	}
	marks := make([]string, len(runIDs))
	args := make([]any, len(runIDs))
	for i, id := range runIDs {
		if strings.TrimSpace(id) == "" {
			return nil, errors.New("performance comparison: run id is required")
		}
		marks[i] = "?"
		args[i] = id
	}
	query := `SELECT pr.run_id,pr.job_id,COALESCE(bb.batch_id,''),COALESCE(bb.worker_slot_count,0),pr.wall_ms,
		COALESCE((SELECT AVG((julianday(completed_at)-julianday(started_at))*86400000.0) FROM benchmark_batch_jobs bj WHERE bj.batch_id=bb.batch_id)/NULLIF(pr.wall_ms,0),0),
		COALESCE((SELECT AVG(cpu_avg_pct) FROM resource_observations ro WHERE ro.run_id=pr.run_id),0),
		COALESCE((SELECT MAX(cpu_peak_pct) FROM resource_observations ro WHERE ro.run_id=pr.run_id),0),
		COALESCE((SELECT MAX(rss_peak_bytes) FROM resource_observations ro WHERE ro.run_id=pr.run_id),0),
		COALESCE((SELECT AVG(gpu_avg_pct) FROM resource_observations ro WHERE ro.run_id=pr.run_id),0),
		COALESCE((SELECT MAX(gpu_peak_pct) FROM resource_observations ro WHERE ro.run_id=pr.run_id),0)
		FROM performance_runs pr LEFT JOIN benchmark_batches bb ON bb.run_id=pr.run_id
		WHERE pr.run_id IN (` + strings.Join(marks, ",") + `) ORDER BY pr.started_at, pr.run_id`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("performance comparison: query runs: %w", err)
	}
	defer rows.Close()
	out := make([]capperformance.RunComparison, 0, len(runIDs))
	for rows.Next() {
		var c capperformance.RunComparison
		if err := rows.Scan(&c.RunID, &c.JobID, &c.BatchID, &c.WorkerSlotCount, &c.WallMS, &c.Concurrency, &c.CPUAvgPct, &c.CPUPeakPct, &c.RSSPeakBytes, &c.GPUAvgPct, &c.GPUPeakPct); err != nil {
			return nil, err
		}
		c.RTF, c.CacheRatio, c.ScalingEfficiency = r.runDerived(ctx, c.RunID, c.WallMS, c.Concurrency, c.WorkerSlotCount)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *ReportReader) runDerived(ctx context.Context, runID string, wallMS int64, concurrency float64, slots int) (float64, float64, float64) {
	var elapsed, source, hits, total float64
	if err := r.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(elapsed_ms),0),COALESCE(SUM(source_duration_ms),0),COALESCE(SUM(cache_hit),0),COUNT(*) FROM performance_operations WHERE run_id=?`, runID).Scan(&elapsed, &source, &hits, &total); err != nil {
		return 0, 0, 0
	}
	if total == 0 {
		return 0, 0, 0
	}
	rtf := float64(0)
	if source > 0 {
		rtf = elapsed / source
	}
	ratio := float64(0)
	if total > 0 {
		ratio = hits / total
	}
	eff := float64(0)
	if slots > 0 && concurrency > 0 {
		eff = concurrency / float64(slots)
	}
	return rtf, ratio, eff
}
