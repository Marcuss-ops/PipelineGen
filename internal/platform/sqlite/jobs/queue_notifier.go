// Package jobs — queue_notifier.go (canonical typed-port refactor, June 2026).
//
// PR 7 followup (June 2026, codex/qdrant-app-writers-fail-closed followup):
// the unexported `queueNotifier` struct was first promoted to a `QueueNotifier`
// struct (rename pass) and then to a `QueueNotifier` INTERFACE — the canonical
// pattern per AGENTS.md Pattern 0 (typed-port abstraction). The previous struct
// shape would force every consumer to depend on the concrete type which blocks
// future Postgres LISTEN/NOTIFY adapters and prevents the type-alias plumbing
// in internal/capabilities/jobs/queue/notifier.go from satisfying its
// compile-time assertion `var _ QueueNotifier = (*sqljobs.SQLiteStore)(nil)`.
//
// Resolution: QueueNotifier is now an interface with exactly two methods
// (Subscribe + Broadcast), and the default SQLiteStore-backed implementation
// is named `notifier` (unexported, package-internal) so consumers cannot
// accidentally depend on the struct shape. The application-tier
// `type QueueNotifier = sqljobs.QueueNotifier` alias picks up the
// interface (Go type aliases resolve to whatever the target is, struct or
// interface). Lifecycle + broadcast semantics are unchanged from the prior
// struct.

package jobs

import "sync"

// QueueNotifier is the canonical typed port for the in-process
// wake-on-Enqueue primitive (PR-Polling / ADR-0002 §D6.5, June 2026).
// Single-process scope — a future Postgres adapter ships a separate
// LISTEN/NOTIFY adapter that also satisfies this interface.
//
// Method set is intentionally minimal: Subscribe returns the live
// notification channel; Broadcast wakes every live subscriber and
// replaces the channel so the next Subscribe returns the fresh one.
// Adding methods here is a typed-port drift and requires updating
// every concrete implementation; the alternate path is to introduce
// a new interface (e.g. BulkNotifier) that embeds QueueNotifier.
type QueueNotifier interface {
	Subscribe() <-chan struct{}
	Broadcast()
}

// notifier is the default QueueNotifier implementation: an internal
// channel wrapped by a mutex. Broadcast closes + replaces the
// channel; Subscribe returns the current channel. NOT exported —
// callers go through the QueueNotifier interface so future adapter
// replacements (Postgres) are drop-in.
type notifier struct {
	mu sync.Mutex
	ch chan struct{}
}

// newNotifier returns a fresh SQLite-backed QueueNotifier
// implementation.
func newNotifier() *notifier {
	return &notifier{ch: make(chan struct{})}
}

// Subscribe returns the current live channel (the one the next
// Broadcast will close + replace).
func (n *notifier) Subscribe() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ch
}

// Broadcast closes the current channel and installs a fresh open
// channel. All in-flight subscribers unblock; new subscribers join
// the fresh channel.
func (n *notifier) Broadcast() {
	n.mu.Lock()
	defer n.mu.Unlock()
	close(n.ch)
	n.ch = make(chan struct{})
}

// Compile-time assertion: the default implementation satisfies the
// canonical port. Defence-in-depth against accidental drift.
var _ QueueNotifier = (*notifier)(nil)
