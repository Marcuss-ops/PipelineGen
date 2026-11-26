// errors.go — terminal-error classifier re-exports.
//
// The classifier lives in internal/kernel/event (terminal_error.go) so the
// SQLite pool and the PostgreSQL media worker cannot drift: an outbox terminal
// error is a storage-engine-independent fact. This package is the
// engine-neutral name for it.
//
// The three handler outcomes are:
//
//   - nil             → the event is marked completed.
//   - retryable error → retried with exponential backoff; dead-letter after
//     max_attempts.
//   - terminal error  → dead-lettered immediately, bypassing the backoff
//     countdown.
package outbox

import "github.com/Marcuss-ops/PipelineGen/internal/kernel/event"

// TerminalError wraps an inner error to mark it non-retryable by an outbox
// pool. Alias (not a new type) so `errors.As(err, &te)` keeps matching across
// both engine adapters.
type TerminalError = event.TerminalError

// NewTerminalError wraps err so a pool classifies it as terminal. Returns nil
// when err is nil.
func NewTerminalError(err error) error {
	return event.NewTerminalError(err)
}

// IsTerminal reports whether err is a non-retryable outbox handler result: true
// for *TerminalError anywhere in the chain, and for the legacy "(terminal)"
// message breadcrumb. Returns false for nil.
func IsTerminal(err error) bool {
	return event.IsTerminal(err)
}
