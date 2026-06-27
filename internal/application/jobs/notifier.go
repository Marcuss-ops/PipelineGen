// Package jobs (application-tier) — QueueNotifier port.
//
// Pre-PR-Queue-Split cleanup history (June 2026):
//   - The duplicate `notifier.go` in internal/infrastructure/database/sqlite/jobs
//     was deleted because it was a stale copy of queue_notifier.go (the canonical
//     queueNotifier struct + newQueueNotifier + Subscribe + Broadcast).
//   - That deletion was safe for the infrastructure tier — the canonical
//     interface in repository_commands.go (var _ QueueNotifier port) is
//     untouched, and the compile-time assertion
//     `var _ QueueNotifier = (*SQLiteStore)(nil)` in repository.go is
//     still satisfied.
//   - BUT the application tier had no local copy of `QueueNotifier` after
//     Wave 5 PR 3 (June 2026) removed the prior notifier.go from this
//     directory — and the deletion of the infrastructure-tier notifier.go
//     surfaced this missing-local-port gap as a build breakage:
//     `internal/application/jobs/types.go:141` and
//     `internal/application/jobs/worker.go:148:168` referenced
//     `QueueNotifier` which had no in-package definition.
//
// This file restores the application-tier port as a Go type alias to
// the canonical infrastructure-tier interface. Rationale:
//   - Compile-time seam unchanged: the assertion
//     `var _ QueueNotifier = (*sqljobs.SQLiteStore)(nil)` at
//     internal/app/lifecycle_job_runner.go (the composition-root wiring
//     site) continues to verify that the SQLiteStore satisfies the
//     application-tier port.
//   - Cross-package type alias (not re-declaration) keeps the two
//     contract surfaces in lock-step — if repository_commands.go
//     changes the interface method set, the application tier sees the
//     change at compile time, not via interface-divergence drift.
//   - Avoids a fresh `interface {}` re-declaration which would create
//     a second canonical port surface (against godlike/06's
//     "one owner per fact" + the project's pattern-0 port abstraction).
package jobs

import sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"

// QueueNotifier is the application-tier wake-on-Enqueue port.
//
// Type alias of sqljobs.QueueNotifier (the canonical interface declared
// at internal/infrastructure/database/sqlite/jobs/repository_commands.go).
// Workers (Worker.NewWorker) + RunnerConfig.Notifier both use this port;
// the composition root (internal/app/lifecycle_job_runner.go) passes a
// *sqljobs.SQLiteStore which satisfies BOTH the type alias and the
// canonical interface (compile-time assertion marker referenced in this
// file's package-level doc + in lifecycle_job_runner.go).
type QueueNotifier = sqljobs.QueueNotifier

// Compile-time assertion: the canonical SQLiteStore satisfies the
// application-tier QueueNotifier port. The same assertion is expected
// at internal/app/lifecycle_job_runner.go where the composition root
// wires *sqljobs.SQLiteStore into RunnerConfig.Notifier. Both
// assertions are intentional duplication — defense-in-depth so the
// port contract is checked both at the consumer (here, application
// tier) and at the wiring site (composition root).
var _ QueueNotifier = (*sqljobs.SQLiteStore)(nil)
