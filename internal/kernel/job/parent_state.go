// Package job — parent_state.go (canonical 5-state machine, Step 12B-C1/5, July 2026).
//
// Promotes the typed ParentState enum + state-machine validator from the
// application-layer result field (internal/capabilities/voiceover/parent_state.go,
// added by PR-VO-AUDIT-P05 micro-commit #4 of 8 in June 2026) to the canonical
// domain surface (this file). The 4-value result enum in voiceover pkg remains
// for wire-shape back-compat until C3 wire-up propagates the canonical names
// into job.Result["parent_state"].
//
// State machine transitions (canonical per user-spec, Step 12B):
//
//	Dispatching       + first child SUCCEEDED                → WaitingChildren
//	Dispatching       + REQUIRED child FAILED at dispatch    → FailedTerminal
//	WaitingChildren   + last child terminal                  → Aggregating
//	WaitingChildren   + REQUIRED child FAILED (with pending) → FailedTerminal
//	Aggregating       + Compute() with all SUCCEEDED         → Succeeded
//	Aggregating       + Compute() with all REQUIRED succeeded + optional-only failures → Succeeded
//	Succeeded/FailedTerminal + any                            → ErrAlreadyTerminal
//
// Idempotency: a duplicate ChildTerminatedEvent for an already-seen childJobID
// returns ErrDuplicateChildEvent without state mutation — proper outbox replay
// protection (pipeline can replay events without matrix-corrupting transitions).
package job

import (
	"errors"
	"fmt"
)

// ParentState is the canonical typed parent-job lifecycle state.
// 5 named values per user-spec; wire value is lower-snake string.
type ParentState string

const (
	// ParentStateDispatching — initial state. Parent job enqueued but children
	// not yet dispatched (or enqueue in progress). Accepts the first
	// child_terminated event and transitions to WaitingChildren (or
	// FailedTerminal if a REQUIRED child already failed at dispatch).
	ParentStateDispatching ParentState = "dispatching"

	// ParentStateWaitingChildren — all children dispatched, at least one child
	// still in flight. Receives child_terminated events; aggregates
	// (terminated, succeeded, failed) counts. Last child terminal event
	// triggers Aggregating.
	ParentStateWaitingChildren ParentState = "waiting_children"

	// ParentStateAggregating — all children terminal; aggregator evaluating
	// final outcome. Compute() finalizes the transition to Succeeded or
	// FailedTerminal. Idempotent Compute() calls are safe.
	ParentStateAggregating ParentState = "aggregating"

	// ParentStateSucceeded — terminal success. ALL children SUCCEEDED. No
	// further transitions accepted.
	ParentStateSucceeded ParentState = "succeeded"

	// ParentStateFailedTerminal — terminal failure. At least one REQUIRED child
	// FAILED or could not be enqueued. No further transitions accepted.
	ParentStateFailedTerminal ParentState = "failed_terminal"
)

// IsTerminal reports whether the state has no allowed outgoing transitions.
// Used by callers to short-circuit outbox event handling.
func (s ParentState) IsTerminal() bool {
	return s == ParentStateSucceeded || s == ParentStateFailedTerminal
}

// IsValid reports whether s is one of the canonical 5 named states.
// Strictly used by deserializers/parsers to reject legacy wire values
// or hand-crafted strings.
func (s ParentState) IsValid() bool {
	switch s {
	case ParentStateDispatching,
		ParentStateWaitingChildren,
		ParentStateAggregating,
		ParentStateSucceeded,
		ParentStateFailedTerminal:
		return true
	}
	return false
}

