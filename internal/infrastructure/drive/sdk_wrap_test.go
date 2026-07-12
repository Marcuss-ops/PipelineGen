// Package drive — sdk_wrap_test.go (Azione 5/8 di Step 7, July 2026,
// FASE 6 Cut 6.1.D rewrite July 2026)
//
// Verifies the wrap behaviour exercised by uploader.go + folder_manager.go
// + admin.go: when a Drive SDK call returns a typed *googleapi.Error
// matching the canonical retry taxonomy (429, 5xx, 408), retry.WrapTransient
// routes it through the Decision() walker, where the registered
// googleapi Classifier (registry_google.go) emits Retryable=true, and
// the typed envelope *TransientInfrastructureError is produced. The
// typed path (errors.As) of retry.IsTransient then classifies the
// wrapped error authoritatively without depending on substring
// matching.
//
// FASE 6 Cut 6.1.D (July 2026): production retry.IsTransient became a
// PURE typed probe (RetryableError interface + *TransientInfrastructureError
// carrier via errors.As). The pre-cut substring taxonomy is REMOVED
// from production; raw `errors.New("googleapi: 429 ...")` strings
// are NOT classified by the production googleapi Classifier (which
// matches the typed `*googleapi.Error` shape via errors.As, not raw
// strings). For the pre-cut taxonomy surface, see the FORWARD-POINTER
// below.
//
// FORWARD-POINTER: see pkg/retry/transient_legacy_test.go for the
// pre-FASE-6 substring taxonomy (transientSubstringsLegacy slice +
// unexported classifyLegacyTransientForTest helper). The unexported
// helper is only accessible from within the pkg/retry package
// itself, so drive-pkg tests cannot opt into the legacy classifier
// — the pre-cut surface is observable in pkg/retry's own test
// suite only. This is the SINGLE CANONICAL forward-pointer for
// the pre-cut taxonomy across the test tree; do not duplicate it
// in test function docs (use "see the package doc FORWARD-POINTER"
// as a cross-reference instead).
//
// The adapter-level integration (Drive SDK → *TransientInfrastructureError
// → retry.IsTransient typed-path) is exercised by the existing drive
// tests in uploader_test.go / uploader_ops_test.go; this file adds the
// focused unit-level guard for the wrap contract itself.
package drive

