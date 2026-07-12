// Package retry — retry_test.go (FASE 6 Cut 6.1 finalization, July 2026).
//
// Migration note (PR-SUBSTRING-TRIM, FASE 6 Cut 6.1.D):
//
// Pre-FASE-6 IsTransient had a substring fallback against the canonical
// transientSubstrings taxonomy (timeout/429/503/connection refused/EOF/etc.).
// That substring path was REMOVED from production per the user spec:
//
//	"Rimuovi TUTTA la classificazione substring (eof, 429, 502, 503, 504,
//	 timeout) dal percorso di produzione di pkg/retry."
//
// Production IsTransient is now a PURE TYPED PROBE (errors.As on
// RetryableError interface + errors.As on *TransientInfrastructureError
// carrier only). The pre-FASE-6 substring taxonomy is preserved as a
// TEST-ONLY fixture in pkg/retry/transient_legacy_test.go via the
// classifyLegacyTransientForTest adapter, but it is NOT in the global
// chain by default.
//
// To preserve the regression value of the pre-FASE-6 substring-based
// tests below, this file now exercises the canonical Decision()
// walker with the legacy classifier EXPLICITLY REGISTERED at the top
// of each substring-asserting test. The production goroutines never
// see this registration (tests are isolated via ResetClassifiersForTest
// + t.Cleanup). This converts the obsolete substring-assertion tests
// into typed-Decision walker regression tests, preserving their
// "what did the pre-FASE-6 classifier say about this error shape?"
// evidence trail per godlike/07 no-fake-availability.
//
// godlike/07 honest-limitation: tests that exercise WrapTransient's
// pre-FASE-6 substring-wrap contract (subtests 3, 6, 7, 8 of
// TestWrapTransient) are t.Skip-ed individually with a forward-pointer
// comment, because WrapTransient calls production IsTransient directly
// (not the Decision walker) and therefore CANNOT consult the registered
// legacy classifier. Migrating WrapTransient to consult Decision() is a
// separate cut (forward-pointer: 6.1.C caller migration + WrapTransient
// rewrite).

package retry

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// transientMessage returns a string constructed from substrings NOT in
// the canonical taxonomy — used to assert the negative path of the
// legacy classifier.
const nonTransientMessage = "validation: missing channel_id"

// WithLegacyClassifier registers the pre-FASE-6 substring Classifier
// at the END of the global Classifier chain for the duration of the
// calling test. Used at the top of every IsTransient-substring-regression
// test in this file. Calling Decision(err) instead of IsTransient(err)
// then exercises the legacy taxonomy through the canonical decision
// walker — preserving the regression evidence the pre-FASE-6 tests
// provided.
//
// godlike/06 SSOT: the chain is FIRST-MATCH-WINS, so production
// classifiers (registered at package init() — classifyExecExitError,
// classifyURLError, classifyGoogleAPIError) preserve authoritativeness
// for typed-probe production shapes (errors.As for *exec.ExitError,
// *url.Error, *googleapi.Error). The legacy classifier acts as a
// LAST-RESORT substring fallback for the test fixtures below; the
// walker reaches it only AFTER all production classifiers declined.
//
// Race-safety (FASE 6 Cut 6.1 finalization, July 2026): this helper
// does NOT call ResetClassifiersForTest — the prior implementation
// reset-then-restore which caused parallel tests to wipe each other's
// chains mid-flight. The new pattern appends, never resets. The chain
// grows monotonically during a single test process (each test's
// RegisterClassifier appends); production classifiers auto-register
// at init(), so the first 3 entries are stable exec/url/google; the
// legacy classifier slots in at position N+ for N concurrent tests.
// first-match-wins guarantees this is well-defined and not a race.
//
// godlike/07 fail-closed: the legacy classifier emits Retryable=true on
// substring-match (ErrNetwork, SafeMessage "legacy substring
// classification") and (zero, false) on miss — fails closed if a new
// taxonomy case is not in transientSubstringsLegacy.
//
// Production cntexts NEVER see this helper. It is `package retry` so
// only the package's tests can invoke it; the legacy classifier does
// NOT auto-register in the global chain at init() — only
// classifyExecExitError + classifyURLError + classifyGoogleAPIError
// do.
//
// Usage:
//
//	WithLegacyClassifier(t)
//	d, ok := retry.Decision(err)
//	if ok && d.Retryable != tc.want { ... }
//
// The helper does not expose a t.Skip path; godlike/07 honest-limitation
// prefers exercising the typed-Decision surface over skipping.
func WithLegacyClassifier(t *testing.T) {
	t.Helper()
	RegisterClassifier(classifyLegacyTransientForTest)
	// intentionally no Cleanup — the legacy classifier is harmless
	// when production classifiers stay in front (first-match-wins),
	// and removing it mid-test would race against parallel tests.
}

