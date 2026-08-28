package observability

// concurrency.go — ConcurrencyTracker reconstructs the REAL concurrency of a
// bounded parallel fan-out from per-task timing events. It answers the
// "configured N, but did it really run N-wide?" question that wall time alone
// cannot: configured workers ≠ observed workers when a lock, a shared
// dependency (e.g. an LLM server's own parallelism), or an I/O bottleneck
// serializes the pool.
//
// Each task records queued_at (submitted to the pool), started_at (the pool
// actually ran it), and completed_at. From those the tracker derives:
//
//	queue_ms       = started_at - queued_at        (pool saturation latency)
//	max_observed   = peak overlapping in-flight tasks
//	avg_observed   = total work / wall             (average parallelism)
//
// It is deliberately independent of the execution layer: callers (renderer,
// per-cue translator) record events; the tracker only reconstructs numbers.

import (
	"sync"
	"time"
)

// OpTiming is one task's lifecycle within a fan-out. WorkerID is the fan-out
// task slot (0..N-1) — the pool is a bounded semaphore, not persistent OS
// threads, so the reconstruction is driven by the timestamps, not the slot id.
type OpTiming struct {
	Operation   string
	ID          string // clip id, language code, etc.
	WorkerID    int
	QueuedAt    time.Time
	StartedAt   time.Time
	CompletedAt time.Time
}

// QueueMS is the latency between submission and execution start.
func (o OpTiming) QueueMS() int64 {
	return nonNegMS(o.StartedAt.Sub(o.QueuedAt))
}

// DurationMS is the actual execution time (started → completed).
func (o OpTiming) DurationMS() int64 {
	return nonNegMS(o.CompletedAt.Sub(o.StartedAt))
}

// ConcurrencyStats is the reconstructed parallelism of one fan-out boundary.
type ConcurrencyStats struct {
	// Configured is the worker limit the fan-out was bounded to.
	Configured int `json:"configured"`
	// MaxObserved is the peak number of concurrently in-flight tasks.
	MaxObserved int `json:"max_observed"`
	// AvgObserved is total work / wall (average parallelism during execution).
	AvgObserved float64 `json:"avg_observed"`
	// WallMS is the fan-out wall time (first start → last completion).
	WallMS int64 `json:"wall_ms"`
	// TotalWorkMS is the summed per-task duration (work ≠ wall).
	TotalWorkMS int64 `json:"total_work_ms"`
	// TotalQueueMS is the summed queue latency across all tasks.
	TotalQueueMS int64 `json:"total_queue_ms"`
	// MaxQueueMS is the longest single-task queue latency.
	MaxQueueMS int64 `json:"max_queue_ms"`
}

// ConcurrencyTracker collects OpTiming events thread-safely. The zero value is
// usable.
type ConcurrencyTracker struct {
	mu     sync.Mutex
	events []OpTiming
}

// Record appends one task's timing. Safe for concurrent use.
func (t *ConcurrencyTracker) Record(e OpTiming) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.events = append(t.events, e)
	t.mu.Unlock()
}

// Events returns a snapshot copy of the recorded events.
func (t *ConcurrencyTracker) Events() []OpTiming {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]OpTiming, len(t.events))
	copy(out, t.events)
	return out
}

// Stats reconstructs the concurrency from the recorded events. configured is
// the worker limit the caller set on the fan-out.
func (t *ConcurrencyTracker) Stats(configured int) ConcurrencyStats {
	events := t.Events()
	s := ConcurrencyStats{Configured: configured}
	if len(events) == 0 {
		return s
	}

	var totalWork, totalQueue, maxQueue int64
	var minStart, maxEnd time.Time
	bounds := make([]concurrencyPoint, 0, len(events)*2)
	for _, e := range events {
		totalWork += e.DurationMS()
		q := e.QueueMS()
		totalQueue += q
		if q > maxQueue {
			maxQueue = q
		}
		if minStart.IsZero() || e.StartedAt.Before(minStart) {
			minStart = e.StartedAt
		}
		if e.CompletedAt.After(maxEnd) {
			maxEnd = e.CompletedAt
		}
		bounds = append(bounds, concurrencyPoint{at: e.StartedAt, delta: 1}, concurrencyPoint{at: e.CompletedAt, delta: -1})
	}
	// Peak overlap comes from the canonical sweep (same deterministic
	// end-before-start tie-breaker used by the batch and per-phase
	// derivations). AvgObserved stays WORK-based (total work / wall), which
	// is this tracker's documented semantics — the sweep's time-weighted
	// average is used by the batch-level derivations.
	s.MaxObserved, _ = sweepConcurrency(bounds, 0)

	s.WallMS = nonNegMS(maxEnd.Sub(minStart))
	s.TotalWorkMS = totalWork
	s.TotalQueueMS = totalQueue
	s.MaxQueueMS = maxQueue
	if s.WallMS > 0 {
		s.AvgObserved = float64(totalWork) / float64(s.WallMS)
	}
	return s
}

func nonNegMS(d time.Duration) int64 {
	if d < 0 {
		return 0
	}
	return d.Milliseconds()
}
