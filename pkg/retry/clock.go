// Package retry — clock.go (FASE 3.8, July 2026).
//
// Injectable time source for retry-loop sleeps. The interface is
// intentionally narrow (single method) so tests can swap in a
// deterministic fake without pulling in a 3rd-party mocking
// framework. Production code uses RealClock; tests inject a fake
// clock that advances on demand.
//
// Why this seam exists (godlike/06 SSOT):
//
//  1. The FASE 3.8 mandate bans static `time.Sleep` in `internal/`
//     outside test files (CI gate: scripts/ci-architectural-checks.sh
//     Check N — added in Commit 4 of this bundle).
//  2. The migration targets (internal/app/shutdown.go,
//     internal/capabilities/jobs/worker/runner.go) need a ctx-aware
//     sleep that ALSO accepts a fake clock for deterministic test
//     coverage (no 100ms flake on slow CI).
//  3. Rewriting time.Sleep inline at the call site is error-prone:
//     the canonical `select { case <-time.After(d): case <-ctx.Done(): }`
//     pattern is duplicated, ctx cancellation is sometimes forgotten,
//     and the Options struct has no place to thread a Clock through.
//
// Solution: Clock interface (1 method) + RealClock production default
// + Options.Clock field (zero-value → RealClock fallback) + Sleep
// public helper.
//
// ═══════════ Components ═════════════════════════════════════════════════════
//
// (1) Clock — minimal injector surface.
//
//   type Clock interface { After(d time.Duration) <-chan time.Time }
//
// Deliberately one method: the only operation retry-loop sleeps need
// is "fire a channel after duration". Adding Now() or Sleep() would
// force callers that don't care about either to provide no-op
// implementations.
//
// (2) RealClock — production implementation.
//
//   type RealClock struct{}
//   func (RealClock) After(d) <-chan time.Time { return time.After(d) }
//
// Wraps time.After. Production callers never name RealClock
// explicitly — Options{} (zero Clock field) selects it via
// ClockFromOptions.
//
// (3) ClockFromOptions — the picker.
//
//   opts.Clock != nil → caller's clock (test injectability)
//   opts.Clock == nil → RealClock{} (production default)
//
// Centralised so callers don't need to nil-check themselves. Used
// from BOTH Sleep (the helper) AND DoWithValue (the canonical retry
// loop), keeping the "production default is RealClock" invariant
// in one place.
//
// (4) Sleep — the canonical ctx-aware block.
//
//   func Sleep(ctx, d, opts) error
//
// Blocks for d (or until ctx cancellation). Returns nil on normal
// completion; ctx.Err() on cancellation. FASE 3.8 migration target
// for static time.Sleep sites in internal/.
//
// (5) Options.Clock — the seam.
//
//   type Options struct { ...; Clock Clock }
//
// Production pass Options{} (zero Clock field → RealClock via the
// picker). Tests pass Options{Clock: fakeClock} for deterministic
// timing.
//
// ═══════════ Usage Examples ═════════════════════════════════════════════════════════
//
// Production (zero-value Options):
//
//     // internal/app/shutdown.go — was: time.Sleep(100 * time.Millisecond)
//     if err := retry.Sleep(context.Background(), 100*time.Millisecond, retry.Options{}); err != nil {
//         log.Warn("cancel during settle drain", zap.Error(err))
//     }
//
// Test (fake clock):
//
//     func TestSleep_CancelDuringWait(t *testing.T) {
//         clk := newFakeClock(time.Now())
//         ctx, cancel := context.WithCancel(context.Background())
//         go func() {
//             clk.Advance(50 * time.Millisecond)
//             cancel()
//         }()
//         err := retry.Sleep(ctx, 100*time.Millisecond, retry.Options{Clock: clk})
//         if !errors.Is(err, context.Canceled) {
//             t.Errorf("got %v; want context.Canceled", err)
//         }
//     }

package retry

import (
	"context"
	"time"
)

// ── Clock interface ──────────────────────────────────────────────────────────

