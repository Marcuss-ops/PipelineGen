// Package jobs — queue_notifier.go is the in-process queue-change
// broadcast primitive for the canonical SQLiteStore.
//
// PR-Reaper followup (Wave-22 cleanup, June 2026): the prior
// queueNotifier type definition was lost during a prior refactor
// (the field reference survived in repository.go but the type
// itself became undefined). This file restores the canonical
// implementation per PR-Polling / ADR-0002 §D6.5 (June 2026).
//
// Semantics:
//   - One shared channel is alive at any moment.
//   - Subscribe returns a read-only view of the live channel.
//   - Broadcast closes the live channel and atomically swaps in a
//     fresh open channel; concurrent subscribers blocked on the
//     closed channel unblock immediately, and any new Subscribe
//     call observes the fresh channel.
//
// This is the "wake-on-Enqueue" primitive that lets in-process
// worker pools exit their backoff sleep the instant a new job
// arrives, instead of waiting for the next backoff tick. Subscriber
// loops are typed as:
//
//	for {
//	    select {
//	    case <-notifier.Subscribe():
//	        runOnce(...)
//	    case <-time.After(backoff):
//	        runOnce(...)
//	    case <-ctx.Done():
//	        return
//	    }
//	}
//
// In-process scope: this implementation does NOT cross process
// boundaries — a single SQLiteStore is per-process. The future
// postgres adapter provides its own QueueNotifier implementation
// backed by LISTEN/NOTIFY (out of scope for PR-Polling).
package jobs

import "sync"

// queueNotifier is the in-process wake-on-change broadcast primitive
// owned by exactly one *SQLiteStore per process. Channel-replacement
// pattern: there is always one mutable `ch` field behind a mutex; on
// Broadcast, the old channel is close()d and a fresh chan struct{}
// is installed. Subscribers hold the live channel and unblock when
// it closes.
//
// The empty struct channel (chan struct{}) is sized zero bytes —
// closures wake the goroutine by signal only, never by value.
type queueNotifier struct {
	mu sync.Mutex
	ch chan struct{}
}

// newQueueNotifier constructs a queueNotifier with an initial open
// channel. The first Broadcast must close this initial channel and
// install a fresh one. Subscribers created before the first Broadcast
// receive a closed-channel return on their next select.
func newQueueNotifier() *queueNotifier {
	return &queueNotifier{ch: make(chan struct{})}
}

// Subscribe returns the live notifier channel. The returned channel
// is read-only (zero receive value). It closes on the next Broadcast
// (and is then replaced by a fresh channel available to subsequent
// Subscribe calls).
//
// Concurrency-safe: holds the mutex only during the read of the
// current channel pointer; the receive on the returned channel does
// NOT hold the mutex, so a slow subscriber does not block a
// concurrent Broadcast.
func (n *queueNotifier) Subscribe() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ch
}

// Broadcast closes the live channel and atomically replaces it with a
// fresh open one. All subscribers blocked on the closed channel
// unblock in this single call (the close() wakes every receiver at
// once — channel ownership means close() is observable to multiple
// receivers concurrently).
//
// Concurrency-safe: holds the mutex for the entire swap+close window
// so two concurrent Broadcast calls cannot install the same channel
// pointer or close an already-closed channel.
func (n *queueNotifier) Broadcast() {
	n.mu.Lock()
	defer n.mu.Unlock()
	close(n.ch)
	n.ch = make(chan struct{})
}

// Compile-time assertion: *queueNotifier satisfies the QueueNotifier
// port declared in repository_commands.go. The same assertion lives
// at the bottom of repository.go for *SQLiteStore (which forwards
// Subscribe/Broadcast to its embedded *queueNotifier).
var _ QueueNotifier = (*queueNotifier)(nil)
