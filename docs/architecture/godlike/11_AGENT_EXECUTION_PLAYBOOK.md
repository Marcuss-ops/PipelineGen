# Agent Execution Playbook

## Preparation

Before a change, inspect the current repository and identify the existing owner, registry entries, constructors, routes, jobs, data model, and tests. Start from current `main` and define a bounded file set.

Do not assume a document is current when compiling code or generated manifests show otherwise.

## Scope

One task solves one responsibility. Avoid combining a feature, broad refactor, dependency update, and unrelated cleanup.

Search before creating. Reuse the canonical registry, resolver, sampler, normalizer, codec, repository, event, health check, and platform adapter when one already exists.

## Forbidden additions

Do not introduce:

- handler-to-database access;
- raw database handles in application contracts;
- provider dispatch outside canonical registries;
- dependency setters;
- compatibility aliases;
- pass-through wrappers;
- silent fallback paths;
- permanent allowlists;
- fake routes;
- duplicate job handlers;
- untracked temporary architecture.

Direct changes to `main` happen only when the user explicitly requests them.

## Testing

Run focused tests first, then wider affected-package tests. Code changes require formatting, build validation, relevant tests, and architecture checks. Shared registry or contract changes should run the full Go suite when practical.

Removal work needs negative evidence: no caller, registration, config key, migration dependency, operational script, metric, or documentation reference remains.

## Migration method

Use EXPAND, BACKFILL, CUTOVER, CONTRACT for data or runtime migrations. Define an exit gate for every phase. Do not leave the old path active after cutover.

## Final verification

Before publishing:

- inspect the final diff;
- confirm only intended files changed;
- refresh generated output;
- verify tests and architecture checks;
- inspect recent commit history;
- confirm the expected commit is present remotely;
- report any limitation honestly.

## Documentation

Manual documents explain policy and reasoning. Live inventories of routes, jobs, providers, services, and ownership must be generated from code.