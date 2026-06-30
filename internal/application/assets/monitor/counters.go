package monitor

import "sync/atomic"

// ChannelCounters holds the dual sliding-window budget counters that
// drive the per-channel MaxVideosPerRun semantics inside
// ChannelMonitor.checkChannel.
//
// Plan Channel Monitor — Blocco 4 (June 2026). Step 2 replaces the
// single atomic.Int32 `acceptedCount` (which conflated "analyses
// reserved" with "jobs successfully enqueued") with two counters so
// the two metrics are observable independently and the orchestration
// can roll them back independently in Step 3.
//
//   - AnalysisReservations: number of MaxVideosPerRun slots consumed
//     by videos that passed the filter chain (MinViews, MaxClipDuration,
//     title-keyword, semantic-budget, semantic-score) and entered the
//     enqueueClipExtract tail. Incremented inside TryReserve's CAS
//     success path. Step 3 will pair it with ReleaseReservation when
//     the enqueue tail decides not to queue (no jobs.service, no
//     interesting segments, marshal failure, jobs.Enqueue error,
//     ActiveKey collision).
//
//   - SuccessfulEnqueues: number of youtube_clip.extract jobs that
//     actually landed in the job broker. In Step 2 this counter is
//     incremented in lockstep with AnalysisReservations (parity
//     behaviour with the previous acceptedCount — every slot reserved
//     is automatically counted as "would-have-enqueued"). Step 3
//     will move the increment to AFTER enqueueClipExtract returns
//     Enqueued=true so the two counters diverge and expose the gap
//     between "permit consumed" and "job queued".
//
// Both counters are atomic.Int32 because checkChannel runs them under
// a sync.WaitGroup of N=5 concurrent worker goroutines per channel,
// the underlying tryReserve uses CompareAndSwap, and Step 3's
// ReleaseReservation needs lock-free decrement from a different
// goroutine than the one that incremented (the enqueueClipExtract
// tail runs in the worker context while the release is logically
// the SAME goroutine — but the type signature guarantees no
// re-acquisition races).
type ChannelCounters struct {
	AnalysisReservations atomic.Int32
	SuccessfulEnqueues   atomic.Int32
}

// TryReserve atomically reserves a MaxVideosPerRun slot if
// AnalysisReservations has not yet hit `limit`. Returns true if the
// slot was reserved (caller MUST proceed into the analysis tail),
// false if the budget is saturated (caller MUST NOT consume more
// capacity).
//
// In Step 2 the increment is lockstep on both counters — every
// successful reservation is also a SuccessfulEnqueues++. Step 3
// will:
//
//  1. Remove the lockstep SuccessfulEnqueues++ from this method.
//  2. Add a ChannelCounters.RecordEnqueue() that only bumps
//     SuccessfulEnqueues and is called after enqueueClipExtract
//     returns Enqueued=true.
//  3. Add a ChannelCounters.ReleaseReservation() that decrements
//     AnalysisReservations and is called when the enqueue tail
//     fails or decides not to queue.
//
// Behaviour parity with the previous free-function
// `(m *ChannelMonitor).tryReserve(*atomic.Int32, int) bool` from
// process_video.go is preserved: the call site becomes
// `counters.TryReserve(channel.MaxVideosPerRun)` instead of
// `m.tryReserve(uint32(counters.AnalysisReservations.Load()), ...)`,
// with the CAS dance moved INSIDE the struct so process_video.go no
// longer imports sync/atomic.
func (c *ChannelCounters) TryReserve(limit int) bool {
	for {
		cur := c.AnalysisReservations.Load()
		if cur >= int32(limit) {
			return false
		}
		if c.AnalysisReservations.CompareAndSwap(cur, cur+1) {
			// Step 2: lockstep increment — preserved for parity.
			// Step 3 replaces this with a RecordEnqueue() call from
			// the enqueueClipExtract success path.
			c.SuccessfulEnqueues.Add(1)
			return true
		}
	}
}

// Reservations returns AnalysisReservations as int — convenient for
// the public ChannelCheckResult surface (operators read the count
// without dealing with atomic.Int32 directly).
func (c *ChannelCounters) Reservations() int {
	return int(c.AnalysisReservations.Load())
}

// Enqueued returns SuccessfulEnqueues as int — same pattern as
// Reservations(). In Step 2 the two values are guaranteed equal;
// in Step 3 they may diverge when a reservation is rolled back
// without a corresponding enqueue (or vice-versa via the
// RecordEnqueue path).
func (c *ChannelCounters) Enqueued() int {
	return int(c.SuccessfulEnqueues.Load())
}
