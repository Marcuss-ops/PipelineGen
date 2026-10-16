package service

import "sync"

// Notifier is the SINGLE wake-up signal for long-poll claims (U2).
//
// Both HTTP endpoints — POST /jobs/claim (wait_ms) and POST /jobs/claim/wait
// (max_wait_ms) — are thin wrappers over Service.WaitAndClaim, which is the
// claim-side long-poll loop. WaitAndClaim coalesces wakes via Notifier and
// falls back to bounded re-poll so signal loss cannot stall a worker. The signal
// carries no data: the database (or memory store) remains the single source
// of truth, and every woken waiter re-runs the atomic ClaimState (SKIP
// LOCKED). Submit, ClaimState, Complete, Rendered, Fail, RequeueExpired and
// external NotifyState all call Notify(); WaitAndClaim (claim side) and
// WaitState (producer terminal-state side) are the only consumers, and each
// keeps its own bounded re-poll, so the two share this single primitive
// instead of introducing a competing timer.
// The service re-poll loop (pollDelay 1s→10s exponential) is the third
// mechanism folded into this one Notifier so there are no competing timers.
//
// This is the same semantic split as Postgres LISTEN/NOTIFY — the signal
// only wakes listeners, it never assigns work.
//
// Thundering-herd mitigation: waiters form a FIFO queue and Notify wakes
// exactly ONE waiter (the longest-parked). A broadcast would wake every parked
// claim per state change (one atomic re-claim each), so an N-worker fleet pays
// N claims per submit. Single-wake keeps the steady-state cost at one claim
// per state change. Missed/coalesced wakes can never stall a worker because
// every consumer also runs a bounded re-poll (WaitAndClaim 1s→10s, WaitState
// 250ms→2s): the re-poll is the timeout fallback that delivers the broadcast
// semantic eventually. Broadcast() exists for the multi-job path
// (RequeueExpired requeues n>1): callers that made n jobs claimable wake n
// times or call Broadcast when n exceeds the parked-waiter count.
// TestWaitAndClaimConcurrentSubmitWakeDeliversOnce pins the resulting "N
// waiters, M submits, exactly M claims" invariant.
type Notifier struct {
	mu      sync.Mutex
	queue   []chan struct{}
	present map[chan struct{}]struct{}
}

// NewNotifier creates a ready notifier.
func NewNotifier() *Notifier {
	return &Notifier{present: make(map[chan struct{}]struct{})}
}

// Notify wakes a single waiter (FIFO: longest-parked first) and is a no-op
// when nobody is parked. Coalescing is intentional: the store is the source
// of truth and each woken waiter re-claims atomically; losers re-park and
// rely on the next Notify or their bounded re-poll.
func (n *Notifier) Notify() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for len(n.queue) > 0 {
		ch := n.queue[0]
		n.queue = n.queue[1:]
		if _, ok := n.present[ch]; !ok {
			continue // already abandoned via Forget; skip stale entry
		}
		delete(n.present, ch)
		close(ch)
		return
	}
}

// Broadcast wakes every current waiter. It is the timeout/multi-job fallback
// for single-wake: use it when n jobs became claimable at once and n single
// wakes would under-notify, or when a wake may have been consumed by a waiter
// whose claim lost the race.
func (n *Notifier) Broadcast() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, ch := range n.queue {
		if _, ok := n.present[ch]; !ok {
			continue
		}
		delete(n.present, ch)
		close(ch)
	}
	n.queue = nil
}

// Done parks the caller in the FIFO waiter queue and returns its private wake
// channel. The caller must call Forget(ch) when it stops waiting (deadline /
// context cancel) so an abandoned waiter does not consume a future single
// wake. A woken channel is already dequeued and must not be forgotten.
func (n *Notifier) Done() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	ch := make(chan struct{})
	if n.present == nil {
		n.present = make(map[chan struct{}]struct{})
	}
	n.queue = append(n.queue, ch)
	n.present[ch] = struct{}{}
	return ch
}

// Forget removes ch from the waiter queue when it was never woken (deadline
// or cancellation). It is a no-op for already-woken channels.
func (n *Notifier) Forget(ch <-chan struct{}) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for i, q := range n.queue {
		if (<-chan struct{})(q) == ch {
			n.queue = append(n.queue[:i], n.queue[i+1:]...)
			break
		}
	}
	// present is keyed by bidirectional channels; linear scan is avoided by
	// tracking only queue membership above, but also drop the map entry when
	// the channel identity matches.
	for k := range n.present {
		if (<-chan struct{})(k) == ch {
			delete(n.present, k)
			break
		}
	}
}
