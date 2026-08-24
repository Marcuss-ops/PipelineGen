// Package completion — completionbus.go (Cut 6.5, July 2026).
//
// Canonical typed-port JobCompletionBus + concrete in-process impl.
//
// Cut 6.5 (the user-facing half of the canonical "no per-job polling
// cycles" goal): Cut 6.3 wired the WORKER tier's typed LeaseState
// propagation; this file ships the API/CLI's sister typed-port so any
// future handler or CLI command that needs to wait for a job to
// terminal-transition can subscribe to the bus instead of polling
// broker.Get(j.ID) in a tight loop.
//
// godlike/06 SSOT one-canonical-owner-per-fact: the in-process `notifier`
// implementation is the SOLE publish-side surface; future Postgres
// LISTEN/NOTIFY adapter ships as a separate file satisfying the same
// JobCompletionBus interface (the Pattern 0 typed-port abstraction
// promised by AGENTS.md + Cut 6.5's design). Single-process scope is
// documented at the QueueNotifier analog (queue_notifier.go) — a
// future Postgres adapter that uses real pg_notify + a server-side
// LISTEN goroutine to broadcast events to multi-process subscribers is
// a forward-pointer.
//
// godlike/07 minimum-blast-radius: every field on JobCompletionEvent is
// optional with defensively-checked accessors (no nil-deref on a
// partially-populated event from a future Postgres adapter); every
// Subscription can be safely .Close()'d twice (idempotent); every
// Await returns ctx.Err() immediately when the ctx is cancelled.
//
// Cut 6.5 API migration pattern (the canonical contract for any
// future handler/CLI that wants to await completion — the pre-Cut-6.5
// world polled broker.Get in a tight loop, duplicating load across
// every concurrent waiter):
//
//	sub, err := deps.bus.Subscribe(ctx, jobID)
//	if err != nil { return err }
//	defer sub.Close()
//	// Race-window check: the job may have completed BEFORE Subscribe
//	// returned (Publish happened first). Without this fallback, the
//	// waiter hangs forever — godlike/07 fail-closed:
//	if j, _ := deps.jobs.Get(ctx, jobID); j != nil && j.Status.IsTerminal() {
//	    return convertToCompletionEvent(j)
//	}
//	evt, err := sub.Await(ctx)
//	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
//	    return ErrWaitTimedOut
//	}
//	return evt
//
// Pattern-derived from the wake-on-Enqueue QueueNotifier
// (queue_notifier.go) — same single-mutex + per-key map + buffer-size-1
// channel recipe. Differences:
//   - bus keys are jobIDs (Q-notifier is a single global channel)
//   - bus subscriptions are typed Subscription envelopes (Q-notifier
//     returns the raw <-chan struct{})
//   - bus Close() REMOVES from map but does NOT close the channel
//     (race-safe; see race notes below)
//   - bus channels are buffered size 1 (job completes ≤ 1 time per
//     per-subscriber instance; drop-on-full policy is fail-closed
//     fail-fast)
//
// RACE NOTES (godlike/07 minimum-blast-radius):
//   - Publish acquires the lock, snapshots subs[jobID], releases the
//     lock, and sends to each captured channel. A concurrent Close()
//     that DELETED the channel from the map AFTER our snapshot still
//     has a captured reference; we send to a channel that's no
//     longer in the bookkeeping. We deliberately DO NOT close the
//     channel from Close() because close+send concurrent races would
//     panic. The channel is GC'd once the last reference drops (the
//     Await goroutine holding the Subscription, and the subscription
//     itself after Close()).
//   - Close() does delete the map entry (memory-leak prevention); it
//     also deletes the parent key when subs[jobID] reaches length 0.
//   - The dropped-on-full Publish policy is safe: any waiter that
//     missed the bus event MUST fall back to the DB Get() (godlike/07
//     fail-closed contract from the migration template above).
package policy