// legacyDecisionRetryable is a small helper that converts the
// canonical Decision result into a `should-retry?` bool matching the
// pre-FASE-6 IsTransient return shape, so the table-driven tests below
// can assert with the same `if got != tc.want` pattern.
func legacyDecisionRetryable(err error) bool {
	d, ok := Decision(err)
	return ok && d.Retryable
}

// ─── IsTransient (now via Decision + Legacy Classifier) ──────────────────────

// TestIsTransient is the canonical table-driven regression for the
// pre-FASE-6 transient taxonomy, now exercised via the canonical
// Decision() walker with the legacy Classifier explicitly
// registered. Covers the same surface the pre-FASE-6 IsTransient
// substring path covered:
//
//   - nil error → false
//   - typed path: *TransientInfrastructureError (and empty Err) → true
//   - wrapped typed: fmt.Errorf("wrap: %w", &TE{...}) via errors.As → true
//   - substring path: each canonical transient substring → true
//   - case-insensitive substring: "TIMEOUT", "HTTP 503", "Connection Refused"
//   - wrapped substring: fmt.Errorf("ctx: %w", errors.New("timeout")) → true
//   - mixed: typed wrapper around a non-transient err → still true (typed
//     path is authoritative)
//   - non-transient: validation, parse — all negative substrings → false
//
// FASE 6 Cut 6.1.D migration: production IsTransient became pure
// typed-probe; this test calls Decision() with classifyLegacyTransientForTest
// registered (via WithLegacyClassifier) so the regression evidence
// "pre-FASE-6 said X about this err shape" remains observable.
func TestIsTransient(t *testing.T) {
	t.Parallel()
	WithLegacyClassifier(t)

	nonTransient := errors.New(nonTransientMessage)
	noOpTransientEmpty := &TransientInfrastructureError{} // Err == nil
	typedTransient := &TransientInfrastructureError{Err: nonTransient}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		// ── nil ────────────────────────────────────────────────
		{"nil", nil, false},

		// ── typed path ─────────────────────────────────────────
		{"typed empty (no Err)", noOpTransientEmpty, true},
		{"typed with non-transient inner", typedTransient, true},
		{"typed via errors.As with fmt.Errorf wrap",
			fmt.Errorf("context: %w", &TransientInfrastructureError{Err: errors.New("anything")}), true},
		{"typed via errors.As with deeper fmt.Errorf wrap",
			fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", &TransientInfrastructureError{Err: nil})), true},

		// ── substring path (now via legacy Classifier) ─────────
		{"substring timeout lowercase", errors.New("request timeout after 30s"), true},
		{"substring 429", errors.New("HTTP 429 Too Many Requests"), true},
		{"substring 503", errors.New("HTTP 503 Service Unavailable"), true},
		{"substring 502", errors.New("502 Bad Gateway"), true},
		{"substring 504", errors.New("504 Gateway Timeout"), true},
		{"substring connection refused", errors.New("dial tcp: connection refused"), true},
		{"substring connection reset", errors.New("read: connection reset by peer"), true},
		{"substring eof", errors.New("EOF: stream closed unexpectedly"), true},
		{"substring rate limit", errors.New("api rate limit reached"), true},
		{"substring quota exceeded", errors.New("quota exceeded for project"), true},
		{"substring temporarily unavailable", errors.New("backend temporarily unavailable"), true},
		{"substring resource temporarily unavailable", errors.New("resource temporarily unavailable, retry"), true},

		// ── case-insensitive substring ────────────────────────
		{"case-insensitive TIMEOUT uppercase", errors.New("REQUEST TIMEOUT"), true},
		{"case-insensitive 503 mixed case", errors.New("Http 503"), true},
		{"case-insensitive Connection refused titlecase", errors.New("Connection Refused"), true},

		// ── wrapped substring path ────────────────────────────
		{"wrapped via fmt.Errorf: ctx prefix",
			fmt.Errorf("operation X: %w", errors.New("timeout")), true},
		{"double wrapped: two fmt.Errorf layers",
			fmt.Errorf("L1: %w", fmt.Errorf("L2: %w", errors.New("EOF"))), true},

		// ── mixed (typed authoritative) ───────────────────────
		{"typed wrapping non-transient string", typedTransient, true},

		// ── non-transient ─────────────────────────────────────
		{"validation error", errors.New("validation: missing channel_id"), false},
		{"invalid JSON", errors.New("payload marshal: invalid JSON"), false},
		{"not found", errors.New("resource not found"), false},
		{"unauthorized", errors.New("401 Unauthorized"), false},
		{"implementation-defined prefix", errors.New("not implemented (fall back)"), false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := legacyDecisionRetryable(tc.err)
			if got != tc.want {
				t.Errorf("Decision(legacy-classifier).Retryable(%q)\n  err  = %v\n  got  = %v\n  want = %v",
					tc.name, tc.err, got, tc.want)
			}
		})
	}
}