// Clock is the minimal injectable time source for retry-loop sleeps.
// The interface is intentionally narrow (one method) so a fake
// implementation can be written in ~40 lines without depending on
// third-party mocking frameworks (see pkg/retry/clock_test.go for
// the canonical fakeClock shape).
//
// Production code does NOT pass Clock explicitly — Options{}
// (zero Clock field) selects RealClock via ClockFromOptions. The
// seam exists for test injectability (FASE 3.8 migration goal: a
// Clock-injected Sleep makes tests deterministic AND FAST instead
// of waiting on real wall-clock time).
type Clock interface {
	// After returns a channel that fires after duration d. The
	// canonical semantics match time.After: the channel is fired
	// exactly once and not closed. Callers select on the channel
	// alongside context.Done() so cancellation interrupts the wait.
	After(d time.Duration) <-chan time.Time
}

// ── RealClock ────────────────────────────────────────────────────────────────

// RealClock is the production Clock implementation. It delegates to
// time.After so production semantics are byte-identical to pre-CLOCK
// (FASE 3.8) behaviour.
//
// Production callers should not name RealClock{} explicitly in
// Options — leave Options.Clock zero and ClockFromOptions returns
// this. Tests that want to assert time semantics for OTHER
// components (e.g. RealClock.After behavior) can pin to RealClock{}
// to capture the production default.
//
// Compile-time assertion: RealClock MUST satisfy Clock. Drift here
// is a build failure, not a runtime None-pointer panic.
var _ Clock = RealClock{}

type RealClock struct{}

// After delegates to time.After. RealClock is a zero-size struct; it
// can be inlined or stack-allocated freely without measurement
// pressure (Clock is held only by Options, never copied in hot loops).
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// ── ClockFromOptions ─────────────────────────────────────────────────────────

// ClockFromOptions returns opts.Clock if non-nil. Otherwise it returns
// RealClock{} so callers never need to nil-check the Clock field.
//
// This is the single source of truth for the "nil Clock → real
// wall-clock" default. Both Sleep (the public helper) and
// DoWithValue (the canonical retry loop) call it; if the production
// default ever changes (e.g. to a monotonic-only clock for
// determinism), it changes here exactly once.
func ClockFromOptions(opts Options) Clock {
	if opts.Clock != nil {
		return opts.Clock
	}
	return RealClock{}
}

// ── Sleep ────────────────────────────────────────────────────────────────────

// Sleep blocks for d (or until ctx is cancelled) using the Clock
// from opts. Returns nil on normal completion; returns ctx.Err() on
// cancellation.
//
// FASE 3.8 migration target for static time.Sleep sites in
// internal/. The pre-FASE-3.8 sites that called bare time.Sleep are
// migrated to retry.Sleep(ctx, d, retry.Options{}) which preserves
// the production-default wall-clock behaviour via RealClock while
// ALSO surfacing a typed error path (ctx.Err()) instead of silently
// returning when cancellation lands while the goroutine is asleep.
//
// ctx-aware cancellation: pre-FASE-3.8 sites that used raw
// `time.Sleep(d)` did NOT honour SIGTERM or ctx cancellation during
// the sleep — a settle-drain of 100ms with cancellation mid-drain
// would block until d elapsed. Sleep selects on ctx.Done() so
// cancellation aborts the wait immediately, which is the canonical
// godlike/06 "fail-closed seam" pattern.
//
// Production callers pass retry.Options{} (zero Clock field).
// Tests pass Options{Clock: myFakeClock} for deterministic timing.
//
// Returning ctx.Err() (not nil) on cancellation lets callers
// distinguish "slept fully" from "interrupted by ctx" via
// errors.Is — important for shutdown paths where the next step is
// gated on the sleep completing (e.g. shutdown.go's settle drain
// followed by goroutine Stop with a hard timeout cap).
func Sleep(ctx context.Context, d time.Duration, opts Options) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clk := ClockFromOptions(opts)
	select {
	case <-clk.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
