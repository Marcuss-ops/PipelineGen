// Package jobs — queue_notifier.go (canonical typed-port refactor, June 2026).
//
// Phase 5 (June 2026, chore/repository-cleanup): the QueueNotifier
// INTERFACE was MOVED from this file to internal/application/jobs/
// notifier.go (the application-tier typed-port location per AGENTS.md
// Pattern 0). This file now holds ONLY the concrete `notifier` struct
// + its methods. *sqljobs.SQLiteStore (defined in repository.go) exposes
// Subscribe + Broadcast methods that delegate to a private *notifier
// instance and satisfy the application-tier QueueNotifier port via
// duck-typing.
//
// The pre-Phase-5 in-package assertion
// `var _ QueueNotifier = (*notifier)(nil)` is DROPPED. Rationale: the
// assertion would now require this file to import the application tier
// (to reference jobs.QueueNotifier), creating a forbidden
// infrastructure → application import cycle. The transitive
// application-tier assertion in
// `internal/application/jobs/notifier.go::var _ QueueNotifier = (*sqljobs.SQLiteStore)(nil)`
// is the canonical seam marker; it verifies the SQLiteStore path (the
// only public surface composition root actually wires), so the
// in-package *notifier is covered transitively.
//
// History:
//   - Pre-PR-7 (June 2026): queueNotifier was an unexported struct.
//   - PR 7 (codex/qdrant-app-writers-fail-closed followup): promoted
//     to a `QueueNotifier` struct (rename) and then to a `QueueNotifier`
//     INTERFACE in this file. Type alias in application/jobs/notifier.go
//     resolved `type QueueNotifier = sqljobs.QueueNotifier`.
//   - Phase 5: interface moved to application tier, alias dropped,
//     in-package assertion dropped (cycle prevention).

package jobs

import "sync"

// notifier is the default QueueNotifier implementation: an internal
// channel wrapped by a mutex. Broadcast closes + replaces the
// channel; Subscribe returns the current channel. NOT exported —
// callers go through the application-tier QueueNotifier interface
// (internal/application/jobs/notifier.go) so future adapter
// replacements (Postgres LISTEN/NOTIFY) are drop-in.
//
// Lifecycle: lifecycle is fully owned by *SQLiteStore (constructed
// in NewSQLiteStore). The SQLiteStore's exported Subscribe /
// Broadcast methods (defined in repository.go) are the typed-port
// surface; they delegate to a private *notifier instance here.
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
