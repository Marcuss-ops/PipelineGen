// Package ports — canonical application-level port surface (Fase 5(a),
// July 2026).
//
// godlike/06 SSOT (one canonical owner per fact): this package is the
// SOLE canonical home for the cross-cutting application-layer ports that
// use cases depend on. The 6 ports enumerated below are the typed seams
// where the application layer meets its infrastructure-agnostic
// collaborators.
//
// AGENTS.md Phase 5(a) compliance (July 2026):
//
//   - Port interfaces are declared here in the SAME package as the
//     canonical use case that consumes them (Pattern 0 from
//     godlike/05; "define ports alongside use cases").
//   - Port interfaces are narrow (1-3 methods) so callers can substitute
//     hand-rolled fakes in tests (godlike/07 minimum-blast-radius).
//   - The infrastructure-side adapters that satisfy these ports
//     lift without importing the application layer (the port is
//     the structural seam; nothing on either side speaks directly
//     to the other).
//
// # Phase 5(a) — Ports declared in this push
//
//   - JobFinalizer          (alias to internal/domain/finalization.JobFinalizer)
//   - Publisher             (alias to internal/capabilities/delivery.Publisher)
//   - OperationRepository   (alias to internal/application/operations.OperationsRepository)
//   - Clock                 (NEW interface)
//   - MetricsSink           (NEW interface)
//   - ArtifactStagingStore  (NEW interface)
//
// # Phase 5(b) — caller-migration commitments (NOT this push)
//
//   - internal/application/jobs/worker_execution.go imports nothing
//     from internal/platform/sqlite/*.
//   - internal/application/assets/persistence/writer.go imports
//     nothing from internal/platform/sqlite/*,
//     nothing from github.com/prometheus/client_golang,
//     nothing from os.*/hashing-FS utilities.
//   - The symlink import path `internal/application/ports.X` is the
//     canonical reference for the 6 port names across all post-5(b)
//     callers.
//   - Pre-5(b) callers MAY keep using package-qualified names
//     (e.g. `finalization.JobFinalizer`) for the migration window;
//     the aliases in this file preserve byte-stable identity.
//
// # Forward-pointer (godlike/06 SSOT alignment)
//
//   - Push 5.2 removes the SQLite-specific imports from worker_execution.go
//     and assets/persistence/writer.go (caller-migration).
//   - Push 5.3 enforces internal/api → application boundary against
//     concrete SQLite / Drive / FFmpeg provider orchestrators.
//   - Push 5.5 lands BuildServer(ServerDependencies) fail-fast with
//     startup validator + capability registry.
//
// No new routing, provider-selection, source-policy, sampling, or
// resolution logic enters a shared registry, resolver, or sampler in
// this push (AGENTS.md "shared resolver rule"). All 6 ports are pure
// type declarations.
package ports
