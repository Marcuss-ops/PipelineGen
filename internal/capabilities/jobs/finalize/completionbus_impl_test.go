package finalize

// completionbus_test_impl_test.go — in-process concrete JobCompletionBus.
//
// This is the Wave-3 demolition relocation of the canonical concrete impl
// (`notifier` + `NewBus` + `subscription` envelope) formerly living in
// completionbus.go. Production keepers are the typed-port surface only
// (JobCompletionBus / Subscription / events); no production wiring
// constructs the in-process bus any more. The e2e tests that exercise
// bus semantics (internal/capabilities/jobs/job_finalizer_e2e_test.go and
// completionbus_test.go) consume this impl via finalize.NewBus(); keeping
// it here as a test-only file preserves that coverage without keeping
// unreachable production code alive.

import (
	"context"
	"errors"
	"sync"
)

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
