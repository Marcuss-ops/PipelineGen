// Package jobs — parent_aggregator_flow.go owns the per-parent aggregation
// flow: claim one waiting parent, decode its result, merge child terminal
// states, and finalize when the aggregate dictates. It was extracted from
// parent_aggregator.go to keep that file inside the godlike/08
// max_lines_per_file_strict cap (600); the move is behaviour-preserving.
package jobs

import (
	"context"
	"encoding/json"
	"errors"

	domainremote "github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

func (a *ScriptParentAggregator) aggregateOne(ctx context.Context, j job.Job) error {
	// Decode the typed parent result.
	var parentResult ScriptParentResult
	if len(j.Result) > 0 {
		if err := decodeScriptParentResult(j.Result, &parentResult); err != nil {
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
		if id == "" {
			continue
		}
		filtered = append(filtered, id)
	}
	childIDs = filtered

	// Edge case: zero children enqueued → FAILED terminal immediately.
	//
	// FASE 4 (July 2026) spec close-out: zero children created =
	// FAILED terminal (not partial_success). The pre-FASE-4 mapping
	// (ScriptParentPartialSuccess) conflated dispatch-failure (zero
	// enqueued) with partial-success (mixed terminal) — two
	// semantically distinct states. The pre-FASE-4 mapping was a
	// false-positive terminal leak that masked dispatch failures
	// in the operator dashboard. The canonical terminal for "no
	// children enqueued" is ScriptParentFailedTerminal (per the
	// 5-state machine; the constant is already declared in this
	// file at line 138-139).
	if len(childIDs) == 0 {
		a.finalizeParent(ctx, j.ID, ScriptAggregateResult{
			ParentState:    ScriptParentFailedTerminal,
			TotalItems:     0,
			ParentRevision: j.Revision,
		})
		return nil
	}

	// Query each child and count terminal outcomes. Keep one progress slot
	// per requested item so failed-enqueue placeholders cannot shift the
	// language associated with a later child.
	succeeded := 0
	failed := parentResult.FailedEnqueue
	allTerminal := true
	stageStatuses := make([]job.StageLanguageStatus, len(parentResult.ChildJobIDs))
	childStageIndex := make(map[string]int, len(childIDs))
	for index, childID := range parentResult.ChildJobIDs {
		language := ""
		if index < len(parentResult.PerLanguage) {
			language = parentResult.PerLanguage[index]
		}
		status := job.StageQueued
		if childID == "" {
			status = job.StageFailed
		}
		stageStatuses[index] = job.StageLanguageStatus{
			Stage: job.StageScript, Language: language, Status: status, JobID: childID,
		}
		if childID != "" {
			childStageIndex[childID] = index
		}
	}
	childStageProgress := make(map[string]job.StageProgress)
	for _, childID := range childIDs {
		var status job.Status
		var childOK bool
		var childResult ScriptChildResult

		childJob, err := a.deps.JobsSvc.Get(ctx, childID)
		if err != nil {
			a.deps.Logger.Warn("ScriptParentAggregator: Get child failed",
				zap.String("parent_job_id", j.ID),
				zap.String("child_job_id", childID),
				zap.Error(err))
			allTerminal = false
			continue
		}
		if childJob == nil {
			a.deps.Logger.Warn("ScriptParentAggregator: child job missing",
				zap.String("parent_job_id", j.ID),
				zap.String("child_job_id", childID))
			allTerminal = false
			continue
		}
		status = childJob.Status

		// P0.1-gate: broker SUCCEEDED + result.ok=false → FAILED.
		if status == job.StatusSucceeded {
			if isOKFalseOverride := scriptItemP0_1Gate(childJob.Result); isOKFalseOverride {
				a.deps.Logger.Warn("ScriptParentAggregator: child broker-succeeded but result.ok=false",
					zap.String("parent_job_id", j.ID),
					zap.String("child_job_id", childID))
				status = job.StatusFailed
			}
		}
		switch status {
		case job.StatusFailed, job.StatusCancelled:
			if len(childJob.Result) > 0 {
				if err := json.Unmarshal(childJob.Result, &childResult); err == nil {
					if childResult.OK != nil {
						childOK = *childResult.OK
					}
				}
			}
		case job.StatusSucceeded:
			if len(childJob.Result) > 0 {
				if err := json.Unmarshal(childJob.Result, &childResult); err == nil {
					if childResult.OK != nil {
						childOK = *childResult.OK
					}
				}
			}
		}

		// Non-terminal children → parent stays open (re-tick).
		if !status.IsTerminal() {
			allTerminal = false
			continue
		}

		// All script-batch children are OPTIONAL. Count terminal outcome.
		if stageIndex, ok := childStageIndex[childID]; ok {
			stageStatuses[stageIndex].Status = job.StageFailed
			if status == job.StatusSucceeded && childOK {
				stageStatuses[stageIndex].Status = job.StageCompleted
			}
		}
		if len(childResult.StageProgress) > 0 {
			childStageProgress = job.MergeStageProgress(childStageProgress, childResult.StageProgress)
		}
		if (status == job.StatusSucceeded) && childOK {
			succeeded++
		} else {
			failed++
		}
	}

	// Skip until all children are terminal.
	if !allTerminal {
		return nil
	}

	// Compute total items and derive the aggregate parent state.
	totalItems := parentResult.TotalItems
	if totalItems <= 0 {
		totalItems = len(childIDs) + parentResult.FailedEnqueue
	}

	var state ScriptParentState
	switch {
	case failed == totalItems && totalItems > 0:
		state = ScriptParentFailedTerminal
	case failed > 0:
		state = ScriptParentPartialSuccess
	default:
		state = ScriptParentSucceeded
	}

	aggResult := ScriptAggregateResult{
		ParentState:    state,
		TotalItems:     totalItems,
		SucceededCount: succeeded,
		FailedCount:    failed,
		ParentRevision: j.Revision,
		ChildIDs:       childIDs,
		PerLanguage:    parentResult.PerLanguage,
		StageProgress:  job.MergeStageProgress(job.AggregateStageProgressByStage(stageStatuses), childStageProgress),
	}
	a.finalizeParent(ctx, j.ID, aggResult)
	return nil
}

// decodeScriptParentResult accepts both the legacy root result shape and the
// canonical completion envelope shape ({"data":{...}}). The worker stores
// completed handler results through the typed envelope, while older rows and
// hermetic tests still use the root map directly.
func decodeScriptParentResult(raw []byte, out *ScriptParentResult) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	if out.ParentState != "" || out.ParentJobID != "" || len(out.ChildJobIDs) > 0 {
		return nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Data) == 0 {
		return err
	}
	return json.Unmarshal(envelope.Data, out)
}

// finalizeParent builds the result map from the typed aggregate result
// and persists it via jobsSvc.FinalizeAggregateParent with version-based CAS.
// Mirrors voiceover/jobs/parent_aggregator.go::finalizeParent.
func (a *ScriptParentAggregator) finalizeParent(ctx context.Context, parentJobID string, agg ScriptAggregateResult) {
	resultMap := map[string]any{
		"parent_state":        string(agg.ParentState),
		"_aggregator_version": agg.ParentRevision,
		"total_items":         agg.TotalItems,
		"succeeded_count":     agg.SucceededCount,
		"failed_count":        agg.FailedCount,
		"child_job_ids":       agg.ChildIDs,
		"per_language":        agg.PerLanguage,
		"stage_progress":      agg.StageProgress,
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

	if err := a.deps.JobsSvc.FinalizeAggregateParent(ctx, parentJobID, targetStatus, resultMap, errMsg, agg.ParentRevision); err != nil {
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
		zap.Int("revision", agg.ParentRevision))
	if a.deps.RunRepo != nil {
		if targetStatus == job.StatusFailed {
			runID := runIDForJob(ctx, a.deps.RunRepo, parentJobID)
			noteRunLedgerFailure(a.deps.Logger, "fail_run", runID, a.deps.RunRepo.FailRun(ctx, scriptgen.FailRunInput{
				RunID:        runID,
				FailedStage:  scriptgen.StageFailed,
				ErrorCode:    "SCRIPT_BATCH_FAILED",
				ErrorMessage: errMsg,
			}))
		} else if run, lookupErr := a.deps.RunRepo.GetByJobID(ctx, parentJobID); lookupErr == nil && run != nil {
			// The terminal projection for a batch parent. A dropped write here
			// leaves the run the aggregator just declared COMPLETED persisted as
			// RUNNING, so it is surfaced instead of discarded.
			noteRunLedgerFailure(a.deps.Logger, "stage_completed", run.ID,
				a.deps.RunRepo.UpdateStage(ctx, run.ID, scriptgen.RunStatusCompleted, scriptgen.StageCompleted))
		}
	}
}

// runIDForJob resolves the run a job produced. An empty result is returned for
// BOTH "no run" and "the lookup itself failed", which is why the caller passes
// it to noteRunLedgerFailure: a terminal stage write addressed to "" cannot
// land, and the dropped write must not be mistaken for a run that needed no
// update.
func runIDForJob(ctx context.Context, repo scriptgen.RunRepository, jobID string) string {
	if run, err := repo.GetByJobID(ctx, jobID); err == nil && run != nil {
		return run.ID
	}
	return ""
}

// scriptItemP0_1Gate overrides a child to FAILED when broker status
// is SUCCEEDED but result.ok=false (false-success gate).
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
