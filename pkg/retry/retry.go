// Package retry is the canonical repository-wide primitive for two
// intertwined concerns:
//
//  1. Transient-infrastructure error classification — "should I retry
//     this?" should always be answered with the same predicate.
//  2. Bounded retry loops with exponential backoff + jitter — long-running
//     operations on flaky infrastructure (HTTP, SQLite, eSDK, broker)
//     route through this package instead of bespoke retry loops.
//
// Step 7 (July 2026) consolidated three previously-duplicated retry
// implementations into this single package:
//
//   - monitor.isTransientEnqueueError            (deleted)
//   - tagutil.IsTransientDownloadError           (deleted)
//   - youtube/usecase.IsTransientExtractionError (kept as a thin wrapper
//     that delegates to retry.IsTransient so the typed-path
//     authoritativeness from *ExtractionError is preserved)
//
// Anything that needs a transient-classifier or a retry-loop MUST route
// through this package. CI gate (Azione 8/8D) bans substring-match
// retry-classifiers outside pkg/retry.
//
// ═══════════ Components ═════════════════════════════════════════════════════
//
// (1) TransientInfrastructureError — typed carrier for transient errors.
//
//   - Marks an error as transient at a typed level (not via substring).
//   - errors.As(err, &TransientInfrastructureError{}) is the canonical
//     "is this transient?" probe: preferred over string-matching because
//     the typed path is robust to upstream message-format changes.
//   - Unwrap surfaces the inner error for errors.Is / errors.As chains.
//
// (2) IsTransient — canonical "should I retry this?" predicate.
//
//   - Returns true when err is non-nil AND either:
//     (a) err is or wraps a *TransientInfrastructureError (typed path), OR
//     (b) err.Error() contains one of the canonical transient-substrings
//         in (4) below (substring fallback).
//   - Pass it to retry.Do as the IsRetryable Option. This is the SINGLE
//     canonical retry-classifier for the whole codebase.
//
// (3) WrapTransient — canonical typed-wrapping helper at infra boundaries.
//
//   - Returns err wrapped in *TransientInfrastructureError when err matches
//     the canonical taxonomy; otherwise returns err unchanged.
//   - Idempotent: already-typed errors pass through (no double-wrap).
//   - Nil-safe: returns nil for nil.
//   - Use at SQLite / HTTP / SDK boundaries so the typed path reaches
//     retry.IsTransient authoritatively on farther-up retry loops.
//
// (4) transientSubstrings taxonomy — canonical substring fallback.
//
//   timeout, connection refused, connection reset, connection is already
//   closed, eof, 429, 503, 502, 504, rate limit, quota exceeded,
//   temporarily unavailable, resource temporarily unavailable,
//   database is locked, sqlite busy.
//
//   These are the substring-path fallback. Where possible, prefer typed
//   wrapping (1+3) for new code; the substring path is retained as a
//   safety net for raw SDK errors not yet tagged at the typed layer.
//
// (5) DefaultOptions + bounded jitter.
//
//   - MaxAttempts:     3
//   - InitialBackoff:  1 * time.Second
//   - MaxBackoff:      30 * time.Second
//   - BackoffFactor:   2.0 (exponential)
//   - JitterFraction:  0.25 (±25% uniform randomisation)
//
//   The default ±25% jitter kills thundering-herd retry storms when many
//   goroutines converge on the same transient error (e.g. N workers
//   enqueuing from a SQLite-locked hot row in lockstep). With
//   JitterFraction=0 every retry attempt would sleep the same interval —
//   defeating the "spread out" intent of retry. JitterFraction is clamped
//   to [0, 1] defensively (negative or >1 values become 0 / 1 respectively).
//
// ═══════════ Usage Examples ═════════════════════════════════════════════════════════
//
// Example 1 — monitor/enqueue.go (channel-monitor discovery lease commit).
// Canonical pattern: retry.DoWithValue wraps the broker call, retry.IsTransient
// gates on transient classification at the typed path.
//
//	// internal/application/assets/monitor/enqueue.go
//	err := retry.DoWithValue(ctx, func() (int64, error) {
//	    return m.repo.MarkEnqueued(ctx, id, "committed")
//	}, retry.Options{
//	    MaxAttempts:    5,
//	    InitialBackoff: 100 * time.Millisecond,
//	    MaxBackoff:     2 * time.Second,
//	    IsRetryable:    retry.IsTransient,  // canonical predicate
//	})
//
// Example 2 — images/storage_search.go (DuckDuckGo page retrieval).
// Canonical pattern: HTTP fetch wrapped in retry.Do, retry.IsTransient as the
// IsRetryable predicate, transient classification centralized in pkg/retry.
//
//	// internal/application/images/storage_search.go
//	err := retry.Do(ctx, func() error {
//	    req, reqErr := http.NewRequestWithContext(ctx, "GET", imgURL, nil)
//	    if reqErr != nil {
//	        return fmt.Errorf("%w: create request: %v", ErrImageTransient, reqErr)
//	    }
//	    resp, doErr := s.client.Do(req)
//	    if doErr != nil {
//	        return fmt.Errorf("%w: download: %v", ErrImageTransient, doErr)
//	    }
//	    // ... classify HTTP status, return ErrImageTransient on retryable ...
//	    return nil
//	}, retry.Options{
//	    MaxAttempts:    3,
//	    InitialBackoff: 200 * time.Millisecond,
//	    IsRetryable:    retry.IsTransient,  // canonical predicate
//	})
//
// Example 3 — Drive SDK error wrapping at the adapter exit (Azione 5/8).
// Canonical pattern: retry.WrapTransient at the raw SDK exit promotes any
// transient-classified googleapi.Error to a typed carrier. Farther-up retry
// loops that query retry.IsTransient hit the typed path FIRST (via errors.As)
// and short-circuit on terminal errors without substring matching.
//
//	// internal/infrastructure/drive/uploader.go
//	created, err := u.Service.Files.Create(file).Context(ctx).Do()
//	if err != nil {
//	    // typed wrap: transient googleapi.Error (429, 503, timeout) becomes
//	    // *TransientInfrastructureError; terminal errors (404, 403) pass through.
//	    return nil, fmt.Errorf("drive put failed: %w", retry.WrapTransient(err))
//	}
//
// ═══════════════════════════════════════════════════════════════════════════════════════
package retry

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"time"
)

