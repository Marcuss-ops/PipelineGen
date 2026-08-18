package observability

import (
	"testing"
	"time"
)

func TestConcurrencyTracker_MaxObservedAndAvg(t *testing.T) {
	base := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	tr := &ConcurrencyTracker{}
	// Two fully overlapping tasks, each queued 10ms.
	tr.Record(OpTiming{Operation: "r", ID: "a", WorkerID: 0, QueuedAt: base, StartedAt: base.Add(10 * time.Millisecond), CompletedAt: base.Add(100 * time.Millisecond)})
	tr.Record(OpTiming{Operation: "r", ID: "b", WorkerID: 1, QueuedAt: base, StartedAt: base.Add(10 * time.Millisecond), CompletedAt: base.Add(100 * time.Millisecond)})
	s := tr.Stats(2)
	if s.MaxObserved != 2 {
		t.Fatalf("max_observed = %d, want 2", s.MaxObserved)
	}
	// wall = 90ms, work = 90+90 = 180ms → avg = 2.0.
	if s.AvgObserved < 1.99 || s.AvgObserved > 2.01 {
		t.Fatalf("avg_observed = %f, want ~2.0", s.AvgObserved)
	}
	if s.TotalQueueMS != 20 || s.MaxQueueMS != 10 {
		t.Fatalf("queue stats wrong: %+v", s)
	}
	if s.WallMS != 90 {
		t.Fatalf("wall_ms = %d, want 90", s.WallMS)
	}
}

func TestConcurrencyTracker_SequentialIsOne(t *testing.T) {
	base := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	tr := &ConcurrencyTracker{}
	tr.Record(OpTiming{Operation: "r", ID: "a", WorkerID: 0, QueuedAt: base, StartedAt: base, CompletedAt: base.Add(50 * time.Millisecond)})
	tr.Record(OpTiming{Operation: "r", ID: "b", WorkerID: 1, QueuedAt: base, StartedAt: base.Add(60 * time.Millisecond), CompletedAt: base.Add(110 * time.Millisecond)})
	s := tr.Stats(2)
	if s.MaxObserved != 1 {
		t.Fatalf("max_observed = %d, want 1 (non-overlapping tasks)", s.MaxObserved)
	}
}

func TestConcurrencyTracker_EmptyStats(t *testing.T) {
	tr := &ConcurrencyTracker{}
	s := tr.Stats(4)
	if s.MaxObserved != 0 || s.WallMS != 0 || s.AvgObserved != 0 {
		t.Fatalf("empty tracker must return zero stats: %+v", s)
	}
}
