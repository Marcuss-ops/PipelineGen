// Package retry is the canonical repository-wide primitive for two
// intertwined concerns:
//
//  1. Transient-infrastructure error classification — "should I retry
//     this?" should always be answered with the same predicate.
//     See transient.go for IsTransient / WrapTransient / the typed
//     TransientInfrastructureError carrier and the canonical
//     transientSubstrings taxonomy.
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
// through this package (transient.go for the predicate, options.go for
// the configuration knobs, this file for the orchestrating Do loop).
// CI gate (Azione 8/8D) bans substring-match retry-classifiers
// outside pkg/retry/.
//
// ═══════════ File index ═════════════════════════════════════════════════════
//
//   - retry.go        — orchestrator: Do, DoWithValue, RetryAfterError.
//     Slim (~160 LoC). Owns the canonical retry-loop
//     surface that callers consume.
//   - transient.go    — typed classifier: TransientInfrastructureError,
//     transientSubstrings, RetryableError, IsTransient,
//     IsTransientString, WrapTransient. Owns the
//     taxonomy + the canonical "should I retry?"
//     predicate + the typed-wrap helper.
//   - options.go      — configuration knobs: Options struct,
//     RetryOptions alias, DefaultOptions, norm
//     (defensive coalesce), BackoffFor (exponential
//     backoff + bounded jitter math).
//   - errors.go       — error category taxonomy: ErrorCategory enum,
//     Classify(err) → (category, retryable bool),
//     Retryable(err) shortcut, 7 typed category
//     constants (ErrNetwork/ErrTimeout/ErrLockBusy/
//     ErrValidation/ErrMissingHandler/ErrBadPayload/
//     ErrUnknown). Audit P1 #2, July 2026.
//   - clock.go        — injectable time source: Clock interface,
//     RealClock, ClockFromOptions picker, Sleep helper.
//     FASE 3.8, July 2026.
//   - google_api_error.go — typed *GoogleAPIError envelope + 6 sentinels
//     (ErrGoogleAPIThrottled/Server/Permission/NotFound/
//     Client/Unknown) + ClassifyGoogleAPIError +
//     parseRetryAfter. P1.5, July 2026.
//
// Pre-PR-SPLIT-RETRY-PKG: retry.go was 559 LoC and held all 6
// capabilities above in one file. The split preserves godlike/06
// SSOT (one canonical owner per fact) and godlike/07 minimum-blast-
// radius (zero signature changes, zero new exported symbols, package-
// scoped lookup paths preserved for all pre-split callers).
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
//	    IsRetryable:    retry.IsTransient,  // canonical predicate (transient.go)
//	})
//
// Example 2 — images/storage_search.go (DuckDuckGo page retrieval).
// Canonical pattern: HTTP fetch wrapped in retry.Do, retry.IsTransient as the
// IsRetryable predicate, transient classification centralized in transient.go.
//
//	// internal/capabilities/images/workflow/storage_search.go
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
//	    IsRetryable:    retry.IsTransient,  // canonical predicate (transient.go)
//	})
//
// Example 3 — Drive SDK error wrapping at the adapter exit (Azione 5/8).
// Canonical pattern: retry.WrapTransient (transient.go) at the raw SDK exit
// promotes any transient-classified error to a typed carrier. Farther-up
// retry loops that query retry.IsTransient hit the typed path FIRST (via
// errors.As) and short-circuit on terminal errors without substring matching.
// retry.ClassifyGoogleAPIError (google_api_error.go) is the canonical
// googleapi-specific wrap that's used as an upstream-typed alternative.
//
//	// internal/infrastructure/drive/uploader.go
//	created, err := u.Service.Files.Create(file).Context(ctx).Do()
//	if err != nil {
//	    // typed wrap: transient googleapi.Error (429, 503, timeout) becomes
//	    // *TransientInfrastructureError via transient.go::WrapTransient;
//	    // googleapi-specific kinds (Throttled/Server/etc.) become
//	    // *GoogleAPIError via google_api_error.go::ClassifyGoogleAPIError.
//	    return nil, fmt.Errorf("drive put failed: %w", retry.WrapTransient(err))
//	}
//
// ═══════════════════════════════════════════════════════════════════════════════════════
package retry

import (
	"context"
	"errors"
	"time"
)

// ── RetryAfterError interface (canonical Do-loop contract) ────────────────

