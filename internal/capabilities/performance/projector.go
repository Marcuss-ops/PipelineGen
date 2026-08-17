package performance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	perf "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/performance"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// JobMeta carries durable job-row correlation fields that the finalized run
// report does not carry. They come from the canonical jobs table and are
// required so a projected run stays correlated (root_job_id must not be empty;
// the control plane verifier treats an empty root as an uncorrelated run).
type JobMeta struct {
	RootJobID string
	VideoID   string
	GitSHA    string
	HostID    string
}

// Projector projects completed jobs into the durable performance registry
// (performance_runs / performance_steps). It reads through the shared report
// source, builds the canonical performance report, and persists the run row
// plus one step per measured phase through the registry port. It never writes
// timings it did not read: only measured phases become steps.
type Projector struct {
	reports  perf.ReportSource
	registry Registry
}

// NewProjector builds a projector over a report source and a registry. Both are
// required; a nil registry or source makes ProjectJob fail closed rather than
// silently degrade to a no-op.
func NewProjector(reports perf.ReportSource, registry Registry) *Projector {
	return &Projector{reports: reports, registry: registry}
}

// ProjectJob loads the job's canonical inputs, builds the performance report,
// and persists the run row plus one step per measured phase. It returns the
// projected rows so callers can report what was written. Re-running for the
// same job is idempotent (run_id / step_id are deterministic).
func (p *Projector) ProjectJob(ctx context.Context, jobID string, meta JobMeta) (Run, []Step, error) {
	if p == nil || p.reports == nil || p.registry == nil {
		return Run{}, nil, errors.New("performance projector: report source and registry are required")
	}
	if jobID == "" {
		return Run{}, nil, errors.New("performance projector: job id is required")
	}
	run, audio, steps, err := p.reports.Load(ctx, jobID)
	if err != nil {
		return Run{}, nil, err
	}
	// Fail closed on a non-finalized run: a RUNNING report (e.g. a job that
	// reached a terminal broker state but never finalized its observability
	// run) has no wall time and must not be persisted as a RUNNING row.
	if run.Status == kernobs.StatusRunning || run.FinishedAt.IsZero() {
		return Run{}, nil, fmt.Errorf("performance projector: job %s has no finalized run (status=%s)", jobID, run.Status)
	}
	report := perf.Build(run, audio, steps, perf.DefaultPhaseResolver{})
	runRow, stepRows := Project(run, report, meta)
	if err := p.registry.RecordRun(ctx, runRow); err != nil {
		return Run{}, nil, err
	}
	for _, s := range stepRows {
		if err := p.registry.RecordStep(ctx, s); err != nil {
			return Run{}, nil, err
		}
	}
	return runRow, stepRows, nil
}

// Project maps a finalized run report plus a performance report into the
// durable Run and Step rows. It is a pure function: only measured phases
// become steps and nothing is estimated. The returned rows are keyed by the
// run ID so repeated projection converges on the same rows.
func Project(run kernobs.RunReport, report perf.PerformanceReport, meta JobMeta) (Run, []Step) {
	runRow := Run{
		RunID:        run.RunID,
		JobID:        run.JobID,
		RootJobID:    meta.RootJobID,
		VideoID:      meta.VideoID,
		GitSHA:       meta.GitSHA,
		WorkerID:     run.WorkerID,
		HostID:       meta.HostID,
		Status:       statusForRun(run.Status),
		WallMS:       run.WallTimeMs,
		MetadataJSON: reportMetadata(report),
		StartedAt:    formatPerformanceTime(run.StartedAt),
		CompletedAt:  formatPerformanceTime(run.FinishedAt),
	}
	steps := make([]Step, 0, len(report.Phases))
	for _, m := range report.Phases {
		if !m.Measured {
			continue
		}
		steps = append(steps, Step{
			StepID:       run.RunID + ":" + string(m.Phase),
			RunID:        run.RunID,
			JobID:        run.JobID,
			Name:         string(m.Phase),
			Status:       "SUCCEEDED",
			DurationMS:   m.DurationMS,
			MetadataJSON: counterMetadata(m.Counters),
			StartedAt:    formatPerformanceTime(run.StartedAt),
			CompletedAt:  formatPerformanceTime(run.FinishedAt),
		})
	}
	return runRow, steps
}

// statusForRun maps the kernel run status onto the performance_runs CHECK
// alphabet (RUNNING / SUCCEEDED / FAILED). CANCELLED and ABANDONED collapse to
// FAILED because they are non-success terminal states and the registry has no
// dedicated spelling for them.
func statusForRun(status string) string {
	switch status {
	case kernobs.StatusSucceeded:
		return "SUCCEEDED"
	case kernobs.StatusRunning:
		return "RUNNING"
	default:
		return "FAILED"
	}
}

// reportMetadata embeds the non-phase summary (script split, waits, audio,
// unmeasured phases) into the run's metadata_json so the durable row carries
// the full picture without duplicating per-phase columns.
func reportMetadata(report perf.PerformanceReport) string {
	meta := struct {
		Script     perf.ScriptSummary     `json:"script"`
		Waits      perf.WaitSummary       `json:"waits"`
		Audio      perf.AudioSummary      `json:"audio"`
		Unmeasured []perf.UnmeasuredPhase `json:"unmeasured,omitempty"`
	}{Script: report.Script, Waits: report.Waits, Audio: report.Audio, Unmeasured: report.Unmeasured}
	b, err := json.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func counterMetadata(counters map[string]float64) string {
	if len(counters) == 0 {
		return "{}"
	}
	b, err := json.Marshal(counters)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func formatPerformanceTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
