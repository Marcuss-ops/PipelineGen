package outboxevents

import (
	"errors"
	"fmt"
	"testing"
)

// TestIsTerminal_NilReturnsFalse pins the base case: a nil error is
// not terminal (handlers that return nil are happy-path, not
// dead-letter candidates).
func TestIsTerminal_NilReturnsFalse(t *testing.T) {
	if IsTerminal(nil) {
		t.Fatal("IsTerminal(nil) must return false (nil == success, not a terminal error)")
	}
}

// TestIsTerminal_TypedError verifies the canonical path: a handler
// that wraps with NewTerminalError is classified as terminal
// regardless of the wrapped cause.
func TestIsTerminal_TypedError(t *testing.T) {
	cause := errors.New("qdrant schema mismatch")
	wrapped := NewTerminalError(cause)
	if !IsTerminal(wrapped) {
		t.Fatal("NewTerminalError-wrapped error must be classified as terminal")
	}
	// errors.Is / errors.As must still reach the wrapped cause so
	// callers can recover the underlying context.
	if !errors.Is(wrapped, cause) {
		t.Errorf("errors.Is(wrapped, cause) = false; want true so unwrap chain intact")
	}
	var te *TerminalError
	if !errors.As(wrapped, &te) {
		t.Errorf("errors.As(wrapped, *TerminalError) = false; want true")
	}
}

// TestIsTerminal_StringBreadcrumb verifies the backward-compat
// recognition of the legacy "(terminal)" string suffix emitted by
// delivery.go / provider_sync.go. Adding this guarantee to IsTerminal
// means those existing handlers benefit from the dead-letter short
// circuit without a code change.
//
// The "complete sentence contains breadcrumb" cases pin the contract
// precisely: any error message where "(terminal)" appears as a
// sub-string counts as terminal, by design. Future "tighten the
// match" refactors that would not accept these strings WILL break
// the existing delivery.go / provider_sync.go breadcrumb path and
// the contract test will surface it.
func TestIsTerminal_StringBreadcrumb(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"plain_errors_new", errors.New("delivery.requested: unsupported provider (terminal)")},
		{"fmt_wrap_with_breadcrumb", fmt.Errorf("delivery: %w", errors.New("(terminal) bad payload"))},
		{"wrapped_breadcrumb_in_outer", fmt.Errorf("pool: %w", errors.New("handler returned: (terminal)"))},
		{"complete_sentence_with_breadcrumb", errors.New("status reached (terminal) state cleanly")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !IsTerminal(tc.err) {
				t.Errorf("IsTerminal = false on breadcrumb-bearing error %q; want true", tc.err.Error())
			}
		})
	}
}

// TestIsTerminal_PlainErrorReturnsFalse pins the conservative
// default: a normal non-wrapped error is retryable (terminal=false).
// The Pool's exponential-backoff path is the right place for these.
//
// Three near-miss cases pin the breadth rule precisely:
//   - "terminal illness" — the word "terminal" without parens is a
//     common English word; it must NOT trigger dead-letter (would
//     bury real retries as silent dead letter rows).
//   - "SIGTERM" — short Unix signal name accidentally contains
//     "term"; must NOT trigger.
//   - "terminally broken format" — hyphenated prefix must NOT.
//
// Adding these to the contract test prevents a future "let's be
// smarter about substring matching" refactor from silently firing
// false positives on routine operational messages.
func TestIsTerminal_PlainErrorReturnsFalse(t *testing.T) {
	cases := []error{
		errors.New("network: timeout"),
		errors.New("qdrant: 503 service unavailable"),
		fmt.Errorf("wrapped: %w", errors.New("transient")),
		errors.New("terminally broken format"),
		errors.New("terminal illness detected"),
		errors.New("process group sent SIGTERM to children"),
		errors.New("connection termination by peer (cleanly)"),
	}
	for i, e := range cases {
		if IsTerminal(e) {
			t.Errorf("case %d: IsTerminal(plain %q) = true; want false", i, e.Error())
		}
	}
}

// TestNewTerminalError_NilReturnsNil locks the nil-safe behaviour:
// callers can write `return outboxevents.NewTerminalError(fmt.Errorf(...))`
// without an outer nil-guard. The contract is documented in
// NewTerminalError; this test prevents future regressions.
func TestNewTerminalError_NilReturnsNil(t *testing.T) {
	if got := NewTerminalError(nil); got != nil {
		t.Fatalf("NewTerminalError(nil) = %v; want nil", got)
	}
}

// TestTerminalError_ErrorFormat verifies the message surface stays
// stable so log fields don't lose context when the wrap is applied.
// We expose the underlying error's message verbatim (no injected
// breadcrumb) because the typed *TerminalError IS the signal.
func TestTerminalError_ErrorFormat(t *testing.T) {
	cause := errors.New("qdrant: dimension mismatch")
	got := NewTerminalError(cause).Error()
	if got != "qdrant: dimension mismatch" {
		t.Fatalf("Error() = %q; want unchanged underlying message %q", got, cause.Error())
	}
}

// TestTerminalError_Unwrap exposes the inner error so errors.Is /
// errors.As traverse both directions. Critical for tests that
// assert a specific known error type AND expect it to also be
// classified as terminal.
func TestTerminalError_Unwrap(t *testing.T) {
	sentinel := errors.New("sentinel-cause")
	wrapped := NewTerminalError(sentinel)
	if !errors.Is(wrapped, sentinel) {
		t.Fatal("errors.Is must reach sentinel through wrap")
	}
	if unwrapped := errors.Unwrap(wrapped); unwrapped != sentinel {
		t.Fatalf("errors.Unwrap = %v; want %v", unwrapped, sentinel)
	}
}

// TestIsTerminal_DoubleWrapTerminal ensures that wrapping a
// TerminalError in fmt.Errorf still classifies as terminal — the
// typed classifier walks the full chain via errors.As.
func TestIsTerminal_DoubleWrapTerminal(t *testing.T) {
	inner := NewTerminalError(errors.New("bad"))
	outer := fmt.Errorf("delivery layer: %w", inner)
	if !IsTerminal(outer) {
		t.Fatal("IsTerminal(double-wrapped TerminalError) must be true; errors.As must walk the chain")
	}
}
