// Package jobs — parent_aggregator.go (P0 #4 audit 2026-07-03 closure,
// mirror of voiceover/jobs/parent_aggregator.go at commit 7f319edb).
//
// ParentAggregator is the background poller that reads parent
// script.generate jobs with parent_state=waiting_children,
// queries their children's terminal
// statuses from the broker, computes the canonical aggregate
// ParentState via domain job.StateMachine (5-state machine — all
// script-batch children are OPTIONAL since GenerationItemV2 has no
// Required field), and updates the parent's Result map via
// jobsSvc.FinalizeAggregateParent when the state transitions to a terminal value.
//
// P0 #4 mirror (audit 2026-07-03) of voiceover P0 #1 (commit 7f319edb).
// The structural differences vs voiceover are:
//   - ScriptParentResult carries script-specific fields
//     (total_items, succeeded_count, failed_count) instead of
//     voiceover's per-language counters.
//   - ScriptChildResult is derived from the result map written by
//     ScriptGenerateItemJobHandler (key set: ok, status, item_id).
//   - All script-batch children are OPTIONAL by default (GenerationItemV2
//     has no Required field); the StateMachine.Transition rule
//     "① REQUIRED-failed short-circuit" never fires. The aggregate
//     therefore settles on Succeeded when at least one child succeeded
//     (some-failed-op-only-OK), and FailedTerminal only when every
//     child definitively failed.
//
// The P0 #1-style false-success gate is EXTENDED to scripts:
// when a child's broker-status reads SUCCEEDED but its result.ok
// is false (the engine produced structurally empty output), the
// aggregator overrides the child to FAILED before feeding the
// StateMachine. See scriptItemP0_1Gate for the canonical heuristic.
//
// Why a background poller (not synchronous in HandleJob): the child job's
// terminal status is written by the worker AFTER HandleJob returns —
// a synchronous call inside HandleJob would read a stale RUNNING/LEASED
// status for the triggering child. A single-threaded ticker avoids
// the read-modify-write race from concurrent child completions.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainremote "github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	"go.uber.org/zap"
)

// ScriptAggregatorDeps wires the parent aggregator's single external
// dependency (the jobs service) through a narrow interface so tests
// can inject stubs without constructing the full broker. Mirrors
// voiceover/jobs/parent_aggregator.go::AggregatorDeps exactly.
type ScriptAggregatorDeps struct {
	JobsSvc ScriptAggregatorJobsService
	Logger  *zap.Logger
	// PollInterval is the background-tick interval. Production: 30s.
	// Zero or negative defaults to 30s (mirrors voiceover).
	PollInterval time.Duration
}

// ScriptAggregatorJobsService is the narrow surface the ParentAggregator
// needs from the jobs broker. The production *appjobs.Service satisfies
// this implicitly via Go interface satisfaction. The FinalizeAggregateParent
// method enables the canonical no-lease CAS re-flip after children
// reach terminal status.
//
// Pattern 0 surface (AGENTS.md) — tests inject stubs via this interface
// without instantiating the full lease machinery.
type ScriptAggregatorJobsService interface {
	Get(ctx context.Context, id string) (*job.Job, error)
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
	OK            bool     `json:"ok"`
	ParentJobID   string   `json:"parent_job_id"`
	TotalItems    int      `json:"total_items"`
	ChildJobIDs   []string `json:"child_job_ids"`
	ParentState   string   `json:"parent_state"`
	RequestID     string   `json:"request_id,omitempty"`
	FailedEnqueue int      `json:"failed_enqueue_count,omitempty"`
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
	OK          *bool  `json:"ok,omitempty"`
	Status      string `json:"status"`
	ItemID      string `json:"item_id"`
	JobID       string `json:"job_id"`
	ParentJobID string `json:"parent_job_id"`
	RequestID   string `json:"request_id,omitempty"`
	Error       string `json:"error,omitempty"`
}