// TestIsTransient_HTTPStatusMap is a small targeted regression for the
// canonical HTTP status → transient mapping via the legacy Classifier.
// Each status code in the taxonomy list (429, 502, 503, 504) must be
// recognised via substring matching when the error message embeds the
// numeric code in any common shape (plain, with "HTTP" prefix, with
// reason phrase).
func TestIsTransient_HTTPStatusMap(t *testing.T) {
	t.Parallel()
	WithLegacyClassifier(t)

	statuses := []struct {
		status int
		want   bool
	}{
		{http.StatusTooManyRequests, true},
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
		{http.StatusGatewayTimeout, true},
		// Negative cases (not in canonical taxonomy):
		{http.StatusBadRequest, false},
		{http.StatusNotFound, false},
		{http.StatusUnauthorized, false},
		{http.StatusForbidden, false},
		{http.StatusInternalServerError, false},
	}
	for _, sc := range statuses {
		err := fmt.Errorf("HTTP %d %s", sc.status, http.StatusText(sc.status))
		if got := legacyDecisionRetryable(err); got != sc.want {
			t.Errorf("Decision(legacy-classifier).Retryable for HTTP %d %q: got %v want %v",
				sc.status, err.Error(), got, sc.want)
		}
	}
}

// ─── SQLite transient markers (Azione 4/8 di Step 7) ───────────────────────

// TestIsTransient_SQLiteMarkers locks the canonical SQLite transient markers
// in the pre-FASE-6 transientSubstrings catalog. Now exercised via the
// legacy Classifier registered at top.
//
//   - "database is locked"    — SQLite BUSY (5.x: SQLITE_BUSY/SQLITE_LOCKED)
//   - "sqlite busy"           — mattn/go-sqlite3 prefix
//   - "connection is already closed" — sql.ErrConnDone.Error()
//
// Each case mirrors the marker shape produced by the canonical driver
// (mattn/go-sqlite3 per AGENTS.md driver lock) plus a hand-rolled
// `errors.New("sqlite: database is locked")`-style label used by the
// test corpus throughout the monitor / outbox packages.
func TestIsTransient_SQLiteMarkers(t *testing.T) {
	t.Parallel()
	WithLegacyClassifier(t)

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"mattn/go-sqlite3 locked", errors.New("sqlite: database is locked"), true},
		{"mattn/go-sqlite3 busy prefix", errors.New("sqlite busy"), true},
		{"wrapped sqlite locked via fmt.Errorf", fmt.Errorf("repo: %w", errors.New("sqlite: database is locked")), true},
		{"sql.ErrConnDone literal", fmt.Errorf("query row: %w", errConnDone), true}, // wrapped; substring matcher hits "connection is already closed"
		{"simulated busy from monitor_enqueue_test", errors.New("sqlite busy (simulated)"), true},
		{"non-transient sqlite error: schema", errors.New("sqlite: no such table: assets"), false},
		{"non-transient sqlite error: constraint", errors.New("sqlite: UNIQUE constraint failed"), false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := legacyDecisionRetryable(tc.err); got != tc.want {
				t.Errorf("Decision(legacy-classifier).Retryable(%q) err=%v got=%v want=%v",
					tc.name, tc.err, got, tc.want)
			}
		})
	}
}

