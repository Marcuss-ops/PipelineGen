# Migration Roadmap

## Strategy

The migration is incremental. Main stays usable while each responsibility moves to one canonical path. Avoid a repository-wide big bang.

Each implementation block owns one problem, one bounded file set, focused tests, and a complete CONTRACT phase before the next overlapping migration begins.

## Phase 0 — Freeze new debt

Add ratchet checks that reject new:

- `interface{}` in contracts;
- compatibility aliases and forwarding wrappers;
- direct route or job registration outside canonical registries;
- dependency setters;
- raw database handles crossing application boundaries;
- provider/source switch dispatch;
- permanent allowlist entries.

Exit gate: current debt cannot increase.

## Phase 1 — Remove dead runtime surfaces

Remove proven dead or misleading behavior:

- Artlist recommendation route backed by an always-missing resolver;
- removed Realtime service shells and fields;
- always-nil AutoHarvest wiring;
- commands that report success without executing work;
- routes registered with unavailable mandatory services;
- duplicated Script compatibility forwarders.

Exit gate: no route or job exists solely for removed code.

## Phase 2 — Canonical module descriptor

Introduce the typed capability descriptor and make the current module registry consume it.

Start with a compatibility adapter around existing modules, but do not create a second long-lived registry.

Exit gate: routes, jobs, health checks, and lifecycle hooks can be listed from one registry.

## Phase 3 — Generated architecture manifests

Build `archgen` from the frozen registry and migration metadata.

Generate capability, route, job, provider, dependency, health, and table reports. Remove manually maintained live inventories after parity is proven.

Exit gate: generated output matches runtime registration.

## Phase 4 — Vertical capability pilot: Artlist

Move Artlist into one vertical capability because it exercises HTTP, jobs, scraper integration, Drive, catalog persistence, normalization, and indexing.

Required cutovers:

- one request normalizer;
- one job codec;
- one enqueue path;
- one execution handler;
- one provider/search path;
- one metadata finalization path;
- one module descriptor.

Exit gate: deleting the old Artlist package layout leaves no callers.

## Phase 5 — Assets and semantic retrieval

Make Assets the canonical owner of asset identity/search semantics. Qdrant remains a projection adapter.

Remove obsolete Realtime/Association shells and wire media search directly through typed Assets ports and the vector adapter.

Exit gate: one search API, one recommendation use case, one vector projection path.

## Phase 6 — Script capability

Consolidate script generation around the canonical pipeline and typed commands.

Remove duplicate `ClipServices` fields, cross-package aliases, removed-service ports, parallel job registration, and alternate post-generation orchestration.

Exit gate: one script pipeline and one job handler family.

## Phase 7 — Remaining vertical slices

Migrate in this order:

1. YouTube;
2. Images;
3. Voiceover;
4. Content;
5. Channels/monitoring;
6. Jobs;
7. System/health.

Order may change only when dependency evidence justifies it.

## Phase 8 — Data ownership closure

Eliminate raw database escape hatches, parallel writers, generic update APIs, schema bootstraps outside the canonical runner, and duplicated fact storage.

Introduce or finish typed ownership for locations, processing, artifacts, delivery, workflows, and outbox.

Exit gate: every durable fact and table has one writer and one owner.

## Phase 9 — Strict mode

Enable zero-tolerance CI:

- zero architecture allowlist;
- zero compatibility aliases;
- zero fake routes;
- zero duplicate constructors;
- zero job or route duplication;
- zero undocumented deprecations;
- zero manual live inventories.

## Phase 10 — Optional storage evolution

Only after strict mode, evaluate PostgreSQL for proven multi-writer, high-availability, or concurrency needs. Reuse canonical repository ports and avoid application-level dual-write architecture.

## Actions to execute

- Create one tracked work item per phase and per bounded capability migration, with owner, dependencies, file scope, tests, rollback, and evidence links.
- Capture a current-state baseline before each phase and define exact numeric or zero-reference exit criteria.
- Execute phases in order unless a written dependency analysis proves a safe reordering.
- Keep each implementation change limited to one phase outcome and rebase it frequently on current `main`.
- Record EXPAND, BACKFILL, CUTOVER, and CONTRACT evidence for every stateful migration.
- Prevent the next overlapping phase from starting until the previous exit gate is verified.
- Update this roadmap after every completed block with commit IDs, test evidence, generated reports, and remaining gaps.
- Re-run repository-wide architecture searches after each CONTRACT step to detect leftovers.
- Treat Phase 10 as an explicit architecture decision, not an automatic continuation.

## Final DONE check

The roadmap is DONE only when:

- [ ] Phase 0 prevents all listed debt categories from increasing.
- [ ] Phase 1 leaves no dead or misleading route, job, handler, field, or service shell.
- [ ] Phase 2 provides one validated, frozen capability registry.
- [ ] Phase 3 generates manifests that match runtime routes, jobs, providers, dependencies, health checks, and ownership.
- [ ] Artlist, Assets, Scripts, and every Phase 7 capability have completed their vertical migration and CONTRACT cleanup.
- [ ] Every durable fact and table has one owner and one writer.
- [ ] Strict mode reports zero accepted architecture violations, baselines, and allowlists.
- [ ] Every phase has owner, commit evidence, tests, generated evidence, and a verified exit gate.
- [ ] Old package paths and registrations have zero repository references.
- [ ] Phase 10 has either an approved evidence-based ADR or an explicit decision to remain on the current storage model.
- [ ] `main` passes the full test, architecture, and generation suite after all mandatory phases.