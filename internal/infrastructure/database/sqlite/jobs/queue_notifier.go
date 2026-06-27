package jobs

import "sync"

// QueueNotifier is the exported application-tier wake-on-Enqueue port.
// The original unexported `queueNotifier` was promoted to `QueueNotifier`
// so the application-tier type alias `type QueueNotifier = sqljobs.QueueNotifier`
// (internal/application/jobs/notifier.go) is satisfiable by *SQLiteStore.
// Lifecycle and broadcast semantics are unchanged.
type QueueNotifier struct {
	mu sync.Mutex
	ch chan struct{}
}

func newQueueNotifier() *QueueNotifier {
	return &QueueNotifier{ch: make(chan struct{})}
}

func (n *QueueNotifier) Subscribe() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ch
}

func (n *QueueNotifier) Broadcast() {
	n.mu.Lock()
	defer n.mu.Unlock()

	close(n.ch)
	n.ch = make(chan struct{})
}
