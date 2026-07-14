// Package job — parent state aliases (PR-KERNEL-JOB-POPULATE, July 2026).
//
// The canonical parent-state types now live in internal/kernel/job.
// This file re-exports them as transparent aliases so existing
// callers that import internal/domain/job continue to compile
// during the Wave 5 contraction window.
package job

import (
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Parent state types ───────────────────────────────────────────────

type (
	// ParentState is the canonical typed parent-job lifecycle state.
	ParentState = kerneljob.ParentState

	// ChildOutcome describes the terminal outcome of a single child.
	ChildOutcome = kerneljob.ChildOutcome

	// ChildTerminatedEvent is the canonical outbox event payload.
	ChildTerminatedEvent = kerneljob.ChildTerminatedEvent

	// StateMachine is the typed validator for parent-job lifecycle.
	StateMachine = kerneljob.StateMachine

	// StateSnapshot is the durable projection of a StateMachine.
	StateSnapshot = kerneljob.StateSnapshot
)

// ParentState constants.
const (
	ParentStateDispatching     = kerneljob.ParentStateDispatching
	ParentStateWaitingChildren = kerneljob.ParentStateWaitingChildren
	ParentStateAggregating     = kerneljob.ParentStateAggregating
	ParentStateSucceeded       = kerneljob.ParentStateSucceeded
	ParentStateFailedTerminal  = kerneljob.ParentStateFailedTerminal
)

// Parent state sentinel errors.
var (
	ErrInvalidTransition     = kerneljob.ErrInvalidTransition
	ErrAlreadyTerminal       = kerneljob.ErrAlreadyTerminal
	ErrExpectedChildrenUnset = kerneljob.ErrExpectedChildrenUnset
	ErrDuplicateChildEvent   = kerneljob.ErrDuplicateChildEvent
)

// NewStateMachine constructs a fresh state machine.
var NewStateMachine = kerneljob.NewStateMachine
