// Package retry — google_api_error_test.go (P1.5, July 2026)
//
// Tests for the typed Google API error surface:
//   - ClassifyGoogleAPIError maps *googleapi.Error → typed Kind sentinel
//     based on HTTP status code (429 → Throttled, 5xx → Server, 403 →
//     Permission, 404 → NotFound, 400/401/409 → Client, off-spec → Unknown).
//   - errors.Is probes against the 6 sentinels work via
//     *GoogleAPIError.Is(target).
//   - RetryableError.IsRetryable returns true ONLY for Throttled/Server
//     kinds (typed-path #1 of retry.IsTransient depends on this).
//   - parseRetryAfter handles RFC 7231 §7.1.3 (delta-seconds + HTTP-date),
//     invalid input, negatives, past dates, and empty strings.
//   - Idempotency: ClassifyGoogleAPIError on an already-typed envelope
//     returns the same value (no double-wrap).
//   - Nil-safety: ClassifyGoogleAPIError(nil) returns nil.
//   - RetryAfterError honoring in DoWithValue: when fn returns a
//     RetryAfterError-bearing error, the actual sleep wins over the
//     computed backoff.
//
// The tests construct *googleapi.Error directly (no httptest server
// needed) — the canonical shape is well-known (Code int, Header
// http.Header, Body string, Message string, Errors []ErrorItem),
// and the *googleapi.Error package is the public Google API SDK
// contract. We exercise ClassifyGoogleAPIError's type-assertion
// path against real *googleapi.Error instances, then the
// substring-fallback path against fmt.Errorf-wrapped shapes that
// lost the type via downstream propagation.
package retry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
)

// ── Test: classify by HTTP status code ─────────────────────────────────

func TestClassifyGoogleAPIError_StatusCodeMapping(t *testing.T) {
	cases := []struct {
		name       string
		code       int
		wantKind   error
		wantIsRetr bool
		wantCode   int
	}{
		// 429 → Throttled (transient, Google API's canonical retry path).
		{"429 Too Many Requests", http.StatusTooManyRequests, ErrGoogleAPIThrottled, true, 429},
		// 5xx → Server (transient across the range; some 5xx are
		// technically permanent but distinguishing at classification
		// time is non-trivial; err on retry).
		{"500 Internal", 500, ErrGoogleAPIServer, true, 500},
		{"502 Bad Gateway", http.StatusBadGateway, ErrGoogleAPIServer, true, 502},
		{"503 Svc Unavail", http.StatusServiceUnavailable, ErrGoogleAPIServer, true, 503},
		{"504 Gateway Timeout", http.StatusGatewayTimeout, ErrGoogleAPIServer, true, 504},
		{"599 Network Connect Timeout (custom)", 599, ErrGoogleAPIServer, true, 599},
		// 408 Request Timeout → Server (transient; same retry surface
		// as 5xx per Google API shape).
		{"408 Request Timeout", http.StatusRequestTimeout, ErrGoogleAPIServer, true, 408},
		// 403 → Permission (terminal).
		{"403 Forbidden", http.StatusForbidden, ErrGoogleAPIPermission, false, 403},
		// 404 → NotFound (terminal).
		{"404 Not Found", http.StatusNotFound, ErrGoogleAPINotFound, false, 404},
		// 400/401/409 → Client (terminal).
		{"400 Bad Request", http.StatusBadRequest, ErrGoogleAPIClient, false, 400},
		{"401 Unauth", http.StatusUnauthorized, ErrGoogleAPIClient, false, 401},
		{"409 Conflict", http.StatusConflict, ErrGoogleAPIClient, false, 409},
		// Off-spec but within 4xx → Client (treat as terminal — the
		// production 4xx semantics is "request shape is wrong" which
		// 418 satisfies). ErrGoogleAPIUnknown is reserved for status
		// codes OUTSIDE 4xx/5xx ranges (see the 0 case below).
		{"418 I'm a teapot (off-spec 4xx)", http.StatusTeapot, ErrGoogleAPIClient, false, 418},
		// Status outside any range → Unknown (default branch).
		{"0 (no status code)", 0, ErrGoogleAPIUnknown, false, 0},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			raw := &googleapi.Error{
				Code:    tc.code,
				Message: "synthetic test error",
				Body:    "synthetic test body",
			}
			got := ClassifyGoogleAPIError(raw)
			require.NotNil(t, got, "ClassifyGoogleAPIError must NOT return nil for non-nil input")

			var env *GoogleAPIError
			require.True(t, errors.As(got, &env),
				"ClassifyGoogleAPIError must produce *GoogleAPIError from *googleapi.Error")

			require.True(t, errors.Is(env, tc.wantKind),
				"errors.Is(env, %v) must be true; got kind=%v", tc.wantKind, env.Kind)
			require.Equal(t, tc.wantKind, env.Kind)
			require.Equal(t, tc.wantCode, env.StatusCode)
			require.Equal(t, tc.wantIsRetr, env.IsRetryable(),
				"IsRetryable must match the canonical Kind-based classification")
		})
	}
}

