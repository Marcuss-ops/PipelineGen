// Package jobs — JobStatsReader port (PR-0, June 2026).
//
// PR-0 split: the API Jobs handler previously called
// `h.service.GetStats(ctx)` against the canonical *appjobs.Service
// (concrete), reading the SQLite-only helper via a runtime
// type-assertion. The canonical `job.Service` domain interface does
// NOT carry `GetStats` — the helper is infra-specific and the
// inspection belongs on a narrow reader port, not the orchestrator
// surface. PR-0 introduces this port; the api handler now takes
// (job.Service, JobStatsReader) so the Stats endpoint consumes the
// reader explicitly while every other endpoint consumes the
// orchestrator.
//
// *appjobs.Service satisfies both: it is the composition-root single
// concrete that builds the job.Service value AND the JobStatsReader
// value at WireRegistry. Compile-time assertion below catches
// signature drift if either side changes shape.
//
// JobStats is owned by the kernel job contract, so this port is
// independent of the persistence adapter and can be implemented by
// SQLite, PostgreSQL, or an in-memory reader.
package jobs

import (
	"context"
	"encoding/json"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type HistoryFilter struct {
	Status string
	Type   string
	From   *time.Time
	To     *time.Time
	Limit  int
	Offset int
}

type HistoryItem struct {
	JobID       string          `json:"job_id"`
	RunID       string          `json:"run_id,omitempty"`
	Operation   string          `json:"operation"`
	Status      string          `json:"status"`
	Correlation string          `json:"correlation_id,omitempty"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	DurationMs  int64           `json:"duration_ms,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
	Report      json.RawMessage `json:"report,omitempty"`
}

type HistoryReader interface {
	ListHistory(context.Context, HistoryFilter) ([]HistoryItem, error)
	// GetRunReport returns the canonical run report JSON
	// (run_observability.report_json) for the most recent run of a job, or
	// nil when no report exists. It is the read surface for the canonical
	// timing diagnostics (e.g. /api/jobs/:id/full).
	GetRunReport(context.Context, string) (json.RawMessage, error)
}

// JobStatsReader is the narrow port for job statistics.
// Production bindings: *appjobs.Service (delegates to the SQLite
// repository via type-assertion). Tests can stub with a fake reader
// returning synthesized values.
//
// The port returns the canonical kernel JobStats DTO; the admin Stats
// endpoint serializes it 1:1 and no adapter-specific type leaks through.
type JobStatsReader interface {
	GetStats(ctx context.Context) (*job.JobStats, error)
}

// Compile-time assertion: *Service satisfies JobStatsReader.
// Catches signature drift at build time.
var _ JobStatsReader = (*Service)(nil)
