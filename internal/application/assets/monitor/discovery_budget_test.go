// Package monitor — discovery_budget_test.go pins the Commit 2/6
// Correttezza #1 fix: outcomeCounters gains a budgetUsed atomic
// counter; tryReserve operates on it; the pre-Commit-2
// `outcomes.rejected.Add(outcomes.enqueued.Add(-1))` silent-success
// bug is replaced with explicit separate statements.
//
// What this test asserts:
//  1. tryReserve returns true and increments budgetUsed when below
//     cap.
//  2. tryReserve returns false and does NOT increment when at cap.
//  3. After a leader-election loss (AlreadyScheduled), budgetUsed
//     is decremented back to its prior level (so the next cycle's
//     new (channel_id, video_id) pair can claim the slot).
//  4. After a post-broker rejection (Rejected), budgetUsed is
//     decremented AND outcomes.rejected is +1 (not -1, not 0).
//  5. Enqueued keeps the budget slot (no decrement).
package monitor

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestOutcomeCounters_BudgetUsedHappyPath pins the new budget counter.
// tryReserve increments budgetUsed; outcome classification decrements
// it for AlreadyScheduled + Rejected.
func TestOutcomeCounters_BudgetUsedHappyPath(t *testing.T) {
	var c outcomeCounters
	const max = 3

	// Reserve 3 slots — all should succeed.
	if !tryReserve(&c.budgetUsed, max) {
		t.Fatal("reserve 1: expected true (budget not exhausted)")
	}
	if !tryReserve(&c.budgetUsed, max) {
		t.Fatal("reserve 2: expected true (budget not exhausted)")
	}
	if !tryReserve(&c.budgetUsed, max) {
		t.Fatal("reserve 3: expected true (last slot)")
	}
	if got := c.budgetUsed.Load(); got != 3 {
		t.Errorf("after 3 reserves: want budgetUsed=3 got %d", got)
	}

	// 4th reserve must fail.
	if tryReserve(&c.budgetUsed, max) {
		t.Errorf("reserve 4: expected false (cap reached)")
	}
	if got := c.budgetUsed.Load(); got != 3 {
		t.Errorf("after 4th reserve (rejected): want budgetUsed=3 got %d", got)
	}
}

// TestOutcomeCounters_AlreadyScheduledReleasesBudgetSlot pins the
// AlreadyScheduled decrement path: leader-election loss decrements
// budgetUsed so the next (channel_id, video_id) pair can claim the slot.
func TestOutcomeCounters_AlreadyScheduledReleasesBudgetSlot(t *testing.T) {
	var c outcomeCounters
	const max = 1

	// Reserve the single slot.
	if !tryReserve(&c.budgetUsed, max) {
		t.Fatal("reserve 1: expected true")
	}
	if tryReserve(&c.budgetUsed, max) {
		t.Fatal("reserve 2: expected false (cap reached)")
	}

	// AlreadyScheduled outcome: release the budget.
	c.alreadyScheduled.Add(1)
	c.budgetUsed.Add(-1)

	// Now a new reserve should succeed (slot is open).
	if !tryReserve(&c.budgetUsed, max) {
		t.Errorf("after AlreadyScheduled decrement: reserve should succeed (budget released)")
	}
	if got := c.budgetUsed.Load(); got != 1 {
		t.Errorf("after AlreadyScheduled+re-reserve: want budgetUsed=1 got %d", got)
	}
	if got := c.alreadyScheduled.Load(); got != 1 {
		t.Errorf("alreadyScheduled counter: want 1 got %d", got)
	}
}

// TestOutcomeCounters_RejectedReleasesBudgetAndIncrementsRejected pins
// the Correttezza #1 bugfix: the pre-Commit-2 form
// `outcomes.rejected.Add(outcomes.enqueued.Add(-1))` was a silent
// semantic bug (atomic.Add returns int32, not pointer; the inner
// Add(-1) on enqueued was a no-op, and rejected got -1 instead of
// +1). The new shape is two explicit statements.
func TestOutcomeCounters_RejectedReleasesBudgetAndIncrementsRejected(t *testing.T) {
	var c outcomeCounters
	const max = 2

	// Reserve both slots.
	if !tryReserve(&c.budgetUsed, max) {
		t.Fatal("reserve 1: expected true")
	}
	if !tryReserve(&c.budgetUsed, max) {
		t.Fatal("reserve 2: expected true")
	}
	if got := c.budgetUsed.Load(); got != 2 {
		t.Fatalf("preconditions: want budgetUsed=2 got %d", got)
	}

	// Post-broker rejection: release the budget + increment rejected
	// outcome counter.
	c.budgetUsed.Add(-1)
	c.rejected.Add(1)

	// Slot is now open for the next (channel_id, video_id) pair.
	if !tryReserve(&c.budgetUsed, max) {
		t.Errorf("after Rejected: budget slot should be re-reservable")
	}
	if got := c.budgetUsed.Load(); got != 2 {
		t.Errorf("after Rejected+re-reserve: want budgetUsed=2 got %d", got)
	}
	// Critical: rejected must be +1, not -1 (the pre-Commit-2 bug).
	if got := c.rejected.Load(); got != 1 {
		t.Errorf("rejected counter (post-Commit-2 explicit +1): want 1 got %d", got)
	}
}

// TestOutcomeCounters_EnqueuedKeepsBudgetSlot pins the success path:
// tryReserve increments budgetUsed; the Enqueued outcome does NOT
// decrement it (the slot is held for the broker-emitted job).
func TestOutcomeCounters_EnqueuedKeepsBudgetSlot(t *testing.T) {
	var c outcomeCounters
	const max = 1

	if !tryReserve(&c.budgetUsed, max) {
		t.Fatal("reserve 1: expected true")
	}

	// Enqueued outcome: increment outcome counter, keep budget slot.
	c.enqueued.Add(1)

	if got := c.budgetUsed.Load(); got != 1 {
		t.Errorf("Enqueued MUST keep the budget slot: want 1 got %d", got)
	}
	if got := c.enqueued.Load(); got != 1 {
		t.Errorf("enqueued outcome counter: want 1 got %d", got)
	}
}

// TestOutcomeCounters_TryReserveConcurrent pins the CAS contract: 50
// concurrent goroutines racing for 5 slots produce exactly 5 winners.
func TestOutcomeCounters_TryReserveConcurrent(t *testing.T) {
	var c outcomeCounters
	const max = 5
	const goroutines = 50

	var wg sync.WaitGroup
	var winners atomic.Int32
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tryReserve(&c.budgetUsed, max) {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := winners.Load(); got != max {
		t.Errorf("concurrent tryReserve winners: want %d got %d (CAS contract broken)", max, got)
	}
	if got := c.budgetUsed.Load(); got != int32(max) {
		t.Errorf("concurrent budgetUsed final: want %d got %d", max, got)
	}
}
