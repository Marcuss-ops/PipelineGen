package jobs

import "sync"

type queueNotifier struct {
	mu sync.Mutex
	ch chan struct{}
}

func newQueueNotifier() *queueNotifier {
	return &queueNotifier{ch: make(chan struct{})}
}

func (n *queueNotifier) Subscribe() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ch
}

func (n *queueNotifier) Broadcast() {
	n.mu.Lock()
	defer n.mu.Unlock()

	close(n.ch)
	n.ch = make(chan struct{})
}
