// Package jobs — parent_aggregator.go (micro-commit #5, Step 4, June 2026).
//
// ParentAggregator is the background poller that reads parent
// voiceover.generate jobs with parent_state=waiting_children or
// parent_state=partial_success, queries their children's terminal
// statuses from the broker, computes the canonical aggregate
// ParentState via voiceover.AggregateChildOutcomes, and updates
// the parent's Result map via jobsSvc.Complete when the state
// transitions to a terminal value.
//
// Why a background poller (not synchronous in HandleJob): the
// child job's terminal status is written by the dispatcher AFTER
// HandleJob returns — a synchronous call inside HandleJob would
// read a stale RUNNING/LEASED status for the triggering child.
// A single-threaded ticker avoids the read-modify-write race
// from concurrent child completions on SQLite.
package jobs

import (
	"context"
	"encoding/json"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"go.uber.org/zap"
)

// AggregatorDeps wires the parent aggregator's single external
// dependency (the jobs service) through a narrow interface so
// tests can inject stubs without constructing the full broker.
type AggregatorDeps struct {
	// JobsSvc is the narrow port used to List/Get/Complete jobs.
	// MANDATORY — fail-fast per AGENTS.md WireUp pattern.
	JobsSvc AggregatorJobsService

	// Logger is OPTIONAL (nil-safe via zap.NewNop()).
	Logger *zap.Logger

	// PollInterval is the background-tick interval. Production: 30s.
	// Zero or negative defaults to 30s.
	PollInterval time.Duration
}

// AggregatorJobsService is the narrow surface the ParentAggregator
// needs from the jobs broker. The production *appjobs.Service
// satisfies this implicitly. Extracting it as an interface lets
// tests inject stubs without the dispatcher + lease machinery.
type AggregatorJobsService interface {
	List(ctx context.Context, filter job.Filter) ([]job.Job, error)
	Get(ctx context.Context, id string) (*job.Job, error)
	Complete(ctx context.Context, id string, result map[string]any) error
}

// Compile-time assertion: *appjobs.Service satisfies AggregatorJobsService.
var _ AggregatorJobsService = (*appjobs.Service)(nil)

// ParentAggregator is the background poller that re-finalises parent
// jobs once all their children have reached terminal status.
type ParentAggregator struct {
	deps AggregatorDeps
}

// NewParentAggregator constructs the poller. JobsSvc is mandatory
// (panic on nil — fail-fast per AGENTS.md WireUp pattern).
// Logger is optional (nil-safe via zap.NewNop()).
func NewParentAggregator(deps AggregatorDeps) *ParentAggregator {
	if deps.JobsSvc == nil {
		panic("voiceover.Jobs.NewParentAggregator: JobsSvc is required (AggregatorDeps.JobsSvc)")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	if deps.PollInterval <= 0 {
		deps.PollInterval = 30 * time.Second
	}
	return &ParentAggregator{deps: deps}
}

// Start launches the background ticker goroutine. The ticker runs
// Tick() once per PollInterval. The goroutine exits when ctx is
// cancelled. Idempotent across multiple calls (the ticker owns its
// lifecycle independently).
func (a *ParentAggregator) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(a.deps.PollInterval)
		defer ticker.Stop()
		a.deps.Logger.Info("voiceover parent aggregator started",
			zap.Duration("poll_interval", a.deps.PollInterval))
		// Run once immediately on start.
		a.Tick(ctx)
		for {
			select {
			case <-ctx.Done():
				a.deps.Logger.Info("voiceover parent aggregator stopped")
				return
			case <-ticker.C:
				a.Tick(ctx)
			}
		}
	}()
}

// Tick performs one aggregation sweep. Lists all voiceover.generate
// parent jobs with non-terminal parent_state, reads their children,
// and updates the parent Result when all children are terminal.
// Errors on individual parents are logged and skipped — a failed
// parent will be retried on the next tick.
func (a *ParentAggregator) Tick(ctx context.Context) {
	jobs, err := a.deps.JobsSvc.List(ctx, job.Filter{
		Type: ptrStr(job.TypeVoiceoverGenerate),
	})
	if err != nil {
		a.deps.Logger.Error("ParentAggregator.Tick: List failed", zap.Error(err))
		return
	}
	if len(jobs) == 0 {
		return
	}

	for _, j := range jobs {
		if err := a.aggregateOne(ctx, j); err != nil {
			a.deps.Logger.Warn("ParentAggregator.Tick: aggregateOne failed",
				zap.String("parent_job_id", j.ID), zap.Error(err))
		}
	}
}