// ScriptAggregateResult is the typed output of the aggregation loop.
// Built from the domain StateMachine (Transition + Compute) and passed
// to finalizeParent for FinalizeAggregateParent persistence. Mirrors
// voiceover's VoiceoverAggregateResult.
type ScriptAggregateResult struct {
	ParentState         ScriptParentState
	TotalItems          int
	SucceededCount      int
	FailedCount         int
	StateMachineVersion int
	ChildIDs            []string
}

// ScriptParentState is the canonical typed enum for script-batch
// parents (mirror of voiceover.ParentState). 5 named values matching
// the domain 5-state machine semantics.
type ScriptParentState string

const (
	// ScriptParentDispatching — initial state.
	ScriptParentDispatching ScriptParentState = "dispatching"
	// ScriptParentWaitingChildren — fan-out complete, awaiting child terminals.
	ScriptParentWaitingChildren ScriptParentState = "waiting_children"
	// ScriptParentPartialSuccess — some optional children succeeded,
	// some failed (mixed). Settled before aggregator finalises if
	// intermediate progress is desired.
	ScriptParentPartialSuccess ScriptParentState = "partial_success"
	// ScriptParentSucceeded — all children succeeded (or some-failed-op-only-OK).
	ScriptParentSucceeded ScriptParentState = "succeeded"
	// ScriptParentFailedTerminal — every child definitively failed.
	ScriptParentFailedTerminal ScriptParentState = "failed_terminal"
)

// IsTerminal reports whether the state has no allowed outgoing transitions.
func (s ScriptParentState) IsTerminal() bool {
	return s == ScriptParentSucceeded || s == ScriptParentFailedTerminal
}

// ScriptParentAggregator is the background poller that re-finalises
// parent jobs once all their children have reached terminal status.
type ScriptParentAggregator struct {
	deps    ScriptAggregatorDeps
	started atomic.Bool
	// previouslyTerminal caches children that were terminal on the
	// previous tick so the retry tick skips Get() for them (§15.2 voiceover
	// pattern — same optimisation for scripts).
	previouslyTerminal map[string]map[string]cachedScriptChildTerminalState
}

