// Package performance adapts the read-only performance aggregator to SQLite.
// It is a platform adapter: it reads the canonical observability report and
// workflow checkpoint from the observability database and the runner execution
// steps from the job-registry database, then hands them to the capability-side
// aggregator. It never writes and never estimates timings.
package performance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	perf "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/performance"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// Source implements perf.ReportSource over the observability and job-registry
// SQLite databases. It is the single read path shared by any CLI/API/report
// surface that projects a job's performance — no consumer should re-derive
// these queries locally.
type Source struct {
	jobs *sql.DB
	obs  *sql.DB
}

// NewSource builds a report source from the job-registry database (job_steps)
// and the observability database (run_observability).
func NewSource(jobsDB, obsDB *sql.DB) (*Source, error) {
	if jobsDB == nil || obsDB == nil {
		return nil, errors.New("performance source: jobs and observability databases are required")
	}
	return &Source{jobs: jobsDB, obs: obsDB}, nil
}

var _ perf.ReportSource = (*Source)(nil)

// Load reads the canonical RunReport, the job's AudioPipelineMetrics, and the
// recorded execution steps for one job. The latest run is authoritative.
func (s *Source) Load(ctx context.Context, jobID string) (kernobs.RunReport, scriptgeneration.AudioPipelineMetrics, []scriptgeneration.ExecutionStep, error) {
	if s == nil || s.obs == nil || s.jobs == nil {
		return kernobs.RunReport{}, scriptgeneration.AudioPipelineMetrics{}, nil, errors.New("performance source: databases are not configured")
	}
	if jobID == "" {
		return kernobs.RunReport{}, scriptgeneration.AudioPipelineMetrics{}, nil, errors.New("performance source: job id is required")
	}

	run, audio, err := s.loadRun(ctx, jobID)
	if err != nil {
		return kernobs.RunReport{}, scriptgeneration.AudioPipelineMetrics{}, nil, err
	}
	steps, err := s.loadSteps(ctx, jobID)
	if err != nil {
		return kernobs.RunReport{}, scriptgeneration.AudioPipelineMetrics{}, nil, err
	}
	return run, audio, steps, nil
}

func (s *Source) loadRun(ctx context.Context, jobID string) (kernobs.RunReport, scriptgeneration.AudioPipelineMetrics, error) {
	var reportJSON, payloadJSON string
	err := s.obs.QueryRowContext(ctx,
		`SELECT report_json, workflow_payload_json FROM run_observability WHERE job_id=? ORDER BY created_at DESC LIMIT 1`,
		jobID).Scan(&reportJSON, &payloadJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return kernobs.RunReport{}, scriptgeneration.AudioPipelineMetrics{}, fmt.Errorf("performance source: no run for job %s", jobID)
		}
		return kernobs.RunReport{}, scriptgeneration.AudioPipelineMetrics{}, fmt.Errorf("performance source: read run %s: %w", jobID, err)
	}

	var run kernobs.RunReport
	if err := json.Unmarshal([]byte(reportJSON), &run); err != nil {
		return kernobs.RunReport{}, scriptgeneration.AudioPipelineMetrics{}, fmt.Errorf("performance source: decode run report %s: %w", jobID, err)
	}

	audio := scriptgeneration.AudioPipelineMetrics{}
	if payloadJSON != "" && payloadJSON != "{}" {
		var checkpoint struct {
			Result *scriptgeneration.GenerateResult `json:"result,omitempty"`
		}
		if err := json.Unmarshal([]byte(payloadJSON), &checkpoint); err != nil {
			return kernobs.RunReport{}, scriptgeneration.AudioPipelineMetrics{}, fmt.Errorf("performance source: decode workflow checkpoint %s: %w", jobID, err)
		}
		if checkpoint.Result != nil && checkpoint.Result.AudioMetrics != nil {
			audio = *checkpoint.Result.AudioMetrics
		}
	}
	return run, audio, nil
}

func (s *Source) loadSteps(ctx context.Context, jobID string) ([]scriptgeneration.ExecutionStep, error) {
	rows, err := s.jobs.QueryContext(ctx,
		`SELECT step_id, step_name, step_type, status, started_at, completed_at, duration_ms, error_message FROM job_steps WHERE job_id=? ORDER BY created_at ASC, rowid ASC`,
		jobID)
	if err != nil {
		return nil, fmt.Errorf("performance source: read steps %s: %w", jobID, err)
	}
	defer rows.Close()

	steps := make([]scriptgeneration.ExecutionStep, 0, 8)
	for rows.Next() {
		var (
			step         scriptgeneration.ExecutionStep
			startedAt    sql.NullString
			completedAt  sql.NullString
			errorMessage sql.NullString
			stepID       sql.NullString
			stepName     sql.NullString
			stepType     sql.NullString
			status       sql.NullString
			durationMS   sql.NullInt64
		)
		if err := rows.Scan(&stepID, &stepName, &stepType, &status, &startedAt, &completedAt, &durationMS, &errorMessage); err != nil {
			return nil, fmt.Errorf("performance source: scan step %s: %w", jobID, err)
		}
		step.StepID = stepID.String
		step.Name = stepName.String
		step.Type = stepType.String
		step.Status = status.String
		step.DurationMS = durationMS.Int64
		step.ErrorMessage = errorMessage.String
		if step.StartedAt, err = parseStepTime(startedAt); err != nil {
			return nil, fmt.Errorf("performance source: parse step start %s: %w", jobID, err)
		}
		if step.CompletedAt, err = parseStepTime(completedAt); err != nil {
			return nil, fmt.Errorf("performance source: parse step end %s: %w", jobID, err)
		}
		steps = append(steps, step)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("performance source: iterate steps %s: %w", jobID, err)
	}
	return steps, nil
}

func parseStepTime(v sql.NullString) (time.Time, error) {
	if !v.Valid || v.String == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, v.String)
	if err != nil {
		t, err = time.Parse("2006-01-02 15:04:05", v.String)
	}
	return t, err
}
