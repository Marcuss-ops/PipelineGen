// Package jobs — parent_aggregator.go (FASE 1 wiring, July 2026).
//
// ParentAggregator is the background poller that reads parent
// voiceover.generate jobs with parent_state=waiting_children or
// parent_state=partial_success, queries their children's terminal
// statuses from the broker, computes the canonical aggregate
// ParentState via domain job.StateMachine (5-state machine with
// REQUIRED/optional distinction), and updates the parent's Result
// map via jobsSvc.TerminalFlip when the state transitions to a
// terminal value.
//
// FASE 1 (July 2026): replaced voiceover.AggregateChildOutcomes
// (4-state pure classifier) with domain job.StateMachine
// (Transition + Compute). The StateMachine handles REQUIRED-failed
// short-circuits in Transition() and distinguishes optional-only
// failures in Compute().
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
	"errors"
	"sync/atomic"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainremote "github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
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
//
// Audit 2026-07-03 P0 #1 (added TerminalFlip): the legacy Complete
// surface is preserved for back-compat with non-aggregate callers
// (e.g. pre-orchestrator handlers that delegate directly). The
// aggregator's updateParentState routes aggregate-specific writes
// through TerminalFlip, which is the canonical no-lease CAS path
// for post-fan-out parent state transitions.
type AggregatorJobsService interface {
	List(ctx context.Context, filter job.Filter) ([]job.Job, error)
	Get(ctx context.Context, id string) (*job.Job, error)
	Complete(ctx context.Context, id string, result map[string]any) error
	// TerminalFlip is the audit 2026-07-03 P0 #1 closure surface:
	// the aggregator's no-lease CAS that re-finalises the parent
	// (status, parent_state) atomically. targetStatus MUST be
	// job.StatusSucceeded (when aggregate=succeeded/partial_success)
	// or job.StatusFailed (when aggregate=failed_terminal). The
	// underlying broker guards on (status, json_extract
	// result_json.'$.parent_state' IN awaiting-values) so concurrent
	// aggregator ticks and replays are first-to-act wins.
	TerminalFlip(ctx context.Context, id string, targetStatus job.Status, result map[string]any, errMsg string) error
}

// Compile-time assertion: *appjobs.Service satisfies AggregatorJobsService.
var _ AggregatorJobsService = (*appjobs.Service)(nil)