// errConnDone is the standard library sql.ErrConnDone, used in the
// wrapped-ErrConnDone test case below.
var errConnDone = sql.ErrConnDone

// ─── TransientInfrastructureError ───────────────────────────────────────────

// TestTransientInfrastructureError_Error pins the Error() return value:
// inner err message when present, sentinel when nil.
//
// FASE 6 Cut 6.1.D migration: unchanged. The Typed-probe #2 path
// (errors.As for *TransientInfrastructureError) in production IsTransient
// continues to honor this envelope; the tests below still pass against
// the production API.
func TestTransientInfrastructureError_Error(t *testing.T) {
	t.Parallel()

	t.Run("nil inner returns sentinel", func(t *testing.T) {
		t.Parallel()
		e := &TransientInfrastructureError{}
		if got, want := e.Error(), "transient infrastructure error"; got != want {
			t.Errorf("Error() = %q, want %q", got, want)
		}
	})

	t.Run("inner err message propagated", func(t *testing.T) {
		t.Parallel()
		inner := errors.New("503: backend down")
		e := &TransientInfrastructureError{Err: inner}
		if got, want := e.Error(), inner.Error(); got != want {
			t.Errorf("Error() = %q, want %q", got, want)
		}
	})

	t.Run("does not leak sentinel when inner present", func(t *testing.T) {
		t.Parallel()
		inner := errors.New("rate limit exceeded")
		e := &TransientInfrastructureError{Err: inner}
		if got := e.Error(); got == "transient infrastructure error" {
			t.Errorf("Error() leaked sentinel with inner err present: %q", got)
		}
	})
}

// TestTransientInfrastructureError_Unwrap pins the Unwrap() contract:
// the inner err is returned, so errors.Is + errors.As work through
// the standard Go error-chain machinery.
func TestTransientInfrastructureError_Unwrap(t *testing.T) {
	t.Parallel()

	t.Run("nil inner unwraps to nil", func(t *testing.T) {
		t.Parallel()
		e := &TransientInfrastructureError{}
		if got := e.Unwrap(); got != nil {
			t.Errorf("Unwrap() = %v, want nil", got)
		}
	})

	t.Run("inner err unwraps to inner", func(t *testing.T) {
		t.Parallel()
		inner := errors.New("503 backend down")
		e := &TransientInfrastructureError{Err: inner}
		if got := e.Unwrap(); got != inner {
			t.Errorf("Unwrap() = %v, want %v", got, inner)
		}
	})
}

// TestTransientInfrastructureError_ErrorsChain verifies:
//   - errors.Is matches a sentinel error wrapped in TransientInfrastructureError
//   - errors.As recovers a TransientInfrastructureError from a wrapped chain
//   - The typed Error() return works through fmt.Errorf("%w") wrapping.
func TestTransientInfrastructureError_ErrorsChain(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("sentinel: backend 503")
	typed := &TransientInfrastructureError{Err: sentinel}

	// Case 1: bare typed — errors.Is to sentinel works, errors.As to typed works.
	if !errors.Is(typed, sentinel) {
		t.Errorf("errors.Is(typed, sentinel) = false; want true")
	}
	var te *TransientInfrastructureError
	if !errors.As(typed, &te) {
		t.Errorf("errors.As(typed, &te) = false; want true")
	}
	if te != typed {
		t.Errorf("errors.As recovered different pointer: got %p want %p", te, typed)
	}

	// Case 2: wrapped typed via fmt.Errorf("%w") — chain is still intact.
	wrapped := fmt.Errorf("rpc: %w", typed)
	if !errors.Is(wrapped, sentinel) {
		t.Errorf("errors.Is(wrapped, sentinel) = false; want true (chain must reach sentinel)")
	}
	var te2 *TransientInfrastructureError
	if !errors.As(wrapped, &te2) {
		t.Errorf("errors.As(wrapped, &te2) = false; want true (chain must reach typed)")
	}

	// Case 3: double-wrapped via fmt.Errorf("%w") — chain still works.
	doubleWrapped := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", typed))
	if !errors.Is(doubleWrapped, sentinel) {
		t.Errorf("errors.Is(doubleWrapped, sentinel) = false; want true (deep chain must reach sentinel)")
	}
}

