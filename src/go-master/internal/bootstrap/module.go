package bootstrap

// CleanupFunc is returned by initialization functions to handle teardown.
// Callers should defer or schedule cleanup to release resources and cancel
// background goroutines on shutdown. Nil is a valid CleanupFunc (no-op).
type CleanupFunc func()


