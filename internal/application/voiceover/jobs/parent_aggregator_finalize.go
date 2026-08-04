// Package jobs — parent_aggregator_finalize.go (PR-SPLIT-VO-PARENT-AGG, July 2026).
//
// Owns the per-parent finalize method (finalizeParent). Extracted from
// parent_aggregator.go per godlike/06 SSOT one-canonical-owner-per-fact:
// this file is the SOLE canonical owner of the finalizeParent method
// (per-parent resultMap construction + FinalizeAggregateParent CAS
// dispatch + cache clear + log).
//
// Sibling files in the parent_* family (post-split canonical layout):
//   - parent_aggregator.go (slim orchestrator) — deps + struct + interface
//   - NewParentAggregator + Start + Tick + var _ compile-time pin
//   - isKnownTypedParentState whitelist.
//   - parent_aggregator_aggregate.go (sibling) — aggregateOne (per-parent
//     aggregation loop).
//   - parent_aggregator_finalize.go (this file) — finalizeParent (per-parent
//     persist + log + cache clear).
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
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	domainremote "github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

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
		"stage_progress":        agg.StageProgress,
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
