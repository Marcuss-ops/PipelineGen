// Package retry provides a unified retry primitive with exponential backoff,
// context awareness, and configurable retryable-error predicates.
//
// Replaces duplicated retry implementations across the codebase.
package retry

import (
	"context"
	"errors"
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
var transientSubstrings = []string{
	"timeout",
	"connection refused",
	"connection reset",
	"eof",
	"429",
	"503",
	"502",
	"504",
	"rate limit",
	"quota exceeded",
	"temporarily unavailable",
	"resource temporarily unavailable",
}

// IsTransient returns true when err is non-nil AND either:
//   - err is (or wraps) a *TransientInfrastructureError, OR
//   - err.Error() contains one of the canonical transient-infrastructure
//     substrings (timeout, connection refused, 429, 503, etc.).
//
// This is the single canonical "should I retry this?" predicate for
// the whole codebase. Callers that previously implemented their own
// substring matcher (monitor.isTransientEnqueueError,
// tagutil.IsTransientDownloadError, youtube/usecase.IsTransientExtractionError)
// should migrate to this function and, where typed wrapping is feasible,
// use TransientInfrastructureError.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var te *TransientInfrastructureError
	if errors.As(err, &te) {
		return true
	}
	lower := strings.ToLower(err.Error())
	for _, s := range transientSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
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
func DefaultOptions() Options {
	return Options{
		MaxAttempts:    3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
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
	// Apply jitter: random fraction of the current delay.
	// JitterFraction of 0 means no jitter; 0.3 means up to ±30% random variance.
	if opts.JitterFraction > 0 && opts.JitterFraction <= 1.0 {
		jitter := delay * opts.JitterFraction
		delay = delay - jitter + jitter*2.0*float64(time.Now().UnixNano()%1000000)/1000000.0
	}
	return time.Duration(delay)
}