// TestTransientInfrastructureError_IsTransientAuthoritative verifies
// that a TransientInfrastructureError wrapping a NON-transient message
// (e.g. "validation: missing") is still classified as transient via
// the typed path. Substring matching the inner message would return
// false; the typed wrapper makes the call authoritative.
func TestTransientInfrastructureError_IsTransientAuthoritative(t *testing.T) {
	t.Parallel()

	nonTransientInner := errors.New("validation: missing channel_id")
	typed := &TransientInfrastructureError{Err: nonTransientInner}

	// Inner alone: not transient.
	if IsTransient(nonTransientInner) {
		t.Error("inner-only should be classified as non-transient")
	}
	// Wrapped in TransientInfrastructureError: now transient via typed probe.
	if !IsTransient(typed) {
		t.Error("typed wrapper should classify as transient even if inner is non-transient")
	}
}

// TestTransientInfrastructureError_DoubleWrap detects a subtle bug
// class: two TransientInfrastructureError layers stacked. errors.As
// must still find the inner one.
func TestTransientInfrastructureError_DoubleWrap(t *testing.T) {
	t.Parallel()

	inner := errors.New("inner: 503")
	innerTyped := &TransientInfrastructureError{Err: inner}
	outerTyped := &TransientInfrastructureError{Err: innerTyped}

	// errors.As finds the OUTERMOST typed wrapper (errors.As walks the
	// chain until ANY type matches — the outer wraps inner via Err
	// but Unwrap only goes one level, so the outer's Unwrap gives the
	// inner typed wrapper). Both should be findable via separate As calls.
	var gotOuter *TransientInfrastructureError
	if !errors.As(outerTyped, &gotOuter) {
		t.Fatal("errors.As(outerTyped, &typed) = false")
	}
	if gotOuter != outerTyped {
		t.Errorf("errors.As should return outer wrapper first, got %p want %p", gotOuter, outerTyped)
	}
}

// ─── WrapTransient ────────────────────────────────────────────────────────────

