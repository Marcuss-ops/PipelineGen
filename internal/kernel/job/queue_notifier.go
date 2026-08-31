package job

// QueueNotifier is the minimal wake-on-enqueue contract used by polling
// workers. Implementations broadcast to current subscribers and expose a
// fresh channel for subsequent subscribers.
//
// The contract is provider-neutral: SQLite, PostgreSQL LISTEN/NOTIFY, and
// in-memory test implementations can satisfy it without importing the jobs
// application package.
type QueueNotifier interface {
	Subscribe() <-chan struct{}
	Broadcast()
}