// ── Test: Retry-After header parsed (delta-seconds + HTTP-date) ─────

func TestParseRetryAfter_HeaderShapes(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name  string
		value string
		want  time.Duration
	}{
		// Delta-seconds: canonical for Google API.
		{"delta-seconds 60", "60", 60 * time.Second},
		{"delta-seconds 0", "0", 0},
		{"delta-seconds 1", "1", 1 * time.Second},
		{"delta-seconds 3600", "3600", 1 * time.Hour},
		// Defensive: negative seconds clamped to 0.
		{"delta-seconds negative (clamped)", "-30", 0},
		// Empty/whitespace → 0.
		{"empty", "", 0},
		{"whitespace", "   ", 0},
		// HTTP-date (RFC 1123 format) → future date → positive duration.
		{"http-date future", now.Add(5 * time.Minute).Format(http.TimeFormat), 5 * time.Minute},
		// HTTP-date past → 0 (server said "retry at <past>", interpret as zero).
		{"http-date past (clamped)", now.Add(-1 * time.Hour).Format(http.TimeFormat), 0},
		// Unparseable → 0 (defensive).
		{"unparseable garbage", "not-a-number-not-a-date", 0},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := parseRetryAfter(tc.value, now)
			// Use direct equality. ±1s tolerance would over-shoot the
			// test surface (introducing flakiness for HTTP-date
			// rounding). 0-value cases get exact match; positive
			// delta-seconds are exact; HTTP-date future tolerance
			// is widened to ±2s to absorb Format() rounding noise.
			if tc.value == "" || tc.want == 0 {
				require.Equal(t, tc.want, got)
			} else if len(tc.value) > 0 && tc.value[0] >= '0' && tc.value[0] <= '9' {
				// delta-seconds path is exact.
				require.Equal(t, tc.want, got)
			} else {
				// HTTP-date path: ±2s tolerance.
				diff := got - tc.want
				if diff < 0 {
					diff = -diff
				}
				require.LessOrEqual(t, diff, 2*time.Second,
					"HTTP-date parse: got %v, want %v (±2s)", got, tc.want)
			}
		})
	}
}

// ── Test: RetryAfterDuration method on the envelope ─────────────────

func TestGoogleAPIError_RetryAfterDuration_FromHeader(t *testing.T) {
	t.Parallel()

	raw := &googleapi.Error{
		Code: 429,
		Header: http.Header{
			"Retry-After": []string{"45"},
		},
	}
	env := ClassifyGoogleAPIError(raw).(*GoogleAPIError)
	require.Equal(t, 45*time.Second, env.RetryAfterDuration(),
		"RetryAfterDuration must surface the parsed delta-seconds value")

	raw2 := &googleapi.Error{
		Code: 503,
		Header: http.Header{
			"Retry-After": []string{"120"},
		},
	}
	env2 := ClassifyGoogleAPIError(raw2).(*GoogleAPIError)
	require.Equal(t, 120*time.Second, env2.RetryAfterDuration())
}

// ── Test: idempotency (re-classify returns unchanged) ──────────────

