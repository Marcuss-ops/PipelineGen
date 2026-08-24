// Package jobs (application-tier) — QueueNotifier port.
//
// AGENTS.md Pattern 0 typed-port abstraction (June 2026): this file
// declares the application-tier QueueNotifier as a Go type alias to
// the canonical infrastructure-tier interface declared in
// internal/infrastructure/database/sqlite/jobs/queue_notifier.go.
//
// Why an alias and not a re-declaration:
//   - Compile-time seam unchanged: the assertion
//     `var _ QueueNotifier = (*sqljobs.SQLiteStore)(nil)` here, plus
//     the equivalent assertion at
//     internal/app/lifecycle_job_runner.go (the composition-root
//     wiring site), both verify that *sqljobs.SQLiteStore satisfies
//     the canonical interface. SQLiteStore does so via its Subscribe
//   - Broadcast methods that delegate to a private `*notifier`
//     channel (the unexported struct implementation living in
//     queue_notifier.go — see that file for the mutex + chan detail).
//   - Cross-package alias (not re-declaration) keeps the two contract
//     surfaces in lock-step: if the interface method set changes in
//     queue_notifier.go, this alias picks up the change at compile
//     time (no interface-divergence drift between application-tier
//     consumers and infrastructure-tier implementers).
//   - Avoids a fresh `interface {}` re-declaration which would
//     create a second canonical port surface (against godlike/06's
//     "one owner per fact" + the project's pattern-0 port abstraction
//     rule).
//
// History note: the prior version of this file carried a long
// "Pre-PR-Queue-Split cleanup history" comment block referencing
// QueueNotifier as a struct + assertions in repository_commands.go.
// That history was rendered stale by the Pattern-0 struct→interface
// refactor in queue_notifier.go (June 2026, codex/qdrant-app-writers-
// fail-closed followup). The current doc-comment captures the live
// rationale only; see commit 61818692 ("Restore clip-aware script
// docs flow", 2026-06-27) for the file's lineage via git blame.
//
// Consumers of this port (verified via grep, June 2026):
//   - internal/application/jobs/worker.go (Worker.NewWorker takes
//     a QueueNotifier arg + subscribes via port.Subscribe for
//     in-process wake on Enqueue)
//   - internal/application/jobs/lifecycle_job_runner.go
//     (RunnerConfig.Notifier wires the composition-root SQLiteStore)
//   - internal/application/jobs/types.go (RunnerConfig carries the
//     port as a struct member)
//
// Nil-safety: nil port = typed-nil interface value; concrete callers
// pattern-match `if notifier == nil { return SKIP }` rather than
// calling methods on the nil interface.
package jobs

import sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"

// QueueNotifier is the application-tier wake-on-Enqueue port.
//
// Type alias of sqljobs.QueueNotifier (the canonical interface declared
// at internal/infrastructure/database/sqlite/jobs/queue_notifier.go).
// Go type aliases resolve to whatever the target is — struct or
// interface — so the alias picks up the Pattern-0 interface shape
// from queue_notifier.go automatically. If a future PR moves the
// canonical port (e.g. to a Postgres LISTEN/NOTIFY adapter under a
// new package), update the import path below; the alias plumbing
// remains unchanged.
type QueueNotifier = sqljobs.QueueNotifier

// Compile-time assertion: *sqljobs.SQLiteStore satisfies the
// application-tier QueueNotifier port. The same assertion lives at
// internal/app/lifecycle_job_runner.go (the composition-root wiring
// site) — both are intentional duplicates (defence-in-depth) so the
// port contract is verified both at the consumer (here, application
// tier) and at the wiring site (composition root).
//
// Forward-pointer (deadline 2026-08-01, owner: jobs-tier): collapse to a
// single assertion at the composition-root site once the worker lifecycle
// lands its constructor-mock removal. Tracked in
// architecture/current.yaml#follow_up_tickets. Until then the second
// assertion here is the documented transitional baseline per AGENTS.md
// zero-baseline rule.
var _ QueueNotifier = (*sqljobs.SQLiteStore)(nil)
