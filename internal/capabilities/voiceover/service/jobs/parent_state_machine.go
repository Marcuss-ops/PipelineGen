// Package jobs — parent_state_machine.go
// (PR-VO-PARENT-AGGREGATOR-SPLIT, P0 #4 in VO-DECOMPOSITION-2026-07-04, deadline 2026-08-01).
//
// parent_state_machine.go is the SINGLE canonical owner of the
// domain 5-state machine → voiceover 4-state enum wire-shape
// mapping per godlike/06 SSOT (one owner per fact). It is the
// thin-mechanical extraction of the domainToVoiceoverParentState
// function from parent_aggregator.go.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - this file owns: domainToVoiceoverParentState (the mapping
//     function). No new enum is created here — the "Phase" term
//     in the action plan refers to the existing voiceover.ParentState
//     enum (defined in internal/application/voiceover/parent_state.go).
//   - voiceover.ParentState lives in internal/application/voiceover/parent_state.go
//     (STAYS THERE; this file is the consumer, not the owner).
//   - domain job.ParentState lives in internal/domain/job/state_machine.go
//     (STAYS THERE; this file is the consumer, not the owner).
//   - parent_aggregator.go owns the orchestrator (Tick, aggregateOne,
//     finalizeParent).
//
// godlike/07 minimal-blast-radius: pure code-motion. The mapping
// logic is preserved VERBATIM:
//   - domain.ParentStateSucceeded + zero failures → voiceover.ParentSucceeded
//   - domain.ParentStateSucceeded + ≥1 failure   → voiceover.ParentPartialSuccess
//   - domain.ParentStateFailedTerminal          → voiceover.ParentFailed
//   - non-terminal state (defensive fallback)   → voiceover.ParentPartialSuccess
package jobs

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
) // domainToVoiceoverParentState maps the domain 5-state machine result
// to the voiceover 4-state result enum for wire-shape back-compat.
// Succeeded with optional failures maps to partial_success so the
// API response distinguishes "all succeeded" from "succeeded with
// warnings".
//
// PR-VO-PARENT-AGGREGATOR-SPLIT (P0 #4): the mapping was previously
// inline at the bottom of parent_aggregator.go; extracted here so
// the state-machine integration is testable in isolation and so
// the parent_aggregator.go orchestrator stays thin.
//
// Mapping contract (preserved VERBATIM from the pre-PR version):
//   - domain.ParentStateSucceeded:
//   - if zero failures → voiceover.ParentSucceeded
//   - if ≥1 failures  → voiceover.ParentPartialSuccess
//     (succeeded-with-warnings semantics per FASE 2 wire shape)
//   - domain.ParentStateFailedTerminal:
//   - voiceover.ParentFailed
//   - any other (non-terminal) state:
//   - defensive fallback to voiceover.ParentPartialSuccess
//     (the aggregator only calls this AFTER sm.Compute() has
//     returned successfully, so a non-terminal state is a
//     defensive default, not a normal path).
//
// "Phase enum" interpretation (action plan terminology): the
// action plan refers to "parent_state_machine.go (Phase enum)".
// This file is the SINGLE canonical owner of the state-machine
// MAPPING (the function), NOT a new "Phase" typed enum. The
// existing voiceover.ParentState (4-value typed enum in
// internal/application/voiceover/parent_state.go) is the "Phase"
// the action plan refers to. The mapping function is the bridge
// between the domain 5-state machine (job.ParentState) and the
// voiceover 4-state wire enum (voiceover.ParentState). The enums
// themselves live in their SSOT locations (NOT here) per
// godlike/06 one-canonical-owner-per-fact.
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
