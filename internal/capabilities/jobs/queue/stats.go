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
// The JobStats type currently lives in
// internal/platform/sqlite/jobs. Moving it to
// internal/domain/job is a deferred follow-up (it would touch the
// repository surface + JSON wire shape). PR-0 keeps the read port
// infra-aware for minimum-churn; the godlike/06 layer-violation
// review is an explicit ticketed item in the Wave 22 follow-up
// backlog.
package queue

import (
	"context"
	"encoding/json"
	"time"

	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
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
// The port deliberately returns the SQLite-native JobStats type:
// the admin Stats endpoint serializes it 1:1 today. A deferred
// Wave 22 follow-up relocates JobStats to internal/domain/job and
// lifts the api layer off the infra dependency — until then this
// port is the minimum that lets a JobsHandler depend on (job.Service,
// JobStatsReader) instead of *appjobs.Service concrete.
type JobStatsReader interface {
	GetStats(ctx context.Context) (*sqljobs.JobStats, error)
}

// Compile-time assertion: *Service satisfies JobStatsReader.
// Catches signature drift at build time.
var _ JobStatsReader = (*Service)(nil)