import (
	"context"
	"errors"
	"sync"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ErrWaitTimedOut is the canonical typed-error sentinel returned by
// Subscription.Await when the supplied context is cancelled before the
// completion event arrives. Forwarded to callers as-is via wrap; the
// errors.Is probe matches the typed sentinel so concurrent consumers
// can branch on the typed reason (godlike/06/07 contract).
var ErrWaitTimedOut = errors.New("completion bus: subscription Await timed out (ctx cancelled before publish)")

// JobCompletionEvent is the canonical typed payload published on the
// bus. Maps to the post-finalization job snapshot — enough state for
// any API/CLI waiter to render the terminal transition without a
// follow-up DB Get.
//
// godlike/07 minimum-blast-radius: Err is nil on success-path events
// (StatusSucceeded); non-nil on StatusFailed / StatusDeadLetter.
// Revision is post-finalization (the canonical fence used by the
// typed `CompleteWithArtifacts` finalizer).
type JobCompletionEvent struct {
	JobID       string
	Attempt     int
	FinalStatus job.Status
	Err         error
	Revision    int
}

// Subscription is the typed-port envelope returned by
// JobCompletionBus.Subscribe. Pattern 0 godlike/06 SSOT: no raw
// channels escape the typed-port surface; callers consume via
// Await + Close. This eliminates an entire class of channel-misuse
// bugs (e.g. blocking on a closed channel, failing to close a
// subscription and leaking the channel reference).
type Subscription interface {
	// Await blocks until either:
	//   - a posted JobCompletionEvent arrives (returns it), OR
	//   - the supplied ctx is cancelled (returns ErrWaitTimedOut wrapping ctx.Err()).
	// Await is single-shot — once it returns, the subscription is drained
	// and Close should be called. Calling Await again returns the
	// zero-value event + ErrSubscriptionClosed (fail-closed posture
	// against reusing a drained subscription).
	Await(ctx context.Context) (JobCompletionEvent, error)

	// Close removes the subscription from the bus bookkeeping and
	// permits garbage collection of the underlying channel. Idempotent
	// (a second Close call returns silently).
	Close()
}

// ErrSubscriptionClosed is returned when Await is called on a drained
// or Closed subscription. godlike/07 fail-closed: never silently
// re-await.
var ErrSubscriptionClosed = errors.New("completion bus: subscription already closed or drained")

// JobCompletionBus is the canonical typed-port for completion-event
// publish/subscribe. Composition root constructs a single instance via
// NewBus() and threads it to:
//   - completion.Service (publish on every Complete / CompleteWithArtifacts path)
//   - API handlers (subscribe + Await on /api/jobs/:id/wait-for-completion —
//     forward-pointer HTTP surface)
//   - CLI commands (subscribe + Await on `cmd/admin/buscmd --wait jobID` —
//     forward-pointer CLI surface)
//
// Godlike/06 SSOT: JobCompletionBus is the SOLE canonical typed-port
// for completion-event delivery. No other abstraction (channel, bus,
// pub/sub) may be introduced for this purpose. Future Postgres
// LISTEN/NOTIFY adapter satisfies this same interface (the
// Postgres-side impl adds a ctx-aware constructor at composition-init
// time — not a Subscribe-signature parameter); callers are agnostic to
// in-process vs distributed transport.
//
// Pattern 0 single-method discipline: Subscribe takes only the key.
// Cancellation lives at the per-Await ctx (the canonical caller-side
// boundary). A future Postgres LISTEN adapter that needs LISTEN-session
// lifecycle can re-introduce a separate constructor argument; the
// typed-port surface stays minimal.
type JobCompletionBus interface {
	Publish(evt JobCompletionEvent)
	Subscribe(jobID string) (Subscription, error)
}

// ── canonical concrete impl ─────────────────────────────────────────────

// NewBus constructs the canonical in-process JobCompletionBus. The
// returned bus is concurrency-safe; zero-value is NOT usable.
func NewBus() JobCompletionBus {
	return &notifier{subs: map[string][]chan JobCompletionEvent{}}
}

// notifier is the canonical in-process JobCompletionBus impl.
//
// Same Recipe as jobs/sqljobs.Notifier (queue_notifier.go) —
// mutex-guarded map of buffered channels — keyed by jobID here vs.
// single-channel-key for QueueNotifier.
//
// Unexported (godlike/06 SSOT: composition root never holds the
// concrete pointer; only the JobCompletionBus interface surface
// is exported). Future Postgres adapter satisfies the same
// interface with a pg_notify-bridge impl.
type notifier struct {
	mu   sync.Mutex
	subs map[string][]chan JobCompletionEvent
}

// Compile-time assertion: notifier satisfies the canonical
// JobCompletionBus interface. Defence-in-depth against accidental
// drift in the typed-port shape.
var _ JobCompletionBus = (*notifier)(nil)

// Publish delivers evt to every live subscriber on evt.JobID. The
// captured-subs slice is taken under lock; the actual `<-ch` send
// happens AFTER the lock is released to keep the critical section
// narrow. Concurrent Close() correctly removes the subscription's
// channel from the bookkeeping, but the captured snapshot still
// contains the channel ptr — the channel is intentionally NOT closed
// (race-safety: a concurrent send-on-closed-channel would panic).
// The channel is GC'd once dropped from the post-Publish goroutine
// scope.
//
// godlike/07 drop-on-full policy: if a subscriber's channel buffer
// is full (they ignored a previous event somehow), the new event is
// DROPPED. The waiter must fall back to the DB Get path documented
// in the migration template. This is intentional fail-closed —
// blocking Publish could deadlock any broker goroutine upstream.
func (n *notifier) Publish(evt JobCompletionEvent) {
	n.mu.Lock()
	subs := n.subs[evt.JobID]
	n.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- evt:
		default:
			// Drop: waiter is behind. They will fall back to DB Get.
		}
	}
}

// Subscribe registers a new subscription for `jobID` and returns a
// typed Subscription envelope. Caller-side cancellation lives at the
// per-Await ctx (the canonical caller-supplied boundary); no
// lifecycle-scope context lives on Subscribe — a future Postgres
// LISTEN adapter that needs LISTEN-session lifecycle introduces it
// via a separate constructor argument, not a Subscribe parameter.
//
// godlike/07 minimum-blast-radius: every Subscribe call must be
// paired with exactly one Close() call (the canonical migration
// template uses `defer sub.Close()`). Forgot-Closes leak channels.
func (n *notifier) Subscribe(jobID string) (Subscription, error) {
	if jobID == "" {
		return nil, errors.New("completion bus: Subscribe with empty jobID")
	}
	ch := make(chan JobCompletionEvent, 1)
	n.mu.Lock()
	n.subs[jobID] = append(n.subs[jobID], ch)
	n.mu.Unlock()
	return &subscription{jobID: jobID, ch: ch, bus: n}, nil
}

