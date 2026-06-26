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