// Sentinel errors used by Transition + Compute.
var (
	// ErrInvalidTransition is returned when a state-machine event arrives in
	// an unsupported state (e.g. child event during Aggregating, Compute()
	// call from a non-Aggregating state).
	ErrInvalidTransition = errors.New("parent_state: invalid transition")

	// ErrAlreadyTerminal is returned when an event arrives after the parent
	// has reached a terminal state. Noisy for monitoring: indicates either
	// outbox replay-after-terminal or a child event arriving too late.
	ErrAlreadyTerminal = errors.New("parent_state: already in terminal state")

	// ErrExpectedChildrenUnset is returned by NewStateMachine when
	// expectedChildren <= 0. A parent with no children cannot use the
	// state-machine surface directly — caller should aggregate inline.
	ErrExpectedChildrenUnset = errors.New("parent_state: expected_children must be > 0")

	// ErrDuplicateChildEvent is returned when the same child JobID arrives
	// twice. Outbox-driven semantics make this idempotent protection
	// essential; aggregator swallows the error and logs.
	ErrDuplicateChildEvent = errors.New("parent_state: duplicate child_terminated event")
)

// ChildOutcome describes the terminal outcome of a single child for the
// parent aggregator. Required=true means the parent terminal state depends
// on this child — a REQUIRED-failed propagates to FailedTerminal even
// among many successes.
type ChildOutcome struct {
	JobID     string `json:"job_id"`
	Succeeded bool   `json:"succeeded"`
	Required  bool   `json:"required"`
	Error     string `json:"error,omitempty"`
	// Status is the broker-side terminal status (job.StatusSucceeded or
	// job.StatusFailed or job.StatusCancelled). Useful for forensic
	// logging when surfaced to the result map.
	Status string `json:"status"`
}

// ChildTerminatedEvent is the canonical outbox event payload that drives
// the outbox.Dispatcher.EnqueueChildTerminated → aggregator.Handle path.
// One event per child terminal transition. Aggregator consumes
// idempotently (replay protection: ErrDuplicateChildEvent on repeat).
type ChildTerminatedEvent struct {
	ParentJobID string       `json:"parent_job_id"`
	ChildJobID  string       `json:"child_job_id"`
	Outcome     ChildOutcome `json:"outcome"`
	// EmittedAt is the time the outbox event was enqueued (used as a
	// tie-breaker on out-of-order arrival when the same child emits
	// multiple events — typically only once, but defensive).
	EmittedAt string `json:"emitted_at,omitempty"`
}

// StateMachine is the typed validator for parent-job lifecycle. One
// instance per parent in aggregator memory; the durable projection is
// the (state, expected_children, terminated_children, version) tuple in
// parent_aggregator_state SQLite table (Step 12B-C2 migration
// 121_parent_aggregator_state.sql).
//
// StateMachine is NOT thread-safe; the aggregator owns access serialisation
// (single-threaded outbox handler in C2).
type StateMachine struct {
	parentJobID        string
	state              ParentState
	expectedChildren   int
	terminatedChildren int
	succeeded          []string
	failed             []string
	childIDs           []string
	version            int
	seen               map[string]struct{}
}

// NewStateMachine constructs a fresh state machine. expectedChildren
// must be ≥1 (a parent with zero children should call Compute() directly
// after enqueueing, not use the state machine). Panics on nil usage
// in composition (fail-fast per AGENTS.md WireUp pattern).
//
// succeeded/failed slices are initialised to []string{} (not nil) so
// Failed() / Succeeded() return non-nil empty slices; callers and tests
// can rely on `len(...) == 0` AND `assert.Equal([]string{}, ...)` both
// being stable (Go testify distinguishes nil slice from empty slice).
func NewStateMachine(parentJobID string, expectedChildren int) *StateMachine {
	if expectedChildren <= 0 {
		panic(fmt.Errorf("domain.job.NewStateMachine: %w (parent=%s)", ErrExpectedChildrenUnset, parentJobID))
	}
	return &StateMachine{
		parentJobID:      parentJobID,
		state:            ParentStateDispatching,
		expectedChildren: expectedChildren,
		succeeded:        []string{},
		failed:           []string{},
		seen:             make(map[string]struct{}, expectedChildren),
	}
}

// State returns the current state (read-only).
func (sm *StateMachine) State() ParentState { return sm.state }

// Terminated returns the count of children that have reached terminal.
func (sm *StateMachine) Terminated() int { return sm.terminatedChildren }