// subscription is the concrete Subscription envelope. Godlike/06
// SSOT: the only Subscription implementation in the codebase is the
// one returned by NewBus().Subscribe; future Postgres adapters ship
// their own Subscription concrete.
type subscription struct {
	// jobID is the canonical key for unsubscribe. Cached on the
	// subscription so Close() can locate its parent's bookkeeping
	// without re-reading the bus.
	jobID string
	// ch is the buffered-size-1 channel of JobCompletionEvent. Single
	// channel per subscription; not shared.
	ch chan JobCompletionEvent
	// bus is a back-reference for Close() to take the mutex and
	// remove the channel from the bookkeeping.
	bus *notifier
	// closed is set to true on first Close() call; subsequent Await
	// calls see ErrSubscriptionClosed.
	closed bool
	// mu guards the closed flag (read on Await, write on Close).
	mu sync.Mutex
	// drained is set true after Await returns the queued event; a
	// second Await call sees ErrSubscriptionClosed (single-shot
	// contract).
	drained bool
}

// Await blocks until either the queued event arrives or the supplied
// ctx is cancelled. Returns ErrWaitTimedOut wrapping ctx.Err() on
// ctx-cancel; returns the queued Event on publish; returns
// ErrSubscriptionClosed on a closed-or-already-drained subscription
// (godlike/07 fail-closed).
func (s *subscription) Await(ctx context.Context) (JobCompletionEvent, error) {
	s.mu.Lock()
	if s.closed || s.drained {
		s.mu.Unlock()
		return JobCompletionEvent{}, ErrSubscriptionClosed
	}
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		s.mu.Lock()
		s.drained = true
		s.mu.Unlock()
		return JobCompletionEvent{}, wrapWaitTimedOut(ctx.Err())
	case evt := <-s.ch:
		s.mu.Lock()
		s.drained = true
		s.mu.Unlock()
		return evt, nil
	}
}

// Close removes the subscription from the bus bookkeeping. Idempotent
// (a second Close call sees closed=true and returns silently).
//
// Race note: the channel is NOT closed here. Send-on-closed-channel
// panics, and there's no way to atomically "remove from map AND close
// AND ensure no Publish snapshot captures it after removal" without
// adding a per-channel refcount. The cleaner pattern is to drop
// the channel from the bookkeeping and let GC handle the channel
// itself. Memory leak prevention lives in the bookkeeping cleanup
// (delete the parent key when subs[jobID] reaches len=0); correctness
// of concurrent Publish lives in the snapshot-at-loop-iteration
// pattern (snapshot taken under lock, sends happen after).
func (s *subscription) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	bus := s.bus
	ch := s.ch
	jobID := s.jobID
	s.mu.Unlock()

	// Re-check after-reacquire: if s was the only subscriber and the
	// parent key is now empty, drop the parent key (memory hygiene).
	bus.mu.Lock()
	defer bus.mu.Unlock()
	deleteFromBucket(bus.subs, jobID, ch)
}

// deleteFromBucket removes ch from n.subs[jobID]. Deletes the parent
// key when the bucket reaches length 0 (memory-leak prevention —
// millions of dead jobs would otherwise accumulate under a busy
// production workload). Free function so Close can call it without
// re-acquiring the subscription mutex.
func deleteFromBucket(subs map[string][]chan JobCompletionEvent, jobID string, ch chan JobCompletionEvent) {
	bucket := subs[jobID]
	for i, c := range bucket {
		if c == ch {
			bucket = append(bucket[:i], bucket[i+1:]...)
			break
		}
	}
	if len(bucket) == 0 {
		delete(subs, jobID)
		return
	}
	subs[jobID] = bucket
}

// wrapWaitTimedOut preserves the ctx.Err() chain identity so callers
// can probe both errors.Is(err, ErrWaitTimedOut) AND
// errors.Is(err, context.Canceled) / errors.Is(err, context.DeadlineExceeded).
// The wrap uses %w so both typed sentinels are reachable via
// errors.Is.
func wrapWaitTimedOut(ctxErr error) error {
	if ctxErr == nil {
		return ErrWaitTimedOut
	}
	return &waitTimedOutErr{ctxErr: ctxErr}
}

// waitTimedOutErr is the typed-error wrapper preserving both the
// canonical godlike/06 sentinel AND the original ctx.Err().godlike/07
// fail-closed dual-probe.
type waitTimedOutErr struct {
	ctxErr error
}

func (e *waitTimedOutErr) Error() string {
	return ErrWaitTimedOut.Error() + ": " + e.ctxErr.Error()
}

func (e *waitTimedOutErr) Unwrap() []error {
	return []error{ErrWaitTimedOut, e.ctxErr}
}
