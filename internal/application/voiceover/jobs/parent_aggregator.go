// Package jobs — parent_aggregator.go (FASE 1 wiring, July 2026;
// PR-VO-PARENT-AGGREGATOR-SPLIT, P0 #4 in VO-DECOMPOSITION-2026-07-04,
// deadline 2026-08-01).
//
// ParentAggregator is the background poller that reads parent
// voiceover.generate jobs with parent_state=waiting_children or
// parent_state=partial_success, queries their children's terminal
// statuses from the broker, computes the canonical aggregate
// ParentState via domain job.StateMachine (5-state machine with
// REQUIRED/optional distinction), and updates the parent's Result
// map via jobsSvc.FinalizeAggregateParent when the state transitions to a
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
//
// 4-file split (PR-VO-PARENT-AGGREGATOR-SPLIT, P0 #4):
//   - parent_aggregator.go (this file) — THIN orchestrator: ParentAggregator
//     struct + NewParentAggregator + Start + Tick + aggregateOne + finalizeParent.
//   - parent_eligibility.go — cache (§15.2) + IsParentAwaitingAggregation gate
//   - ZeroChildrenAggregateResult short-circuit.
//   - parent_state_machine.go — domainToVoiceoverParentState
//     (the 5-state → 4-state wire-shape mapping).
//   - parent_aggregator_state.go — P1.2 typed column constants
//     (JobParentStateColumn) + dual-write contract documentation.
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
// Audit 2026-07-03 P0 #1 (added FinalizeAggregateParent): the legacy Complete
// surface is preserved for back-compat with non-aggregate callers
// (e.g. pre-orchestrator handlers that delegate directly). The
// aggregator's finalizeParent routes aggregate-specific writes
// through FinalizeAggregateParent, which is the canonical no-lease CAS path
// for post-fan-out parent state transitions.
//
// FASE 2 (July 2026): FinalizeAggregateParent now accepts expectedVersion from
// the domain StateMachine so the SQL layer can add `AND revision = ?`
// as a second CAS fence alongside the existing (status, parent_state)
// guard. A zero expectedVersion means "skip the revision check"
// (backward-compatible with callers that don't own a StateMachine).
//
// FASE 3 (July 2026): ListAwaitingAggregation replaces the generic
// List(type=voiceover.generate) + in-memory JSON filter. The new
// method uses an optimized SQL query with idx_jobs_type_status
// index + json_extract WHERE clause (voiceover.md §10.5).
type AggregatorJobsService interface {
	List(ctx context.Context, filter job.Filter) ([]job.Job, error)
	Get(ctx context.Context, id string) (*job.Job, error)
	Complete(ctx context.Context, id string, result map[string]any) error
	// ListAwaitingAggregation returns voiceover.generate parents awaiting
	// aggregation (parent_state IN waiting_children/partial_success,
	// broker status IN RUNNING/FINALIZING/SUCCEEDED). Uses the optimized
	// idx_jobs_type_status index + json_extract filter.
	ListAwaitingAggregation(ctx context.Context, parentType string, limit int) ([]job.Job, error)
	// FinalizeAggregateParent is the canonical no-lease CAS that re-finalises the
	// parent (status, parent_state) atomically. expectedVersion is
	// the domain StateMachine.Version() — when > 0, the SQL layer
	// adds `AND revision = expectedVersion` as a second CAS fence.
	FinalizeAggregateParent(ctx context.Context, id string, targetStatus job.Status, result map[string]any, errMsg string, expectedVersion int) error
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

	// previouslyTerminal caches children that were terminal on the
	// previous tick, keyed by parentJobID → childID. On retry ticks,
	// cached children skip the Get() call and are fed directly to the
	// StateMachine with their cached terminal status. The cache is
	// cleared when the parent is finalised.
	//
	// §15.2 (July 2026): this cache prevents the aggregator from
	// re-querying already-terminal children (e.g. c-it, c-pt) on a
	// retry tick when only one child (c-en) changed status.
	previouslyTerminal map[string]map[string]cachedChildTerminalState
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

// Tick performs one aggregation sweep. Uses the optimized
// ListAwaitingAggregation query (voiceover.md §10.5) which filters
// parent_state via json_extract in SQL rather than loading all
// voiceover.generate jobs and filtering in Go memory.
// Errors on individual parents are logged and skipped — a failed
// parent will be retried on the next tick.
func (a *ParentAggregator) Tick(ctx context.Context) {
	jobs, err := a.deps.JobsSvc.ListAwaitingAggregation(ctx, job.TypeVoiceoverGenerate, 100)
	if err != nil {
		a.deps.Logger.Error("ParentAggregator.Tick: ListAwaitingAggregation failed", zap.Error(err))
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

// aggregateOne processes a single parent job: deserializes into typed
// VoiceoverParentResult, reads child outcomes via typed VoiceoverChildResult,
// computes the aggregate via domain StateMachine, and finalizes the parent
// via finalizeParent with version-based CAS.
//
// FASE 2 (July 2026): all internal map[string]any access replaced with
// typed DTOs (VoiceoverParentResult, VoiceoverChildResult,
// VoiceoverAggregateResult). The broker boundary (FinalizeAggregateParent) still
// accepts map[string]any for wire-shape back-compat.
func (a *ParentAggregator) aggregateOne(ctx context.Context, j job.Job) error {
	// Step 1: unmarshal parent result into typed VoiceoverParentResult.
	var parentResult VoiceoverParentResult
	if len(j.Result) > 0 {
		if err := json.Unmarshal(j.Result, &parentResult); err != nil {
			a.deps.Logger.Debug("ParentAggregator: cannot unmarshal parent result, skipping",
				zap.String("parent_job_id", j.ID), zap.Error(err))
			return nil
		}
	}

	// Step 2: only process parents awaiting aggregation.
	if !IsParentAwaitingAggregation(&parentResult) {
		return nil
	}

	// Step 3: extract child job IDs.
	childIDs := parentResult.ChildJobIDs
	// Filter empty strings (failed-enqueue placeholders).
	filtered := make([]string, 0, len(childIDs))
	for _, id := range childIDs {
		if id != "" {
			filtered = append(filtered, id)
		}
	}
	childIDs = filtered
	if len(childIDs) == 0 {
		a.finalizeParent(ctx, j.ID, ZeroChildrenAggregateResult())
		return nil
	}

	// Step 4: construct domain StateMachine + explicit TransitionToWaitingChildren.
	sm := job.NewStateMachine(j.ID, len(childIDs))
	if err := sm.TransitionToWaitingChildren(childIDs); err != nil {
		a.deps.Logger.Debug("ParentAggregator: TransitionToWaitingChildren rejected",
			zap.String("parent_job_id", j.ID), zap.Error(err))
		return nil
	}

	allTerminal := true
	requiredFailed := 0
	for _, childID := range childIDs {
		// Step 4a (§15.2, July 2026): check the previously-terminal
		// cache before hitting Get(). Children that were terminal on
		// the previous tick are fed directly to the StateMachine
		// without a broker round-trip. This prevents the retry tick
		// from re-querying already-terminal siblings when only one
		// child changed status.
		var status job.Status
		var childErr string
		var childRequired bool

		if cached, wasCached := loadCachedTerminalChild(a.previouslyTerminal, j.ID, childID); wasCached {
			status = cached.status
			childErr = cached.errStr
			childRequired = cached.required
			logCacheHit(a.deps.Logger, j.ID, childID, string(status))
		} else {
			childJob, err := a.deps.JobsSvc.Get(ctx, childID)
			if err != nil {
				a.deps.Logger.Warn("ParentAggregator: Get child failed",
					zap.String("parent_job_id", j.ID),
					zap.String("child_job_id", childID),
					zap.Error(err))
				allTerminal = false
				continue
			}
			status = childJob.Status
			if status == job.StatusQueued || status == job.StatusLeased || status == job.StatusRunning || status == job.StatusFinalizing || status == job.StatusRetryWait {
				allTerminal = false
			}

			// P0.1 gate: typed child result.
			if len(childJob.Result) > 0 {
				var childResult VoiceoverChildResult
				if err := json.Unmarshal(childJob.Result, &childResult); err == nil {
					if childResult.OK != nil && !*childResult.OK {
						if status == job.StatusSucceeded {
							a.deps.Logger.Warn("ParentAggregator: child broker-succeeded but result.ok=false (P0.1 gate override)",
								zap.String("parent_job_id", j.ID),
								zap.String("child_job_id", childID))
							status = job.StatusFailed
						}
						childErr = childResult.Error
					}
				}
			}

			// FASE 2: typed child payload deserialization.
			if len(childJob.Payload) > 0 {
				var childPayload VoiceoverChildPayload
				if jsonErr := json.Unmarshal(childJob.Payload, &childPayload); jsonErr == nil {
					childRequired = childPayload.Required
				}
			}
		}

		if !status.IsTerminal() {
			// Non-terminal children are not yet classified as required-failed.
			// The count is updated when the child reaches terminal.
		} else if childRequired && (status == job.StatusFailed || status == job.StatusCancelled) {
			requiredFailed++
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

		// §15.2 (July 2026): cache terminal children in the loop so the
		// retry tick can skip re-querying them via Get(). Only truly
		// terminal children are cached (status.IsTerminal() gate
		// excludes RETRY_WAIT/RUNNING/LEASED/etc.). The Required flag
		// from the child payload is preserved in the cache so REQUIRED-
		// failed children are not downgraded to optional on retry ticks.
		if status.IsTerminal() {
			if a.previouslyTerminal == nil {
				a.previouslyTerminal = make(map[string]map[string]cachedChildTerminalState)
			}
			storeCachedTerminalChild(a.previouslyTerminal, j.ID, childID, cachedChildTerminalState{
				status:   status,
				required: childRequired,
				errStr:   childErr,
			})
		}
	}

	// Step 5: skip if not all children are terminal.
	if !allTerminal {
		return nil
	}

	// Step 6: compute canonical aggregate state.
	if err := sm.Compute(); err != nil {
		a.deps.Logger.Error("ParentAggregator: StateMachine.Compute failed",
			zap.String("parent_job_id", j.ID), zap.Error(err))
		return nil
	}

	newPS := domainToVoiceoverParentState(sm)
	// FASE 2 (July 2026): version CAS uses j.Revision (the DB-level
	// revision at the time of List), NOT sm.Version() (the in-memory
	// StateMachine counter). The SQL `revision` column is bumped on
	// every Complete/Fail/FinalizeAggregateParent transaction; passing the
	// pre-flip revision lets the UPDATE check `AND revision = ?`
	// for optimistic locking. A concurrent FinalizeAggregateParent would have
	// bumped revision, causing a 0 rows-affected → CAS conflict.
	aggResult := VoiceoverAggregateResult{
		ParentState:         newPS,
		TotalChildren:       len(childIDs),
		SucceededCount:      len(sm.Succeeded()),
		FailedCount:         len(sm.Failed()),
		RequiredFailedCount: requiredFailed,
		StateMachineVersion: j.Revision,
		ChildIDs:            childIDs,
	}
	a.finalizeParent(ctx, j.ID, aggResult)
	return nil
}

// finalizeParent builds the result map from the typed aggregate result
// and persists it via jobsSvc.FinalizeAggregateParent with version-based CAS.
//
// FASE 2 (July 2026): replaces updateParentState(map[string]any, ParentState)
// with a typed VoiceoverAggregateResult struct. The expectedVersion from
// the domain StateMachine is passed to FinalizeAggregateParent so the SQL layer can
// add `AND revision = ?` as a second CAS fence.
//
// godlike/06 SSOT: this is the SINGLE canonical writer of post-fan-out
// parent (status, parent_state) tuples.
func (a *ParentAggregator) finalizeParent(ctx context.Context, parentJobID string, agg VoiceoverAggregateResult) {
	resultMap := map[string]any{
		"parent_state":          string(agg.ParentState),
		"_aggregator_version":   agg.StateMachineVersion,
		"total_children":        agg.TotalChildren,
		"succeeded_count":       agg.SucceededCount,
		"failed_count":          agg.FailedCount,
		"required_failed_count": agg.RequiredFailedCount,
	}

	targetStatus := job.StatusSucceeded
	errMsg := ""
	if agg.ParentState == voiceover.ParentFailed {
		targetStatus = job.StatusFailed
		errMsg = "parent aggregate: all children definitively failed (FASE 2 version CAS)"
	}

	// Clear the terminal-child cache unconditionally — once we attempt
	// FinalizeAggregateParent, the cached state is no longer needed regardless of
	// outcome (success, idempotent replay, or CAS conflict).
	clearCachedTerminalChildren(a.previouslyTerminal, parentJobID)

	if err := a.deps.JobsSvc.FinalizeAggregateParent(ctx, parentJobID, targetStatus, resultMap, errMsg, agg.StateMachineVersion); err != nil {
		if errors.Is(err, domainremote.ErrAlreadyTerminalAggregate) {
			a.deps.Logger.Info("ParentAggregator: parent already finalised (replay no-op)",
				zap.String("parent_job_id", parentJobID),
				zap.String("parent_state", string(agg.ParentState)))
			return
		}
		a.deps.Logger.Warn("ParentAggregator: FinalizeAggregateParent failed",
			zap.String("parent_job_id", parentJobID),
			zap.String("target_status", string(targetStatus)),
			zap.String("new_parent_state", string(agg.ParentState)),
			zap.Int("expected_version", agg.StateMachineVersion),
			zap.Error(err))
		return
	}
	a.deps.Logger.Info("ParentAggregator: parent state transition",
		zap.String("parent_job_id", parentJobID),
		zap.String("parent_state", string(agg.ParentState)),
		zap.String("target_status", string(targetStatus)),
		zap.Int("version", agg.StateMachineVersion))
}

// domainToVoiceoverParentState moved to parent_state_machine.go
// (PR-VO-PARENT-AGGREGATOR-SPLIT, P0 #4 in VO-DECOMPOSITION-2026-07-04).
// See parent_state_machine.go for the canonical implementation.