// ── TransientInfrastructureError ────────────────────────────────────────────

// TransientInfrastructureError wraps an error to mark it as
// transient (retryable) at a typed level. Callers that know an
// error is infrastructure-level (e.g. SQLite locked, HTTP 503,
// DNS timeout) can wrap the original error with this type so
// downstream IsTransient predicates don't need substring matching.
//
// Usage:
//
//	if isSQLiteLocked(err) {
//	    return &retry.TransientInfrastructureError{Err: err}
//	}
//
// Better: use the in-package `WrapTransient(err)` helper which
// idempotently wraps based on the canonical taxonomy, so callers
// don't need their own ad-hoc predicate.
//
// The Unwrap method surfaces the inner error for errors.Is / errors.As.
type TransientInfrastructureError struct {
	Err error
}

func (e *TransientInfrastructureError) Error() string {
	if e.Err == nil {
		return "transient infrastructure error"
	}
	return e.Err.Error()
}

func (e *TransientInfrastructureError) Unwrap() error {
	return e.Err
}

// ── IsTransient ─────────────────────────────────────────────────────────────

// transientSubstrings is the canonical taxonomy of transient-infrastructure
// error substrings. Mirrors the taxonomy previously duplicated in
// monitor/enqueue.go::isTransientEnqueueError (removed Step 7, July 2026).
// Azione 4/8 (July 2026) added the SQLite-canonical markers below:
//
//   - "database is locked"   — SQLite busy marker (5.x: SQLITE_BUSY)
//   - "sqlite busy"          — mattn/go-sqlite3 prefix
//   - "connection is already closed" — sql.ErrConnDone.Error()
//
// NOTE on sqlassets.ErrStateConflict: it is a typed *logical* sentinel
// ("row state is in conflict"). The canonical contract today is that
// this error remains TERMINAL (callers explicitly force retryable=false
// after `errors.Is(err, sqlassets.ErrStateConflict)`). It is therefore
// not added to transientSubstrings — doing so would silently flip a
// terminal logical error to a transient infra error.
var transientSubstrings = []string{
	"timeout",
	"connection refused",
	"connection reset",
	"connection is already closed",
	"eof",
	"429",
	"503",
	"502",
	"504",
	"rate limit",
	"quota exceeded",
	"temporarily unavailable",
	"resource temporarily unavailable",
	"database is locked",
	"sqlite busy",
	// Google API / gRPC canonical shapes (Azione 8/8F di Step 7, July 2026).
	// Each entry is the lowercase no-space form; case-insensitive matching
	// against lowercased err.Error() accepts upstream camelCase / SNAKE_CASE
	// shapes (userRateLimitExceeded, deadlineExceeded, backendError,
	// serviceUnavailable, quotaExceeded, Resource_Exhausted).
	"userratelimitexceeded",
	"deadlineexceeded",
	"backenderror",
	"serviceunavailable",
	"quotaexceeded",
	"resource_exhausted",
}

// RetryableError is a structural interface for errors that carry their
// own retryability classification. Any error type with an IsRetryable()
// bool method satisfies this interface — no import of pkg/retry required.
//
// Example: Qdrant's *APIError has IsRetryable() bool (returns
// APIError.Retryable), so it satisfies RetryableError automatically.
// Image retrieval's ErrImageTransient can implement IsRetryable() bool
// { return true } to signal transient semantics without importing pkg/retry.
//
// Checked by IsTransient BEFORE the substring-fallback path so typed
// errors always win over heuristic matching.
type RetryableError interface {
	IsRetryable() bool
}

