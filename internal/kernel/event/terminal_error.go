// Package event — terminal_error.go: the canonical outbox terminal-error
// classifier.
//
// An outbox handler can return one of three shapes:
//
//   - nil              → the event is completed.
//   - retryable error  → MarkFailed with exponential backoff; dead-letter
//     once max_attempts is exhausted.
//   - terminal error   → dead-letter IMMEDIATELY, bypassing the backoff
//     countdown (retrying cannot fix the cause).
//
// This classification is an outbox fact, not an engine fact: the SQLite pool
// (internal/platform/sqlite/outboxevents) and the PostgreSQL media worker
// (internal/platform/postgres/media) share ONE fact family, so they must share
// ONE classifier. Declaring it per engine invited exactly the drift this file
// removes — the PostgreSQL worker used to retry terminal envelopes until
// max_attempts, so a malformed delete-saga payload burned the whole backoff
// budget before dead-lettering.
//
// ──────────────────────────────────────────────────────────────────
// Backward-compat breadcrumb recognition
// ──────────────────────────────────────────────────────────────────
//
// Several real handlers (delivery.go's ErrUnsupportedProvider /
// ErrSchemaVersionMismatch, provider_sync.go's ErrUnknownProvider /
// ErrInvalidMode) were written before this classification existed and signal
// "do not retry" via the "(terminal)" string suffix in the error message.
// IsTerminal honours that promise by also returning true for any error whose
// .Error() contains "(terminal)". New handlers SHOULD wrap with
// NewTerminalError instead — the explicit wrap is greppable, survives
// refactors of the error message, and is the canonical shape in code review.
// The breadcrumb path exists only to keep the pre-existing error sites working.
package event

import (
	"errors"
	"strings"
)

// terminalBreadcrumb is the legacy self-tag embedded by older handler error
// messages. Recognised by IsTerminal so those handlers get the dead-letter
// short-circuit without modifying their return statements.
const terminalBreadcrumb = "(terminal)"

// TerminalError wraps an inner error to mark it non-retryable by an outbox
// worker. Use NewTerminalError to wrap; use errors.As to unwrap.
type TerminalError struct {
	Err error
}

// NewTerminalError wraps err so the outbox classifies it as terminal.
//
// Returns nil when err is nil so callers can write
//
//	return event.NewTerminalError(fmt.Errorf("..."))
//
// without a nil-guard at every call site. Not intended for happy-path returns:
// pass nil directly when the handler succeeded.
func NewTerminalError(err error) error {
	if err == nil {
		return nil
	}
	return &TerminalError{Err: err}
}

// Error returns the underlying error's message so logs / structured fields keep
// their original shape. The breadcrumb is NOT injected here — the typed
// *TerminalError is itself the signal.
func (e *TerminalError) Error() string {
	if e == nil || e.Err == nil {
		return "outbox terminal error"
	}
	return e.Err.Error()
}

// Unwrap exposes the inner error so errors.Is / errors.As traverse the chain.
func (e *TerminalError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsTerminal reports whether err is a non-retryable outbox handler result. True
// when:
//
//   - err (or any wrapped error in its chain) is *TerminalError, OR
//   - err.Error() contains the legacy terminalBreadcrumb "(terminal)".
//
// Returns false for nil. Plain errors (transient network errors,
// sql.ErrConnDone, context.DeadlineExceeded) return false and fall through to
// the worker's exponential-backoff path.
//
// Concurrency: pure function over the error value; no shared state.
func IsTerminal(err error) bool {
	if err == nil {
		return false
	}
	var te *TerminalError
	if errors.As(err, &te) {
		return true
	}
	return strings.Contains(err.Error(), terminalBreadcrumb)
}
