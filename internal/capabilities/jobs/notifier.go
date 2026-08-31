// Package jobs (application-tier) — QueueNotifier port.
//
// AGENTS.md Pattern 0 typed-port abstraction: the application layer owns
// this narrow contract. Concrete implementations (SQLite, Postgres, or an
// in-memory test adapter) satisfy it structurally at their adapter/wiring
// sites; the jobs package must not import an infrastructure implementation
// merely to prove conformance.
//
// Consumers of this port include Worker and RunnerConfig. Production wiring
// injects the SQLite adapter, while tests can provide an independent
// implementation without importing the platform package.
package jobs

import job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

// QueueNotifier is retained as an application compatibility alias. The
// canonical contract is owned by kernel/job so application and provider
// packages share one port definition without depending on each other.
type QueueNotifier = job.QueueNotifier