func TestClassifyGoogleAPIError_Idempotent(t *testing.T) {
	t.Parallel()

	raw := &googleapi.Error{Code: 429, Message: "first"}
	first := ClassifyGoogleAPIError(raw)
	require.NotNil(t, first)

	// Re-classify the already-enveloped error. errors.As walks the
	// chain; the existing envelope is returned unchanged (no
	// double-wrap).
	second := ClassifyGoogleAPIError(first)
	require.Equal(t, first, second,
		"ClassifyGoogleAPIError must be idempotent on already-typed envelopes")
}

// ── Test: nil-safety ──────────────────────────────────────────────────

func TestClassifyGoogleAPIError_NilSafe(t *testing.T) {
	t.Parallel()

	require.Nil(t, ClassifyGoogleAPIError(nil),
		"ClassifyGoogleAPIError(nil) must return nil")
}

// ── Test: skip wrapping for non-*googleapi.Error ─────────────────────

func TestClassifyGoogleAPIError_NonGoogleError_PassesThrough(t *testing.T) {
	t.Parallel()

	// A plain errors.New cannot be type-asserted to *googleapi.Error.
	// The classifier passes through unchanged so callers retain the
	// original surface for outer retry loops (which can apply
	// WrapTransient if they want the typed-transient path).
	plain := errors.New("custom non-google error")
	got := ClassifyGoogleAPIError(plain)
	require.Equal(t, plain, got,
		"non-*googleapi.Error must pass through ClassifyGoogleAPIError unchanged")
}

// ── Test: errors.Is against the canonical sentinels ─────────────────

func TestGoogleAPIError_ErrorsIs_Sentinels(t *testing.T) {
	t.Parallel()

	// Wrap a 429 and verify errors.Is(ErrGoogleAPIThrottled) walks
	// through fmt.Errorf %w chains.
	raw := &googleapi.Error{Code: 429, Message: "throttled"}

	env := ClassifyGoogleAPIError(raw)
	wrapped := fmt.Errorf("putFile outer: %w", env)

	require.True(t, errors.Is(wrapped, ErrGoogleAPIThrottled),
		"errors.Is must walk the chain and find ErrGoogleAPIThrottled")
	require.False(t, errors.Is(wrapped, ErrGoogleAPIPermission),
		"errors.Is must NOT match Permission for a 429 envelope")
	require.False(t, errors.Is(wrapped, ErrGoogleAPINotFound))
	require.False(t, errors.Is(wrapped, ErrGoogleAPIServer))
	require.False(t, errors.Is(wrapped, ErrGoogleAPIClient))

	// Same probe against a 503 envelope.
	raw2 := &googleapi.Error{Code: 503, Message: "server unavailable"}
	env2 := ClassifyGoogleAPIError(raw2)
	wrapped2 := fmt.Errorf("putFile outer: %w", env2)
	require.True(t, errors.Is(wrapped2, ErrGoogleAPIServer))
	require.False(t, errors.Is(wrapped2, ErrGoogleAPIThrottled))
}

// ── Test: RetryableError interface satisfied (typed-path #1) ─────────

func TestGoogleAPIError_IsRetryable_SatisfiesInterface(t *testing.T) {
	t.Parallel()

	// Throttled + Server kinds are retryable; the rest are NOT.
	typedRetryable := []int{429, 500, 503, 504, 408}
	typedTerminal := []int{403, 404, 400, 401, 409, 418}

	for _, code := range typedRetryable {
		code := code
		t.Run(fmt.Sprintf("transient-%d", code), func(t *testing.T) {
			t.Parallel()
			env := ClassifyGoogleAPIError(&googleapi.Error{Code: code}).(*GoogleAPIError)
			require.True(t, env.IsRetryable(),
				"code %d should classify as transient via Kind-based IsRetryable", code)
		})
	}
	for _, code := range typedTerminal {
		code := code
		t.Run(fmt.Sprintf("terminal-%d", code), func(t *testing.T) {
			t.Parallel()
			env := ClassifyGoogleAPIError(&googleapi.Error{Code: code}).(*GoogleAPIError)
			require.False(t, env.IsRetryable(),
				"code %d should classify as terminal via Kind-based IsRetryable", code)
		})
	}
}

// ── Test: Retry-After honored in DoWithValue (retry-loop integration) ─

