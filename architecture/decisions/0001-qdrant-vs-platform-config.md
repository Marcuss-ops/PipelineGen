# ADR 0001: Cross-cutting observability primitives live in `internal/infrastructure/observability/`

> **Status:** Accepted.
>
> **Approved-by:** Architecture + ops/infra (RW-PROD-013 closure ceremony, 2026-06-27). Acceptance criterion per the dep-chain rule in `docs/operations/04-remote-worker-production-readiness-tickets.md`: “Non iniziare il ticket successivo finché il precedente non ha test verdi e criteri di accettazione verificati” — RW-PROD-013 had green tests (TestWorkerMetricsRegistered + TestWithWorker_* + TestLogKeyConstantsStable) AND a registry anchor decision recorded here before RW-PROD-016 could begin.
>
> **Deciders:** Architecture + ops/infra (gating the RW-PROD-013 → RW-PROD-016 dependency chain).
>
> **Scope:** All canonical observability primitives — Prometheus metric names, label cardinalities, log field keys, and the DoctorConfig structural port — that downstream dashboards, alert rules, and log queries join against.

## Context

PipelineGen has two centers of gravity for runtime data:

1. **Qdrant side** (`internal/application/qdrant/`, with `qdrant` and `qdrant/snapshot` subpackages): holds domain logic and the canonical projection sequence. The QDRANT-005 closure added `qdrant_reconciler_*` metrics and a `qdrant_alias_*` family; these are exposed via the `PromMetricsAdapter` struct that translates reconciler-domain signals into Prometheus counters/histograms.

2. **Platform/config side** (`internal/platform/config/`): owns service-level shapes — `ServerConfig`, `WorkersConfig`, `StorageConfig`, `SecurityConfig`, plus the new `TLSConfig`/`MTLSConfig` sub-structs. The DoctorConfig adapter (`internal/application/workerdoctor/config_adapter.go`) consumes a narrow port of these shapes.

Until this decision was filed, two ambiguities were corrupting dashboards and tests in parallel:

- **QDRANT-005D** (Wave 20 in `architecture/current.yaml`): `prometheus.DefaultGatherer` started emitting FAILS because the AWK lens in `scripts/ci-architectural-checks.sh` was misclassifying cross-package type redeclarations as same-package duplicates. The redirect was the wrong default — the underlying decision was unclear about where to put the new metric names.

- **workerdoctor probe compile** (RW-PROD-016, queued): the doctor probe file `default_probes.go` had to stub 11 methods on the `DoctorConfig` adapter because `ServerConfig.TLS` and `WorkersConfig.MTLS` sub-structs didn't exist in canonical `Config`. Operators reading the doctor probe output couldn't tell whether a "false" was a real production-false or a stub-returns-zero false.

The dep chain in `docs/operations/04-remote-worker-production-readiness-tickets.md` rule states explicitly (Italian): *"Non iniziare il ticket successivo finché il precedente non ha test verdi e criteri di accettazione verificati"* ("Do not begin the next ticket until the previous one has green tests and acceptance criteria verified"). The acceptance criterion for RW-PROD-013 is therefore closed only when an anchor rule exists for where the metrics live — without it, RW-PROD-016 cannot compile end-to-end against canonical Config.

## Decision

All cross-cutting observability primitives (Prometheus metric names, label cardinalities, log field keys, and the `DoctorConfig` structural port) live in **`internal/infrastructure/observability/`** as the canonical registry anchor.

Specific consequences:

1. **Metric names** are declared in `internal/infrastructure/observability/metrics.go` (existing) or co-located per-concern files under that package (e.g. `worker_metrics.go`, future `qdrant_alias_switch.go`). Domain packages (`internal/application/qdrant/`, `internal/application/workerdoctor/`, ...) NEVER declare new metric names directly; they consume the package-level vars.

2. **Log field-key constants** (`LogKeyWorkerID`, `LogKeySessionID`, `LogKeyJobID`, `LogKeyTaskID`, `LogKeyAttemptID`, `LogKeyCorrelationID`) live in `internal/infrastructure/observability/worker_metrics.go`. Renaming any of them is a breaking change for downstream dashboards / log queries — the `TestLogKeyConstantsStable` test pins the values.

3. **The `DoctorConfig` structural port** (`internal/application/workerdoctor/`) is a PATTERN-0 port that wraps canonical `*config.Config` via the adapter — but the workflow is: canonical Config grows the shape FIRST (`ServerConfig.TLS`, `WorkersConfig.MTLS`), the adapter collapses to a direct delegation AFTER. There is never a phase where the adapter stubs a method that canonical Config could/should expose.