import (
	"errors"
	"testing"

	"google.golang.org/api/googleapi"

	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// TestWrapSDKTransient_TypedErrorAuthoritative locks the invariant
// Azione 5/8 ships for the typed-SDK path: every typed *googleapi.Error
// returned from a Drive SDK call that matches the canonical transient
// HTTP status taxonomy (429, 502, 503, 504) is wrapped by
// retry.WrapTransient into a *TransientInfrastructureError via the
// registered googleapi Classifier (registry_google.go), and the typed
// path (errors.As) of retry.IsTransient classifies the wrapped envelope
// authoritatively.
//
// FASE 6 Cut 6.1.D (July 2026): the test uses REAL *googleapi.Error
// typed values (not raw `errors.New("googleapi: 429 ...")` strings)
// because production retry.IsTransient is now a pure typed probe
// and the registered googleapi Classifier matches the typed
// *googleapi.Error shape via errors.As — not raw strings. The
// pre-FASE-6 "Azione 8/8F" block (6 camelCase + SNAKE_CASE shape
// checks) is REMOVED: production no longer substring-matches
// gerr.Message — the googleapi Classifier only inspects gerr.Code
// (HTTP status). See the package doc FORWARD-POINTER for the
// pre-cut surface location.
//
// Companion test: TestWrapSDKTransient_RawStringWrapAuthoritative
// covers the SDK-boundary "raw-string pre-wrapped envelope" path
// (DNS, network, raw transport errors) that this test does not.
func TestWrapSDKTransient_TypedErrorAuthoritative(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// err is a typed *googleapi.Error. The Drive SDK returns
		// this exact type on exit (Files.Create, Files.Get, etc.).
		// The production googleapi Classifier (registry_google.go)
		// matches it via errors.As(err, &gerr) where gerr is
		// *googleapi.Error. HTTP 429 → ErrGoogleAPIThrottled
		// (retryable); HTTP 5xx → ErrGoogleAPIServer (retryable).
		err error
	}{
		{"429 Too Many Requests", &googleapi.Error{Code: 429, Message: "Too Many Requests"}},
		{"503 backendError", &googleapi.Error{Code: 503, Message: "backendError"}},
		{"503 serviceUnavailable", &googleapi.Error{Code: 503, Message: "serviceUnavailable"}},
		{"504 Gateway Timeout", &googleapi.Error{Code: 504, Message: "Gateway Timeout"}},
		{"502 Bad Gateway", &googleapi.Error{Code: 502, Message: "Bad Gateway"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Wrap: the registered googleapi Classifier must produce
			// a typed carrier for the transient HTTP-status shape.
			wrapped := retry.WrapTransient(tc.err)
			var te *retry.TransientInfrastructureError
			if !errors.As(wrapped, &te) {
				t.Fatalf("WrapTransient(%v) did not produce *TransientInfrastructureError, got %T (%v)",
					tc.err, wrapped, wrapped)
			}
			if te.Err != tc.err {
				t.Errorf("WrapTransient chained wrong inner: got %v want %v", te.Err, tc.err)
			}

			// Typed-path recognition: retry.IsTransient must reach
			// this error via errors.As (typed path #2 in
			// transient.go::IsTransient), NOT via substring matching.
			if !retry.IsTransient(wrapped) {
				t.Errorf("IsTransient(wrapped) = false; want true (typed-probe path via *TransientInfrastructureError)")
			}

			// Idempotency: double-wrap does not stack wrappers. The
			// canonical behaviour (per pkg/retry.WrapTransient
			// contract) is to return wrapped unchanged once a typed
			// layer is already present.
			doubleWrapped := retry.WrapTransient(wrapped)
			if doubleWrapped != wrapped {
				t.Errorf("WrapTransient is not idempotent: got %p want %p",
					doubleWrapped, wrapped)
			}
		})
	}
}

// TestWrapSDKTransient_RawStringWrapAuthoritative locks the invariant
// for the SDK-boundary "raw-string pre-wrapped envelope" path:
// non-typed emit shapes (DNS, network, raw transport errors) that
// the Drive SDK can also produce are wrapped at the SDK boundary into
// a *TransientInfrastructureError envelope, and the typed path
// (errors.As) of retry.IsTransient classifies the envelope
// authoritatively.
//
// FASE 6 Cut 6.1.D (July 2026): the negative-pin `!retry.IsTransient(tc.err)`
// for the raw string BEFORE the wrap guards against accidental
// re-introduction of the pre-FASE-6 substring fallback in production.
// A pre-wrapped envelope (`&TransientInfrastructureError{Err: tc.err}`)
// is the canonical SDK-boundary emission shape: in production, the
// Decision() walker's Catch-all Classifier produces it via
// retry.WrapTransient, and IsTransient then reaches the envelope
// via errors.As (typed path #2 in transient.go::IsTransient). The
// test below constructs the envelope directly to pin the typed-probe
// invariant without depending on the Catch-all Classifier's exact
// emission semantics.
//
// Companion test: TestWrapSDKTransient_TypedErrorAuthoritative covers
// the typed-SDK path (typed *googleapi.Error with canonical transient
// HTTP status) that this test does not. See the package doc
// FORWARD-POINTER for the pre-cut surface location.
func TestWrapSDKTransient_RawStringWrapAuthoritative(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{"rate limit (raw string)", errors.New("api rate limit reached")},
		{"quota exceeded (raw string)", errors.New("quota exceeded for project")},
		{"timeout (raw string)", errors.New("deadline exceeded: timeout awaiting response")},
		{"connection refused (raw string)", errors.New("dial tcp: connection refused")},
		{"temporarily unavailable (raw string)", errors.New("backend temporarily unavailable")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Pre-wrap: raw string is NOT classified by the typed
			// probe (no substring fallback in production). This
			// guards against accidental re-introduction of the
			// pre-FASE-6 substring path.
			if retry.IsTransient(tc.err) {
				t.Errorf("raw err %q MUST NOT classify as transient post-Cut 6.1.D (no substring fallback)", tc.err)
			}

			// Pre-wrap at the SDK boundary: the canonical adapter
			// emission shape wraps the raw err in a typed envelope.
			envelope := &retry.TransientInfrastructureError{Err: tc.err}

			// Typed-path recognition: IsTransient reaches the
			// envelope via errors.As (typed path #2).
			if !retry.IsTransient(envelope) {
				t.Errorf("IsTransient(wrapped envelope) = false; want true (typed-probe path via *TransientInfrastructureError)")
			}

			// Idempotency: double-wrap on the already-typed
			// envelope is a no-op.
			doubleWrapped := retry.WrapTransient(envelope)
			if doubleWrapped != envelope {
				t.Errorf("WrapTransient(already-typed envelope) is not idempotent: got %p want %p",
					doubleWrapped, envelope)
			}
		})
	}
}

