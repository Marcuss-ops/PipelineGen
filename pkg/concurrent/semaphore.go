// Package concurrent provides goroutine-safe concurrency primitives:
// errgroup-style parallel execution with cancellation, bounded map/reduce,
// panic-safe fire-and-forget goroutines, and a typed Semaphore.
//
// Commit G (June 2026): added Semaphore primitive so Ollama client
// (and any future bounded-fanout caller) can throttle programmatically
// instead of via the bare make(chan struct{}, N) idiom that has
// proliferated across the codebase. New code should use Semaphore;
// legacy `sem chan struct{}` sites are unchanged for back-compat.
package concurrent

import "context"

// ── Semaphore — typed config-bounded rate limiter ────────────────

// Semaphore is a configurable-width rate-limiting semaphore.
//
// It is thin a wrapper over a buffered channel; the cap(.) value is
// the maximum number of concurrent holders. Acquire blocks until a
// slot is available; Release returns the slot to the pool. The
// ctx-aware variant AcquireCtx returns ctx.Err() rather than blocking
// forever when the parent context is cancelled.
//
// Why a typed primitive vs `chan struct{}`: the bare-channel idiom
// has leaked into 8+ files (artlist run_orchestrator_stages.go,
// classifier.go, etc.) and the Acquire/Release semantics drift file
// to file. The typed primitive locks the acquisition-release contract
// in one place and lets a future CI gate (e.g. Check 47 — no bare
// chan struct outside the pkg/) gate the idiom.
//
// Compile-time assertions for the public surface that legacy
// callers can be migrated to Semaphore without API churn are
// intentionally absent: the migration is opt-in, not forced.
type Semaphore chan struct{}

// NewSemaphore constructs a Semaphore with the given max concurrency.
// Negative or zero values are rejected at construction (fail-fast:
// a misconfigured semaphore should surface loud at ctor time, not
// silently cap at 0 → block forever).
func NewSemaphore(max int) Semaphore {
	if max < 1 {
		panic("concurrent.NewSemaphore: max must be >= 1 (received " + itoa(max) + ")")
	}
	return make(Semaphore, max)
}

// Acquire blocks until a slot is available.
func (s Semaphore) Acquire() { s <- struct{}{} }

// Release returns a slot to the pool. Safe to call once per Acquire;
// double-release leaks a slot into the pool (caller's invariant to
// maintain, just like the bare-chan idom).
func (s Semaphore) Release() { <-s }

// AcquireCtx acquires a slot or returns ctx.Err() on cancellation.
// Use this variant when the caller has a context-aware deadline and
// does not want to block past ctx cancellation.
func (s Semaphore) AcquireCtx(ctx context.Context) error {
	select {
	case s <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TryAcquire returns true if a slot was acquired without blocking,
// false if the semaphore is full. Useful for non-blocking best-effort
// callers (cache warmup probes, opportunistic prefetch, etc).
func (s Semaphore) TryAcquire() bool {
	select {
	case s <- struct{}{}:
		return true
	default:
		return false
	}
}

// Cap returns the maximum number of concurrent holders. Read-only;
// mirror of the make(chan ...) cap width.
func (s Semaphore) Cap() int { return cap(s) }

// Helper: avoid importing strconv for the panic message — keep this
// file's import list lean.
//
// itoa renders non-negative integers in ASCII decimal. Negative ints
// (only possible if the caller violated the precondition; NewSemaphore
// panics on them) render with a leading minus sign.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = '0' + byte(n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
