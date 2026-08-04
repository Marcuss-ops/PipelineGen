// Package jobs — parent_aggregator_aggregate.go (PR-SPLIT-VO-PARENT-AGG, July 2026).
//
// Owns the per-parent aggregation method (aggregateOne). Extracted from
// parent_aggregator.go per godlike/06 SSOT one-canonical-owner-per-fact:
// this file is the SOLE canonical owner of the aggregateOne method
// (the per-parent deserialise → StateMachine.Transition loop →
// VoiceoverAggregateResult construction pipeline).
//
// Sibling files in the parent_* family (post-split canonical layout):
//   - parent_aggregator.go (slim orchestrator) — deps + struct + interface
//   - NewParentAggregator + Start + Tick + var _ compile-time pin
//   - isKnownTypedParentState whitelist.
//   - parent_aggregator_aggregate.go (this file) — aggregateOne (per-parent
//     aggregation loop: deserialise parent result + read-side preference for
//     typed parent_state_typed column + StateMachine.Transition loop with
//     cached-terminal-child short-circuit + VoiceoverAggregateResult
//     construction).
//   - parent_aggregator_finalize.go (sibling) — finalizeParent
//     (per-parent persist + log + cache clear).
//   - parent_aggregator_state.go — JobParentStateColumn constant (P1.2 typed
//     column SSOT) + dual-write contract documentation.
//   - parent_state_machine.go — domainToVoiceoverParentState (the 5-state →
//     4-state wire-shape mapping).
//   - parent_eligibility.go — cached terminal child state helpers (§15.2) +
//     IsParentAwaitingAggregation gate + ZeroChildrenAggregateResult
//     short-circuit.
package jobs

import (
	"context"
	"encoding/json"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
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

	// PR-P1.2-SQL-DUAL-WRITE (July 2026): READ-SIDE PREFERENCE.
	// The typed parent_state_typed column (added by migration 129,
	// populated atomically by repository_lifecycle.go::FinalizeAggregateParent)
	// is the AUTHORITATIVE source going forward. During the BACKFILL
	// window, override the JSON-derived parentResult.ParentState with
	// the typed-column value when it is non-empty AND well-formed.
	// The JSON fallback (parentResult.ParentState) covers pre-P1.2
	// rows + concurrent writes in flight (the typed column is empty
	// until the FinalizeAggregateParent UPDATE commits) + malformed
	// typed values (a writer bug or a backfill-CLI race that wrote
	// an unknown string).
	//
	// godlike/06 SSOT (one canonical owner per fact): the typed
	// column name lives ONLY in
	// internal/application/voiceover/jobs/parent_aggregator_state.go::JobParentStateColumn
	// — the SQL mirror (internal/infrastructure/database/sqlite/jobs/parentStateTypedColumn)
	// is package-private. Both must agree (per the cross-package
	// SSOT discipline; the explicit drift test was DROPPED per
	// godlike/07 minimum-blast-radius — see repository_lifecycle_dualwrite_test.go).
	//
	// godlike/07 no-fake-availability (MUST-FIX #1 from code-reviewer):
	// validate the typed-column value is a KNOWN voiceover.ParentState
	// constant before overriding. An unknown value (e.g. "garbage"
	// from a writer bug) would otherwise cause
	// IsParentAwaitingAggregation to silently return false, the
	// aggregator to silently skip the parent, and no log/counter to
	// surface the issue. The validation is a 4-value whitelist
	// (the canonical voiceover.ParentState value space) — anything
	// outside it falls back to the JSON value with a Warn log so
	// the operator dashboard can alert.
	//
	// godlike/07 NIT #1: when BOTH surfaces are populated AND
	// disagree (e.g. typed="failed", JSON="waiting_children" from a
	// concurrent writer or a stale reader view), the typed column
	// silently wins. This is the correct precedence per the
	// contract, but the disagreement is a diagnostic signal of a
	// racing writer or a backfill-CLI bug. Log a Warn (no error
	// return — the typed column is authoritative).
	//
	// Field-key convention (MUST-FIX #3 from code-reviewer, PR-P1.2-SQL-DUAL-WRITE):
	// the 2 new Warn field keys use `parent_state_typed` and
	// `parent_state_json` to match the existing aggregator log
	// format (`zap.String("parent_state", ...)` in finalizeParent).
	// Operator dashboards can group by field rather than by the
	// legacy ad-hoc `typed_column` / `json_field` / `json_fallback`
	// keys.
	if j.ParentStateTyped != "" {
		if isKnownTypedParentState(j.ParentStateTyped) {
			if parentResult.ParentState != "" && parentResult.ParentState != j.ParentStateTyped {
				// Typed/JSON disagreement — log a Warn, then typed wins.
				a.deps.Logger.Warn("ParentAggregator: typed parent_state_typed and JSON parent_state disagree; typed column wins per BACKFILL contract",
					zap.String("parent_job_id", j.ID),
					zap.String("parent_state_typed", j.ParentStateTyped),
					zap.String("parent_state_json", parentResult.ParentState))
			}
			parentResult.ParentState = j.ParentStateTyped
		} else {
			// Malformed typed column — log Warn + fall back to JSON
			// (the existing parentResult.ParentState from the
			// json.Unmarshal above).
			a.deps.Logger.Warn("ParentAggregator: typed parent_state_typed has unknown value; falling back to JSON resultMap[parent_state]",
				zap.String("parent_job_id", j.ID),
				zap.String("parent_state_typed", j.ParentStateTyped),
				zap.String("parent_state_json", parentResult.ParentState))
		}
	}

	// Step 2: only process parents awaiting aggregation.
	if !IsParentAwaitingAggregation(&parentResult) {
		return nil
	}

	// Step 3: extract child job IDs.
	childIDs := parentResult.ChildJobIDs
	// Filter empty strings (failed-enqueue placeholders), retaining the
	// original child→language relationship for post-aggregation telemetry.
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
	childIDs = filtered
	if len(childIDs) == 0 {
		res := ZeroChildrenAggregateResult()
		// PR-P1.2-SQL-DUAL-WRITE: the typed column is authoritative.
		// If it says a non-waiting non-terminal state (e.g. partial_success),
		// preserve it even in the zero-children short-circuit. Otherwise
		// keep the FASE 4 failed mapping for waiting_children/empty typed.
		if j.ParentStateTyped != "" &&
			j.ParentStateTyped != "waiting_children" &&
			isKnownTypedParentState(j.ParentStateTyped) {
			res.ParentState = voiceover.ParentState(j.ParentStateTyped)
		}
		a.finalizeParent(ctx, j.ID, res)
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

	stageStatuses := make([]job.StageLanguageStatus, 0, len(childIDs))
	for i, childID := range childIDs {
		language := childLanguages[childID]
		if language == "" && i < len(parentResult.PerLanguage) {
			// Legacy parents may not have aligned per-language telemetry;
			// retain the old positional fallback only when no explicit
			// child mapping is available.
			language = parentResult.PerLanguage[i]
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

	aggResult := VoiceoverAggregateResult{
		ParentState:         newPS,
		TotalChildren:       len(childIDs),
		SucceededCount:      len(sm.Succeeded()),
		FailedCount:         len(sm.Failed()),
		RequiredFailedCount: requiredFailed,
		StateMachineVersion: j.Revision,
		ChildIDs:            childIDs,
		StageProgress:       job.AggregateStageProgressByStage(stageStatuses),
	}
	a.finalizeParent(ctx, j.ID, aggResult)
	return nil
}
