// Package jobs (notifier.go) — canonical in-process QueueNotifier port
// (PR-Polling / ADR-0002 §D6.5, June 2026).
//
// Application layer owns the port; infrastructure layer provides the
// implementation. *sqljobs.SQLiteStore satisfies the interface (it
// forwards Subscribe/Broadcast to its embedded *queueNotifier per
// repository.go).
package jobs

// QueueNotifier is the canonical in-process wake-up broadcast port
// consumed by Worker (worker.go).
type QueueNotifier interface {
	Subscribe() <-chan struct{}
	Broadcast()
}
