// Package job — parent_state_test.go (Step 12B-C1/5 unit tests, July 2026).
//
// 15+ unit tests covering the canonical 5-state machine for parent-job lifecycle:
// IsTerminal/IsValid helpers, NewStateMachine panic guard, every legal transition
// path (Dispatching → WaitingChildren, WaitingChildren → Aggregating, Aggregating
// → Succeeded via Compute, REQUIRED-failed short-circuits), plus failure modes
// (duplicate child event idempotency, already-terminal rejection, Compute called
// from non-Aggregating, snapshot deep-copy).
package job

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── State-level helpers ────────────────────────────────────────────

func TestParentState_IsTerminal(t *testing.T) {
	assert.True(t, ParentStateSucceeded.IsTerminal())
	assert.True(t, ParentStateFailedTerminal.IsTerminal())
	assert.False(t, ParentStateDispatching.IsTerminal())
	assert.False(t, ParentStateWaitingChildren.IsTerminal())
	assert.False(t, ParentStateAggregating.IsTerminal())
}

func TestParentState_IsValid(t *testing.T) {
	assert.True(t, ParentStateDispatching.IsValid())
	assert.True(t, ParentStateWaitingChildren.IsValid())
	assert.True(t, ParentStateAggregating.IsValid())
	assert.True(t, ParentStateSucceeded.IsValid())
	assert.True(t, ParentStateFailedTerminal.IsValid())
	assert.False(t, ParentState("garbage").IsValid())
	assert.False(t, ParentState("").IsValid())
	// Legacy wire values from migration candidates — NOT valid in canonical surface.
	assert.False(t, ParentState("partial_success").IsValid())
	assert.False(t, ParentState("failed").IsValid())
	assert.False(t, ParentState("waiting_children_partial").IsValid())
}

// ── NewStateMachine construction ───────────────────────────────────

func TestNewStateMachine_InitialStateIsDispatching(t *testing.T) {
	sm := NewStateMachine("p-1", 4)
	assert.Equal(t, ParentStateDispatching, sm.State())
	assert.Equal(t, 0, sm.Terminated())
	assert.Equal(t, 4, sm.Expected())
	assert.Equal(t, 0, sm.Version())
	assert.False(t, sm.State().IsTerminal())
	assert.Equal(t, []string{}, sm.Succeeded())
	assert.Equal(t, []string{}, sm.Failed())
}

func TestNewStateMachine_PanicsOnZeroExpectedChildren(t *testing.T) {
	assert.Panics(t, func() { _ = NewStateMachine("p", 0) },
		"expectedChildren = 0 must panic (WireUp fail-fast)")
	assert.Panics(t, func() { _ = NewStateMachine("p", -1) },
		"expectedChildren < 0 must panic (WireUp fail-fast)")
	assert.Panics(t, func() { _ = NewStateMachine("p", -100) },
		"any negative expectedChildren must panic")
}

// ── Dispatching → WaitingChildren ──────────────────────────────────

func TestStateMachine_DispatchingToWaitingChildren(t *testing.T) {
	sm := NewStateMachine("p", 4)
	err := sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p",
		ChildJobID:  "c1",
		Outcome:     ChildOutcome{JobID: "c1", Succeeded: true, Required: true},
	})
	require.NoError(t, err)
	assert.Equal(t, ParentStateWaitingChildren, sm.State())
	assert.Equal(t, 1, sm.Terminated())
	assert.Equal(t, 1, sm.Version())
	assert.Equal(t, []string{"c1"}, sm.Succeeded())
	assert.Equal(t, []string{}, sm.Failed())
}

func TestStateMachine_DispatchingOptionalFailedGoesToWaitingChildren_NotFailed(t *testing.T) {
	// An OPTIONAL child failed at dispatch — parent still belongs in
	// WaitingChildren (other children may succeed). Per godlike/07
	// fail-closed: REQUIRED-failed-only propagates.
	sm := NewStateMachine("p", 4)
	err := sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p",
		ChildJobID:  "c1",
		Outcome:     ChildOutcome{JobID: "c1", Succeeded: false, Required: false, Error: "transient"},
	})
	require.NoError(t, err)
	assert.Equal(t, ParentStateWaitingChildren, sm.State())
}

func TestStateMachine_DispatchingRequiredFailedGoesToFailedTerminal(t *testing.T) {
	sm := NewStateMachine("p", 4)
	err := sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p",
		ChildJobID:  "c1",
		Outcome:     ChildOutcome{JobID: "c1", Succeeded: false, Required: true, Error: "tts unavailable"},
	})
	require.NoError(t, err)
	assert.Equal(t, ParentStateFailedTerminal, sm.State())
	assert.True(t, sm.State().IsTerminal())
}