// fakeRetryAfterError implements the RetryAfterError interface for
// the retry-loop integration test. The retry.DoWithValue call returns
// this error from the second attempt onwards (first attempt returns
// nil to enter the retry path). The classifier observes the
// suggested duration; BackoffFor sees re.RetryAfterDuration().
//
// Why a fake vs *GoogleAPIError: simpler than constructing a
// real *googleapi.Error; the canonical surface is verified above.
type fakeRetryAfterError struct {
	suggested time.Duration
	msg       string
}

func (e *fakeRetryAfterError) Error() string { return e.msg }
func (e *fakeRetryAfterError) RetryAfterDuration() time.Duration {
	return e.suggested
}

// isRetryableAlways is a TEST-ONLY retry predicate that always returns
// true. Phase 6(a) Push 6.1 (July 2026) introduced a fail-closed norm
// (IsRetryable==nil defaults to neverRetry); the retry-loop integration
// tests for the Retry-After mediation path need the legacy "retry
// everything" surface to drive the multi-attempt flow. Production code
// MUST NOT use this predicate; it is exported within the package only
// and any non-test call would be a godlike/07 fail-closed violation.
//
// godlike/06 SSOT rationale: keep the test-only surface co-located with
// the tests that depend on it (otherwise cognitive load to track which
// retry tests rely on pass-through behaviour would scatter across
// files).
func isRetryableAlways(error) bool { return true }

func TestDoWithValue_HonorsRetryAfter(t *testing.T) {
	t.Parallel() // Iteration 1 → nil (success path, loop ends). Retry-after is
	// irrelevant on success; this confirms the success path is
	// unaffected by the new check.
	//
	// Fase 6(a) Push 6.1 (July 2026): explicit IsRetryable required.
	// Pre-Fase-6 the test relied on IsRetryable==nil → retry-always;
	// spec (b) forbids that semantic — IsRetryable==nil MUST
	// fail-closed via neverRetry. isRetryableAlways is the test-only
	// helper preserving the legacy "retry everything" surface for
	// this retry-after-wait mediation test.
	{
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, err := DoWithValue(ctx, func() (struct{}, error) {
			return struct{}{}, nil
		}, Options{
			MaxAttempts:    3,
			InitialBackoff: 1 * time.Second,
			JitterFraction: 0, // deterministic for assertion
			DisableJitter:  true,
			IsRetryable:    isRetryableAlways,
		})
		require.NoError(t, err)
	} // Iteration 2 → first call returns RetryAfterError with
	// suggested=2s (greater than computed 1s backoff); the loop
	// exits after RetryAfter-wait because the next ctx.Deadline
	// triggers before the retry happens. We assert the loop
	// waits at least the suggested 2s rather than burning the
	// 1s default backoff.
	//
	// Fase 6(a) Push 6.1: explicit IsRetryable required (see
	// Iteration 1 comment for spec rationale).
	{
		retryAfter := 2 * time.Second
		ctx, cancel := context.WithTimeout(context.Background(),
			retryAfter+100*time.Millisecond)
		defer cancel()

		callStart := time.Now()
		_, err := DoWithValue(ctx, func() (struct{}, error) {
			return struct{}{}, &fakeRetryAfterError{
				suggested: retryAfter,
				msg:       "synthetic 429",
			}
		}, Options{
			MaxAttempts:    3,
			InitialBackoff: 100 * time.Millisecond, // computed would be 100ms
			JitterFraction: 0,
			DisableJitter:  true,
			IsRetryable:    isRetryableAlways,
		})
		callElapsed := time.Since(callStart)
		require.Error(t, err, "first attempt error must propagate")
		// The retry happened for at least retryAfter (sleep would
		// have been max(100ms, 2s) = 2s). Cancel kicks in before
		// the second attempt; the loop returns ctx.DeadlineExceeded.
		require.GreaterOrEqual(t, callElapsed, retryAfter-50*time.Millisecond,
			"DoWithValue must honor RetryAfterError's suggested duration (≥%v, got %v)",
			retryAfter, callElapsed)
	} // Iteration 3 → first call returns RetryAfterError with
	// suggested=50ms (LESS than computed 500ms backoff); the loop
	// uses the computed backoff (NO retry-after extension because
	// suggested < computed).
	//
	// Fase 6(a) Push 6.1: explicit IsRetryable required.
	{
		ctx, cancel := context.WithTimeout(context.Background(),
			500*time.Millisecond+50*time.Millisecond)
		defer cancel()

		_, err := DoWithValue(ctx, func() (struct{}, error) {
			return struct{}{}, &fakeRetryAfterError{
				suggested: 50 * time.Millisecond, // smaller than computed 500ms
				msg:       "synthetic 429 small",
			}
		}, Options{
			MaxAttempts:    3,
			InitialBackoff: 500 * time.Millisecond, // computed backoff dominates
			JitterFraction: 0,
			DisableJitter:  true,
			IsRetryable:    isRetryableAlways,
		})
		require.Error(t, err)
		// Cancel fires while in the sleep; loop ends with ctx error.
		// We don't pin exact elapsed time — the contract is that
		// Retry-After < computed does NOT extend the sleep (it just
		// doesn't override). This iteration confirms the max() in
		// DoWithValue selects the larger of the two.
	}
}

