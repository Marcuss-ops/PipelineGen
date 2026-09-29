package concurrent

import (
	"context"
	"sync"
)

// FairSemaphore is a capacity-bounded semaphore that prevents ONE owner from
// monopolizing the resource while a DIFFERENT owner is waiting.
//
// Why it exists (measured, 2026-09-28): the voiceover Drive-upload limiter was
// a plain `chan struct{}` with cap 3, shared by every job in the process. While
// two jobs finished their own publication phases, a third job's single
// `final_audio` upload waited 85.7 s for a slot sized at ~5 s of work — a pure
// head-of-line block that the timing report charged as "upload work". A plain
// channel is FIFO over *acquisitions*, not over *owners*: a job that pipelines
// 20 uploads simply queues 20 acquirers ahead of the other job's single one.
//
// Admission rule (the whole point of the type):
//
//   - an owner holding ZERO slots is always admissible when capacity is free;
//   - an owner already holding a slot may take another only while no OTHER
//     owner is starving (waiting with zero slots).
//
// The rule is checked on the fast path and on every hand-off, so a starving
// owner is served before any owner that already has progress. Capacity and
// FIFO order within one owner are preserved.
//
// FairSemaphore is safe for concurrent use. The zero value is NOT usable:
// construct it with NewFairSemaphore.
type FairSemaphore struct {
	mu     sync.Mutex
	cap    int
	active map[string]int
	total  int
	queue  []*fairWaiter
}

// fairWaiter is one blocked AcquireCtx call. ready is closed exactly once, when
// the slot is handed to this waiter.
type fairWaiter struct {
	owner string
	ready chan struct{}
	done  bool
}

// NewFairSemaphore constructs a FairSemaphore with the given max concurrency.
// Zero or negative values are rejected at construction (fail-fast: a
// misconfigured gate must surface loud at ctor time, not block forever),
// mirroring NewSemaphore.
func NewFairSemaphore(max int) *FairSemaphore {
	if max < 1 {
		panic("concurrent.NewFairSemaphore: max must be >= 1 (received " + itoa(max) + ")")
	}
	return &FairSemaphore{cap: max, active: make(map[string]int)}
}

// Cap returns the maximum number of concurrent holders.
func (s *FairSemaphore) Cap() int {
	if s == nil {
		return 0
	}
	return s.cap
}

// AcquireCtx acquires a slot for owner, blocking until one is free or ctx is
// done. On success it returns the release function; call it exactly once.
// On cancellation it returns nil and ctx.Err() WITHOUT holding a slot, so a
// cancelled waiter can never leak capacity.
//
// owner identifies the competing work unit (in the script pipeline: the run
// id). An empty owner is a valid, shared identity: unowned callers compete
// with each other under the same rule and can never starve an owned one.
func (s *FairSemaphore) AcquireCtx(ctx context.Context, owner string) (func(), error) {
	if s == nil {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.admissibleLocked(owner, nil) {
		s.activateLocked(owner)
		s.mu.Unlock()
		return func() { s.release(owner) }, nil
	}
	w := &fairWaiter{owner: owner, ready: make(chan struct{})}
	s.queue = append(s.queue, w)
	s.mu.Unlock()

	release := func() { s.release(owner) }
	select {
	case <-w.ready:
		// Admission and cancellation raced: the slot was already handed over,
		// so honour the cancellation by returning the slot, never by leaking it.
		select {
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		default:
		}
		return release, nil
	case <-ctx.Done():
		s.mu.Lock()
		if w.done {
			// Handed off between our select and this lock: give it back.
			s.mu.Unlock()
			release()
			return nil, ctx.Err()
		}
		s.removeWaiterLocked(w)
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

// TryAcquireCtx attempts to acquire without blocking. It reports false when the
// slot is not immediately admissible — including when the owner is not
// starving and another owner is queued (best-effort callers must not jump the
// fairness queue).
func (s *FairSemaphore) TryAcquireCtx(owner string) (func(), bool) {
	if s == nil {
		return func() {}, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.admissibleLocked(owner, nil) {
		return nil, false
	}
	s.activateLocked(owner)
	return func() { s.release(owner) }, true
}

// InFlight reports the number of held slots (diagnostics/tests).
func (s *FairSemaphore) InFlight() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total
}

// Waiting reports the number of blocked acquirers (diagnostics/tests).
func (s *FairSemaphore) Waiting() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// admissibleLocked is the fairness rule. self is the queued waiter being
// considered (nil on the fast path) and is excluded from the starving-owner
// scan so a waiter never blocks on itself.
func (s *FairSemaphore) admissibleLocked(owner string, self *fairWaiter) bool {
	if s.total >= s.cap {
		return false
	}
	if s.active[owner] == 0 {
		// A starving owner (or a brand-new one) always gets a free slot.
		return true
	}
	// The owner already holds progress: it may only take another slot while no
	// other owner is starved. This is what turns the old FIFO-of-acquisitions
	// into fairness across jobs.
	for _, q := range s.queue {
		if q == self || q.done {
			continue
		}
		if q.owner != owner && s.active[q.owner] == 0 {
			return false
		}
	}
	return true
}

func (s *FairSemaphore) activateLocked(owner string) {
	s.active[owner]++
	s.total++
}

func (s *FairSemaphore) removeWaiterLocked(target *fairWaiter) {
	for i, q := range s.queue {
		if q == target {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			return
		}
	}
}

func (s *FairSemaphore) release(owner string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[owner] > 0 {
		s.active[owner]--
		s.total--
		if s.active[owner] == 0 {
			delete(s.active, owner)
		}
	}
	s.drainLocked()
}

// drainLocked hands free capacity to queued waiters, re-evaluating the fairness
// rule after each hand-off so a starving owner cannot be overtaken by a later
// admission from the same releasing owner.
func (s *FairSemaphore) drainLocked() {
	for {
		admitted := false
		for i, w := range s.queue {
			if w.done || !s.admissibleLocked(w.owner, w) {
				continue
			}
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			w.done = true
			s.activateLocked(w.owner)
			close(w.ready)
			admitted = true
			break
		}
		if !admitted {
			return
		}
	}
}
