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
//   (5) BackoffFor — exponential backoff with MaxBackoff cap and
//       bounded jitter math. Pure function: takes the attempt count
//       and the Options, returns the duration the next retry should
//       sleep before being called. math/rand global Float64 is safe
//       for concurrent use as of Go 1.20+ and is sufficient here
//       (this is NOT a security primitive — purely retry desync).
//
// ═══════════ Usage from retry.go (orchestrator) ═════════════════════════════
//
// retry.DoWithValue calls norm once before the loop starts and
// BackoffFor once per-retry-sleep. The two helpers live here
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
	// Default: 0.25 (applied by norm unless DisableJitter is true).
	//
	// Setting JitterFraction to 0 alone is treated as "use the canonical
	// default" so that callers who do not specify a fraction still get the
	// production-safe ±25% desync. To explicitly disable jitter, set
	// DisableJitter to true.
	JitterFraction float64

	// DisableJitter explicitly disables the canonical default jitter.
	// Use this when deterministic timing is required (e.g. CI latency
	// assertions). When false, JitterFraction=0 is upgraded to the
	// canonical 0.25 default by norm.
	DisableJitter bool

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
// assertions) must set `DisableJitter: true` (not merely `JitterFraction: 0`,
// which norm() upgrades to the canonical 0.25 default).
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
// Fields NOT coalesced via a callback-changer:
//   - OnRetry: zero value (nil) is the canonical "no observability callback"
//     surface; coalescing would silently make production callers opt
//     into telemetry they did not request.
//   - Clock: zero value (nil) is handled by ClockFromOptions (which
//     itself falls through to RealClock{}); coalescing here would
//     diverge from BackoffFor's downstream ClockFromOptions call.
//
// Fase 6(a) strict change (no backward-compatible default): when
// IsRetryable is nil, norm sets it to a FAIL-CLOSED no-retry predicate
// (returns false unconditionally). Pre-Fase-6 the zero-value was
// "always retry" (DoWithValue's `if opts.IsRetryable != nil` check
// skipped the predicate on nil), which silently retried terminal
// failures (auth-revoked, missing-handler, validation) up to
// MaxAttempts. The Fase 6(a) user spec explicitly forbids
// "IsRetryable==nil means retry always": nil MUST be fail-closed
// noop, NEVER "retry everything". Callers who want the legacy
// behaviour MUST pass an explicit predicate.
//
// Migration path: any retry.Do / retry.DoWithValue site that does NOT
// pass IsRetryable today MUST be updated to pass an explicit
// predicate (typically retry.IsTransient for the existing
// substring-path surface, or a typed-only Decision() call in
// Push 6.1.x follow-ups). The fail-closed default is intentional:
// unknown predicates are safer than retry-on-everything during
// the migration window.
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
	if o.IsRetryable == nil {
		// godlike/07 fail-closed (Fase 6(a), July 2026): nil predicate
		// MUST NOT mean "retry always". The default predicates against
		// every error (retryable = false) so the caller gets back the
		// FIRST attempt's error verbatim without retrying. Push 6.1.x
		// callers MUST pass an explicit IsRetryable: classifier,
		// matching the typed-only adapter surface.
		o.IsRetryable = neverRetry
	}
	// FASE 6(a) fix (July 2026): zero-value Options MUST apply the
	// canonical JitterFraction=0.25, not the historical JitterFraction=0
	// (which silently disabled jitter for callers passing retry.Options{}
	// instead of DefaultOptions()). The JitterFraction value is the
	// single most-missed noise-control knob: zero jitter collapses
	// N workers in lockstep onto the same retry instant, producing the
	// canonical "thundering-herd retry storm" pattern documented in
	// the FASE 2 stock_e2e/02_poll_terminal.json fixture. Coalescing
	// here — rather than ONLY in DefaultOptions() — means a caller
	// passing retry.Options{IsRetryable: predicate} gets the canonical
	// 25% desync envelope for free.
	//
	// DisableJitter is the explicit opt-out: callers that need
	// deterministic timing (e.g. CI latency assertions) set
	// DisableJitter=true. When DisableJitter is true, JitterFraction=0
	// is left untouched and BackoffFor skips the jitter step entirely.
	// When DisableJitter is false, JitterFraction=0 is upgraded to the
	// canonical 0.25 default. Negative or out-of-range values are
	// clamped by BackoffFor, not here.
	if o.JitterFraction == 0 && !o.DisableJitter {
		o.JitterFraction = 0.25
	}
	return o
}

// neverRetry is the canonical fail-closed no-retries predicate.
// Used by norm when IsRetryable is nil. Production callers MUST NOT
// depend on this default; it exists only so a forgotten IsRetryable
// doesn't accidentally retry every error.
//
// godlike/07 NO-FAKE-AVAILABILITY rationale: a production caller that
// passes Options{IsRetryable: nil} and gets neverRetry as the
// default is still functional — first-attempt's error is surfaced,
// no retries are burned on terminal shapes. The default is a SAFETY
// VALVE, not a feature.
func neverRetry(error) bool { return false }

// ── exponential backoff + bounded jitter ───────────────────────────────────

// BackoffFor computes the delay before retry attempt `attemptCount`
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
//
// godlike/06 SSOT (one-canonical-owner-per-fact): this function is
// the SOLE canonical owner of the "compute exponential backoff" math
// across the PipelineGen codebase. Callers needing a one-shot backoff
// duration (e.g. SQL scheduler's `available_at` write, monitor's
// `nextCheckTime`) call BackoffFor with an explicit Options literal
// instead of inlining their own math. Callers needing a retry loop
// call retry.Do / retry.DoWithValue (which routes its per-retry sleep
// through this same function).
//
// NOTE on norm(): BackoffFor does NOT call norm(opts) on the inbound
// Options. The retry-loop caller already invokes norm inside
// DoWithValue before the loop starts; one-shot callers must pass an
// explicit Options literal (InitialBackoff / BackoffFactor / MaxBackoff
// / JitterFraction). This preserves deterministic scheduling — a
// server-side SQL `available_at` write must NOT carry hidden jitter;
// the schedule value is the persisted retry target, not a random
// pre-sleep duration.
func BackoffFor(attemptCount int, opts Options) time.Duration {
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
	// multiplies the (already capped) delay. DisableJitter=true always
	// disables jitter. JitterFraction=0 disables jitter here in
	// BackoffFor, but callers passing Options through retry.Do or
	// retry.DoWithValue should note that norm() upgrades JitterFraction=0
	// to the canonical 0.25 default unless DisableJitter=true. Values
	// outside [0, 1.0] are clamped (defensive: prevents negative or
	// super-multiplicative delays from a typo'd option).
	//
	// math/rand global Float64 is safe for concurrent use as of Go 1.20+
	// and is sufficient for jitter (this is not a security primitive).
	// Thunderbolt-fleet retry storms desynchronize via this factor.
	f := opts.JitterFraction
	if opts.DisableJitter {
		f = 0
	}
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