// TestWrapSDKTransient_NonTransientPassesThrough guards the negative
// path: retry.WrapTransient MUST leave non-transient errors alone.
// A Drive 404 (file not found), 400 (bad request), or 403 (forbidden)
// propagates verbatim — these are terminal errors that retry predicates
// must NOT classify as transient.
//
// FASE 6 Cut 6.1.D (July 2026): the negative cases use typed
// *googleapi.Error with terminal status codes (403, 404, 400, 401,
// 409). The registered googleapi Classifier (registry_google.go)
// emits Retryable=false for these (ErrGoogleAPIPermission,
// ErrGoogleAPINotFound, ErrGoogleAPIClient), so WrapTransient does
// NOT wrap. The two non-SDK cases (validation: missing field,
// unparseable JSON) are raw strings the Classifier chain does not
// match, so WrapTransient also passes them through unchanged.
func TestWrapSDKTransient_NonTransientPassesThrough(t *testing.T) {
	t.Parallel()

	nonTransient := []struct {
		name string
		err  error
	}{
		{"404 Not Found", &googleapi.Error{Code: 404, Message: "Not Found"}},
		{"400 Bad Request", &googleapi.Error{Code: 400, Message: "Bad Request — invalid query"}},
		{"403 Forbidden", &googleapi.Error{Code: 403, Message: "The user does not have sufficient permissions"}},
		{"401 Unauthorized", &googleapi.Error{Code: 401, Message: "Login Required"}},
		{"409 Conflict", &googleapi.Error{Code: 409, Message: "folder name already exists"}},
		{"validation: missing field", errors.New("validation: missing channel_id")},
		{"unparseable JSON", errors.New("payload marshal: invalid JSON")},
	}
	for _, tc := range nonTransient {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if retry.IsTransient(tc.err) {
				t.Errorf("raw err %q should NOT classify as transient", tc.err)
			}
			got := retry.WrapTransient(tc.err)
			if got != tc.err {
				t.Errorf("WrapTransient(non-transient) returned different pointer: got %p want %p",
					got, tc.err)
			}
			if retry.IsTransient(got) {
				t.Errorf("wrapped non-transient err still classified as transient: %v", got)
			}
		})
	}
}

// TestWrapSDKTransient_NilSafe verifies the nil-safe contract:
// retry.WrapTransient(nil) returns nil. The drive adapter's nil check
// on `if err != nil` matters less to this test (call sites always
// gate on nil err first) but the helper's nil-handling is what makes
// inline `retry.WrapTransient(err)` safe to drop into fmt.Errorf %w
// arguments without explicit nil-guarding.
func TestWrapSDKTransient_NilSafe(t *testing.T) {
	t.Parallel()
	if got := retry.WrapTransient(nil); got != nil {
		t.Errorf("WrapTransient(nil) = %v, want nil", got)
	}
}