// ParentAggregator is the background poller that re-finalises parent
// jobs once all their children have reached terminal status.
type ParentAggregator struct {
	deps AggregatorDeps
	// started is an idempotency guard: calling Start more than once
	// would otherwise spawn a second ticker goroutine that races on the
	// SQLite read-modify-write path in aggregateOne. atomic.Bool keeps
	// the guard lock-free on the hot path.
	started atomic.Bool
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
// cancelled. Idempotent: subsequent calls are no-ops (atomic guard
// prevents double-spawning the ticker goroutine).
func (a *ParentAggregator) Start(ctx context.Context) {
	if !a.started.CompareAndSwap(false, true) {
		// Already started — do not spawn a second ticker. The first
		// goroutine still owns the lifecycle and will exit on ctx.Done().
		a.deps.Logger.Info("voiceover parent aggregator Start called twice; ignoring (idempotency guard)")
		return
	}
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
//
// P0.1 false-success gate (Step 4 child-result audit, July 2026):
// a child job may be marked SUCCEEDED by the broker even though the
// per-item pipeline failed (ProcessVoiceoverItemUseCase returned
// result.Status=StatusFailed but err==nil → the handler now returns
// an error via the P0.1 gate in GenerateItemJobHandler, but for
// children completed BEFORE the P0.1 gate landed, the broker status
// may still say SUCCEEDED while result.ok=false). The aggregator
// MUST inspect each child's result.ok before classifying — a child
// with result.ok=false is treated as FAILED regardless of broker
// status. This is defense-in-depth: even if the P0.1 handler gate
// misses a case, the aggregator's re-read of the result map catches
// it at the parent-finalisation boundary.
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
	}	// Step 4: construct domain StateMachine and feed child terminal events.
	// FASE 1 (July 2026): replaces voiceover.AggregateChildOutcomes with
	// the canonical 5-state domain.StateMachine (Dispatching →
	// WaitingChildren → Aggregating → Succeeded/FailedTerminal).
	// The StateMachine handles REQUIRED-failed short-circuits in
	// Transition() and distinguishes optional-only failures in Compute().
	//
	// P0.1 false-success gate: the broker status is the primary signal
	// but the child's result.ok is the authoritative secondary signal.
	// A child broker:Succeeded + result.ok:false MUST be treated as
	// FAILED — the per-item pipeline failed but the handler (pre-P0.1)
	// returned (resultMap, nil), so the broker saw success.
	sm := job.NewStateMachine(j.ID, len(childIDs))
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
		if status == job.StatusQueued || status == job.StatusLeased || status == job.StatusRunning || status == job.StatusFinalizing || status == job.StatusRetryWait {
			allTerminal = false
		}

		// P0.1 gate: inspect the child's result.ok field as the
		// authoritative secondary signal. If result.ok == false, the
		// per-item pipeline failed even if the broker says SUCCEEDED.
		childErr := ""
		if len(childJob.Result) > 0 {
			var childResult map[string]any
			if err := json.Unmarshal(childJob.Result, &childResult); err == nil {
				if ok, hasOK := childResult["ok"].(bool); hasOK && !ok {
					if status == job.StatusSucceeded {
						a.deps.Logger.Warn("ParentAggregator: child broker-succeeded but result.ok=false (P0.1 gate override)",
							zap.String("parent_job_id", j.ID),
							zap.String("child_job_id", childID))
						status = job.StatusFailed
					}
					if errStr, _ := childResult["error"].(string); errStr != "" {
						childErr = errStr
					}
				}
			}
		}

		// Extract Required flag from child payload (GenerateVoiceoverItemCommand).
		// Default false — FASE 2 will wire the fanout to set Required explicitly.
		childRequired := false
		if len(childJob.Payload) > 0 {
			var childPayload map[string]any
			if jsonErr := json.Unmarshal(childJob.Payload, &childPayload); jsonErr == nil {
				if req, ok := childPayload["required"].(bool); ok {
					childRequired = req
				}
			}
		}

		// Feed child terminal event to the domain StateMachine.
		succeeded := (status == job.StatusSucceeded)
		if tErr := sm.Transition(job.ChildTerminatedEvent{
			ParentJobID: j.ID,
			ChildJobID:  childID,
			Outcome: job.ChildOutcome{
				JobID:     childID,
				Succeeded: succeeded,
				Required:  childRequired,
				Error:     childErr,
				Status:    string(status),
			},
		}); tErr != nil {
			a.deps.Logger.Debug("ParentAggregator: StateMachine.Transition skipped",
				zap.String("parent_job_id", j.ID),
				zap.String("child_job_id", childID),
				zap.Error(tErr))
		}
	}

	// Step 5: if not all children are terminal, skip this parent
	// (next tick will re-evaluate). If we can't determine (some Get
	// calls failed), keep current state.
	if !allTerminal {
		return nil
	}

	// Step 6: compute canonical aggregate state via domain StateMachine.
	if err := sm.Compute(); err != nil {
		a.deps.Logger.Error("ParentAggregator: StateMachine.Compute failed",
			zap.String("parent_job_id", j.ID), zap.Error(err))
		return nil
	}

	newPS := domainToVoiceoverParentState(sm)
	a.updateParentState(ctx, j.ID, parentResult, newPS)
	return nil
}