// Expected returns the expected total child count.
func (sm *StateMachine) Expected() int { return sm.expectedChildren }

// Version returns the monotonic counter (incremented on each successful
// Transition). Used by the durable parent_aggregator_state projection for
// optimistic CAS updates (C2 migration + repository).
func (sm *StateMachine) Version() int { return sm.version }

// Succeeded returns the JobIDs of children that reached SUCCEEDED.
func (sm *StateMachine) Succeeded() []string { return sm.succeeded }

// Failed returns the JobIDs of children that reached FAILED.
func (sm *StateMachine) Failed() []string { return sm.failed }

// TransitionToWaitingChildren explicitly transitions the parent from
// Dispatching to WaitingChildren. Called after fan-out completes and
// all child jobs are enqueued, BEFORE any child-terminated events
// arrive. Records the child IDs for later cross-reference.
//
// FASE 1 (July 2026): this method makes the fan-out→WaitingChildren
// transition explicit, replacing the implicit "first child event
// triggers Dispatching→WaitingChildren" path. The Transition() method
// still handles the implicit path as a backward-compatible fallback.
//
// Errors:
//   - ErrAlreadyTerminal: state is already Succeeded or FailedTerminal
//   - ErrInvalidTransition: state is not Dispatching (e.g. already
//     WaitingChildren or Aggregating — a previous tick already
//     constructed and fed this parent)
func (sm *StateMachine) TransitionToWaitingChildren(childIDs []string) error {
	if sm.state.IsTerminal() {
		return fmt.Errorf("%w: parent=%s state=%s", ErrAlreadyTerminal, sm.parentJobID, sm.state)
	}
	if sm.state != ParentStateDispatching {
		return fmt.Errorf("%w: parent=%s state=%s (expected dispatching)", ErrInvalidTransition, sm.parentJobID, sm.state)
	}
	if len(childIDs) == 0 {
		return fmt.Errorf("%w: parent=%s childIDs is empty (expected at least 1 child)", ErrInvalidTransition, sm.parentJobID)
	}
	sm.state = ParentStateWaitingChildren
	sm.childIDs = append([]string{}, childIDs...)
	sm.version++
	return nil
}

// ChildIDs returns the child job IDs recorded at TransitionToWaitingChildren.
// Returns nil if TransitionToWaitingChildren was never called (implicit
// Dispatching→WaitingChildren via first child event).
func (sm *StateMachine) ChildIDs() []string { return sm.childIDs }

// Transition applies a single child_terminated event. Idempotent on duplicate
// child JobID (returns ErrDuplicateChildEvent). Idempotent on terminal state
// (returns ErrAlreadyTerminal).
//
// FASE 1 (July 2026): Dispatching→WaitingChildren can now happen via
// TransitionToWaitingChildren() (explicit) or via the first child event
// (implicit, backward-compatible fallback). Rule ② below only fires when
// the state is still Dispatching — if TransitionToWaitingChildren has
// already moved to WaitingChildren, it's a no-op.
//
// Transition table (canonical per user-spec, Step 12B), re-evaluated
// uniformly on every event regardless of prior state:
//
//	① REQUIRED child failed (any state)        → FailedTerminal
//	② Dispatching → WaitingChildren (1st event, if still Dispatching)
//	③ WaitingChildren → last expected child   → Aggregating
//	④ Aggregating → event received             → ErrInvalidTransition
//	⑤ Succeeded/FailedTerminal + any event     → ErrAlreadyTerminal
func (sm *StateMachine) Transition(ev ChildTerminatedEvent) error {
	if sm.state.IsTerminal() {
		return fmt.Errorf("%w: parent=%s state=%s", ErrAlreadyTerminal, sm.parentJobID, sm.state)
	}
	if _, dup := sm.seen[ev.ChildJobID]; dup {
		return fmt.Errorf("%w: parent=%s child=%s", ErrDuplicateChildEvent, sm.parentJobID, ev.ChildJobID)
	}
	sm.seen[ev.ChildJobID] = struct{}{}
	sm.terminatedChildren++
	sm.version++

	if ev.Outcome.Succeeded {
		sm.succeeded = append(sm.succeeded, ev.ChildJobID)
	} else {
		sm.failed = append(sm.failed, ev.ChildJobID)
	}

	// ① REQUIRED-failed short-circuit applies uniformly (any non-terminal state).
	if ev.Outcome.Required && !ev.Outcome.Succeeded {
		sm.state = ParentStateFailedTerminal
		return nil
	}

	// ② Dispatching → WaitingChildren on first event.
	if sm.state == ParentStateDispatching {
		sm.state = ParentStateWaitingChildren
	}

	// ③ WaitingChildren → Aggregating when last expected child has terminated.
	//    And ④ Aggregating → any further event is malformed (Compute is canonical).
	if sm.state == ParentStateWaitingChildren {
		if sm.terminatedChildren >= sm.expectedChildren {
			sm.state = ParentStateAggregating
		}
		return nil
	}
	if sm.state == ParentStateAggregating {
		return fmt.Errorf("%w: parent=%s state=aggregating (call Compute)", ErrInvalidTransition, sm.parentJobID)
	}
	return nil
}

