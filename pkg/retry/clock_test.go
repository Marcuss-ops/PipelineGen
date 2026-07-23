// Package retry — clock_test.go (FASE 3.8, July 2026).
//
// Test coverage for the injectable Clock seam (pkg/retry/clock.go):
//
//   (a) TestSleep_FakeClock_BasicCompletion: Sleep returns nil when the
//       injected fake clock's After channel fires.
//   (b) TestSleep_ContextCancelReturnsErr: Sleep returns ctx.Err() when
//       ctx is already cancelled at call entry.
//   (c) TestSleep_FakeClock_ContextCancelDuringSleep: ctx cancel
//       mid-sleep surfaces ctx.Err() on the same iteration's select.
//   (d) TestSleep_DefaultsToRealClock: Options{} (zero Clock field)
//       selects RealClock and the sleep completes against real time.
//   (e) TestSleep_RealClockWiringParity: Options{Clock: RealClock{}}
//       matches the zero-value default path semantically.
//   (f) TestClockFromOptions_RealClockFallback: nil Clock → RealClock{}
//       at the picker.
//   (g) TestClockFromOptions_NonNilClockWins: when opts.Clock is set,
//       the picker returns exactly that — fallback does NOT kick in.
//   (h) TestDoWithValue_FakeClock_AdvancesThroughRetries: a fake-clock
//       retry-loop test where the wall clock never advances enough to
//       matter — the canonical "Clock injection makes retry-loop tests
//       deterministic AND fast" claim.
//   (i) TestOptions_ZeroValueEqualsRealClockWiring: production
//       backwards-compat — Options{} (zero Clock) ≡ Options{Clock: RealClock{}}
//       on terminal-bail behaviour.

package retry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// ── fakeClock test double ─────────────────────────────────────────────────────

// fakeTimer is the internal queued-channel state for fakeClock.
// `called` is set when the timer fires (via Advance) so the slice
// can compact on each Advance call.
type fakeTimer struct {
	fireAt time.Time
	ch     chan time.Time
	called bool
}

// fakeClock is a manual-advance test double for the Clock interface.
// Each After(d) call adds a (fireAt = now+d, ch) to the queue. Calling
// Advance(d) bumps now forward by d AND fires any timer whose fireAt
// has been reached (≤ new now). Fired timers are removed from the
// queue to keep Advance O(remaining) not O(all-time).
//
// Concurrency: mu guards now + timers. Advance can be called from a
// goroutine while other goroutines call After(); both paths take
// the lock. This is sufficient for the FASE 3.8 test surface where
// at most 2-3 goroutines coexist.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []fakeTimer
}

// Compile-time assertion: fakeClock MUST satisfy Clock. Drift here
// is a build failure, not a runtime nil-pointer panic. Pinned in
// the test side of the package so production binaries are unaffected.
var _ Clock = (*fakeClock)(nil)

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

// After returns a buffered channel (cap 1) so the fake can fire it
// from Advance without blocking on the consumer side.
func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.timers = append(c.timers, fakeTimer{
		fireAt: c.now.Add(d),
		ch:     ch,
	})
	return ch
}

// Advance sets now = now + d and fires any unfired timer whose
// fireAt has been reached.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	remaining := c.timers[:0]
	for _, t := range c.timers {
		if !t.called && !c.now.Before(t.fireAt) {
			select {
			case t.ch <- t.fireAt:
			default:
				// already drained; safe to drop
			}
			t.called = true
		}
		if !t.called {
			remaining = append(remaining, t)
		}
	}
	c.timers = remaining
}

// ── (a) Sleep basic completion ──────────────────────────────────────────────

// TestSleep_FakeClock_BasicCompletion: Sleep returns nil when the
// fake-clock's After channel fires. The test goroutine advances
// the clock AFTER Sleep is in flight (to simulate time passing)
// and asserts Sleep returns nil < 50ms (NOT 100ms wall-clock).
func TestSleep_FakeClock_BasicCompletion(t *testing.T) {
	clk := newFakeClock(time.Now())
	go func() {
		time.Sleep(5 * time.Millisecond)
		clk.Advance(100 * time.Millisecond)
	}()
	start := time.Now()
	err := Sleep(context.Background(), 100*time.Millisecond, Options{Clock: clk})
	elapsed := time.Since(start)
	if err != nil {
		t.Errorf("Sleep = %v; want nil on normal completion", err)
	}
	if elapsed >= 50*time.Millisecond {
		t.Errorf("Sleep took %v wall-clock; want < 50ms (fake clock should make the sleep fast)", elapsed)
	}
}

// ── (b) Context cancellation pre-call ───────────────────────────────────────

// TestSleep_ContextCancelReturnsErr: a pre-cancelled context short-
// circuits Sleep BEFORE consulting the clock (no After() channel
// is created). Returns ctx.Err() immediately.
func TestSleep_ContextCancelReturnsErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Sleep(ctx, 100*time.Millisecond, Options{Clock: newFakeClock(time.Now())})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Sleep = %v; want context.Canceled", err)
	}
}

// ── (c) Context cancellation via cancel() in goroutine ─────────────────────

// TestSleep_FakeClock_ContextCancelDuringSleep: ctx is cancelled
// while Sleep is in the select; cancel goroutine wins the select
// race (synchronously BEFORE the clock advance, since we never
// Advance from the goroutine in this test).
func TestSleep_FakeClock_ContextCancelDuringSleep(t *testing.T) {
	clk := newFakeClock(time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	err := Sleep(ctx, 100*time.Millisecond, Options{Clock: clk})
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Sleep = %v; want context.Canceled", err)
	}
	if elapsed >= 50*time.Millisecond {
		t.Errorf("Sleep took %v wall-clock; want < 50ms (ctx-cancel must short-circuit)", elapsed)
	}
}