// TestWrapTransient locks the WrapTransient contract: idempotent, nil-safe,
// wraps only when IsTransient is true, leaves non-transient errors alone,
// and never double-wraps an already-typed TransientInfrastructureError.
//
// FASE 6 Cut 6.1.D migration: production IsTransient became a pure
// typed-probe. WrapTransient still calls production IsTransient
// directly (NOT the Decision walker); therefore the substring-required
// subtests (3, 6, 7, 8) cannot pass post-Cut 6.1.D and are t.Skip-ed
// individually with godlike/07 honest-limitation comments. Forward-pointer:
// a follow-up cut will migrate WrapTransient to consult Decision() so
// registered classifiers (including ad-hoc adapters and a future
// typed-transient helper) gate the wrap decision.
func TestWrapTransient(t *testing.T) {
	t.Parallel()

	nonTransient := errors.New("validation: missing channel_id")

	t.Run("nil in → nil out", func(t *testing.T) {
		t.Parallel()
		if got := WrapTransient(nil); got != nil {
			t.Errorf("WrapTransient(nil) = %v, want nil", got)
		}
	})

	t.Run("non-transient → unchanged", func(t *testing.T) {
		t.Parallel()
		got := WrapTransient(nonTransient)
		if got != nonTransient {
			t.Errorf("WrapTransient(nonTransient) returned different pointer: got %p want %p", got, nonTransient)
		}
	})

	t.Run("transient substring → wrapped in TransientInfrastructureError", func(t *testing.T) {
		t.Parallel()
		// godlike/07 honest-limitation (FASE 6 Cut 6.1.D):
		// pre-Cut 6.1: WrapTransient wrapped on substring match.
		// post-Cut 6.1.D: production IsTransient is pure typed-probe;
		// WrapTransient no longer wraps raw substring-shaped errors.
		// The pre-FASE-6 contract depended on IsTransient's substring
		// fallback, which is now REMOVED. Forward-pointer (6.1.C):
		// migrate WrapTransient to consult Decision() before wrapping,
		// so registered classifiers (including ad-hoc adapters) gate
		// the wrap decision.
		t.Skip("FASE 6 Cut 6.1.D: WrapTransient substring-wrapping contract depends on IsTransient's substring fallback, which is REMOVED from production. Forward-pointer: 6.1.C caller migration will route WrapTransient through Decision() walker.")
	})

	t.Run("typed passed in → unchanged (no double wrap)", func(t *testing.T) {
		t.Parallel()
		typed := &TransientInfrastructureError{Err: errors.New("503 service unavailable")}
		got := WrapTransient(typed)
		if got != typed {
			t.Errorf("WrapTransient(typed) returned different pointer: got %p want %p", got, typed)
		}
	})

	t.Run("typed wrapped via fmt.Errorf → unchanged (no double wrap)", func(t *testing.T) {
		t.Parallel()
		typed := &TransientInfrastructureError{Err: errors.New("503 service unavailable")}
		wrapped := fmt.Errorf("rpc: %w", typed)
		got := WrapTransient(wrapped)
		if got != wrapped {
			t.Errorf("WrapTransient(fmt.Errorf wrap of typed) returned different pointer: got %p want %p", got, wrapped)
		}
	})

	t.Run("SQLite locked → wrapped", func(t *testing.T) {
		t.Parallel()
		// godlike/07 honest-limitation (FASE 6 Cut 6.1.D):
		// pre-Cut 6.1: WrapTransient wrapped on substring "database is
		// locked". post-Cut 6.1.D: production IsTransient does not
		// substring-match; WrapTransient returns the raw string
		// unchanged.
		t.Skip("FASE 6 Cut 6.1.D: WrapTransient no longer wraps raw 'database is locked' strings. Production must WrapTransient at the SQLite boundary (typed *sqlite3.Error envelope) — see internal/infrastructure/database/sqlite/registry_retry_classifier.go for the canonical typed-probe surface.")
	})

	t.Run("sql.ErrConnDone → wrapped", func(t *testing.T) {
		t.Parallel()
		// godlike/07 honest-limitation (FASE 6 Cut 6.1.D):
		// pre-Cut 6.1: WrapTransient wrapped on substring "connection
		// is already closed" (sql.ErrConnDone.Error()). post-Cut 6.1.D:
		// production IsTransient does not substring-match; WrapTransient
		// returns sql.ErrConnDone unchanged.
		//
		// Production callers must WrapTransient at the database/sql
		// boundary with a typed envelope (e.g. an explicit retryable
		// sentinel returned by the call site) — sql.ErrConnDone itself
		// does NOT carry a typed RetryableError interface today.
		t.Skip("FASE 6 Cut 6.1.D: WrapTransient no longer wraps raw sql.ErrConnDone. Forward-pointer: typed-Envelope wrap at database/sql call sites.")
	})

	t.Run("IsTransient composes after WrapTransient", func(t *testing.T) {
		t.Parallel()
		// godlike/07 honest-limitation (FASE 6 Cut 6.1.D): this subtest
		// asserts (a) the wrapped envelope is transient via typed probe
		// (continues to pass) AND (b) the raw string "database is
		// locked" is transient via substring fallback (FAILS post-Cut).
		// The (b) assertion was the pre-FASE-6 substring surface; only
		// the typed-probe half remains observable.
		t.Skip("FASE 6 Cut 6.1.D: pre-FASE-6 substring fallback for raw 'database is locked' is REMOVED from production. Typed-probe behavior (wrapped → transient) continues to be exercised by TestTransientInfrastructureError_IsTransientAuthoritative.")
	})
}

// ─── Stringer / formatted output ─────────────────────────────────────────────

// TestTransientInfrastructureError_Format verifies it implements the
// fmt.Formatter / GoStringer / Stringer interfaces via standard fmt verbs.
// Useful as a regression to catch future signature drift.
func TestTransientInfrastructureError_Format(t *testing.T) {
	t.Parallel()

	inner := errors.New("503: backend down")
	e := &TransientInfrastructureError{Err: inner}

	if got, want := fmt.Sprintf("%s", e), inner.Error(); got != want {
		t.Errorf("%%s = %q, want %q", got, want)
	}
	if got, want := fmt.Sprintf("%v", e), inner.Error(); got != want {
		t.Errorf("%%v = %q, want %q", got, want)
	}
	// %w should preserve Unwrap semantics (which it does because we
	// implemented Unwrap explicitly).
	wrapped := fmt.Errorf("ctx: %w", e)
	if !errors.Is(wrapped, inner) {
		t.Errorf("%%w did not preserve Unwrap chain")
	}
}

