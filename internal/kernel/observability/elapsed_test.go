package observability

import (
	"context"
	"testing"
	"time"
)

// TestRunElapsedMsUsesCanonicalClock pins that ElapsedMs reads the run's own
// clock (write-once at StartRun) rather than time.Now, so a compatibility
// projection never starts a second timer.
func TestRunElapsedMsUsesCanonicalClock(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	now := base
	obs := &RunObserver{now: func() time.Time { return now }}
	run := obs.StartRun(context.Background(), RunInfo{JobID: "job-1", AttemptID: "attempt-1"})

	if got := run.ElapsedMs(); got != 0 {
		t.Fatalf("ElapsedMs = %d, want 0 at start", got)
	}
	now = base.Add(5 * time.Second)
	if got := run.ElapsedMs(); got != 5000 {
		t.Fatalf("ElapsedMs = %d, want 5000", got)
	}
	now = base.Add(7*time.Second + 250*time.Millisecond)
	if got := run.ElapsedMs(); got != 7250 {
		t.Fatalf("ElapsedMs = %d, want 7250", got)
	}
}

// TestRunElapsedMsNilSafe pins the nil receiver contract.
func TestRunElapsedMsNilSafe(t *testing.T) {
	var run *Run
	if got := run.ElapsedMs(); got != 0 {
		t.Fatalf("nil ElapsedMs = %d, want 0", got)
	}
}
