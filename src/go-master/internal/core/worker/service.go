package worker

// File refactored into modules:
// - worker_core.go: Service struct, worker registration, retrieval, storage operations
// - worker_status.go: Heartbeat processing, offline checks, worker status queries
// - worker_commands.go: Command management for workers
// - worker_safety.go: Revocation, quarantine, error processing, security helpers

// All modules are ≤300 lines. No dead code found in original file.