// ── WaitingChildren → Aggregating ──────────────────────────────────

func TestStateMachine_WaitingChildrenToAggregating(t *testing.T) {
	sm := NewStateMachine("p", 3)
	require.NoError(t, sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c1",
		Outcome: ChildOutcome{JobID: "c1", Succeeded: true, Required: true},
	}))
	require.NoError(t, sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c2",
		Outcome: ChildOutcome{JobID: "c2", Succeeded: true, Required: false},
	}))
	assert.Equal(t, ParentStateWaitingChildren, sm.State())
	require.NoError(t, sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c3",
		Outcome: ChildOutcome{JobID: "c3", Succeeded: true, Required: true},
	}))
	assert.Equal(t, ParentStateAggregating, sm.State())
	assert.Equal(t, 3, sm.Terminated())
	assert.Equal(t, 3, sm.Version())
	assert.Equal(t, []string{"c1", "c2", "c3"}, sm.Succeeded())
	assert.Equal(t, []string{}, sm.Failed())
}

func TestStateMachine_RequiredChildFailedInWaitingChildren_GoesToFailedTerminal(t *testing.T) {
	// Fail-closed per godlike/07: a REQUIRED child that fails propagates
	// immediately, even when other children are still pending.
	sm := NewStateMachine("p", 4)
	require.NoError(t, sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c1",
		Outcome: ChildOutcome{JobID: "c1", Succeeded: true, Required: true},
	}))
	require.NoError(t, sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c2",
		Outcome: ChildOutcome{JobID: "c2", Succeeded: true, Required: false},
	}))
	require.NoError(t, sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c3",
		Outcome: ChildOutcome{JobID: "c3", Succeeded: false, Required: true, Error: "boom"},
	}))
	assert.Equal(t, ParentStateFailedTerminal, sm.State())
	assert.True(t, sm.State().IsTerminal())
	assert.Equal(t, []string{"c1", "c2"}, sm.Succeeded())
	assert.Equal(t, []string{"c3"}, sm.Failed())
}

// ── Aggregating → terminal via Compute() ──────────────────────────

func TestStateMachine_AggregatingToSucceeded_OnAllSucceeded(t *testing.T) {
	sm := NewStateMachine("p", 4)
	for _, childID := range []string{"c1", "c2", "c3", "c4"} {
		require.NoError(t, sm.Transition(ChildTerminatedEvent{
			ParentJobID: "p", ChildJobID: childID,
			Outcome: ChildOutcome{JobID: childID, Succeeded: true, Required: true},
		}))
	}
	assert.Equal(t, ParentStateAggregating, sm.State())
	require.NoError(t, sm.Compute())
	assert.Equal(t, ParentStateSucceeded, sm.State())
	assert.True(t, sm.State().IsTerminal())
	// Version semantics: 4 transitions only (Compute does NOT increment version)
	assert.Equal(t, 4, sm.Version())
}

func TestStateMachine_AggregatingToFailedTerminal_OnAnyFailed(t *testing.T) {
	sm := NewStateMachine("p", 3)
	require.NoError(t, sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c1",
		Outcome: ChildOutcome{JobID: "c1", Succeeded: true, Required: true},
	}))
	require.NoError(t, sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c2",
		Outcome: ChildOutcome{JobID: "c2", Succeeded: false, Required: false, Error: "upload failed"},
	}))
	require.NoError(t, sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c3",
		Outcome: ChildOutcome{JobID: "c3", Succeeded: true, Required: true},
	}))
	assert.Equal(t, ParentStateAggregating, sm.State())
	require.NoError(t, sm.Compute())
	assert.Equal(t, ParentStateFailedTerminal, sm.State())
	assert.True(t, sm.State().IsTerminal())
}

// ── Idempotency + terminal-state rejection ────────────────────────

func TestStateMachine_DuplicateChildEvent_ReturnsError(t *testing.T) {
	sm := NewStateMachine("p", 3)
	require.NoError(t, sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c1",
		Outcome: ChildOutcome{JobID: "c1", Succeeded: true},
	}))
	err := sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c1",
		Outcome: ChildOutcome{JobID: "c1", Succeeded: true},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDuplicateChildEvent)
	// State and counts unchanged.
	assert.Equal(t, ParentStateWaitingChildren, sm.State())
	assert.Equal(t, 1, sm.Terminated())
	assert.Equal(t, 1, sm.Version())
}

func TestStateMachine_AlreadyTerminal_ReturnsError(t *testing.T) {
	sm := NewStateMachine("p", 1)
	require.NoError(t, sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c1",
		Outcome: ChildOutcome{JobID: "c1", Succeeded: false, Required: true, Error: "required boom"},
	}))
	assert.Equal(t, ParentStateFailedTerminal, sm.State())

	err := sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c2",
		Outcome: ChildOutcome{JobID: "c2", Succeeded: true},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAlreadyTerminal)
}