// aggregateOne processes a single parent job: reads its result map
// to extract child IDs, queries each child's terminal status, computes
// the aggregate ParentState, and updates the parent when the state
// transitions to a terminal value.
func (a *ParentAggregator) aggregateOne(ctx context.Context, j job.Job) error {
	// Step 1: unmarshal parent result to read current parent_state
	// and child_job_ids.
	var parentResult map[string]any
	if len(j.Result) > 0 {
		if err := json.Unmarshal(j.Result, &parentResult); err != nil {
			a.deps.Logger.Debug("ParentAggregator: cannot unmarshal parent result, skipping",
				zap.String("parent_job_id", j.ID), zap.Error(err))
			return nil // non-fatal: not every parent has valid JSON result
		}
	}
	if parentResult == nil {
		parentResult = map[string]any{}
	}

	// Step 2: only process parents that are still in a non-terminal
	// application-level state (waiting_children or partial_success).
	currentPS, _ := parentResult["parent_state"].(string)
	switch voiceover.ParentState(currentPS) {
	case voiceover.ParentWaitingChildren, voiceover.ParentPartialSuccess:
		// proceed — these are the states that need re-aggregation
	default:
		return nil // already terminal (succeeded, failed, or absent)
	}

	// Step 3: extract child job IDs from parent result.
	childIDs := extractChildJobIDs(parentResult)
	if len(childIDs) == 0 {
		// Parent has no recorded children — mark as partial_success.
		// This handles the edge case where the fan-out didn't record
		// child IDs (pre-ActiveKey era) OR the parent was enqueued
		// with empty languages.
		a.updateParentState(ctx, j.ID, parentResult, voiceover.ParentPartialSuccess)
		return nil
	}

	// Step 4: query each child's terminal status from the broker.
	children := make([]voiceover.LanguageOutcome, 0, len(childIDs))
	allTerminal := true
	for _, childID := range childIDs {
		childJob, err := a.deps.JobsSvc.Get(ctx, childID)
		if err != nil {
			a.deps.Logger.Warn("ParentAggregator: Get child failed",
				zap.String("parent_job_id", j.ID),
				zap.String("child_job_id", childID),
				zap.Error(err))
			allTerminal = false
			continue
		}
		status := childJob.Status
		if status == job.StatusQueued || status == job.StatusLeased || status == job.StatusRunning || status == job.StatusRetryWait {
			allTerminal = false
		}
		children = append(children, voiceover.LanguageOutcome{
			ChildJobID: childID,
			Status:     status,
		})
	}

	// Step 5: if not all children are terminal, skip this parent
	// (next tick will re-evaluate). If we can't determine (some Get
	// calls failed), keep current state.
	if !allTerminal {
		return nil
	}

	// Step 6: compute canonical aggregate state.
	newPS := voiceover.AggregateChildOutcomes(children)
	a.updateParentState(ctx, j.ID, parentResult, newPS)
	return nil
}

// updateParentState merges the new parent_state into the result map
// and persists it via jobsSvc.Complete. Idempotent: calling Complete
// on an already-completed job safely overwrites the Result JSON.
func (a *ParentAggregator) updateParentState(ctx context.Context, parentJobID string, resultMap map[string]any, newPS voiceover.ParentState) {
	resultMap["parent_state"] = string(newPS)
	if err := a.deps.JobsSvc.Complete(ctx, parentJobID, resultMap); err != nil {
		a.deps.Logger.Warn("ParentAggregator: Complete failed",
			zap.String("parent_job_id", parentJobID),
			zap.String("new_parent_state", string(newPS)),
			zap.Error(err))
		return
	}
	a.deps.Logger.Info("ParentAggregator: parent state transition",
		zap.String("parent_job_id", parentJobID),
		zap.String("parent_state", string(newPS)))
}

// extractChildJobIDs reads child_job_ids from a parent result map.
// The field is populated by FanoutVoiceoversUseCase.Execute and
// stored in job.Result via toFanoutResultMap.
func extractChildJobIDs(parentResult map[string]any) []string {
	raw, ok := parentResult["child_job_ids"]
	if !ok || raw == nil {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ptrStr returns a pointer to the given string. Used to build
// the job.Filter.Type pointer field. Inline here (no pkg/ptrutil
// import) so the aggregator keeps a tight import surface.
func ptrStr(s string) *string { return &s }
