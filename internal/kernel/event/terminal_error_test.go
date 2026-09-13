package event_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
)

// TestIsTerminal_TypedWrap pins the canonical signal: a *TerminalError anywhere
// in the chain classifies as terminal, and the wrapped cause stays reachable.
func TestIsTerminal_TypedWrap(t *testing.T) {
	cause := errors.New("handler refused the envelope")
	term := event.NewTerminalError(cause)
	if term == nil {
		t.Fatal("NewTerminalError must not return nil for a non-nil cause")
	}
	if !event.IsTerminal(term) {
		t.Error("a *TerminalError must classify as terminal")
	}
	if !errors.Is(term, cause) {
		t.Error("the wrapped cause must remain reachable through errors.Is")
	}
	if !event.IsTerminal(fmt.Errorf("outer: %w", term)) {
		t.Error("a wrapped *TerminalError must still classify as terminal")
	}
	var te *event.TerminalError
	if !errors.As(term, &te) || te.Err != cause {
		t.Errorf("errors.As must expose the TerminalError with its cause; got %+v", te)
	}
}

// TestIsTerminal_NonTerminal pins that ordinary failures keep the retry path.
func TestIsTerminal_NonTerminal(t *testing.T) {
	if event.IsTerminal(nil) {
		t.Error("nil must not classify as terminal")
	}
	for _, err := range []error{
		errors.New("connection reset by peer"),
		errors.New("deadline exceeded"),
		errors.New("postgres unavailable"),
	} {
		if event.IsTerminal(err) {
			t.Errorf("%q must stay retryable", err)
		}
	}
}

// TestIsTerminal_LegacyBreadcrumb pins backward compatibility with the
// pre-classifier handlers that self-tag their message with "(terminal)".
func TestIsTerminal_LegacyBreadcrumb(t *testing.T) {
	if !event.IsTerminal(errors.New("unsupported provider (terminal)")) {
		t.Error("the legacy \"(terminal)\" breadcrumb must classify as terminal")
	}
}

// TestNewTerminalError_NilIsNil pins the nil-guard so callers can wrap
// unconditionally.
func TestNewTerminalError_NilIsNil(t *testing.T) {
	if event.NewTerminalError(nil) != nil {
		t.Error("NewTerminalError(nil) must return nil")
	}
}