// IsTransient returns true when err is non-nil AND either:
//   - err implements RetryableError and IsRetryable() returns true, OR
//   - err is (or wraps) a *TransientInfrastructureError, OR
//   - err.Error() contains one of the canonical transient-infrastructure
//     substrings (timeout, connection refused, 429, 503, etc.).
//
// Decision order (typed wins over substring):
//   1. nil → false
//   2. RetryableError interface → IsRetryable() (typed authoritative path)
//   3. *TransientInfrastructureError via errors.As → true
//   4. Substring fallback against transientSubstrings
//   5. Everything else → false
//
// This is the single canonical "should I retry this?" predicate for
// the whole codebase. Callers that previously implemented their own
// substring matcher (monitor.isTransientEnqueueError,
// tagutil.IsTransientDownloadError, youtube/usecase.IsTransientExtractionError)
// should migrate to this function and, where typed wrapping is feasible,
// use WrapTransient.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	// Typed path #1: RetryableError interface (authoritative).
	var re RetryableError
	if errors.As(err, &re) && re.IsRetryable() {
		return true
	}
	// Typed path #2: TransientInfrastructureError carrier.
	var te *TransientInfrastructureError
	if errors.As(err, &te) {
		return true
	}
	// Substring fallback.
	lower := strings.ToLower(err.Error())
	for _, s := range transientSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// IsTransientString is the string-only version of IsTransient. It performs
// the substring-fallback check against the canonical transientSubstrings
// taxonomy without requiring an error type. Useful when the caller already
// has an error message string (e.g., from an ExtractItem.Error field).
//
// Does NOT check RetryableError or TransientInfrastructureError — this
// is a pure substring matcher. For typed errors, use IsTransient.
func IsTransientString(s string) bool {
	lower := strings.ToLower(s)
	for _, token := range transientSubstrings {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

// ── WrapTransient ───────────────────────────────────────────────────────────

// WrapTransient returns err wrapped in *TransientInfrastructureError
// when err is already transient (per IsTransient), otherwise returns
// err unchanged. Idempotency: if err is already or wraps a
// *TransientInfrastructureError, returns err unchanged (no double
// wrap). Nil-safe: returns nil for nil input.
//
// Typical usage at SQLite / HTTP / DB boundary:
//
//	if err := db.Exec(...); err != nil {
//	    return retry.WrapTransient(err)  // typed-transient only if transient
//	}
//
// Pair with IsTransient for retry control flow:
//
//	err = retry.WrapTransient(rawErr)
//	if retry.IsTransient(err) { ... }
//
// WrapTransient is the canonical migration target for ad-hoc inline
// substring matchers at infra boundaries (Azione 4/8 di Step 7).
func WrapTransient(err error) error {
	if err == nil {
		return nil
	}
	var te *TransientInfrastructureError
	if errors.As(err, &te) {
		return err
	}
	if IsTransient(err) {
		return &TransientInfrastructureError{Err: err}
	}
	return err
}

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

// Do calls fn repeatedly until it succeeds, the context is cancelled, or the
// retry budget is exhausted. If fn returns a non-retryable error (per
// opts.IsRetryable) the loop exits immediately.
//
// A nil error from fn means success.
// If all attempts fail, the last error is returned.
func Do(ctx context.Context, fn func() error, opts Options) error {
	_, err := DoWithValue(ctx, func() (struct{}, error) {
		return struct{}{}, fn()
	}, opts)
	return err
}

// DoWithValue is the generic version of Do. fn returns (T, error).
// On success the value is returned; on permanent failure the zero value of T
// is returned together with the last error.
func DoWithValue[T any](ctx context.Context, fn func() (T, error), opts Options) (T, error) {
	opts = norm(opts)

	var lastErr error
	for i := 0; i < opts.MaxAttempts; i++ {
		if err := ctx.Err(); err != nil {
			var zero T
			return zero, err
		}

		val, err := fn()
		if err == nil {
			return val, nil
		}

		lastErr = err

		if opts.IsRetryable != nil && !opts.IsRetryable(err) {
			var zero T
			return zero, err
		}

		if i < opts.MaxAttempts-1 {
			if opts.OnRetry != nil {
				opts.OnRetry(i, err)
			}
			sleep := sleepDuration(i, opts)
			select {
			case <-time.After(sleep):
			case <-ctx.Done():
				var zero T
				return zero, ctx.Err()
			}
		}
	}
	var zero T
	return zero, lastErr
}

// ── internal helpers ──────────────────────────────────────────────────────────

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
