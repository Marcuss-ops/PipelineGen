// Package outboxevents — TerminalError + IsTerminal classifier.
//
// The classifier itself lives in internal/kernel/event (terminal_error.go) so
// the SQLite pool and the PostgreSQL media worker cannot drift: an outbox
// terminal error is a storage-engine-independent fact. This file is the
// SQLite-side name for it.
//
// The three handler outcomes are:
//
//   - nil            → the event is MarkCompleted.
//   - retryable error → MarkFailed with exponential backoff; dead-letter after
//     max_attempts.
//   - terminal error  → MarkDeadLetter immediately, bypassing the backoff
//     countdown.
//
// New handlers SHOULD wrap with NewTerminalError rather than relying on the
// legacy "(terminal)" message suffix; see the kernel implementation for the
// full rationale.
//
// Ticket reference: QDRANT-002 checklist item G — Retry e classificazione
// errori.
package outboxevents

import "github.com/Marcuss-ops/PipelineGen/internal/kernel/event"

// TerminalError wraps an inner error to mark it non-retryable by the outbox
// Pool. Alias (not a new type) so `errors.As(err, &te)` keeps matching across
// both engine adapters.
type TerminalError = event.TerminalError

// NewTerminalError wraps err so the Pool classifies it as terminal. Returns nil
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
