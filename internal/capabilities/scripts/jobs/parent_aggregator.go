// Package jobs — parent_aggregator.go: background poller that reads
// script.generate parents with parent_state=waiting_children, queries
// their children's terminal statuses, and finalizes the parent via
// FinalizeAggregateParent when the aggregate dictates.
package jobs

import (
	"context"
	"sync/atomic"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// ScriptAggregatorDeps wires the parent aggregator's single external
// dependency (the jobs service) through a narrow interface so tests
// can inject stubs without constructing the full broker. Mirrors
// voiceover/jobs/parent_aggregator.go::AggregatorDeps exactly.
type ScriptAggregatorDeps struct {
	JobsSvc ScriptAggregatorJobsService
	RunRepo scriptgen.RunRepository
	Logger  *zap.Logger
	// PollInterval is the background-tick interval. Production: 30s.
	// Zero or negative defaults to 30s (mirrors voiceover).
	PollInterval time.Duration
}

// Google Docs was retired (2026-10-25): the deferred Docs leg, its run
// completer port and its re-drive enqueuer are gone with it.

// ScriptAggregatorJobsService is the narrow surface the ParentAggregator
// needs from the jobs broker (Pattern 0 — AGENTS.md).
type ScriptAggregatorJobsService interface {
	Get(ctx context.Context, id string) (*job.Job, error)
	// List finds jobs by filter.
	List(ctx context.Context, filter job.Filter) ([]job.Job, error)
	// ListAwaitingAggregation returns script.generate parents awaiting
	// aggregation (parent_state = waiting_children, broker status IN
	// RUNNING/FINALIZING/SUCCEEDED). Commit 3: parentType param scopes
	// the query to script.generate only.
	ListAwaitingAggregation(ctx context.Context, parentType string, limit int) ([]job.Job, error)
	// TerminalFlip is the canonical no-lease CAS that re-finalises the
	// parent (status, parent_state) atomically. expectedVersion is the
	// domain StateMachine.Version() — when > 0, the SQL layer adds
	// `AND revision = ?` as a second CAS fence.
	FinalizeAggregateParent(ctx context.Context, id string, targetStatus job.Status, result map[string]any, errMsg string, expectedVersion int) error
}

// Compile-time assertion: *appjobs.Service satisfies ScriptAggregatorJobsService.
//
// Note: the interface was narrowed in Commit 3 (removed List/Complete — the
// aggregator doesn't use them). *appjobs.Service still satisfies it because
// Go interface satisfaction is structural.
var _ ScriptAggregatorJobsService = (*appjobs.Service)(nil)

// ScriptParentResult is the typed parent job result. Field names
// and JSON tags match toFanoutResultMap's map shape exactly so
// json.Unmarshal(job.Result) into this struct is a drop-in replacement
// for map[string]any access. Mirrors voiceover/jobs/result_dto.go
// ::VoiceoverParentResult but adapted for script-batch semantics.
type ScriptParentResult struct {
	OK            bool                         `json:"ok"`
	ParentJobID   string                       `json:"parent_job_id"`
	TotalItems    int                          `json:"total_items"`
	ChildJobIDs   []string                     `json:"child_job_ids"`
	PerLanguage   []string                     `json:"per_language,omitempty"`
	StageProgress map[string]job.StageProgress `json:"stage_progress,omitempty"`
	ParentState   string                       `json:"parent_state"`
	RequestID     string                       `json:"request_id,omitempty"`
	FailedEnqueue int                          `json:"failed_enqueue_count,omitempty"`
	// AggregatorVersion is the StateMachine version at the last aggregator
	// tick (zero when the fan-out handler wrote the result).
	AggregatorVersion int `json:"_aggregator_version,omitempty"`
}

// IsAwaitingAggregation reports whether the parent is in a non-terminal
// application-level state that the aggregator should process.
//
// Commit 3 P0 #4: only waiting_children is non-terminal. partial_success
// is terminal — once the aggregator finalises a parent as partial_success,
// it must NOT be re-aggregated (P0 #7 infinite aggregation loop).
func (r *ScriptParentResult) IsAwaitingAggregation() bool {
	return ScriptParentState(r.ParentState) == ScriptParentWaitingChildren
}

// ScriptChildResult is the typed per-item child result the aggregator
// reads to apply the P0 #1-style false-success gate (P0 #4 extension).
// Voiceover uses an explicit *bool OK; scripts use a hardbool because
// the handler always emits the field (no absent-vs-false disambiguation
// needed). "parent_state" remains a string rather than typed enum so
// the wire shape stays JSON-stable across future refactors.
type ScriptChildResult struct {
	OK            *bool                        `json:"ok,omitempty"`
	Status        string                       `json:"status"`
	ItemID        string                       `json:"item_id"`
	JobID         string                       `json:"job_id"`
	ParentJobID   string                       `json:"parent_job_id"`
	RequestID     string                       `json:"request_id,omitempty"`
	Error         string                       `json:"error,omitempty"`
	StageProgress map[string]job.StageProgress `json:"stage_progress,omitempty"`
}

// ScriptAggregateResult is the typed output of the aggregation loop.
// Built from the domain StateMachine (Transition + Compute) and passed
// to finalizeParent for FinalizeAggregateParent persistence. Mirrors
// voiceover's VoiceoverAggregateResult.
type ScriptAggregateResult struct {
	ParentState    ScriptParentState
	TotalItems     int
	SucceededCount int
	FailedCount    int
	ParentRevision int
	ChildIDs       []string
	PerLanguage    []string
	StageProgress  map[string]job.StageProgress
}

// ScriptParentState is the canonical typed enum for script-batch
// parents (mirror of voiceover.ParentState). 5 named values matching
// the domain 5-state machine semantics.
type ScriptParentState string

const (
	// ScriptParentWaitingChildren — fan-out complete, awaiting child terminals.
	ScriptParentWaitingChildren ScriptParentState = "waiting_children"
	// ScriptParentPartialSuccess — some optional children succeeded,
	// some failed (mixed).
	ScriptParentPartialSuccess ScriptParentState = "partial_success"
	// ScriptParentSucceeded — all children succeeded.
	ScriptParentSucceeded ScriptParentState = "succeeded"
	// ScriptParentFailedTerminal — every child definitively failed.
	ScriptParentFailedTerminal ScriptParentState = "failed_terminal"
)

// ScriptParentAggregator re-finalises parent jobs once all children
// have reached terminal status.
type ScriptParentAggregator struct {
	deps    ScriptAggregatorDeps
	started atomic.Bool
}

// NewScriptParentAggregator constructs the poller. JobsSvc is mandatory
// (panic on nil — fail-fast per AGENTS.md WireUp pattern).
func NewScriptParentAggregator(deps ScriptAggregatorDeps) *ScriptParentAggregator {
	if deps.JobsSvc == nil {
		panic("scripts.Jobs.NewScriptParentAggregator: JobsSvc is required (ScriptAggregatorDeps.JobsSvc)")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	if deps.PollInterval <= 0 {
		deps.PollInterval = 30 * time.Second
	}
	return &ScriptParentAggregator{deps: deps}
}

// Start launches the background ticker goroutine. Idempotent (atomic.Bool
// guard prevents double-spawn).
func (a *ScriptParentAggregator) Start(ctx context.Context) {
	if !a.started.CompareAndSwap(false, true) {
		a.deps.Logger.Info("script parent aggregator Start called twice; ignoring (idempotency guard)")
		return
	}
	go func() {
		ticker := time.NewTicker(a.deps.PollInterval)
		defer ticker.Stop()
		a.deps.Logger.Info("script parent aggregator started",
			zap.Duration("poll_interval", a.deps.PollInterval))
		a.Tick(ctx)
		for {
			select {
			case <-ctx.Done():
				a.deps.Logger.Info("script parent aggregator stopped")
				return
			case <-ticker.C:
				a.Tick(ctx)
			}
		}
	}()
}

// Tick performs one aggregation sweep. Errors on individual parents are
// logged and skipped — a failed parent is retried on the next tick.
func (a *ScriptParentAggregator) Tick(ctx context.Context) {
	jobs, err := a.deps.JobsSvc.ListAwaitingAggregation(ctx, appjobs.TypeScriptGenerate, 100)
	if err != nil {
		a.deps.Logger.Error("ScriptParentAggregator.Tick: ListAwaitingAggregation failed", zap.Error(err))
		return
	}
	if len(jobs) == 0 {
		return
	}
	for _, j := range jobs {
		if err := a.aggregateOne(ctx, j); err != nil {
			a.deps.Logger.Warn("ScriptParentAggregator.Tick: aggregateOne failed",
				zap.String("parent_job_id", j.ID), zap.Error(err))
		}
	}
}
