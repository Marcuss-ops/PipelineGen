// Package job — status_is_terminal_test.go (FASE 1 close-out, July 2026).
//
// Pins the canonical status.IsTerminal boundary per the FASE 1
// spec "WAITING_CHILDREN (per batch) e FINALIZING" being explicitly
// non-terminal. A regression here would either collapse a
// multi-child parent into a "finished" state while children are
// still in flight, or seal a SUCCEEDED-eligible job before its
// finalization window has closed.
package job

import "testing"

// TestStatus_IsTerminal_FASE1_NonTerminalBatchStates is the canonical
// pin for the spec clause: WAITING_CHILDREN + FINALIZING are NOT
// terminal. Pre-FASE-1 brokers defaulted to RUNNING/FINALIZING/SUCCEEDED
// for the parent-aggregation window; FASE 1 elevates the wait to
// a first-class broker state, so a future contributor adding it to
// IsTerminal's terminal-set would silently mark WAITING_CHILDREN
// as terminal — exactly the false-success mode the audit P0 #4
// track sought to close out.
//
// godlike/06 SSOT: this boundary is the canonical place to assert
// the spec contract; downstream code (parent_aggregator, zombie
// sweep, lease fence) all rely on IsTerminal returning false here
// to avoid sealing partial-success parents.
func TestStatus_IsTerminal_FASE1_NonTerminalBatchStates(t *testing.T) {
	nonTerminalBatchStates := []struct {
		name   string
		status Status
	}{
		{"WAITING_CHILDREN", StatusWaitingChildren},
		{"FINALIZING", StatusFinalizing},
	}
	for _, tc := range nonTerminalBatchStates {
		t.Run(tc.name, func(t *testing.T) {
			if tc.status.IsTerminal() {
				t.Fatalf("%s.IsTerminal() = true; want false (FASE 1 explicit non-terminal broker state)", tc.name)
			}
		})
	}
}

// TestStatus_IsTerminal_KnownTerminalStates is the canonical pin
// for the canonical terminal-set: claims SUCCEEDED, PARTIALLY_
// SUCCEEDED, FAILED, CANCELLED are the ONLY terminal states.
// INDEX_PENDING is explicitly NON-terminal (the canonical Qdrant
// reconciler owns the row until projection lands). QUEUED/LEASED/
// RUNNING/RETRY_WAIT are obviously non-terminal (pre-leased or
// in-progress).
//
// godlike/06 SSOT: this is the inverse pin to
// TestStatus_IsTerminal_FASE1_NonTerminalBatchStates — together
// they close out the spec clause.
func TestStatus_IsTerminal_KnownTerminalStates(t *testing.T) {
	cases := []struct {
		name     string
		status   Status
		terminal bool
	}{
		{"QUEUED", StatusQueued, false},
		{"LEASED", StatusLeased, false},
		{"RUNNING", StatusRunning, false},
		{"WAITING_CHILDREN", StatusWaitingChildren, false},
		{"FINALIZING", StatusFinalizing, false},
		{"RETRY_WAIT", StatusRetryWait, false},
		{"SUCCEEDED", StatusSucceeded, true},
		{"PARTIALLY_SUCCEEDED", StatusPartiallySucceeded, true},
		{"INDEX_PENDING", StatusIndexPending, false},
		{"FAILED", StatusFailed, true},
		{"CANCELLED", StatusCancelled, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.status.IsTerminal()
			if got != tc.terminal {
				t.Fatalf("%s.IsTerminal() = %v; want %v", tc.name, got, tc.terminal)
			}
		})
	}
}
