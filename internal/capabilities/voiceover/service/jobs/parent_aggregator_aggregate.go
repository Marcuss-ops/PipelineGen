// Package jobs — parent_aggregator_aggregate.go (PR-SPLIT-VO-PARENT-AGG, July 2026).
//
// Owns the per-parent aggregation method (aggregateOne). Extracted from
// parent_aggregator.go per godlike/06 SSOT one-canonical-owner-per-fact:
// this file is the SOLE canonical owner of the aggregateOne orchestration
// (decode → reconcile → eligibility → children → aggregate → finalize).
//
// Sibling files in the parent_* family (post-split canonical layout):
//   - parent_aggregator.go (slim orchestrator) — deps + struct + interface
//   - NewParentAggregator + Start + Tick + var _ compile-time pin
//   - isKnownTypedParentState whitelist.
//   - parent_aggregator_aggregate.go (this file) — aggregateOne (per-parent
//     orchestration) + decodeParentResult + eligibleChildIDs +
//     loadChildOutcomes + buildAggregateResult.
//   - parent_aggregator_finalize.go (sibling) — finalizeParent
//     (per-parent persist + log + cache clear).
//   - parent_aggregator_state.go — JobParentStateColumn constant (P1.2 typed
//     column SSOT) + reconcileParentState + dual-write contract documentation.
//   - parent_state_machine.go — domainToVoiceoverParentState (the 5-state →
//     4-state wire-shape mapping).
//   - parent_eligibility.go — cached terminal child state helpers (§15.2) +
//     IsParentAwaitingAggregation gate + ZeroChildrenAggregateResult
//     short-circuit.
package jobs

