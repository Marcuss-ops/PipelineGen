// Package retry — options.go (PR-SPLIT-RETRY-PKG, July 2026).
//
// Configuration + bounded-jitter math for the retry loop. The 5
// components in this file are the canonical knobs callers dial
// when invoking retry.Do / retry.DoWithValue:
//
//   (1) Options — the 8-field configuration struct.
//   (2) RetryOptions — backward-compatible type alias for Options
//       (kept for callers that name the type explicitly).
//   (3) DefaultOptions — the canonical starting point with JitterFraction
//       = 0.25 (±25% randomization kills thundering-herd retry storms).
//   (4) norm — defensive coalesce of zero-valued Options fields into
//       canonical defaults (so callers passing retry.Options{} get
//       the documented production sane-default surface).
//   (5) sleepDuration — exponential backoff with MaxBackoff cap and
//       bounded jitter math. Pure function: takes the attempt count
//       and the Options, returns the duration the next retry should
//       sleep before being called. math/rand global Float64 is safe
//       for concurrent use as of Go 1.20+ and is sufficient here
//       (this is NOT a security primitive — purely retry desync).
//
// ═══════════ Usage from retry.go (orchestrator) ═════════════════════════════
//
// retry.DoWithValue calls norm once before the loop starts and
// sleepDuration once per-retry-sleep. The two helpers live here
// rather than in retry.go so this file is the single canonical
// "options and jitter math" surface (godlike/06 SSOT one-canonical-
// owner-per-fact). The orchestrator composes them; this file owns
// them.
//
// Test surface: pkg/retry/retry_test.go pins exhaustion + envelope +
// clamping + jitter-variability + cap-after-saturation contracts
// (see TestDefaultOptions_Jitter25Enabled for the canonical default
// value, +TestSleepDuration_* for the math envelopes).

package retry

import (
	"math/rand"
	"time"
)

// ── Options ─────────────────────────────────────────────────────────────────

// Options configures the retry loop behaviour.
// All fields are optional — zero values fall back to sensible defaults.
type Options struct {
	// MaxAttempts is the maximum number of calls to fn (inclusive). Must be >= 1.
	// Default: 3.
	MaxAttempts int

	// InitialBackoff is the delay before the first retry.
	// Default: 1 * time.Second.
	InitialBackoff time.Duration

	// MaxBackoff caps the exponential backoff delay.
	// Default: 30 * time.Second.
	MaxBackoff time.Duration

	// BackoffFactor multiplies the backoff after each attempt.
	// Use 2.0 for exponential, 1.0 for constant.
	// Default: 2.0.
	BackoffFactor float64

	// IsRetryable is an optional predicate. If nil, every error is retried.
	// When non-nil, only errors for which it returns true will be retried;
	// non-retryable errors cause an immediate return.
	IsRetryable func(error) bool

	// JitterFraction adds random jitter to the backoff. Values 0.0–1.0.
	// Default: 0 (no jitter).
	JitterFraction float64

	// OnRetry is an optional callback invoked before each retry attempt.
	// The attempt number is 0-based (0 = first retry).
	OnRetry func(attempt int, err error)

	// Clock is the injectable time source for backoff sleeps.
	// Nil → RealClock{} (production default, delegates to time.After).
	// Tests inject a fake clock via Options{Clock: myFakeClock} for
	// deterministic duration assertions (no 100ms flake on slow CI).
	//
	// FASE 3.8 (July 2026): introduced to support the static
	// time.Sleep ban in internal/ — migration targets route through
	// retry.Sleep(ctx, d, opts) which in turn reads Options.Clock via
	// ClockFromOptions. DoWithValue reads it from the same picker so
	// retry-loop sleeps honour the injected clock consistently.
	Clock Clock
}

// RetryOptions is an alias for Options, kept for backward compatibility.
type RetryOptions = Options

