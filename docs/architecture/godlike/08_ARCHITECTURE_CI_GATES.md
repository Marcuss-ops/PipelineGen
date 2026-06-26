# Architecture CI Gates

> **Status**: **canonical** (promoted June 2026 from godlike/ design-state).
> This document is the single source of truth for executable
> architecture rules and CI gate definitions. The on-disk enforcement
> lives in `scripts/ci-architectural-checks.sh` (legacy ratchets) and
> `cmd/archcheck/main.go` (target-tree ratchets); both binaries cite
> this document as their policy manifest.

## Purpose

Architecture rules must be executable. Reviews reinforce the rules, while CI prevents drift automatically.

## Mandatory checks

CI must verify formatting, static analysis, tests, race-sensitive tests where practical, module tidiness, architecture validation, generated manifests, and a clean working tree after generation.

## Boundary checks

Reject:

- kernel or domain imports of platform, configuration, logging, or transport;
- one capability importing another capability's transport or repository concrete;
- HTTP packages importing SQL, Drive, Qdrant, or process execution;
- application logic importing Gin;
- database opening outside the canonical database package;
- production SQL outside owned repositories and migrations.

## Registry checks

Reject duplicate capability names, route method/path pairs, job types, provider names, resolver keys, sampler keys, health-check names, and canonical service construction sites.

Routes and jobs declared outside the canonical descriptor path are invalid.

## Legacy checks

Reject:

- expired deprecation records;
- new compatibility or alias files;
- cross-package type aliases;
- pass-through forwarding wrappers;
- dependency setters;
- handlers backed by missing mandatory services;
- new permanent allowlist entries;
- temporary architecture comments without a tracked removal item.

## Contract checks

Domain and capability contracts must not expose:

- `interface{}`;
- unbounded `any` without an approved metadata use;
- `map[string]any` as a durable command or job contract;
- raw SQL handles;
- generic update maps;
- transport framework types.

## Data checks

CI verifies one migration directory, immutable released migrations, one table owner, one schema runner, and generated ownership data matching migration metadata.

## Complexity budgets

Initial targets:

- production file above 500 lines: warning, later fail;
- constructor above 8 dependencies: design failure unless a short-lived exception exists;
- capability without one descriptor: fail;
- canonical service built in several locations: fail;
- registry mutation after freeze: fail in tests.

## Generated output

Architecture generation produces capability, route, job, provider, dependency, health, and table manifests. CI fails when regenerated output differs from tracked files.

## Zero-baseline rule

The final acceptable count is zero. Transitional baselines require an owner and deadline. A baseline that only prevents new violations is not completion.

## Actions to execute

- Map every architecture rule to an `archcheck` rule, a generated-manifest parity test, or a standard Go check.
- Add small valid and invalid fixtures for every architecture rule.
- Run formatting, static analysis, module tidiness, tests, architecture checks, and generation checks in CI.
- Add automated checks for boundaries, registries, contracts, legacy patterns, data ownership, and complexity budgets.
- Make registry keys and service construction sites unique by validation.
- Run `archgen` in CI and fail when regenerated files differ from tracked files.
- Give every temporary baseline an owner, expiry, current count, and reduction plan.
- Move each rule from no-growth mode to zero-tolerance when its count reaches zero.
- Produce actionable failures containing rule name, file, remediation, and governing document.

## Final DONE check

- [ ] Every architecture rule is executable and has valid/invalid fixtures.
- [ ] CI runs the required checks for all relevant repository changes.
- [ ] Formatting, static analysis, module tidiness, tests, architecture checks, and generation checks pass on `main`.
- [ ] Duplicate routes, jobs, providers, resolvers, samplers, health checks, table owners, and service constructors fail automatically.
- [ ] Forbidden imports, broad contracts, setters, aliases, wrappers, fake routes, and handler infrastructure access fail automatically.
- [ ] Migration ownership and generated table ownership are verified.
- [ ] Generated manifests are reproducible and leave a clean working tree.
- [ ] Complexity exceptions are owned and expire.
- [ ] All architecture baselines and allowlists have reached zero and are removed.
- [ ] A clean checkout reproduces the same green result.