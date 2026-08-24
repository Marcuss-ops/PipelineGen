package drive

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"google.golang.org/api/googleapi"
)

// makeGoogleAPIError is a hermetic helper for constructing googapi.Error
// carriers with the canonical Code/Message fields. Avoids the production
// http.Client dance entirely — the helper behavior under test is the
// errors.As + Code-probe pattern, not the HTTP wire shape.
func makeGoogleAPIError(code int, msg string) *googleapi.Error {
	return &googleapi.Error{
		Code:    code,
		Message: msg,
	}
}

// TestDriveIsNotFound_Genuine404 verifies that the canonical 404 case
// from Google Drive is correctly classified. The Drive REST surfaces a
// "file not found" via Code == http.StatusNotFound (404) on the
// googleapi.Error envelope; DriveIsNotFound must return true.
func TestDriveIsNotFound_Genuine404(t *testing.T) {
	err := makeGoogleAPIError(http.StatusNotFound, "File not found: <fileID>.")
	if !DriveIsNotFound(err) {
		t.Fatalf("DriveIsNotFound(googleapi.Error{Code: 404, ...}) = false; want true")
	}
}

// TestDriveIsNotFound_OtherHTTPStatus verifies that ANY non-404 HTTP code
// is correctly classified as NOT-found. Specifically...
func TestDriveIsNotFound_OtherHTTPStatus(t *testing.T) {
	cases := []int{
		http.StatusInternalServerError, // 500
		http.StatusForbidden,           // 403
		http.StatusBadGateway,          // 502
		http.StatusUnauthorized,        // 401
		http.StatusTooManyRequests,     // 429
	}
	for _, code := range cases {
		err := makeGoogleAPIError(code, "some Drive error")
		if DriveIsNotFound(err) {
			t.Errorf("DriveIsNotFound(googleapi.Error{Code: %d}) = true; want false (only 404 should match)", code)
		}
	}
}

// TestDriveIsNotFound_NonGoogleAPIError verifies that plain errors
// (non-googleapi.Error envelopes) are correctly classified as NOT-found.
// This is the canonical godlike/07 typed-error guard: we NEVER fall back
// to substring-matching on error messages; non-typed envelopes ALWAYS
// return false so callers can safely distinguish "Drive API 404" from
// "some other code-path returned an error".
func TestDriveIsNotFound_NonGoogleAPIError(t *testing.T) {
	cases := []error{
		errors.New("plain error with no type"),
		errors.New("404 (substring match would false-positive godlike/07)"),
		errors.New("notFound (substring match would false-positive godlike/07)"),
		fmt.Errorf("wrapped plain: %w", errors.New("inner plain")),
		&googleapi.Error{Code: http.StatusInternalServerError, Message: "retryable"},
		// nil-tolerance is tested separately; here we cover all error paths
		// for which DriveIsNotFound MUST return false.
	}
	for _, err := range cases {
		if DriveIsNotFound(err) {
			t.Errorf("DriveIsNotFound(%v) = true; want false (typed-error contract forbids substring-match fallback)", err)
		}
	}
}

// TestDriveIsNotFound_WrappedErrorChain verifies that errors.As walks
// the entire fmt.Errorf %w wrap chain so callers need NOT manually
// unwrap. The Drive SDK's typical pattern returns
// fmt.Errorf("drive service call: %w", originalGoogleAPIError) — our
// helper must classify the 404 correctly through N wrap levels.
func TestDriveIsNotFound_WrappedErrorChain(t *testing.T) {
	base := makeGoogleAPIError(http.StatusNotFound, "File not found: abc123.")
	t.Run("single wrap via fmt.Errorf %w", func(t *testing.T) {
		wrapped := fmt.Errorf("drive call failed: %w", base)
		if !DriveIsNotFound(wrapped) {
			t.Fatalf("DriveIsNotFound(single-wrap) = false; want true (errors.As must walk the %%w chain)")
		}
	})
	t.Run("double wrap via fmt.Errorf %w %w (Go 1.20+)", func(t *testing.T) {
		wrapped := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", base))
		if !DriveIsNotFound(wrapped) {
			t.Fatalf("DriveIsNotFound(double-wrap) = false; want true (errors.As must walk nested %%w chains)")
		}
	})
	t.Run("errors.As carrier extraction returns the typed envelope", func(t *testing.T) {
		wrapped := fmt.Errorf("drive call failed: %w", base)
		var gerr *googleapi.Error
		if !errors.As(wrapped, &gerr) {
			t.Fatalf("errors.As failed to extract googleapi.Error from wrapped error; the dive helper probes the wrong type")
		}
		if gerr.Code != http.StatusNotFound {
			t.Fatalf("extracted googleapi.Error.Code = %d; want %d", gerr.Code, http.StatusNotFound)
		}
		// And DriveIsNotFound agrees via the probe.
		if !DriveIsNotFound(gerr) {
			t.Fatalf("DriveIsNotFound(extracted-carrier) = false; want true (post-extraction the probe must agree)")
		}
	})
}

// TestDriveIsNotFound_NilTolerance verifies that a nil error returns
// false (does NOT panic, does NOT match 404) per godlike/07 nil-tolerance:
// callers should NOT have to nil-guard the helper before invocation.
// This is the safety property that lets production callers write
//
//	if DriveIsNotFound(ctx.Err()) { ... }
//
// without explicit nil-checks.
func TestDriveIsNotFound_NilTolerance(t *testing.T) {
	if DriveIsNotFound(nil) {
		t.Fatalf("DriveIsNotFound(nil) = true; want false (godlike/07 nil-tolerance + classify-as-found would be a panic guard violation)")
	}
}