// updateParentState merges the new parent_state into the result map and
// persists it via jobsSvc.TerminalFlip no-lease CAS (audit 2026-07-03 P0 #1
// closure). The target status is derived from the aggregate state: a
// ParentFailed aggregate flips the broker-level terminal from SUCCEEDED
// (worker-emitted) to FAILED, eliminating the prior "broker status SUCCEEDED
// + result.parent_state=failed" double-truth. Partial-success and succeeded
// aggregates preserve broker.status=SUCCEEDED with the new parent_state
// embedded in the result JSON.
//
// ErrAlreadyTerminalAggregate is treated as an idempotent replay no-op
// (another tick already landed the flip on the prior interval); we
// deliberately do NOT log at warn-level — the contract is godlike/07
// no-fake-success, not silent-degrade (godlike/08 fail-closed posture:
// the message IS the source of truth, no further action required).
//
// Other errors (ErrAggregateCASConflict, infra-level) bubble up at warn-level
// for operator dashboards; the next tick will retry and may re-classify
// if children have moved terminal in the meantime.
//
// godlike/06 SSOT: this is the SINGLE canonical writer of post-fan-out
// parent (status, parent_state) tuples. The legacy Complete path retains
// its lease-protected semantics for worker-driven completions only.
func (a *ParentAggregator) updateParentState(ctx context.Context, parentJobID string, resultMap map[string]any, newPS voiceover.ParentState) {
	resultMap["parent_state"] = string(newPS)
	// Audit 2026-07-03 P0 #1: route the aggregate state to the broker
	// terminal. ParentFailed means "all children definitively failed",
	// which we MUST surface as broker.status=FAILED (not as a SUCCEEDED
	// broker-status + result.parent_state=failed double-truth).
	targetStatus := job.StatusSucceeded
	errMsg := ""
	if newPS == voiceover.ParentFailed {
		targetStatus = job.StatusFailed
		errMsg = "parent aggregate: all children definitively failed (audit 2026-07-03 P0 #1 closure)"
	}
	if err := a.deps.JobsSvc.TerminalFlip(ctx, parentJobID, targetStatus, resultMap, errMsg); err != nil {
		if errors.Is(err, domainremote.ErrAlreadyTerminalAggregate) {
			// Idempotent replay: another tick already finalised. Quiet no-op
			// — the canonical flip landed, the second call is a no-op.
			a.deps.Logger.Info("ParentAggregator: parent already finalised (replay no-op)",
				zap.String("parent_job_id", parentJobID),
				zap.String("parent_state", string(newPS)))
			return
		}
		a.deps.Logger.Warn("ParentAggregator: TerminalFlip failed",
			zap.String("parent_job_id", parentJobID),
			zap.String("target_status", string(targetStatus)),
			zap.String("new_parent_state", string(newPS)),
			zap.Error(err))
		return
	}
	a.deps.Logger.Info("ParentAggregator: parent state transition",
		zap.String("parent_job_id", parentJobID),
		zap.String("parent_state", string(newPS)),
		zap.String("target_status", string(targetStatus)))
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

// domainToVoiceoverParentState maps the domain 5-state machine result
// to the voiceover 4-state result enum for wire-shape back-compat.
// Succeeded with optional failures maps to partial_success so the
// API response distinguishes "all succeeded" from "succeeded with
// warnings".
func domainToVoiceoverParentState(sm *job.StateMachine) voiceover.ParentState {
	switch sm.State() {
	case job.ParentStateSucceeded:
		if len(sm.Failed()) > 0 {
			// Some optional children failed — succeeded with warnings.
			return voiceover.ParentPartialSuccess
		}
		return voiceover.ParentSucceeded
	case job.ParentStateFailedTerminal:
		return voiceover.ParentFailed
	default:
		// Non-terminal state passed to mapping — defensive fallback.
		return voiceover.ParentPartialSuccess
	}
}

// ptrStr returns a pointer to the given string. Used to build
// the job.Filter.Type pointer field. Inline here (no pkg/ptrutil
// import) so the aggregator keeps a tight import surface.
func ptrStr(s string) *string { return &s }