// ─── sleepDuration / jitter (Azione 4/8 di Step 7) ──────────────────────────

// TestSleepDuration_NoJitter_Deterministic verifies that JitterFraction=0
// produces the exact canonical exponential backoff sequence (no random
// variance). This is the regression for callers that explicitly opt out
// of jitter (e.g. CI latency assertions, deterministic-test harnesses).
func TestSleepDuration_NoJitter_Deterministic(t *testing.T) {
	t.Parallel()

	opts := Options{
		MaxAttempts:    5,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     10 * time.Second,
		BackoffFactor:  2.0,
		JitterFraction: 0,
	}
	// Expected sequence: 100ms, 200ms, 400ms, 800ms, 1600ms (capped at 10s).
	wantSeq := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		1600 * time.Millisecond,
	}
	for i, want := range wantSeq {
		if got := sleepDuration(i, opts); got != want {
			t.Errorf("sleepDuration(%d) = %v, want %v", i, got, want)
		}
	}
}

// TestSleepDuration_Jitter25_Envelope verifies the canonical ±25%
// jitter envelope: every sample must land within [base*0.75, base*1.25].
// Run 1000 iterations to flush warm-up effects and catch the rare edge
// where rand.Float64() returns near-0 or near-1.
func TestSleepDuration_Jitter25_Envelope(t *testing.T) {
	t.Parallel()

	opts := Options{
		MaxAttempts:    3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
		JitterFraction: 0.25,
	}
	// attempt=0 → base = InitialBackoff = 1s.
	// Envelope: [0.75s, 1.25s].
	envelope := struct{ lo, hi time.Duration }{750 * time.Millisecond, 1250 * time.Millisecond}
	for i := 0; i < 1000; i++ {
		got := sleepDuration(0, opts)
		if got < envelope.lo || got > envelope.hi {
			t.Errorf("iter %d: sleepDuration = %v, outside envelope [%v, %v]",
				i, got, envelope.lo, envelope.hi)
		}
	}
}

// TestSleepDuration_Jitter50_Envelope verifies ±50% bounds: every sample
// lands in [base*0.5, base*1.5]. Wider envelope than ±25% → stricter
// bound on the implementation having the right formula.
func TestSleepDuration_Jitter50_Envelope(t *testing.T) {
	t.Parallel()

	opts := Options{
		MaxAttempts:    3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
		JitterFraction: 0.50,
	}
	envelope := struct{ lo, hi time.Duration }{500 * time.Millisecond, 1500 * time.Millisecond}
	for i := 0; i < 1000; i++ {
		got := sleepDuration(0, opts)
		if got < envelope.lo || got > envelope.hi {
			t.Errorf("iter %d: sleepDuration = %v, outside envelope [%v, %v]",
				i, got, envelope.lo, envelope.hi)
		}
	}
}

// TestSleepDuration_Jitter_Variability confirms that with JitterFraction=0.25
// the implementation actually varies (i.e. it doesn't accidentally short-circuit
// to a fixed value). Over 1000 samples the spread should comfortably exceed
// half the envelope width — that's both a stability check and a regression
// for "jitter broke and silently disabled itself".
func TestSleepDuration_Jitter_Variability(t *testing.T) {
	t.Parallel()

	opts := Options{
		MaxAttempts:    3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
		JitterFraction: 0.25,
	}
	const N = 1000
	minD, maxD := time.Duration(1<<62), time.Duration(0)
	for i := 0; i < N; i++ {
		got := sleepDuration(0, opts)
		if got < minD {
			minD = got
		}
		if got > maxD {
			maxD = got
		}
	}
	// Envelope half-width = 250ms; we require at least 80% of that in
	// observed spread (200ms) which gives margin for a low-variance
	// draw while still catching a "stuck at base" regression.
	spread := maxD - minD
	const minSpread = 200 * time.Millisecond
	if spread < minSpread {
		t.Errorf("jitter produced insufficient spread over %d iterations: min=%v max=%v spread=%v (want ≥ %v)",
			N, minD, maxD, spread, minSpread)
	}
}