// RetryAfterError is the pkg/retry contract for errors that carry a
// Retry-After suggestion (P1.5, July 2026). Implementing types
// MUST return a non-negative duration; zero means "no header
// supplied; use the calculated backoff". Implementers supply the
// parsed RFC 7231 §7.1.3 hi value (delta-seconds OR HTTP-date).
//
// DoWithValue honors RetryAfterError at the pre-sleep point by
// taking max(computed-backoff, retryAfterDuration) before jitter
// is applied, so Google API throttling shapes (429 with
// Retry-After: 60) wait the upstream-debounced instant rather
// than burning the static backoff first. GoogleAPIError satisfies
// this interface (see pkg/retry/google_api_error.go).
//
// Lives in retry.go (NOT transient.go or wrap.go) because RetryAfterError
// is the canonical Do-loop contract for back-pressure decisions — it is
// read inside DoWithValue's pre-sleep block, not at transient
// classification time. Splitting it into a separate file would force
// callers importing it for the RetryAfterError interface contract
// (e.g. test fakes like fakeRetryAfterError in google_api_error_test.go)
// to depend on transient.go indirectly via transitively-used internal
// helpers, breaking the godlike/06 SSOT seam.
type RetryAfterError interface {
	error
	RetryAfterDuration() time.Duration
}

// ── Do — err-only retry loop ──────────────────────────────────────────────

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

// ── DoWithValue — generic retry loop ──────────────────────────────────────

// DoWithValue is the generic version of Do. fn returns (T, error).
// On success the value is returned; on permanent failure the zero value of T
// is returned together with the last error.
//
// FASE 3.8 (July 2026): the backoff sleep now uses ClockFromOptions(opts)
// instead of bare time.After so tests can inject a fake clock via
// Options.Clock for deterministic duration assertions. Production
// callers see byte-identical behaviour to pre-FASE-3.8 because
// Options{} (zero Clock field) selects RealClock, which delegates
// to time.After. The picker is a single-line change so the retry
// loop's ctx-aware cancellation + RetryAfterError honoring logic
// stay in one place.
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

		// Fase 6(a) (July 2026): norm() guarantees opts.IsRetryable
		// is non-nil — a nil field is fail-closed (coalesced to
		// neverRetry, options.go). The `!= nil` guard below is
		// belt-and-suspenders: it preserves the pre-Fase-6 contract
		// for direct callers who bypass norm() (none in production,
		// but tests sometimes construct Options without norm()). The
		// Fase 6(a) user spec forbids "IsRetryable==nil means retry
		// always" — the fail-closed norm + this nil-guard combine to
		// guarantee that.
		if opts.IsRetryable != nil && !opts.IsRetryable(err) {
			var zero T
			return zero, err
		}

		if i < opts.MaxAttempts-1 {
			if opts.OnRetry != nil {
				opts.OnRetry(i, err)
			}
			sleep := BackoffFor(i, opts)
			// P1.5 (July 2026): honor the Retry-After hint at the
			// pre-sleep point. Google API 429/503 responses carry
			// Retry-After that often exceeds the static exponential
			// backoff — burning 1s/2s/4s before reaching the upstream
			// debounce instant wastes retry budget. max() here means
			// "wait the larger of (computed, suggested)". The
			// assertion that re.RetryAfterDuration() returns non-
			// negative is a contract: the RetryAfterError docs say
			// "MUST return non-negative" — the deliberate no-op on
			// negative value would be a wrapper bug, not a bug here.
			//
			// Why errors.As and NOT direct type assertion: production
			// callers wrap SDK exits via fmt.Errorf %w (e.g. uploader_doPutFile
			// emits `fmt.Errorf("drive put (create %q): %w", req.Filename, err)`).
			// The value that lands here is the wrapped chain, not the
			// raw *GoogleAPIError. errors.As walks Unwrap and matches
			// any layer that satisfies the RetryAfterError contract;
			// direct type assertion `err.(RetryAfterError)` would miss
			// the envelope (godlike/07 no-fake-availability: a passing
			// unit test that uses an unwrapped fake masks the production
			// miss).
			var re RetryAfterError
			if errors.As(err, &re) {
				if ra := re.RetryAfterDuration(); ra > sleep {
					sleep = ra
				}
			}
			select {
			case <-ClockFromOptions(opts).After(sleep):
			case <-ctx.Done():
				var zero T
				return zero, ctx.Err()
			}
		}
	}
	var zero T
	return zero, lastErr
}