// DefaultOptions returns a reasonable starting point for most use-cases.
//
// The default `JitterFraction: 0.25` enables ±25% randomization on top of
// the computed exponential backoff, reducing thundering-herd retry waves
// when many goroutines converge on the same transient error (e.g. the
// SQLite-locked retry storms when a hot row is enqueued from N workers
// in lockstep). Callers that need deterministic timing (e.g. CI latency
// assertions) can override with `JitterFraction: 0`.
func DefaultOptions() Options {
	return Options{
		MaxAttempts:    3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
		JitterFraction: 0.25,
	}
}

// ── bounded-default coalesce ───────────────────────────────────────────────

// norm defensively coalesces zero-valued Options fields into canonical
// defaults so callers passing retry.Options{} (the production shape)
// get the documented sane-default surface without per-field checks
// scattered through the orchestrator.
//
// Fields NOT coalesced:
//   - IsRetryable: zero value (nil) is semantically meaningful — "always
//     retry" — and is NOT coalesced into a sentinel predicate.
//   - OnRetry: zero value (nil) is the canonical "no observability callback"
//     surface; coalescing would silently make production callers opt
//     into telemetry they did not request.
//   - Clock: zero value (nil) is handled by ClockFromOptions (which
//     itself falls through to RealClock{}); coalescing here would
//     diverge from sleepDuration's downstream ClockFromOptions call.
func norm(o Options) Options {
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 3
	}
	if o.InitialBackoff <= 0 {
		o.InitialBackoff = 1 * time.Second
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 30 * time.Second
	}
	if o.BackoffFactor <= 0 {
		o.BackoffFactor = 2.0
	}
	return o
}

// ── exponential backoff + bounded jitter ───────────────────────────────────

// sleepDuration computes the delay before retry attempt `attemptCount`
// (0-based: 0 = first retry's wait). The computation is:
//
//	delay = InitialBackoff × BackoffFactor^attemptCount
//
// capped at MaxBackoff, then jittered by ±JitterFraction:
//
//	delay := delay - jitter + jitter*2.0*rand.Float64()
//
// where jitter = delay * JitterFraction. With f=0 jitter is disabled;
// values outside [0.0, 1.0] are clamped (defensive: prevents negative
// or super-multiplicative delays from a typo'd option).
//
// math/rand global Float64 IS safe for concurrent use as of Go 1.20+
// and is sufficient here — this is not a security primitive, it is
// retry desynchronisation. Thundering-herd retry storms (e.g. N workers
// converging on the same SQLite-locked hot row in lockstep) desync
// because independent goroutines sample independent rand.Float64 draws.
//
// The MaxBackoff cap is applied BEFORE the jitter, so saturation AND
// jitter compose correctly: at cap, the jitter envelope is
// [cap×(1-f), cap×(1+f)] rather than a wider range that would defeat
// the cap's intent.
func sleepDuration(attemptCount int, opts Options) time.Duration {
	delay := float64(opts.InitialBackoff)
	for i := 0; i < attemptCount; i++ {
		delay *= opts.BackoffFactor
		if time.Duration(delay) > opts.MaxBackoff {
			delay = float64(opts.MaxBackoff)
			break
		}
	}
	if time.Duration(delay) > opts.MaxBackoff {
		delay = float64(opts.MaxBackoff)
	}
	// Apply bounded jitter: a uniform random factor in [1 - f, 1 + f]
	// multiplies the (already capped) delay. JitterFraction=0 disables
	// jitter; values outside [0, 1.0] are clamped (defensive: prevents
	// negative or super-multiplicative delays from a typo'd option).
	//
	// math/rand global Float64 is safe for concurrent use as of Go 1.20+
	// and is sufficient for jitter (this is not a security primitive).
	// Thunderbolt-fleet retry storms desynchronize via this factor.
	f := opts.JitterFraction
	if f < 0 {
		f = 0
	}
	if f > 1.0 {
		f = 1.0
	}
	if f > 0 {
		jitter := delay * f
		delay = delay - jitter + jitter*2.0*rand.Float64()
	}
	return time.Duration(delay)
}
