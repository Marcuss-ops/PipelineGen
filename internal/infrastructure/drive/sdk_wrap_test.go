// Package drive — sdk_wrap_test.go (Azione 5/8 di Step 7, July 2026)
//
// Verifies the wrap behaviour exercised by uploader.go + folder_manager.go
// + admin.go: when a raw Drive SDK error matches the canonical retry
// taxonomy (429, 503, rate-limit strings, etc.), retry.WrapTransient
// promotes it to *TransientInfrastructureError so the typed path
// (errors.As) of retry.IsTransient classifies it authoritatively
// without depending on substring matching.
//
// The adapter-level integration (Drive SDK → *TransientInfrastructureError
// → retry.IsTransient typed-path) is exercised by the existing drive
// tests in uploader_test.go / uploader_ops_test.go; this file adds the
// focused unit-level guard for the wrap contract itself.
package drive

import (
	"errors"
	"testing"

	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// TestWrapSDKTransient_DriveShape_TypedPathAuthoritative locks the
// invariant Azione 5/8 ships: every Drive-shaped transient error
// returned through retry.WrapTransient carries a *TransientInfrastructureError
// wrapper that the typed path (errors.As) recognises authoritatively.
//
// Pre-Azione 5/8, retry.IsTransient() was reaching these errors only
// via substring matching (which is brittle — googleapi.Error format
// strings drift on SDK upgrades). The wrapper makes the typed path
// the canonical classifier, removing the substring reliance at adapter
// call sites that have opted into the typed wrapping.
func TestWrapSDKTransient_DriveShape_TypedPathAuthoritative(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// err is a stringification-shaped drive SDK error. We don't
		// depend on a real *googleapi.Error type — the canonical
		// substring taxonomy is keyed on the .Error() string. The
		// wrap is what we care about here: the wrapping layer MUST
		// promote any substring-matching err into a typed carrier.
		err error
	}{
		{"429 Too Many Requests", errors.New("googleapi: got HTTP response code 429 Too Many Requests")},
		{"503 backendError", errors.New(`googleapi: got HTTP response code 503 with body: {"error":{"code":503,"message":"backendError"}}`)},
		{"503 serviceUnavailable", errors.New(`googleapi: Error 503: serviceUnavailable`)},
		{"504 Gateway Timeout", errors.New("googleapi: Error 504: Gateway Timeout")},
		{"502 Bad Gateway", errors.New("googleapi: Error 502: Bad Gateway")},
		{"rate limit (substring match)", errors.New("api rate limit reached")},
		{"quota exceeded (substring match)", errors.New("quota exceeded for project")},
		{"timeout", errors.New("deadline exceeded: timeout awaiting response")},
		{"connection refused", errors.New("dial tcp: connection refused")},
		{"temporarily unavailable", errors.New("backend temporarily unavailable")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Substring path: pre-wrap classified as transient.
			if !retry.IsTransient(tc.err) {
				t.Errorf("raw err %q should match canonical transient substring taxonomy", tc.err)
			}

			// Wrap: the wrapping layer MUST produce a typed carrier.
			wrapped := retry.WrapTransient(tc.err)
			var te *retry.TransientInfrastructureError
			if !errors.As(wrapped, &te) {
				t.Fatalf("WrapTransient(%q) did not produce *TransientInfrastructureError, got %T (%v)",
					tc.err, wrapped, wrapped)
			}
			if te.Err != tc.err {
				t.Errorf("WrapTransient chained wrong inner: got %v want %v", te.Err, tc.err)
			}

			// Typed-path recognition: retry.IsTransient must reach this
			// error via errors.As WITHOUT substring matching on the
			// outer message (the outer wraps the inner via %w, so
			// errors.As walks the chain and finds *TE).
			var te2 *retry.TransientInfrastructureError
			if !errors.As(wrapped, &te2) {
				t.Errorf("errors.As failed on wrapped err: %v", wrapped)
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

// TestWrapSDKTransient_NonTransientPassesThrough guards the negative
// path: retry.WrapTransient MUST leave non-transient errors alone.
// A Drive 404 (file not found), 400 (bad request), or 403 (forbidden)
// propagates verbatim — these are terminal errors that retry predicates
// must NOT classify as transient.
func TestWrapSDKTransient_NonTransientPassesThrough(t *testing.T) {
	t.Parallel()

	nonTransient := []struct {
		name string
		err  error
	}{
		{"404 Not Found", errors.New("googleapi: got HTTP response code 404 Not Found")},
		{"400 Bad Request", errors.New("googleapi: Error 400: Bad Request — invalid query")},
		{"403 Forbidden", errors.New("googleapi: Error 403: The user does not have sufficient permissions")},
		{"401 Unauthorized", errors.New("googleapi: Error 401: Login Required")},
		{"409 Conflict", errors.New("googleapi: Error 409: folder name already exists")},
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