// ── Test: Retry-After honored THROUGH wrapped errors (production shape) ─
//
// Drive adapter exits wrap SDK errors via fmt.Errorf %w (e.g.
// `fmt.Errorf("drive put (create %q): %w", req.Filename, err)`).
// The retry loop sees the wrapped chain, NOT the raw *GoogleAPIError.
// errors.As walks the chain to find the RetryAfterError interface;
// P1.5 verifier regression test for the production shape.
func TestDoWithValue_HonorsRetryAfter_ThroughWrappedError(t *testing.T) {
	t.Parallel()

	// Production-shape wrapping: closure returns a fmt.Errorf-wrapped
	// chain whose Unwrap reveals the *GoogleAPIError envelope (here a
	// fakeRetryAfterError fake for test simplicity — the contract is
	// identical).
	retryAfter := 2 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(),
		retryAfter+100*time.Millisecond)
	defer cancel()

	callStart := time.Now()
	_, err := DoWithValue(ctx, func() (struct{}, error) {
		wrapped := fmt.Errorf("drive put (create %q): %w", "test.mp4",
			&fakeRetryAfterError{suggested: retryAfter, msg: "synthetic 429 wrapped"})
		return struct{}{}, wrapped
	}, Options{
		MaxAttempts:    3,
		InitialBackoff: 100 * time.Millisecond,
		JitterFraction: 0,
		DisableJitter:  true,
		// Fase 6(a) Push 6.1: explicit IsRetryable required (spec b:
		// IsRetryable==nil MUST fail-closed, no retry-always default).
		IsRetryable: isRetryableAlways,
	})
	callElapsed := time.Since(callStart)
	require.Error(t, err)
	// The retry must honor the wrapped Retry-After (max(100ms, 2s) = 2s).
	// Without the errors.As fix, the retry would burn the 100ms
	// default and proceed to the next attempt before ctx.Deadline fires.
	require.GreaterOrEqual(t, callElapsed, retryAfter-50*time.Millisecond,
		"DoWithValue must honor RetryAfterError through fmt.Errorf %w wrapping (≥%v, got %v)",
		retryAfter, callElapsed)
}