// cachedScriptChildTerminalState records the terminal state of a child
// from the previous tick so the retry tick skips re-querying it via
// Get(). Mirrors voiceover's cachedChildTerminalState.
type cachedScriptChildTerminalState struct {
	status job.Status
	ok     bool
	errStr string
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
	jobs, err := a.deps.JobsSvc.ListAwaitingAggregation(ctx, job.TypeScriptGenerate, 100)
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

// aggregateOne processes a single parent job: decodes the typed result,
// verifies it's awaiting aggregation, extracts child IDs, builds the
// canonical domain StateMachine, feeds each child's terminal event
// (with P0.1-style false-success gate override), and finalises via
// FinalizeAggregateParent.
func (a *ScriptParentAggregator) aggregateOne(ctx context.Context, j job.Job) error {
	// Decode the typed parent result.
	var parentResult ScriptParentResult
	if len(j.Result) > 0 {
		if err := json.Unmarshal(j.Result, &parentResult); err != nil {
			a.deps.Logger.Debug("ScriptParentAggregator: cannot unmarshal parent result, skipping",
				zap.String("parent_job_id", j.ID), zap.Error(err))
			return nil
		}
	}

	// Only process parents awaiting aggregation.
	if !parentResult.IsAwaitingAggregation() {
		return nil
	}

	// Filter empty child IDs (failed-enqueue placeholders).
	childIDs := parentResult.ChildJobIDs
	filtered := make([]string, 0, len(childIDs))
	for _, id := range childIDs {
		if id != "" {
			filtered = append(filtered, id)
		}
	}
	childIDs = filtered

	// Edge case: zero children enqueued → PartialSuccess immediately.
	if len(childIDs) == 0 {
		a.finalizeParent(ctx, j.ID, ScriptAggregateResult{
			ParentState:         ScriptParentPartialSuccess,
			TotalItems:          0,
			StateMachineVersion: j.Revision,
		})
		return nil
	}

	// Build the canonical domain StateMachine + explicitly transition
	// to WaitingChildren (mirrors voiceover FASE 1 explicit path).
	sm := job.NewStateMachine(j.ID, len(childIDs))
	if err := sm.TransitionToWaitingChildren(childIDs); err != nil {
		a.deps.Logger.Debug("ScriptParentAggregator: TransitionToWaitingChildren rejected",
			zap.String("parent_job_id", j.ID), zap.Error(err))
		return nil
	}

	allTerminal := true
	for _, childID := range childIDs {
		var status job.Status
		var childErr string
		var childOK bool

		prev, wasCached := a.previouslyTerminal[j.ID]
		if cached, ok := prev[childID]; wasCached && ok {
			status = cached.status
			childErr = cached.errStr
			childOK = cached.ok
			a.deps.Logger.Debug("ScriptParentAggregator: skipping Get for already-terminal child (cache hit)",
				zap.String("parent_job_id", j.ID),
				zap.String("child_job_id", childID),
				zap.String("cached_status", string(status)))
		} else {
			childJob, err := a.deps.JobsSvc.Get(ctx, childID)
			if err != nil {
				a.deps.Logger.Warn("ScriptParentAggregator: Get child failed",
					zap.String("parent_job_id", j.ID),
					zap.String("child_job_id", childID),
					zap.Error(err))
				allTerminal = false
				continue
			}
			// Commit 3 P0 #4: nil-child guard — the repo can legitimately
			// return (nil, nil) when the job does not exist. Treating
			// nil as non-terminal keeps the parent open.
			if childJob == nil {
				a.deps.Logger.Warn("ScriptParentAggregator: child job missing",
					zap.String("parent_job_id", j.ID),
					zap.String("child_job_id", childID))
				allTerminal = false
				continue
			}
			status = childJob.Status

			// P0 #4 P0.1-gate-extension: even when broker says
			// SUCCEEDED, the child result.ok false overrides to FAILED.
			// The handler writes result.ok via scriptItemIsSuccessful;
			// the aggregator applies the override HERE so all downstream
			// gates (StateMachine.Transition, Compute, FinalizeAggregateParent
			// targetStatus mapping) see one consistent truth.
			if status == job.StatusSucceeded {
				if isOKFalseOverride := scriptItemP0_1Gate(childJob.Result); isOKFalseOverride {
					a.deps.Logger.Warn("ScriptParentAggregator: child broker-succeeded but result.ok=false (P0 #4 P0.1-gate-extension override)",
						zap.String("parent_job_id", j.ID),
						zap.String("child_job_id", childID))
					status = job.StatusFailed
				}
			}
			if !status.IsTerminal() {
				// Non-terminal children are not classified here.
				// They will be re-queried on the next tick.
			}
			if status == job.StatusFailed || status == job.StatusCancelled {
				// Decode child result for forensic error string.
				var childResult ScriptChildResult
				if len(childJob.Result) > 0 {
					if err := json.Unmarshal(childJob.Result, &childResult); err == nil {
						childErr = childResult.Error
						if childResult.OK != nil {
							childOK = *childResult.OK
						}
					}
				}
			} else if status == job.StatusSucceeded {
				var childResult ScriptChildResult
				if len(childJob.Result) > 0 {
					if err := json.Unmarshal(childJob.Result, &childResult); err == nil {
						if childResult.OK != nil {
							childOK = *childResult.OK
						}
					}
				}
			}
		}

		// Non-terminal children → parent stays open (re-tick).
		if !status.IsTerminal() {
			allTerminal = false
			continue
		}

		// Feed child terminal event to the canonical domain StateMachine.
		// All script-batch children are OPTIONAL (GenerationItemV2 has
		// no Required field), so we set Required=false on every
		// ChildOutcome. The StateMachine.Transition rule
		// "① REQUIRED-failed short-circuit" therefore never fires for
		// script parents — the aggregate naturally falls through to
		// Compute().
		if tErr := sm.Transition(job.ChildTerminatedEvent{
			ParentJobID: j.ID,
			ChildJobID:  childID,
			Outcome: job.ChildOutcome{
				JobID:     childID,
				Succeeded: (status == job.StatusSucceeded) && childOK,
				Required:  false,
				Error:     childErr,
				Status:    string(status),
			},
		}); tErr != nil {
			a.deps.Logger.Debug("ScriptParentAggregator: StateMachine.Transition skipped",
				zap.String("parent_job_id", j.ID),
				zap.String("child_job_id", childID),
				zap.Error(tErr))
		}

		// Cache terminal children (mirrors voiceover §15.2).
		if status.IsTerminal() {
			if a.previouslyTerminal == nil {
				a.previouslyTerminal = make(map[string]map[string]cachedScriptChildTerminalState)
			}
			if a.previouslyTerminal[j.ID] == nil {
				a.previouslyTerminal[j.ID] = make(map[string]cachedScriptChildTerminalState)
			}
			a.previouslyTerminal[j.ID][childID] = cachedScriptChildTerminalState{
				status: status,
				ok:     childOK,
				errStr: childErr,
			}
		}
	}

	// Skip until all children are terminal.
	if !allTerminal {
		return nil
	}

	// Compute the canonical aggregate (Succeeded or FailedTerminal).
	if err := sm.Compute(); err != nil {
		a.deps.Logger.Error("ScriptParentAggregator: StateMachine.Compute failed",
			zap.String("parent_job_id", j.ID), zap.Error(err))
		return nil
	}

	aggResult := ScriptAggregateResult{
		ParentState:         domainToScriptParentState(sm),
		TotalItems:          len(childIDs),
		SucceededCount:      len(sm.Succeeded()),
		FailedCount:         len(sm.Failed()),
		StateMachineVersion: j.Revision,
		ChildIDs:            childIDs,
	}
	a.finalizeParent(ctx, j.ID, aggResult)
	return nil
}

// finalizeParent builds the result map from the typed aggregate result
// and persists it via jobsSvc.FinalizeAggregateParent with version-based CAS.
// Mirrors voiceover/jobs/parent_aggregator.go::finalizeParent.
func (a *ScriptParentAggregator) finalizeParent(ctx context.Context, parentJobID string, agg ScriptAggregateResult) {
	resultMap := map[string]any{
		"parent_state":        string(agg.ParentState),
		"_aggregator_version": agg.StateMachineVersion,
		"total_items":         agg.TotalItems,
		"succeeded_count":     agg.SucceededCount,
		"failed_count":        agg.FailedCount,
		"child_job_ids":       agg.ChildIDs,
	}

	targetStatus := job.StatusSucceeded
	errMsg := ""
	if agg.ParentState == ScriptParentFailedTerminal {
		targetStatus = job.StatusFailed
		// Marker is canonical per P0 #4 aggregate-flip audit; the test
		// parent_aggregator_test.go::TestFinalizeAggregateParent_AllFailed_PopulatesErrMsg
		// asserts that errMsg contains the literal
		// "script aggregate: all child jobs definitively failed" prefix
		// for audit forensics (the test extracts prefix via substring
		// match — keeps the (P0 #4 narrow-port discipline) suffix free
		// to evolve without breaking the contract).
		errMsg = "script aggregate: all child jobs definitively failed (P0 #4 narrow-port discipline)"
	}

	// Clear the terminal-child cache unconditionally so the next tick
	// starts from scratch if needed (e.g. a concurrent re-aggregation
	// after CAS conflict).
	if a.previouslyTerminal != nil {
		delete(a.previouslyTerminal, parentJobID)
	}

	// Canonical narrow-port call: agg.StateMachineVersion (which IS
	// parent.Revision — aggregateOne set it from j.Revision at tick
	// start) is the 6th argument. The SQL CAS fence in
	// repository_lifecycle.go::FinalizeAggregateParent relies on
	// `revision = ?` matching the row's stored revision. A
	// StateMachine-local counter (e.g. transition count) would
	// mismatch the SQL row's `revision` field on the WHERE clause
	// (CAS fails with ErrAggregateCASConflict on every legitimate
	// call); only the canonical parent.Revision — threaded into
	// agg.StateMachineVersion by aggregateOne from j.Revision —
	// passes the SQL CAS UPDATE. (godlike/07 no-fake-availability /
	// godlike/06 SSOT.)
	//
	// errMsg is constructed separately (init empty above; populated
	// only on ScriptParentFailedTerminal switch above — NOT read
	// from resultMap["error"]).
	if err := a.deps.JobsSvc.FinalizeAggregateParent(ctx, parentJobID, targetStatus, resultMap, errMsg, agg.StateMachineVersion); err != nil {
		if errors.Is(err, domainremote.ErrAlreadyTerminalAggregate) {
			a.deps.Logger.Info("ScriptParentAggregator: parent already finalised (replay no-op)",
				zap.String("parent_job_id", parentJobID),
				zap.String("parent_state", string(agg.ParentState)))
			return
		}
		if errors.Is(err, domainremote.ErrAggregateCASConflict) {
			a.deps.Logger.Warn("ScriptParentAggregator: FinalizeAggregateParent CAS conflict (concurrent tick)",
				zap.String("parent_job_id", parentJobID))
			return
		}
		a.deps.Logger.Warn("ScriptParentAggregator: FinalizeAggregateParent failed",
			zap.String("parent_job_id", parentJobID),
			zap.String("target_status", string(targetStatus)),
			zap.Error(err))
		return
	}
	a.deps.Logger.Info("ScriptParentAggregator: parent state transition",
		zap.String("parent_job_id", parentJobID),
		zap.String("parent_state", string(agg.ParentState)),
		zap.String("target_status", string(targetStatus)),
		zap.Int("revision", agg.StateMachineVersion))
}

// domainToScriptParentState maps the canonical domain 5-state machine
// result to the script-batch 5-state result enum. Succeeded with
// optional failures maps to PartialSuccess so the API response
// distinguishes "all succeeded" from "succeeded with warnings".
// Mirrors voiceover/jobs/parent_aggregator.go::domainToVoiceoverParentState.
func domainToScriptParentState(sm *job.StateMachine) ScriptParentState {
	switch sm.State() {
	case job.ParentStateSucceeded:
		if len(sm.Failed()) > 0 {
			// Some optional children failed — succeeded with warnings.
			return ScriptParentPartialSuccess
		}
		return ScriptParentSucceeded
	case job.ParentStateFailedTerminal:
		return ScriptParentFailedTerminal
	default:
		// Defensive fallback for non-terminal states passed by mistake.
		return ScriptParentWaitingChildren
	}
}

// scriptItemP0_1Gate is the canonical P0 #1-style false-success gate
// EXTENDED to scripts (P0 #4 audit closure). When a child's broker-
// status reads SUCCEEDED but its result map carries `ok: false`,
// the aggregator overrides the child to FAILED.
//
// The handler writes `ok` based on scriptItemIsSuccessful — the
// structural heuristic (Output.Text non-empty OR ScriptID persisted
// OR Cache.Hit). When a child worker emits (resultMap, wrappedErr)
// due to the gate, the broker marks the child FAILED — the gate path
// is the canonical godlike/07 fail-closed mechanism. This is a
// back-up at the aggregator level for handlers that emit (resultMap,
// nil) but structured-emptied result.
func scriptItemP0_1Gate(childJSON []byte) bool {
	if len(childJSON) == 0 {
		return false
	}
	var cr ScriptChildResult
	if err := json.Unmarshal(childJSON, &cr); err != nil {
		return false
	}
	// Aggregate reads ok as *bool to mirror voiceover's explicit-field
	// discipline; present-and-false → override; absent or true → no override.
	if cr.OK == nil {
		return false
	}
	return !*cr.OK
}
