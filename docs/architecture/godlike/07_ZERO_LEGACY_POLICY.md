# Zero Legacy Policy

> **Status**: **canonical** (promoted June 2026 from godlike/ design-state).
> This document is the single source of truth for legacy deprecation
> constraints and the EXPAND / BACKFILL / CUTOVER / CONTRACT migration
> sequence. Supersedes overlapping rules previously restated in
> `AGENTS.md` (Compatibility/Help-utils/Drift-style comments) and
> `ARCHITECTURE.md` (architectural-drift commentary). Phase 1+ rules
> may add a `legacy_policy_doc:` enforcement to `cmd/archcheck` (see
> the 06 promotion for the C1 pattern).

## Goal

PipelineGen does not preserve superseded paths indefinitely. Compatibility is temporary, explicit, observable, and scheduled for removal.

## What counts as legacy

- duplicate route or job entrypoints;
- aliases between old and new domain types;
- pass-through wrappers that preserve an old package boundary;
- dual-read or dual-write behavior after cutover;
- service fields kept only because a package was removed;
- fake routes that always return unavailable or success without work;
- silent fallback to an older implementation;
- unused config keys, metrics, migrations, tests, docs, or comments;
- permanent CI allowlists or grandfathered baselines.

## Default rule

When a canonical replacement is active, the old path is removed in the same migration program. A migration is not complete at CUTOVER; it completes at CONTRACT.

## Temporary deprecation record

Any temporary compatibility item must have:

- unique deprecation ID;
- owner capability;
- exact symbol, route, job, field, or config key;
- replacement;
- introduction date;
- removal deadline;
- tracking issue;
- compatibility test;
- usage metric when runtime traffic is possible.

An expired deprecation fails CI.

## Forbidden compatibility techniques

- `type Old = New` across package boundaries;
- functions that only forward arguments to a replacement;
- `interface{}` fields used to keep removed services compiling;
- constructors returning a service with missing mandatory ports;
- comments such as temporary, compatibility, or legacy without a tracked record;
- hidden fallback branches selected on errors;
- old and new writers active at the same time after cutover.

## Migration sequence

Use:

1. EXPAND: introduce the new canonical structure without breaking current data.
2. BACKFILL: migrate durable state and verify invariants.
3. CUTOVER: switch all callers, routes, jobs, and writers.
4. CONTRACT: remove old types, code, data fields, config, tests, docs, and allowlists.

Dead code with proven zero callers may move directly to CONTRACT.

## Cross-file redeclaration within one Go package

Go cannot distinguish file-level types from package-level types. Two `.go` files in the same package declaring `type X struct{...}` produce the build error `X redeclared in this block` — same struct, two files, same package, compile-time failure. The compiler reports this as a hard error; reviewers cannot "see past" it the way they can with cross-package aliases or pass-through wrappers.

PipelineGen uses a wire-mirror pattern (introduced in QDRANT-005C PR3) to keep infrastructure decoupled from the canonical application layer. The canonical example is `qdrant.SnapshotDescription` (wire) and `dr.SnapshotDescription` (canonical-application):

- `qdrant.SnapshotDescription` (`internal/infrastructure/qdrant/types_dr.go:45`) — wire-only mirror used by RPC decoders and collection-manager wrappers.
- `dr.SnapshotDescription` (`internal/application/qdrant/dr/types.go:38`) — canonical application-layer mirror. The `dr` package does NOT import `qdrant`; an adapter file translates at the seam.

These two declarations live in DIFFERENT PACKAGES by directory path, so Go treats them as distinct types and the compiler is satisfied. The pattern is acceptable, but only when enforced by **at least one** of the following two techniques:

