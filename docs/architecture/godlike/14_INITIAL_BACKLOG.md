# Initial Migration Backlog

This backlog translates the target architecture into the first bounded implementation blocks for the current PipelineGen repository. Revalidate every item against current `main` before changing code.

## Block 1 — Dead HTTP surfaces

- Remove Artlist `/recommend` while its resolver is permanently absent.
- Remove the removed Realtime handler, bundle fields, and registry shell.
- Remove or explicitly mark unavailable any command that returns success without doing work.
- Do not register handlers with missing mandatory services.

Exit gate: every mounted route has a real use case.

## Block 2 — Script ghost ports

- Remove the always-nil AutoHarvest path.
- Remove duplicate and removed-service fields from `ClipServices`.
- Remove local replacement types for deleted Realtime/Association packages when no active implementation remains.
- Remove pass-through aliases and forwarding functions in the Script transport layer.

Exit gate: script contracts contain only wired capabilities.

## Block 3 — Assets search wiring

- Remove the obsolete Realtime gate from Assets search construction.
- Wire normal catalog/provider search independently from vector search.
- Wire Qdrant through one typed vector-search adapter.
- Return typed unavailable errors only for genuinely optional semantic operations.

Exit gate: one active media search service and one recommendation path.

## Block 4 — Capability descriptor foundation

- Introduce the descriptor type and validation.
- Adapt one existing module without creating a permanent second registry.
- Add duplicate route/job/provider tests.
- Add registry freeze tests.

Exit gate: one module can fully describe its runtime contributions.

## Block 5 — Architecture generation

- Generate route, job, provider, health, dependency, and capability manifests.
- Compare generated routes with actual Gin registration in tests.
- Mark current manual inventories as transitional.

Exit gate: generated manifests are checked by CI.

## Block 6 — Artlist vertical slice

- Consolidate request normalization.
- Consolidate job codec and enqueue path.
- Keep one `HandleJob` execution entrypoint.
- Route search through the provider registry.
- Keep one finalization/outbox/index path.
- Move composition into one descriptor.

Exit gate: old Artlist package layout has zero callers.

## Block 7 — Database boundary closure

- Remove `DB() *sql.DB` application-port escapes.
- Replace generic update APIs with owned repository methods.
- Identify and eliminate parallel YouTube writers.
- Generate table ownership from migration metadata.
- Drive architecture allowlists to zero.

Exit gate: one writer and owner per durable fact.

## Block 8 — Strict zero-legacy mode

- Enable failure on new compatibility aliases, forwarding wrappers, fake routes, duplicate registrations, and expired deprecations.
- Remove transitional baselines after each category reaches zero.
- Replace historical migration comments in production files with current invariant comments.

Exit gate: architecture checks report zero accepted violations.