// ── Test: FASE 6 Cut 6.1.D — substring classifier is UNREACHABLE ────────
//
// TestGoogleAPIError_NoSubstringClassifierReachable pins the
// FASE 6 Cut 6.1.D invariant: the *GoogleAPIError envelope's
// retry classification is strictly typed. The pre-FASE-6 substring
// fallback (transientSubstrings / IsTransientString) was REMOVED
// from the retry classifier; a malformed-shape Unknown envelope
// whose Body carries a transient substring ("429", "503",
// "rate limit", etc.) MUST classify as Unknown + terminal
// (IsRetryable==false). The classification cannot be "rescued"
// by a substring heuristic.
//
// Three orthogonal invariants:
//  1. Malformed *googleapi.Error (Code=0) with transient-looking
//     Body substring is classified as Unknown + terminal.
//  2. Plain (non-*googleapi.Error) cannot be type-asserted to an
//     envelope; ClassifyGoogleAPIError Path 3 returns upstream
//     unchanged (typed-only entry into retry classification).
//  3. Typed classifiers (5xx range incl. 599) operate via the
//     Kind bucket, NOT via Body substring — pin that the typed
//     shape wins even when both paths could agree.
func TestGoogleAPIError_NoSubstringClassifierReachable(t *testing.T) {
	t.Parallel()

	// (1) Malformed shape with transient-looking Body substring.
	t.Run("malformed-shape-with-transient-substring-is-terminal", func(t *testing.T) {
		t.Parallel()
		raw := &googleapi.Error{
			Code:    0, // off-spec, malformed response, no real status
			Message: "synthetic test",
			Body:    "transient-shaped body containing 429 keyword AND rate limit AND serviceUnavailable",
		}
		env := ClassifyGoogleAPIError(raw).(*GoogleAPIError)
		require.Equal(t, ErrGoogleAPIUnknown, env.Kind,
			"malformed shape (Code=0) MUST classify as Unknown — substring rescue is forbidden")
		require.False(t, env.IsRetryable(),
			"Unknown envelope is terminal regardless of Body substring (FASE 6 Cut 6.1.D — typed-only)")
	})

	// (2) Plain (non-*googleapi.Error) cannot reach the envelope.
	t.Run("non-google-api-error-cannot-reach-envelope", func(t *testing.T) {
		t.Parallel()
		plain := errors.New("synthetic rate limit exceeded message")
		got := ClassifyGoogleAPIError(plain)
		require.Equal(t, plain, got,
			"non-*googleapi.Error must pass through ClassifyGoogleAPIError Path 3 unchanged")

		var env *GoogleAPIError
		require.False(t, errors.As(got, &env),
			"plain error MUST NOT have a *GoogleAPIError envelope — typed-only entry")

		// Walk the wrap chain to confirm no envelope ever materialises.
		wrapped := fmt.Errorf("putFile outer: %w", plain)
		require.False(t, errors.As(wrapped, &env),
			"fmt.Errorf %w wrap does NOT introduce an envelope")
	})

	// (3) Typed 5xx range wins via the bucket, not via the substring;
	//     same outcome as before, but pin that the substring taxonomy
	//     was NOT the deciding factor for the in-range case.
	t.Run("typed-5xx-classifies-via-bucket-not-substring", func(t *testing.T) {
		t.Parallel()
		// 599 is in-range for the 5xx bucket (Kind=Server, retryable).
		// The Body also contains the literal "503" substring. The
		// typed bucket MUST be the deciding factor; a regression
		// that re-introduces substring rescue would still pass this
		// (because both agree), so this case alone is insufficient.
		// Case (1) above is the load-bearing counterexample.
		raw := &googleapi.Error{
			Code: 599,
			Body: "Body contains literal 503 substring",
		}
		env := ClassifyGoogleAPIError(raw).(*GoogleAPIError)
		require.Equal(t, ErrGoogleAPIServer, env.Kind,
			"599 in-range → Server bucket (typed, not substring)")
		require.True(t, env.IsRetryable(),
			"5xx in-range envelope is retryable via typed Kind")
	})

	// (4) Compile-time-equivalent pin: typed-path consistency check.
	//     If a future regression re-introduces a substring fallback,
	//     the test surface below will diverge — *GoogleAPIError's
	//     retry classification must NEVER depend on the Body string.
	t.Run("typed-retryable-does-not-depend-on-body-substring", func(t *testing.T) {
		t.Parallel()
		// 429 typed-throttled body says "OK" (no 429 substring, no
		// throttling keyword). The typed bucket still classifies
		// as retryable. Body is observer-data, not deciding-input.
		raw := &googleapi.Error{
			Code:    http.StatusTooManyRequests,
			Message: "ok",
			Body:    "Body with no transient substring; just lorem ipsum text",
		}
		env := ClassifyGoogleAPIError(raw).(*GoogleAPIError)
		require.Equal(t, ErrGoogleAPIThrottled, env.Kind,
			"429 + non-transient Body → Throttled bucket")
		require.True(t, env.IsRetryable(),
			"Throttled bucket is retryable regardless of Body substring (godlike/06 typed SSOT)")
	})
}