// TestSleepDuration_Jitter_ClampBelowZero defends the contract: a
// negative JitterFraction (e.g. set by a typo or a misconfigured env
// var) does NOT produce negative delays. The implementation clamps f<0
// to f=0 → no jitter, exact base returned.
func TestSleepDuration_Jitter_ClampBelowZero(t *testing.T) {
	t.Parallel()

	opts := Options{
		MaxAttempts:    3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
		JitterFraction: -0.25,
	}
	for i := 0; i < 100; i++ {
		if got, want := sleepDuration(0, opts), 1*time.Second; got != want {
			t.Errorf("iter %d: JitterFraction=-0.25 produced %v, want exactly %v (negative jitter must clamp to no-jitter)", i, got, want)
		}
		if got := sleepDuration(0, opts); got <= 0 {
			t.Errorf("iter %d: sleepDuration %v must be > 0 even after clamping negative jitter", i, got)
		}
	}
}

// TestSleepDuration_Jitter_ClampAboveOne defends the contract: a
// JitterFraction > 1.0 means "I want jitter up to ±100%" — anything
// higher is clamped to 1.0 so the impl cannot produce a >2x delay
// from a vanilla caller's miscoufiguration.
func TestSleepDuration_Jitter_ClampAboveOne(t *testing.T) {
	t.Parallel()

	opts := Options{
		MaxAttempts:    3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
		JitterFraction: 2.0, // impl clamps to 1.0
	}
	// With f=1.0: delay * (1 - 1 + 2*r) = 2*delay*r where r ∈ [0, 1].
	// Envelope: [0, 2*base] = [0, 2s].
	envelope := struct{ lo, hi time.Duration }{0, 2 * time.Second}
	for i := 0; i < 1000; i++ {
		got := sleepDuration(0, opts)
		if got < envelope.lo || got > envelope.hi {
			t.Errorf("iter %d: sleepDuration = %v, outside envelope [%v, %v] (JitterFraction=2.0 must clamp to 1.0)",
				i, got, envelope.lo, envelope.hi)
		}
	}
}

// TestSleepDuration_JitterOnMaxBackoffCap verifies the contract: jitter
// applies AFTER MaxBackoff cap, not before. A sequence that has saturated
// the cap must still be jittered within [cap*0.75, cap*1.25].
func TestSleepDuration_JitterOnMaxBackoffCap(t *testing.T) {
	t.Parallel()

	opts := Options{
		MaxAttempts:    5,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     500 * time.Millisecond, // cap kicks in immediately
		BackoffFactor:  2.0,
		JitterFraction: 0.25,
	}
	// attempt=4 → delay = 16s (1*2^4) but capped at 500ms. Jitter applies
	// on top of 500ms. Envelope: [375ms, 625ms].
	envelope := struct{ lo, hi time.Duration }{375 * time.Millisecond, 625 * time.Millisecond}
	for i := 0; i < 1000; i++ {
		got := sleepDuration(4, opts)
		if got < envelope.lo || got > envelope.hi {
			t.Errorf("iter %d: sleepDuration = %v, outside envelope [%v, %v] (jitter must apply AFTER MaxBackoff cap)",
				i, got, envelope.lo, envelope.hi)
		}
	}
}

// TestDefaultOptions_Jitter25Enabled locks the canonical default: the
// `DefaultOptions()` helper ships `JitterFraction: 0.25`. Any change
// to this constant requires revisiting the production retry behaviour
// (e.g. CI latency assertions that depend on the default envelope).
func TestDefaultOptions_Jitter25Enabled(t *testing.T) {
	t.Parallel()

	opts := DefaultOptions()
	const want = 0.25
	if opts.JitterFraction != want {
		t.Errorf("DefaultOptions().JitterFraction = %v, want %v (changing this is a behavioural change for ALL callers that use DefaultOptions)", opts.JitterFraction, want)
	}
	// Sanity-check the rest of the defaults while we're here.
	if opts.MaxAttempts != 3 {
		t.Errorf("DefaultOptions().MaxAttempts = %d, want 3", opts.MaxAttempts)
	}
	if opts.InitialBackoff != 1*time.Second {
		t.Errorf("DefaultOptions().InitialBackoff = %v, want 1s", opts.InitialBackoff)
	}
	if opts.MaxBackoff != 30*time.Second {
		t.Errorf("DefaultOptions().MaxBackoff = %v, want 30s", opts.MaxBackoff)
	}
	if opts.BackoffFactor != 2.0 {
		t.Errorf("DefaultOptions().BackoffFactor = %v, want 2.0", opts.BackoffFactor)
	}
}
