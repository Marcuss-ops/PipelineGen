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