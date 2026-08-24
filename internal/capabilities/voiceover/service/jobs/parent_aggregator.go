// Package jobs — parent_aggregator.go slim orchestrator (PR-SPLIT-VO-PARENT-AGG, July 2026).
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
// 6-file split (PR-SPLIT-VO-PARENT-AGG, P2, deadline 2026-08-08):
//   - parent_aggregator.go (this file) — THIN orchestrator: AggregatorDeps
//   - AggregatorJobsService + var _ pin + ParentAggregator struct +
//     NewParentAggregator + Start + Tick + isKnownTypedParentState.
//   - parent_aggregator_aggregate.go — aggregateOne (per-parent
//     deserialise → StateMachine.Transition loop → VoiceoverAggregateResult
//     construction).
//   - parent_aggregator_finalize.go — finalizeParent (per-parent
//     resultMap construction + FinalizeAggregateParent CAS + cache clear).
//   - parent_aggregator_state.go — P1.2 typed column constants
//     (JobParentStateColumn) + dual-write contract documentation.
//   - parent_state_machine.go — domainToVoiceoverParentState
//     (the 5-state → 4-state wire-shape mapping).
//   - parent_eligibility.go — cache (§15.2) + IsParentAwaitingAggregation gate
//   - ZeroChildrenAggregateResult short-circuit.
package jobs

import (
	"context"
	"sync/atomic"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	jobvoiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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
// index + json_extract WHERE clause.
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
		// ── Defense-in-depth recover ────────────────────────────
		// Catches any panic in the tick goroutine (including future
		// nil-derefs in aggregateOne) so the server stays up. The
		// canonical fix is the broker-side typed ErrChildNotFound +
		// a defensive nil-guard in aggregateOne — see
		// PR-VO-AGGREGATEORPHAN-GUARD (forward-pointer, unblocked).
		defer func() {
			if rec := recover(); rec != nil {
				a.deps.Logger.Error("voiceover parent aggregator: PANIC recovered in tick goroutine — voiceover tick skipped this cycle; server stays up",
					zap.Any("panic", rec))
			}
		}()
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
// ListAwaitingAggregation query which filters
// parent_state via json_extract in SQL rather than loading all
// voiceover.generate jobs and filtering in Go memory.
// Errors on individual parents are logged and skipped — a failed
// parent will be retried on the next tick.
func (a *ParentAggregator) Tick(ctx context.Context) {
	jobs, err := a.deps.JobsSvc.ListAwaitingAggregation(ctx, jobvoiceover.TypeGenerate, 100)
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

// domainToVoiceoverParentState moved to parent_state_machine.go
// (PR-VO-PARENT-AGGREGATOR-SPLIT, P0 #4 in VO-DECOMPOSITION-2026-07-04).
// See parent_state_machine.go for the canonical implementation.

// isKnownTypedParentState is the canonical whitelist for the
// PR-P1.2-SQL-DUAL-WRITE read-side validation (MUST-FIX #1 from
// the code-reviewer). Returns true iff s is one of the 4
// canonical voiceover.ParentState values.
//
// godlike/06 SSOT (one canonical owner per fact): the canonical
// value space is the voiceover.ParentState enum at
// internal/application/voiceover/parent_state.go. This helper is
// a thin mirror of the 4 known values for the read-side
// validation; the canonical 4-value list is re-declared here
// (rather than imported + iterated) to keep the import surface
// of the aggregator package minimal (per the existing voiceover
// import already at the top of this file).
//
// godlike/07 minimal-blast-radius: a future change to the
// voiceover.ParentState value space (adding a 5th value) MUST
// update this whitelist in lockstep. A drift surfaces as a
// silent skip (the new value would never reach the StateMachine
// via the read-side preference path) — caught at integration
// time, not at unit-test time. Forward-pointer: a workspace-
// level SSOT drift test could lock this if the risk materializes.
func isKnownTypedParentState(s string) bool {
	switch voiceover.ParentState(s) {
	case voiceover.ParentWaitingChildren,
		voiceover.ParentSucceeded,
		voiceover.ParentPartialSuccess,
		voiceover.ParentFailed:
		return true
	}
	return false
}
