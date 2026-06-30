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
// Step 3 contract — the lockstep SuccessfulEnqueues++ that Step 2
// added is REMOVED here. The slot consumption is now tracked ONLY
// in AnalysisReservations. The matching successful enqueue must
// call RecordEnqueue() to bump SuccessfulEnqueues; the matching
// rollback must call ReleaseReservation() to undo the slot
// consumption. Mismatched calls leak capacity.
//
// Behaviour parity with the previous free-function
// `(m *ChannelMonitor).tryReserve(*atomic.Int32, int) bool` from
// process_video.go is preserved: the call site is still
// `counters.TryReserve(channel.MaxVideosPerRun)` with the CAS
// dance INSIDE the struct.
func (c *ChannelCounters) TryReserve(limit int) bool {
	for {
		cur := c.AnalysisReservations.Load()
		if cur >= int32(limit) {
			return false
		}
		if c.AnalysisReservations.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

// RecordEnqueue bumps SuccessfulEnqueues by 1. Called from
// ChannelMonitor.enqueueClipExtract AFTER
// jobs.Service.Enqueue returns a non-nil *job.Job (the enqueue
// succeeded).
//
// Pair contract: every RecordEnqueue matches exactly one TryReserve
// success — the prior TryReserve that gated the entry into
// enqueueClipExtract's enqueue tail for THIS video. NEVER call
// RecordEnqueue without a corresponding TryReserve on the same
// logical call chain. NEVER call RecordEnqueue on an enqueue that
// failed (use ReleaseReservation instead for the rollback path).
//
// SuccessfulEnqueues is a "cumulative successes" counter; it never
// decrements. The gap between AnalysisReservations (current
// reserved) and SuccessfulEnqueues (cumulative enqueued) is the
// "in flight or rolled back" delta — operators monitoring this gap
// can spot enqueue tail regressions (allocation that never landed).
func (c *ChannelCounters) RecordEnqueue() {
	c.SuccessfulEnqueues.Add(1)
}

// ReleaseReservation decrements AnalysisReservations by 1. Called
// from enqueueClipExtract on every rollback path:
//   - jobsSvc is not wired
//   - findInterestingSegments returned an empty slice (SkipNoSegments)
//   - json.Marshal of the ExtractRequest failed (SkipEnqueueFailed)
//   - jobs.Service.Enqueue returned an error (SkipEnqueueFailed)
//   - jobs.Service.Enqueue returned nil *job.Job without an error
//     (ActiveKey collision: SkipAlreadyActive)
//
// Pair contract: ReleaseReservation <-> TryReserve success (on the
// same logical call chain). NEVER call ReleaseReservation BEFORE
// the matching TryReserve has returned true on the same video — that
// would underflow AnalysisReservations into negative territory and
// corrupt the MaxVideosPerRun gate (the wrap would re-open the
// budget after each release).
//
// Rollback is intentionally idempotent at the SystemTap layer:
// ReleaseReservation cannot bring AnalysisReservations below 0 in
// the CAS-correct usage path. If it does go negative, the caller
// has a bug — the surrounding logs will surface it via the
// AnalysisReservations counter wrap.
//
// SuccessfulEnqueues is intentionally NOT decremented: jobs that
// landed earlier remain historically accurate (you cannot
// un-enqueue a job). After ReleaseReservation,
// AnalysisReservations may be SMALLER than SuccessfulEnqueues; that
// is a healthy "rollback happened" signal that operators can
// read off the two Prometheus counters' difference.
func (c *ChannelCounters) ReleaseReservation() {
	c.AnalysisReservations.Add(-1)
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