import (
	"context"
	"encoding/json"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// aggregateOne processes a single parent job: deserializes into typed
// VoiceoverParentResult, reads child outcomes via typed VoiceoverChildResult,
// computes the aggregate via domain StateMachine, and finalizes the parent
// via finalizeParent with version-based CAS.
//
// FASE 2 (July 2026): all internal map[string]any access replaced with
// typed DTOs (VoiceoverParentResult, VoiceoverChildResult,
// VoiceoverAggregateResult). The broker boundary (FinalizeAggregateParent) still
// accepts map[string]any for wire-shape back-compat.
//
// GOP-11 (November 2026): the body is now the orchestration only. The
// typed/JSON reconciliation lives in parent_aggregator_state.go, the child
// loading loop in loadChildOutcomes and the result construction in
// buildAggregateResult. Behaviour (including the §15.2 cache contract and
// the version-CAS source) is unchanged.
func (a *ParentAggregator) aggregateOne(ctx context.Context, j job.Job) error {
	parentResult, ok := decodeParentResult(a.deps.Logger, j)
	if !ok {
		return nil
	}

	reconcileParentState(a.deps.Logger, j, &parentResult)

	// Step 2: only process parents awaiting aggregation.
	if !IsParentAwaitingAggregation(&parentResult) {
		return nil
	}

	// Step 3: extract child job IDs and their languages.
	childIDs, childLanguages := eligibleChildIDs(&parentResult)
	if len(childIDs) == 0 {
		a.finalizeZeroChildren(ctx, j)
		return nil
	}

	// Step 4: construct domain StateMachine + explicit TransitionToWaitingChildren.
	sm := job.NewStateMachine(j.ID, len(childIDs))
	if err := sm.TransitionToWaitingChildren(childIDs); err != nil {
		a.deps.Logger.Debug("ParentAggregator: TransitionToWaitingChildren rejected",
			zap.String("parent_job_id", j.ID), zap.Error(err))
		return nil
	}

	allTerminal, requiredFailed, childStageProgress := a.loadChildOutcomes(ctx, j, sm, childIDs)

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
	aggResult := buildAggregateResult(j, sm, childIDs, childLanguages,
		parentResult.PerLanguage, requiredFailed, childStageProgress, newPS)
	a.finalizeParent(ctx, j.ID, aggResult)
	return nil
}

// decodeParentResult unmarshals the parent job's result. A malformed payload
// is logged at Debug and skipped (the parent is retried by the next Tick, as
// before); an empty result decodes to the zero DTO.
func decodeParentResult(log *zap.Logger, j job.Job) (VoiceoverParentResult, bool) {
	var parentResult VoiceoverParentResult
	if len(j.Result) > 0 {
		if err := json.Unmarshal(j.Result, &parentResult); err != nil {
			log.Debug("ParentAggregator: cannot unmarshal parent result, skipping",
				zap.String("parent_job_id", j.ID), zap.Error(err))
			return VoiceoverParentResult{}, false
		}
	}
	return parentResult, true
}

// eligibleChildIDs filters the failed-enqueue placeholders (empty strings)
// from the parent's child list, retaining the original child→language
// relationship for post-aggregation telemetry.
func eligibleChildIDs(parentResult *VoiceoverParentResult) ([]string, map[string]string) {
	childIDs := parentResult.ChildJobIDs
	childLanguages := make(map[string]string, len(childIDs))
	filtered := make([]string, 0, len(childIDs))
	for index, id := range childIDs {
		if id == "" {
			continue
		}
		filtered = append(filtered, id)
		if index < len(parentResult.PerLanguage) {
			childLanguages[id] = parentResult.PerLanguage[index]
		}
	}
	return filtered, childLanguages
}

// finalizeZeroChildren writes the canonical zero-children aggregate.
//
// PR-P1.2-SQL-DUAL-WRITE: the typed column is authoritative. If it says a
// non-waiting non-terminal state (e.g. partial_success), preserve it even in
// the zero-children short-circuit. Otherwise keep the FASE 4 failed mapping
// for waiting_children/empty typed.
func (a *ParentAggregator) finalizeZeroChildren(ctx context.Context, j job.Job) {
	res := ZeroChildrenAggregateResult()
	if j.ParentStateTyped != "" &&
		j.ParentStateTyped != "waiting_children" &&
		isKnownTypedParentState(j.ParentStateTyped) {
		res.ParentState = voiceover.ParentState(j.ParentStateTyped)
	}
	a.finalizeParent(ctx, j.ID, res)
}

// loadChildOutcomes reads every child's terminal state (cache first), feeds
// the StateMachine, and returns whether every child is terminal, the
// required-failed count and the merged child stage progress.
//
// §15.2 (July 2026): children that were terminal on the previous tick are fed
// directly to the StateMachine without a broker round-trip. Only truly
// terminal children are cached; the Required flag from the child payload is
// preserved so REQUIRED-failed children are not downgraded on retry ticks.
func (a *ParentAggregator) loadChildOutcomes(ctx context.Context, j job.Job, sm *job.StateMachine, childIDs []string) (bool, int, map[string]job.StageProgress) {
	allTerminal := true
	requiredFailed := 0
	childStageProgress := make(map[string]job.StageProgress)

	for _, childID := range childIDs {
		var childResult VoiceoverChildResult
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
			if childJob == nil {
				a.deps.Logger.Warn("ParentAggregator: child job not found (nil)",
					zap.String("parent_job_id", j.ID),
					zap.String("child_job_id", childID))
				allTerminal = false
				continue
			}
			status = childJob.Status
			if status == job.StatusQueued || status == job.StatusLeased || status == job.StatusRunning || status == job.StatusFinalizing || status == job.StatusRetryWait {
				allTerminal = false
			}

			// P0.1 gate: typed child result.
			if len(childJob.Result) > 0 {
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

		if len(childResult.StageProgress) > 0 {
			childStageProgress = job.MergeStageProgress(childStageProgress, childResult.StageProgress)
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

	return allTerminal, requiredFailed, childStageProgress
}

// buildAggregateResult constructs the typed VoiceoverAggregateResult from the
// StateMachine outcome, preserving the per-language telemetry mapping and the
// legacy positional fallback for parents without aligned per-language data.
func buildAggregateResult(
	j job.Job,
	sm *job.StateMachine,
	childIDs []string,
	childLanguages map[string]string,
	perLanguage []string,
	requiredFailed int,
	childStageProgress map[string]job.StageProgress,
	newPS voiceover.ParentState,
) VoiceoverAggregateResult {
	stageStatuses := make([]job.StageLanguageStatus, 0, len(childIDs))
	for i, childID := range childIDs {
		language := childLanguages[childID]
		if language == "" && i < len(perLanguage) {
			// Legacy parents may not have aligned per-language telemetry;
			// retain the old positional fallback only when no explicit
			// child mapping is available.
			language = perLanguage[i]
		}
		status := job.StageFailed
		for _, succeededID := range sm.Succeeded() {
			if succeededID == childID {
				status = job.StageCompleted
				break
			}
		}
		stageStatuses = append(stageStatuses, job.StageLanguageStatus{
			Stage: job.StageVoiceover, Language: language, Status: status, JobID: childID,
		})
	}

	return VoiceoverAggregateResult{
		ParentState:         newPS,
		TotalChildren:       len(childIDs),
		SucceededCount:      len(sm.Succeeded()),
		FailedCount:         len(sm.Failed()),
		RequiredFailedCount: requiredFailed,
		StateMachineVersion: j.Revision,
		ChildIDs:            childIDs,
		StageProgress:       job.MergeStageProgress(job.AggregateStageProgressByStage(stageStatuses), childStageProgress),
	}
}
