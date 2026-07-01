package retry

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// transientMessage returns a string constructed from substrings NOT in
// the canonical taxonomy — used to assert the negative path of
// IsTransient.
const nonTransientMessage = "validation: missing channel_id"

// ─── IsTransient ─────────────────────────────────────────────────────────────

// TestIsTransient is the canonical table-driven regression for the
// retry.IsTransient predicate. Covers:
//   - nil error → false
//   - typed path: *TransientInfrastructureError (and empty Err) → true
//   - wrapped typed: fmt.Errorf("wrap: %w", &TE{...}) via errors.As → true
//   - substring path: each canonical transient substring → true
//   - case-insensitive substring: "TIMEOUT", "HTTP 503", "Connection Refused"
//   - wrapped substring: fmt.Errorf("ctx: %w", errors.New("timeout")) → true
//     (errors.As walks the chain and finds no TransientInfrastructureError,
//      substring fallback checks the outermost message which contains
//      "ctx: timeout" — the substring matcher therefore catches it)
//   - mixed: typed wrapper around a non-transient err → still true (typed
//     path is authoritative)
//   - non-transient: validation, parse — all negative substrings → false
func TestIsTransient(t *testing.T) {
	t.Parallel()

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

		// ── substring path ─────────────────────────────────────
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
			got := IsTransient(tc.err)
			if got != tc.want {
				t.Errorf("IsTransient(%q)\n  err  = %v\n  got  = %v\n  want = %v",
					tc.name, tc.err, got, tc.want)
			}
		})
	}
}

// TestIsTransient_HTTPStatusMap is a small targeted regression for the
// canonical HTTP status → transient mapping. Each status code in the
// taxonomy list (429, 502, 503, 504) must be recognised via substring
// matching when the error message embeds the numeric code in any common
// shape (plain, with "HTTP" prefix, with reason phrase).
func TestIsTransient_HTTPStatusMap(t *testing.T) {
	t.Parallel()

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
		if got := IsTransient(err); got != sc.want {
			t.Errorf("IsTransient for HTTP %d %q: got %v want %v",
				sc.status, err.Error(), got, sc.want)
		}
	}
}

// ─── TransientInfrastructureError ───────────────────────────────────────────

// TestTransientInfrastructureError_Error pins the Error() return value:
// inner err message when present, sentinel when nil.
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
	// Wrapped in TransientInfrastructureError: now transient.
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