4. **Qdrant metrics** (`qdrant_reconciler_*`, `qdrant_alias_*`) stay consumed via the `PromMetricsAdapter` pattern: domain logic emits domain events, adapter translates to `observability`-package metric vars. This mirrors the canonical Qdrant projection sequence: domain state in SQLite → outbox record → outbox dispatcher emits → adapter bumps the metric.

5. **Reconciler ownership** of metric names stays in `internal/infrastructure/observability/`. The Wave-20 OWNERSHIP entry (`architecture/ownership.yaml::lint_gates[check=5]`) names the `qdrant-hygiene` workstream as the registered owner, but the metric var itself lives in the observability package.

## Consequences

### Positive

- Compile-time assertion `var _ observability.WorkerX = (*registeredMetric)(nil)` (or analog) catches drift between domain logic and the published metric name.
- Doc-side cross-references (e.g. `docs/api/ACTIVE_API_GENERATED.md`) regenerate from a single source.
- Alert rules in `config/alerting_rules.yml` reference metric names that have one canonical anchor — no duplicate per-package definitions.
- The ruler pattern (workerdoctor adapter) is REUSABLE for any future package that needs a narrow view of `*config.Config`: the audit (`Probe<thing>` in `default_probes.go`) becomes possible because canonical config grows the shape first.

### Negative

- Domain packages that want a new metric MUST first add the var in `internal/infrastructure/observability/` and then the consumer. This is two-step instead of one-step.
- The byte-cost of importing `internal/infrastructure/observability/` (which pulls in Prometheus + zap) is paid by every consumer.

### Neutral

- Migration of any pre-existing metric declared outside `internal/infrastructure/observability/` falls under EXPAND/BACKFILL/CUTOVER/CONTRACT — the new file under the canonical location is the BACKFILL phase; the deletions in the old location are CONTRACT.

## Alternatives considered

### A. Domain-package metric declaration

Put each metric name in the package that consumes it (qdrant.go declares `qdrant_reconciler_findings_total`; workerdoctor declares the worker_* set). Rejected because:

- The QDRANT-005D Wave 20 reveal — duplicate type redeclarations within Go's same-package-level model — does not have a stable home for the cross-references between domain packages and dashboards. Without a single anchor, every retry of the lint surfaces the same drift.
- Alert rules and dashboards need ONE place to read what a metric's labels mean.

### B. Shared metrics package under `pkg/metrics/`

Lift the registry to a leaf package (`pkg/metrics/`) so any consumer can import it without crossing the `internal/` boundary. Rejected because `pkg/` is leaf by convention in this codebase (AGENTS.md §"Utilities to prefer") and the observability surface is non-trivial — adding it to `pkg/` would dilute the `pkg/ = external utilities` rule.

### C. Keep the current ambiguous state (no anchor)

Reject status quo. The dep-chain rule for RW-PROD-013 → RW-PROD-016 explicitly demands a registry anchor; without one, RW-PROD-016 cannot compile. Adopted as Option A above.

## Implementation status

- ✓ RW-PROD-013 closed: `internal/infrastructure/observability/worker_metrics.go` is the canonical home for the 16 worker_* metrics + 6 LogKey* constants + WithWorker decorator. The `TestLogKeyConstantsStable` test pins the field names.
- ✓ This ADR filed (`architecture/decisions/0001-qdrant-vs-platform-config.md`).
- ✓ RW-PROD-016 precondition met: `ServerConfig.TLS` + `WorkersConfig.MTLS` sub-structs added in `internal/platform/config/types.go`. Adapter (`internal/application/workerdoctor/config_adapter.go`) collapses the 11 stub methods to direct delegations on the new sub-structs.
- Pending: `prom_worker_metrics_downstream_test.go` (or analog) to assert that every metric name in `worker_metrics.go` is referenced by at least one consumer flow (heartbeat / lease renewals / artifact upload) — the gap between declared and consumed metric names.

## References

- `docs/operations/04-remote-worker-production-readiness-tickets.md` — RW-PROD-013, RW-PROD-016 ticket text.
- `docs/operations/worker-certification-checklist.md` §3 — gates that consume the metrics/log keys.
- `architecture/ownership.yaml::lint_gates[check=5]` — registered owner for the Wave-20 hygiene work.
- `architecture/current.yaml::id=20` — Wave 20 with `deliverables[4 items]` incl. this ADR.
- `architecture/deprecations.yaml` — no record; this ADR does not deprecate anything.
