package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
)

type Reader struct{ jobs, obs *sql.DB }

func NewReader(jobsDB, observabilityDB *sql.DB) (*Reader, error) {
	if jobsDB == nil || observabilityDB == nil {
		return nil, fmt.Errorf("history reader requires jobs and observability databases")
	}
	return &Reader{jobs: jobsDB, obs: observabilityDB}, nil
}

func (r *Reader) ListHistory(ctx context.Context, f appjobs.HistoryFilter) ([]appjobs.HistoryItem, error) {
	where := []string{"1=1"}
	args := make([]any, 0, 8)
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if f.Type != "" {
		where = append(where, "type = ?")
		args = append(args, f.Type)
	}
	if f.From != nil {
		where = append(where, "created_at >= ?")
		args = append(args, f.From.UTC().Format(time.RFC3339))
	}
	if f.To != nil {
		where = append(where, "created_at <= ?")
		args = append(args, f.To.UTC().Format(time.RFC3339))
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 500 {
		limit = 500
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	rows, err := r.jobs.QueryContext(ctx, `SELECT id,type,status,correlation_id,result_json,error,created_at,updated_at,started_at,completed_at FROM jobs WHERE `+strings.Join(where, " AND ")+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list history jobs: %w", err)
	}
	defer rows.Close()
	items := make([]appjobs.HistoryItem, 0, limit)
	ids := make([]string, 0, limit)
	for rows.Next() {
		var item appjobs.HistoryItem
		var status, result, created, updated string
		var started, finished sql.NullString
		if err := rows.Scan(&item.JobID, &item.Operation, &status, &item.Correlation, &result, &item.Error, &created, &updated, &started, &finished); err != nil {
			return nil, fmt.Errorf("scan history job: %w", err)
		}
		item.Status = status
		item.CreatedAt, err = time.Parse(time.RFC3339, created)
		if err != nil {
			return nil, err
		}
		item.UpdatedAt, err = time.Parse(time.RFC3339, updated)
		if err != nil {
			return nil, err
		}
		if result != "" && result != "null" {
			item.Result = []byte(result)
		}
		if started.Valid {
			t, e := time.Parse(time.RFC3339, started.String)
			if e != nil {
				return nil, e
			}
			item.StartedAt = &t
		}
		if finished.Valid {
			t, e := time.Parse(time.RFC3339, finished.String)
			if e != nil {
				return nil, e
			}
			item.FinishedAt = &t
		}
		items = append(items, item)
		ids = append(ids, item.JobID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate history jobs: %w", err)
	}
	for i, id := range ids {
		var runID, runStatus, started, finished, report string
		var wall sql.NullInt64
		err := r.obs.QueryRowContext(ctx, `SELECT run_id,status,started_at,finished_at,wall_time_ms,report_json FROM run_observability WHERE job_id=? ORDER BY created_at DESC LIMIT 1`, id).Scan(&runID, &runStatus, &started, &finished, &wall, &report)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read history run %s: %w", id, err)
		}
		items[i].RunID, items[i].Status, items[i].Report, items[i].DurationMs = runID, runStatus, []byte(report), wall.Int64
		if started != "" {
			t, e := time.Parse(time.RFC3339, started)
			if e != nil {
				return nil, e
			}
			items[i].StartedAt = &t
		}
		if finished != "" {
			t, e := time.Parse(time.RFC3339, finished)
			if e != nil {
				return nil, e
			}
			items[i].FinishedAt = &t
		}
	}
	return items, nil
}

// GetRunReport returns the canonical run report JSON for the most recent run
// of a job, or nil when no report exists. The report_json column already
// carries the full RunReport (stages, operations, and the derived timing
// breakdown), so diagnostics consumers parse it directly.
func (r *Reader) GetRunReport(ctx context.Context, jobID string) (json.RawMessage, error) {
	if jobID == "" {
		return nil, fmt.Errorf("get run report: job id is required")
	}
	var report string
	err := r.obs.QueryRowContext(ctx, `SELECT report_json FROM run_observability WHERE job_id=? ORDER BY created_at DESC LIMIT 1`, jobID).Scan(&report)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read run report %s: %w", jobID, err)
	}
	return json.RawMessage(report), nil
}

var _ appjobs.HistoryReader = (*Reader)(nil)