// ── (d) Default to RealClock on empty Options ───────────────────────────────

// TestSleep_DefaultsToRealClock: Options{} selects RealClock via the
// picker. The sleep uses real wall-clock time (verified by elapsed
// ≥ 5ms — the real clock, not a fake, ticks).
func TestSleep_DefaultsToRealClock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := Sleep(ctx, 10*time.Millisecond, Options{})
	elapsed := time.Since(start)
	if err != nil {
		t.Errorf("Sleep = %v; want nil on normal completion", err)
	}
	if elapsed < 5*time.Millisecond {
		t.Errorf("Sleep elapsed = %v; want >= 5ms (real clock should make the wait real)", elapsed)
	}
	if elapsed >= 100*time.Millisecond {
		t.Errorf("Sleep elapsed = %v; want < 100ms (10ms + jitter budget)", elapsed)
	}
}

// ── (e) RealClock wiring parity ─────────────────────────────────────────────

// TestSleep_RealClockWiringParity: Options{Clock: RealClock{}} must
// match the zero-value default path semantically.
func TestSleep_RealClockWiringParity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := Sleep(ctx, 10*time.Millisecond, Options{Clock: RealClock{}})
	if err != nil {
		t.Errorf("Sleep(real) = %v; want nil on normal completion", err)
	}
}

// ── (f) ClockFromOptions picker — nil fallback ──────────────────────────────

// TestClockFromOptions_RealClockFallback: nil Clock → RealClock{}
// (non-nil, the canonical concrete).
func TestClockFromOptions_RealClockFallback(t *testing.T) {
	got := ClockFromOptions(Options{})
	if got == nil {
		t.Fatal("ClockFromOptions(Options{}) returned nil; want RealClock{} fallback")
	}
	if _, ok := got.(RealClock); !ok {
		t.Errorf("ClockFromOptions returned %T; want RealClock", got)
	}
}

// ── (g) ClockFromOptions picker — non-nil wins ──────────────────────────────

// TestClockFromOptions_NonNilClockWins: when opts.Clock is set, the
// picker returns exactly that — RealClock fallback does NOT kick in.
func TestClockFromOptions_NonNilClockWins(t *testing.T) {
	clk := newFakeClock(time.Now())
	got := ClockFromOptions(Options{Clock: clk})
	if got != Clock(clk) {
		t.Errorf("ClockFromOptions returned %v; want the injected fakeClock", got)
	}
}

// ── (h) DoWithValue(fake clock) determinism ────────────────────────────────

// TestDoWithValue_FakeClock_AdvancesThroughRetries: validates that
// the FASE 3.8 Clock hit in DoWithValue's backoff sleep lets
// tests advance through N retry intervals without ever sleeping
// enough to matter wall-clock-wise.
//
// 200-iter × 1ms pump: robust against scheduler races (even if
// Advance fires before DoWithValue registers its timer, the next
// Advance call still covers any pending fireAt once accumulated
// clock has reached it).
func TestDoWithValue_FakeClock_AdvancesThroughRetries(t *testing.T) {
	clk := newFakeClock(time.Now())
	go func() {
		for i := 0; i < 200; i++ {
			time.Sleep(1 * time.Millisecond)
			clk.Advance(50 * time.Millisecond)
		}
	}()

	var calls int
	walk := func() (struct{}, error) {
		calls++
		// FASE 6 Cut 6.1.D migration: production IsTransient became
		// pure typed-probe; raw SDK strings no longer match. Wrap the
		// walk-fn return in a *TransientInfrastructureError envelope so
		// the IsTransient gate sees retryable=true and DoWithValue retries
		// per the test's MaxAttempts=3 specification. Mirrors the
		// canonical SDK-boundary contract (godlike/06 SSOT).
		return struct{}{}, &TransientInfrastructureError{Err: errors.New("transient: timeout")}
	}

	start := time.Now()
	_, err := DoWithValue(context.Background(), walk, Options{
		MaxAttempts:    3,
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
		BackoffFactor:  1.0,
		JitterFraction: 0,
		DisableJitter:  true,
		IsRetryable:    IsTransient,
		Clock:          clk,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("want transient err after MaxAttempts; got nil")
	}
	if !IsTransient(err) {
		t.Errorf("err should classify as transient; got %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d; want 3 (MaxAttempts exhausted)", calls)
	}
	if elapsed >= 200*time.Millisecond {
		t.Errorf("DoWithValue elapsed = %v; want < 200ms (fake clock should make the loop fast)", elapsed)
	}
}

// ── (i) Backwards-compatibility: zero-value Options == RealClock-Options ─────

// TestOptions_ZeroValueEqualsRealClockWiring: pins the production-
// backwards-compat contract — Options{} (zero Clock field) must
// produce observably-identical fn-call count and error propagation
// to Options{Clock: RealClock{}} when the IsRetryable predicate
// always returns false (so neither path ever sleeps).
func TestOptions_ZeroValueEqualsRealClockWiring(t *testing.T) {
	walk := func() (struct{}, error) {
		return struct{}{}, errors.New("terminal error: bail")
	}
	optsZero := Options{
		MaxAttempts: 5,
		IsRetryable: func(error) bool { return false },
	}
	optsReal := Options{
		MaxAttempts: 5,
		Clock:       RealClock{},
		IsRetryable: func(error) bool { return false },
	}

	_, errZero := DoWithValue(context.Background(), walk, optsZero)
	_, errReal := DoWithValue(context.Background(), walk, optsReal)

	if errZero == nil || errReal == nil {
		t.Fatal("want err from terminal predicate; got nil")
	}
	if errZero.Error() != errReal.Error() {
		t.Errorf("errZero=%v vs errReal=%v: must match", errZero, errReal)
	}
}
