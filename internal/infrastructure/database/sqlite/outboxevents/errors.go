// Package outboxevents — TerminalError + IsTerminal classifier.
//
// Outbox handlers can return one of three shapes:
//
//   - nil  → event is MarkCompleted.
//   - retryable error (any non-nil error that is NOT terminal) →
//     event is MarkFailed with exponential backoff; eventually
//     dead_letter after max_attempts.
//   - terminal error → event is MarkDeadLetter immediately,
//     bypassing the backoff countdown.
//
// Defining a single classification function (IsTerminal) keeps the
// Pool.processEvent site the only place that decides retry vs
// dead_letter; handlers stay free of pool internals.
//
// ──────────────────────────────────────────────────────────────────
// Backward-compat breadcrumb recognition
// ──────────────────────────────────────────────────────────────────
//
// Several real handlers (delivery.go's ErrUnsupportedProvider /
// ErrSchemaVersionMismatch, provider_sync.go's ErrUnknownProvider /
// ErrInvalidMode) were written before this classification existed
// and signal "do not retry" via the "(terminal)" string suffix in
// the error message. The PR comment on ErrUnsupportedProvider:
//
//	//   (We tag the error string with "terminal" so the pool's
//	//   MarkFailed can implement an allowlist for terminal-only
//	//   error families; today the pool always retries on
//	//   non-nil error, but the tag is forward-compatible with a
//	//   future "no_retry" classifier without a wrapper layer.)
//
// IsTerminal honours that promise by also returning true for any
// error whose .Error() contains "(terminal)". New handlers SHOULD
// wrap with NewTerminalError instead — the explicit wrap is greppable,
// survives refactors of the error message, and is the canonical
// shape in subsequent code review. The breadcrumb path is kept only
// to avoid rewriting the ~12 existing error sites scattered across
// delivery.go / provider_sync.go / outboxevents.go's "no handler
// registered" branch.
//
// Ticket reference: QDRANT-002 checklist item G — Retry e
// classificazione errori. This file closes the propagation half of
// G (handler signature + Pool classifier); the typed-error half is
// IndexingHandler.ParsePayload → NewTerminalError, landed alongside
// in this PR.
package outboxevents

import (
	"errors"
	"strings"
)

// terminalBreadcrumb is the legacy self-tag embedded by older handler
// error messages (delivery / provider_sync). Recognised by IsTerminal
// so existing handlers benefit from the dead-letter short-circuit
// without modifying their return statements.
const terminalBreadcrumb = "(terminal)"

// TerminalError wraps an inner error to mark it non-retryable by the
// outbox Pool. Use NewTerminalError to wrap; use errors.As to unwrap.
type TerminalError struct {
	Err error
}

// NewTerminalError wraps err so the Pool classifies it as terminal.
//
// Returns nil when err is nil so callers can write
//
//	return outboxevents.NewTerminalError(fmt.Errorf("..."))
//
// without a nil-guard at every call site. Not intended for use on
// happy-path returns: pass nil directly when the handler succeeded.
func NewTerminalError(err error) error {
	if err == nil {
		return nil
	}
	return &TerminalError{Err: err}
}

// Error returns the underlying error's message so logs / structured
// fields keep their original shape. The "(terminal)" breadcrumb is
// not injected here — the typed *TerminalError is itself the signal;
// IsTerminal recognises this type without relying on string content.
func (e *TerminalError) Error() string {
	if e == nil {
		return "outbox terminal error"
	}
	if e.Err == nil {
		return "outbox terminal error"
	}
	return e.Err.Error()
}

// Unwrap exposes the inner error so errors.Is / errors.As traverse
// the chain. Tests that check `errors.Is(err, target)` continue to
// match the wrapped cause.
func (e *TerminalError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsTerminal reports whether err is a non-retryable outbox handler
// result. True when:
//
//   - err (or any wrapped error in its chain) is *TerminalError, OR
//   - err.Error() contains the legacy terminalBreadcrumb "(terminal)".
//
// Returns false for nil. Plain errors (including transient network
// errors, sql.ErrConnDone, context.DeadlineExceeded) return false and
// fall through to the Pool's exponential-backoff path.
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
