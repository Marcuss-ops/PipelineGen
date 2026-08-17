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

// TestRunTimingSummaryUsesLiveElapsedWall pins that a still-running Run can
// derive the critical path / bottleneck from its live elapsed clock before
// WallTimeMs is finalized (the pre-finish logging path).
func TestRunTimingSummaryUsesLiveElapsedWall(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	now := base
	obs := &RunObserver{now: func() time.Time { return now }}
	run := obs.StartRun(context.Background(), RunInfo{JobID: "job-1", AttemptID: "attempt-1"})

	// Two sequential top-level stages measured on the run's own clock.
	now = base.Add(3 * time.Second)
	run.recordStage(StageReport{Name: "script.prepare", Status: StageStatusCompleted, StartedAt: base, FinishedAt: now, DurationMs: 3000})
	run.recordStage(StageReport{Name: "script.engine", Status: StageStatusCompleted, StartedAt: now, FinishedAt: now.Add(79 * time.Second), DurationMs: 79000})
	now = now.Add(79 * time.Second)

	s := run.TimingSummary()
	if s.WallMs != 82000 {
		t.Fatalf("WallMs = %d, want 82000 (live elapsed clock, not finalized WallTimeMs)", s.WallMs)
	}
	if s.BottleneckStage != "script.engine" {
		t.Fatalf("BottleneckStage = %q, want script.engine", s.BottleneckStage)
	}
	if len(s.CriticalPath) != 2 || s.CriticalPath[0].Name != "script.prepare" || s.CriticalPath[1].Name != "script.engine" {
		t.Fatalf("CriticalPath = %+v, want [script.prepare script.engine]", s.CriticalPath)
	}
	if s.FormatCriticalPath() != "script.prepare(3.7%) > script.engine(96.3%)" {
		t.Fatalf("FormatCriticalPath = %q", s.FormatCriticalPath())
	}
}

// TestRunTimingSummaryNilSafe pins the nil receiver contract.
func TestRunTimingSummaryNilSafe(t *testing.T) {
	var run *Run
	if s := run.TimingSummary(); s.WallMs != 0 || s.CriticalPath != nil {
		t.Fatalf("nil run summary must be zero, got %+v", s)
	}
}