// Compute finalizes the Aggregating → terminal transition. Idempotent
// on terminal state (no-op). Returns ErrInvalidTransition if called
// outside the Aggregating state.
//
// Convention: Compute is called by the aggregator after the last child
// Transition has landed the state at Aggregating. The aggregator's
// outbox-emit handler must NOT skip Compute — without it the parent
// state stays at Aggregating (a perpetual non-terminal).
//
// Required vs Optional semantics (FASE 1, July 2026):
//
// By the time we reach Compute(), all REQUIRED-failed children have
// already short-circuited to FailedTerminal via Transition() rule ①.
// The only failures recorded in sm.failed at this point are OPTIONAL.
//
//   - len(succeeded) == 0 → FailedTerminal: total failure, even though
//     every child was optional. A parent with zero successful outputs
//     cannot be declared Succeeded.
//   - len(succeeded) > 0  → Succeeded: at least one child produced a
//     valid output. OPTIONAL failures are tolerated — the caller
//     inspects sm.Failed() for the warning list.
func (sm *StateMachine) Compute() error {
	switch sm.state {
	case ParentStateSucceeded, ParentStateFailedTerminal:
		return nil // idempotent: terminal → no-op
	case ParentStateAggregating:
		if len(sm.succeeded) == 0 {
			// All children failed (even if all optional) — total failure.
			sm.state = ParentStateFailedTerminal
		} else {
			// At least one child succeeded. OPTIONAL failures tolerated.
			sm.state = ParentStateSucceeded
		}
		return nil
	case ParentStateDispatching, ParentStateWaitingChildren:
		return fmt.Errorf("%w: parent=%s state=%s (call only from Aggregating)", ErrInvalidTransition, sm.parentJobID, sm.state)
	}
	return nil
}

// Snapshot returns a serializable view of the state machine at the
// current version. Used by the durable projection in
// parent_aggregator_state SQLite table.
func (sm *StateMachine) Snapshot() StateSnapshot {
	return StateSnapshot{
		ParentJobID:        sm.parentJobID,
		State:              sm.state,
		ExpectedChildren:   sm.expectedChildren,
		TerminatedChildren: sm.terminatedChildren,
		Succeeded:          append([]string{}, sm.succeeded...),
		Failed:             append([]string{}, sm.failed...),
		ChildIDs:           append([]string{}, sm.childIDs...),
		Version:            sm.version,
	}
}

// StateSnapshot is the durable projection of a StateMachine.
type StateSnapshot struct {
	ParentJobID        string      `json:"parent_job_id"`
	State              ParentState `json:"state"`
	ExpectedChildren   int         `json:"expected_children"`
	TerminatedChildren int         `json:"terminated_children"`
	Succeeded          []string    `json:"succeeded"`
	Failed             []string    `json:"failed"`
	ChildIDs           []string    `json:"child_ids,omitempty"`
	Version            int         `json:"version"`
}