- **(a) moved-to-shared-types-package + lint.** Place the canonical declaration in one shared types package (e.g. `internal/application/qdrant/dr/types.go`), have every other package import it, and have CI fail on any same-package redeclaration. The lint lives at `scripts/ci-architectural-checks.sh::Check 5` (same-package duplicate-type-declaration); the empty allowlist at `docs/architecture/godlike/duplicate-types-allowlist.txt` exists solely for transitional exceptions under the zero-baseline rule (see `docs/architecture/godlike/08_ARCHITECTURE_CI_GATES.md` §"Zero-baseline rule").
- **(b) compile-time assertion gate.** Bridge the two layers with an adapter that asserts the consumer-side type-set at compile time, for example:
  ```go
  var _ dr.SnapshotStore = (*SnapshotStoreAdapter)(nil)
  ```
  Drift here (the assertion failing to compile) IS the cycle-break regression detector — the build error is loud and the recovery is mechanical. This pattern is canonical for every wire-mirror adapter.

The canonical post-mortem is `docs/operations/05-qdrant-redeclaration-recovery.md` (QDRANT-RECOVERY-001, RECOVERED). It documents the `SnapshotDescription` and `PointPayload` collisions that occurred during wave 14-18 + QDRANT-005 closure, the recovery commits `2b67d701` + `38187ded`, and the forward-prevention chain delivered by Wave 20 (`architecture/current.yaml#id-20`). The wave's transitional baselines (zero-baseline rule with owner + deadline) carry this exception forward to the AWK→Go rewrite currently under remediation.

Both techniques assume the EXPAND / BACKFILL / CUTOVER / CONTRACT sequence still applies. The wire-mirror pattern is an EXPAND acknowledgement, not a CONTRACT shortcut — removing either the wire-side or the canonical declaration at CONTRACT time is the responsibility of the owning wave.

## No fake availability

A route is either operational or absent. Commands that perform no work must not return a normal success response. Capabilities with missing mandatory dependencies fail composition or are not registered.

## Historical information

Historical decisions belong in Git history, issues, and ADRs. Production files should explain current invariants, not narrate every prior migration.

## Actions to execute

- Create and validate `architecture/deprecations.yaml` with ID, owner, exact symbol, replacement, introduction date, removal deadline, tracking issue, test, and metric fields.
- Inventory aliases, forwarding wrappers, duplicate routes/jobs, dormant fields, compatibility reads/writes, fallback branches, stale config, tests, docs, metrics, and allowlists.
- Classify every item as immediate removal or temporary deprecation with a hard deadline.
- Add CI checks for expired deprecations, untracked legacy comments, compatibility files, aliases, wrappers, fake routes, and new allowlist entries.
- Add usage metrics for runtime compatibility paths and define the zero-usage observation window required before removal.
- Execute EXPAND, BACKFILL, CUTOVER, CONTRACT for every migration and track evidence for each phase.
- Remove old readers and writers immediately after verified cutover; never keep both active as insurance.
- Delete obsolete config, tests, docs, metrics, comments, scripts, fields, constructors, and generated inventory entries during CONTRACT.
- Move historical explanations into ADRs or Git history and rewrite production comments around current invariants.

## Final DONE check

- [ ] Every temporary compatibility item exists in the deprecation manifest with an owner and future removal deadline.
- [ ] CI fails on expired or untracked deprecations.
- [ ] No cross-package alias, pass-through wrapper, fake route, hidden fallback, removed-service field, or duplicate entrypoint remains.
- [ ] No old and new writer are active after cutover.
- [ ] Every completed migration has executed CONTRACT and removed obsolete code, data access, configuration, tests, docs, metrics, and allowlists.
- [ ] Runtime compatibility metrics show zero use for the required observation period before removal.
- [ ] Architecture baselines are monotone decreasing and ultimately deleted at zero.
- [ ] Repository searches find no untracked `legacy`, `compat`, `temporary`, or deprecated-symbol references in production code.
- [ ] Active routes perform real work and capabilities with missing mandatory dependencies are absent or fail composition.
- [ ] Architecture checks and the full affected test suite pass with zero accepted legacy violations.
- [ ] Every wire-mirror type-pair has either a same-package lint entry (`scripts/ci-architectural-checks.sh::Check 5`) or a compile-time assertion gate; no unmapped cross-package redeclaration survives without one of the two.