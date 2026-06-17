// Package retry provides a unified retry primitive with exponential backoff,
// context awareness, and configurable retryable-error predicates.
//
// Replaces 4+ duplicated retry implementations across the codebase:
//   - youtube/rebuild_job.go (retryDownload + isTransientDownloadError)
//   - ml/ollama/client/client_core.go (inner per-model chatWithRetryAndFallback loop)
//   - handlers/job_handler_phases.go (inline image generation retry)
//   - handlers/batch_chapters.go (inline chapter generation retry)
//   - handlers/job_handler_phases.go (prewarm HTTP retry)
package retry

import (
	"context"
	"time"
)

// Options configures the retry loop behaviour.
// All fields are optional — zero values fall back to sensible defaults.
type Options struct {
	// MaxAttempts is the maximum number of calls to fn (inclusive).  Must be >= 1.
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

	// IsRetryable is an optional predicate.  If nil, every error is retried.
	// When non-nil, only errors for which it returns true will be retried;
	// non-retryable errors cause an immediate return.
	IsRetryable func(error) bool
}

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
// retry budget is exhausted.  If fn returns a non-retryable error (per
// opts.IsRetryable) the loop exits immediately.
//
// A nil error from fn means success.
// If all attempts fail, the last error is returned.
func Do(ctx context.Context, fn func() error, opts Options) error {
	opts = norm(opts)

	var lastErr error
	for i := 0; i < opts.MaxAttempts; i++ {
		// Respect context cancellation before each attempt.
		if err := ctx.Err(); err != nil {
			return err
		}

		err := fn()
		if err == nil {
			return nil
		}

		lastErr = err

		// If the predicate says this error is non-retryable, stop immediately.
		if opts.IsRetryable != nil && !opts.IsRetryable(err) {
			return err
		}

		// Don't sleep after the last attempt.
		if i < opts.MaxAttempts-1 {
			sleep := sleepDuration(i, opts)
			select {
			case <-time.After(sleep):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return lastErr
}

// DoWithValue is the generic version of Do.  fn returns (T, error).
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

// norm fills zero-valued fields with defaults.
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

// sleepDuration returns the delay before the (attemptCount)th retry.
// attemptCount is 0-based: attempt 0 → first retry after initial failure.
func sleepDuration(attemptCount int, opts Options) time.Duration {
	delay := float64(opts.InitialBackoff)
	for i := 0; i < attemptCount; i++ {
		delay *= opts.BackoffFactor
		if time.Duration(delay) > opts.MaxBackoff {
			return opts.MaxBackoff
		}
	}
	if time.Duration(delay) > opts.MaxBackoff {
		return opts.MaxBackoff
	}
	return time.Duration(delay)
}
