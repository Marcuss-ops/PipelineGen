// Package jobs (application-tier) — QueueNotifier typed port.
//
// AGENTS.md Pattern 0 typed-port abstraction (June 2026): this file
// declares the application-tier QueueNotifier INTERFACE. The infrastructure
// adapter at internal/infrastructure/database/sqlite/jobs/queue_notifier.go
// provides the concrete implementation; *sqljobs.SQLiteStore (defined in
// repository.go) satisfies the port via its exported Subscribe + Broadcast
// methods that delegate to a private *notifier channel (also defined in
// queue_notifier.go).
//
// Phase 5 inversion (chore/repository-cleanup, June 2026): before Phase 5,
// the interface was declared in the infrastructure tier
// (sqljobs/queue_notifier.go) and the application tier held a
// `type QueueNotifier = sqljobs.QueueNotifier` type alias. That aliased
// the application tier's port to a concrete-package-declared interface,
// which is the WRONG dependency direction per hexagonal architecture
// (port must be owned by the application; the infrastructure must
// implement it, not declare it). Phase 5 moves the interface to the
// application tier and drops the type alias. The implementation in
// infrastructure stays unchanged; *sqljobs.SQLiteStore still satisfies
// the port via Subscribe / Broadcast.
//
// Consumers (unchanged from pre-Phase-5):
//   - internal/application/jobs/worker.go (Worker.NewWorker takes a
//     QueueNotifier arg + subscribes via port.Subscribe for in-process
//     wake on Enqueue)
//   - internal/app/lifecycle_job_runner.go (RunnerConfig.Notifier wires
//     the composition-root SQLiteStore; *SQLiteStore satisfies the
//     application-tier port via the assertion at the bottom of this file)
//   - internal/application/jobs/types.go (RunnerConfig carries the port
//     as a struct member, no longer via sqljobs import)
//
// Nil-safety: nil port = typed-nil interface value; concrete callers
// pattern-match `if notifier == nil { return SKIP }` rather than calling
// methods on the nil interface.
package jobs

import (
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
)

// QueueNotifier is the application-tier typed port for the in-process
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

// Compile-time assertion: *sqljobs.SQLiteStore satisfies the
// application-tier QueueNotifier port. SQLiteStore exposes Subscribe +
// Broadcast methods that delegate to a private *notifier channel
// (defined in sqljobs/queue_notifier.go). The assertion here verifies
// the structural contract at the application tier — the in-package
// concrete (`*notifier`) is not asserted because the application-tier
// assertion transitively verifies the SQLiteStore path (the public
// surface that composition root actually wires). If a future adapter
// drops the Subscribe/Broadcast method set, this assertion catches
// the drift at the application's import boundary.
var _ QueueNotifier = (*sqljobs.SQLiteStore)(nil)