func TestStateMachine_AlreadyTerminalSucceeded_AlsoRejects(t *testing.T) {
	sm := NewStateMachine("p", 1)
	require.NoError(t, sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c1",
		Outcome: ChildOutcome{JobID: "c1", Succeeded: true, Required: true},
	}))
	require.NoError(t, sm.Compute())
	assert.Equal(t, ParentStateSucceeded, sm.State())

	err := sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c2",
		Outcome: ChildOutcome{JobID: "c2", Succeeded: true},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAlreadyTerminal)
}

// ── Compute() invalid-state guards + idempotency ─────────────────

func TestStateMachine_ComputeFromDispatching_ReturnsError(t *testing.T) {
	sm := NewStateMachine("p", 3)
	err := sm.Compute()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidTransition)
}

func TestStateMachine_ComputeFromWaitingChildren_ReturnsError(t *testing.T) {
	sm := NewStateMachine("p", 3)
	require.NoError(t, sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c1",
		Outcome: ChildOutcome{JobID: "c1", Succeeded: true},
	}))
	err := sm.Compute()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidTransition)
}

func TestStateMachine_ComputeIdempotentOnTerminal(t *testing.T) {
	sm := NewStateMachine("p", 2)
	require.NoError(t, sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c1",
		Outcome: ChildOutcome{JobID: "c1", Succeeded: true},
	}))
	require.NoError(t, sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c2",
		Outcome: ChildOutcome{JobID: "c2", Succeeded: true},
	}))
	require.NoError(t, sm.Compute())
	assert.Equal(t, ParentStateSucceeded, sm.State())
	require.NoError(t, sm.Compute()) // no-op on terminal
	require.NoError(t, sm.Compute()) // still no-op
	assert.Equal(t, ParentStateSucceeded, sm.State())
}

// ── Snapshot deep-copy semantics ──────────────────────────────────

func TestStateMachine_Snapshot_DeepCopy(t *testing.T) {
	sm := NewStateMachine("p", 3)
	require.NoError(t, sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c1",
		Outcome: ChildOutcome{JobID: "c1", Succeeded: true},
	}))
	require.NoError(t, sm.Transition(ChildTerminatedEvent{
		ParentJobID: "p", ChildJobID: "c2",
		Outcome: ChildOutcome{JobID: "c2", Succeeded: false, Error: "boom"},
	}))
	snap := sm.Snapshot()
	assert.Equal(t, "p", snap.ParentJobID)
	assert.Equal(t, ParentStateWaitingChildren, snap.State)
	assert.Equal(t, 3, snap.ExpectedChildren)
	assert.Equal(t, 2, snap.TerminatedChildren)
	assert.Equal(t, []string{"c1"}, snap.Succeeded)
	assert.Equal(t, []string{"c2"}, snap.Failed)
	assert.Equal(t, 2, snap.Version)

	// Mutate the snapshot's slices — must not affect the original.
	snap.Succeeded[0] = "tampered"
	snap.Failed = append(snap.Failed, "injected")
	assert.Equal(t, "c1", sm.Succeeded()[0])
	assert.Equal(t, []string{"c2"}, sm.Failed())
}

// ── 100-batch × 4-figli scale smoke (acceptance criteria) ─────────

// Simulates the Step 12B acceptance smoke: 100 parents × 4 children each,
// ALL SUCCEEDED. Every parent must reach Succeeded on the last child + Compute.
func TestStateMachine_Acceptance_100Batches4Children_AllSucceeded(t *testing.T) {
	const batches = 100
	const childrenPerBatch = 4

	for b := 0; b < batches; b++ {
		sm := NewStateMachine("batch-"+strconv.Itoa(b), childrenPerBatch)
		for c := 0; c < childrenPerBatch; c++ {
			err := sm.Transition(ChildTerminatedEvent{
				ParentJobID: "batch-" + strconv.Itoa(b),
				ChildJobID:  "child-" + strconv.Itoa(c),
				Outcome: ChildOutcome{
					JobID:     "child-" + strconv.Itoa(c),
					Succeeded: true,
					Required:  true,
				},
			})
			require.NoError(t, err, "batch %d child %d Transition must succeed", b, c)
		}
		require.Equal(t, ParentStateAggregating, sm.State(),
			"batch %d must be at Aggregating after %d children", b, childrenPerBatch)
		require.NoError(t, sm.Compute())
		require.Equal(t, ParentStateSucceeded, sm.State(),
			"batch %d must reach Succeeded after Compute", b)
	}
}

// (note: the scale-smoke test uses strconv.Itoa from the stdlib;
// no custom int-to-string helper duplicated here per the code review.)
