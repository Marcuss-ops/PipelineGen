# PipelineGen God-like Architecture Program

## Purpose

This directory defines the policies, standards, and migration sequence required to move PipelineGen toward a modular monolith with one source of truth per responsibility, zero permanent legacy, and no duplicated execution paths.

These documents are intentionally not a second runtime source of truth. Runtime facts such as active routes, job handlers, providers, service constructors, and health checks must ultimately be generated from typed code registries.

## Authority order

When sources disagree, use this order:

1. Compiling code and executable tests.
2. Generated architecture manifests produced from code.
3. `architecture/policy.yaml` and enforced CI rules.
4. These migration and design documents.
5. Historical comments, tickets, and old migration notes.

A document must never keep a removed feature alive. If code is deleted, generated manifests and documentation must be updated in the same change.

## Non-negotiable target

PipelineGen must converge on:

- one composition root;
- one capability registry;
- one HTTP route registry;
- one job handler registry;
- one provider/resolver/sampler path per responsibility;
- one canonical SQLite metadata database for the current deployment model;
- Qdrant as a derived semantic projection, never a second metadata authority;
- no direct handler-to-database access;
- no parallel writers, dual-read compatibility paths, fake routes, silent fallbacks, permanent allowlists, or compatibility aliases;
- no runtime dependency setters or partially initialized services;
- generated API and architecture documentation.

## Document map

1. [`01_NORTH_STAR.md`](01_NORTH_STAR.md) — invariants and final operating model.
2. [`02_TARGET_STRUCTURE.md`](02_TARGET_STRUCTURE.md) — target repository and package structure.
3. [`03_CAPABILITY_STANDARD.md`](03_CAPABILITY_STANDARD.md) — mandatory shape of each capability.
4. [`04_REGISTRIES_AND_SSOT.md`](04_REGISTRIES_AND_SSOT.md) — canonical registries and source-of-truth rules.
5. [`05_DEPENDENCY_RULES.md`](05_DEPENDENCY_RULES.md) — import direction, ports, adapters, and events.
6. [`06_DATA_AND_CONFIG_OWNERSHIP.md`](06_DATA_AND_CONFIG_OWNERSHIP.md) — database, Qdrant, files, Drive, and configuration authority.
7. [`07_ZERO_LEGACY_POLICY.md`](07_ZERO_LEGACY_POLICY.md) — deprecation, compatibility, and deletion rules.
8. [`08_ARCHITECTURE_CI_GATES.md`](08_ARCHITECTURE_CI_GATES.md) — mandatory automated enforcement.
9. [`09_MIGRATION_ROADMAP.md`](09_MIGRATION_ROADMAP.md) — safe implementation sequence.
10. [`10_DEFINITION_OF_DONE.md`](10_DEFINITION_OF_DONE.md) — completion criteria.
11. [`11_AGENT_EXECUTION_PLAYBOOK.md`](11_AGENT_EXECUTION_PLAYBOOK.md) — instructions for humans and coding agents.
12. [`12_REVIEW_CHECKLIST.md`](12_REVIEW_CHECKLIST.md) — strict review checklist.
13. [`13_FEATURE_REMOVAL_CHECKLIST.md`](13_FEATURE_REMOVAL_CHECKLIST.md) — complete removal procedure.

## How to use this program

Every implementation task must reference the relevant document and change one bounded responsibility. Migration work follows EXPAND, BACKFILL, CUTOVER, CONTRACT when data or runtime continuity requires it. Pure dead-code removal may go directly to CONTRACT after proving there are no callers, routes, jobs, data owners, configuration keys, metrics, or operational dependencies.

The destination is not a larger framework. The destination is a smaller, explicit, compiler-enforced system.