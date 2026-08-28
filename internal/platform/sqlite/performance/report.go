package performance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
)

type ReportReader struct{ db *sql.DB }

func NewReportReader(db *sql.DB) (*ReportReader, error) {
	if db == nil {
		return nil, errors.New("performance report: nil database")
	}
	return &ReportReader{db: db}, nil
}

var _ capperformance.PerformanceReportReader = (*ReportReader)(nil)

func (r *ReportReader) PerformanceReport(ctx context.Context, jobID string) (capperformance.PerformanceReport, error) {
	if r == nil || r.db == nil {
		return capperformance.PerformanceReport{}, errors.New("performance report: not configured")
	}
	if strings.TrimSpace(jobID) == "" {
		return capperformance.PerformanceReport{}, errors.New("performance report: job id is required")
	}
	var out capperformance.PerformanceReport
	out.JobID = jobID
	var resultJSON sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT type,status,COALESCE(worker_id,''),COALESCE(host,''),COALESCE(started_at,''),COALESCE(completed_at,''),COALESCE(duration_ms,0),COALESCE(result_json,'{}') FROM jobs WHERE id=?`, jobID).
		Scan(&out.Job.Type, &out.Job.Status, &out.Job.WorkerID, &out.Job.Host, &out.Job.StartedAt, &out.Job.CompletedAt, &out.Job.WallTimeMS, &resultJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, fmt.Errorf("performance report: job %s not found", jobID)
		}
		return out, fmt.Errorf("performance report: read job %s: %w", jobID, err)
	}
	var metrics []struct {
		Name  string
		Value float64
		Unit  string
	}
	rows, err := r.db.QueryContext(ctx, `SELECT metric_name,metric_value,unit FROM job_registry_metrics WHERE job_id=? ORDER BY created_at, rowid`, jobID)
	if err != nil {
		return out, fmt.Errorf("performance report: read registry metrics: %w", err)
	}
	for rows.Next() {
		var m struct {
			Name  string
			Value float64
			Unit  string
		}
		if err := rows.Scan(&m.Name, &m.Value, &m.Unit); err != nil {
			rows.Close()
			return out, err
		}
		metrics = append(metrics, m)
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	for _, m := range metrics {
		switch m.Name {
		case "queue_wait_ms":
			out.Queue.WaitMS = int64(m.Value)
			out.Job.QueueWaitMS = int64(m.Value)
		case "wall_time_ms":
			out.Job.WallTimeMS = int64(m.Value)
		}
	}
	out.Queue.Status = out.Job.Status

	var raw map[string]any
	if err := json.Unmarshal([]byte(resultJSON.String), &raw); err != nil {
		return out, fmt.Errorf("performance report: decode result_json: %w", err)
	}
	if render, ok := raw["render"].(map[string]any); ok {
		if m, ok := render["metrics_v2"].(map[string]any); ok {
			out.Render.MetricsV2 = m
		}
	}

	ops, err := r.OperationStats(ctx, jobID)
	if err != nil {
		return out, err
	}
	out.Render.Operations = ops
	for _, op := range ops {
		out.Render.TotalMS += int64(op.AvgElapsedMS)
	}
	out.Preparation.StageMS = map[string]int64{}
	for _, m := range metrics {
		if strings.HasSuffix(m.Name, "_ms") && m.Name != "queue_wait_ms" && m.Name != "wall_time_ms" {
			out.Preparation.StageMS[m.Name] = int64(m.Value)
			out.Preparation.TotalMS += int64(m.Value)
		}
	}
	out.Derived.CacheRatio = cacheRatio(ops)
	if out.Render.TotalMS > 0 && out.Job.WallTimeMS > 0 {
		out.Derived.XRT = float64(out.Render.TotalMS) / float64(out.Job.WallTimeMS)
		out.Derived.SpeedFactor = 1 / out.Derived.XRT
	}
	if out.Job.WallTimeMS > 0 {
		out.Derived.ClipsPerMinute = 60000 / float64(out.Job.WallTimeMS)
	}
	if out.Job.WallTimeMS > 0 && out.Render.TotalMS > 0 {
		out.Derived.ParallelismEfficiency = float64(out.Render.TotalMS) / float64(out.Job.WallTimeMS)
	}
	if out.Job.WallTimeMS > 0 && out.Preparation.TotalMS > 0 {
		out.Derived.CriticalPathPercent = float64(out.Preparation.TotalMS) / float64(out.Job.WallTimeMS) * 100
	}
	return out, nil
}

func (r *ReportReader) OperationStats(ctx context.Context, jobID string) ([]capperformance.OperationStats, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT operation,COUNT(*),AVG(elapsed_ms),AVG(output_size_bytes),SUM(cache_hit),AVG(source_duration_ms) FROM performance_operations WHERE job_id=? GROUP BY operation ORDER BY operation`, jobID)
	if err != nil {
		return nil, fmt.Errorf("performance report: read operations: %w", err)
	}
	defer rows.Close()
	var out []capperformance.OperationStats
	for rows.Next() {
		var s capperformance.OperationStats
		var elapsed, output, source sql.NullFloat64
		if err := rows.Scan(&s.Operation, &s.Runs, &elapsed, &output, &s.CacheHits, &source); err != nil {
			return nil, err
		}
		s.AvgElapsedMS = elapsed.Float64
		s.AvgOutputSizeBytes = output.Float64
		s.AvgSourceDurationMS = source.Float64
		if s.AvgSourceDurationMS > 0 {
			s.AvgRTF = s.AvgElapsedMS / s.AvgSourceDurationMS
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func cacheRatio(ops []capperformance.OperationStats) float64 {
	var total, hits int64
	for _, o := range ops {
		total += o.Runs
		hits += o.CacheHits
	}
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}
