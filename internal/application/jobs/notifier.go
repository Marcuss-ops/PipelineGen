// Package jobs (notifier.go) — the application-tier port that the
// Worker pool depends on for the wake-on-Enqueue pattern
// (PR-Polling / ADR-0002 §D6.5, Wave 22, June 2026).
//
// Why a type alias:
//
//   The canonical QueueNotifier interface is declared in
//   `internal/infrastructure/database/sqlite/jobs/repository_commands.go`
//   (the same struct name in a different package — Go distinguishes
//   by import path, not name). This file re-exports the same interface
//   to the application-layer callers (worker.go::Worker.notifier,
//   types.go::RunnerConfig.Notifier) under the SAME identifier so:
//
//     1. Cross-package drift compiles out: a future change to the
//        canonical infra-side QueueNotifier signature (e.g., adding a
//        third method) propagates here as a compile-time breaker
//        because the alias IS the same type.
//     2. Compile-time assertion `var _ QueueNotifier =
//        (*sqljobs.SQLiteStore)(nil)` is the seam marker pinned by
//        worker.go (line ~167) and types.go (line ~138) documentation.
//        *SQLiteStore's in-package QueueNotifier implementation
//        (repository.go::var _ QueueNotifier = (*SQLiteStore)(nil))
//        means the alias is identical, so this assertion duplicates
//        the infra-side check (an intentional belt-and-braces
//        affordance: any third call-site that needs to consume the
//        port from this package sees the compile-time verification).
//     3. A future postgres adapter ships its own LISTEN/NOTIFY-impl
//        QueueNotifier in internal/infrastructure/postgres/jobs
//        and slots in here via RunnerConfig.Notifier without
//        recompiling internal/application/** (the port + alias is
//        the seam; the concrete is swappable).
//
// Production-layer usage scope: this port is consumed by the
// in-process Worker pool. The broadcast primitive is single-process
// only — a future postgres LISTEN/NOTIFY plugin is the cross-process
// wake-up path (out of scope here).
package jobs

import (
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
)

// QueueNotifier is the canonical application-tier alias for the
// infra-defined wake-on-Enqueue port (`internal/infrastructure/database/
// sqlite/jobs/repository_commands.go::QueueNotifier`).
//
// The type alias (NOT a redeclared struct interface) is deliberate:
//
//   - *sqljobs.SQLiteStore satisfies the infra-side QueueNotifier via
//     repository.go::var _ QueueNotifier = (*SQLiteStore)(nil).
//     Through the alias, *sqljobs.SQLiteStore ALSO satisfies
//     jobs.QueueNotifier. The same in-process instance can be passed
//     to both layers without conversion.
//   - Worker.notifier (worker.go:148) and RunnerConfig.Notifier
//     (types.go:141) reference `QueueNotifier` from this package
//     directly — no `sqljobs.` qualifier — keeping call sites clean
//     and matching the documented compile-time seam marker.
//
// Method semantics (must match the infra-side declaration):
//
//   - Subscribe() <-chan struct{} — returns the live notifier
//     channel; closes on the next Broadcast; replaced by a fresh
//     open channel available to subsequent Subscribe callers.
//   - Broadcast() — closes the live channel and atomically installs
//     a fresh one; wakes every blocked Subscriber simultaneously.
//
// In-process scope only: this port does NOT cross process boundaries.
// A single SQLiteStore is per-process.
type QueueNotifier = sqljobs.QueueNotifier

// Compile-time assertion (PR-Reaper followup, Wave 22, June 2026):
// *sqljobs.SQLiteStore satisfies the application-side QueueNotifier
// port. Because QueueNotifier is an alias for sqljobs.QueueNotifier,
// this assertion duplicates the in-package infra-side assertion in
// repository.go (intentional — application-tier docs + future
// call-sites that consume the port see the verification here).
//
// Marked in worker.go and types.go as the canonical seam marker
// for any future postgres LISTEN/NOTIFY adapter that must plug in
// here without recompiling internal/application/**.
var _ QueueNotifier = (*sqljobs.SQLiteStore)(nil)
