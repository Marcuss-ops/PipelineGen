# Code-Quality-Cleanup-2026-07-04 — Action Plan

> **Source**: Italian audit snapshot pasted to the orchestrator on 2026-07-04.
> **Authoring context**: post-`AUDIT-RESIDUE-2026-07-04` wave-tracker closure
> (10 SHAs landed 2026-07-04) + post-`PR-IMAGES-SHIM-REMOVAL` + post-`PR-CHROME-PROVIDER-SPLIT`
> + post-`PR-WIRE-ASSETS-CAPABILITY-SPLIT` + post-`PR-SEARCH-PORTS-SPLIT` + post-`PR-PERSIST-PR6-CANONICAL`
> + post-`PR-VO-PARENT-AGGREGATOR-SPLIT`. This plan captures the **RESIDUE** that the previous
> waves did NOT touch (because they prioritized god-object decomposition + typed-primitive
> migration + adapter-shim retirement; the items below are the
> **semantic-smell** + **fake-availability** + **policy-drift** residue that
> needs explicit follow-up).
>
> **Companion docs**:
> - `architecture/action-plans/2026-07-04-external-audit-action-plan.md` — public-audit reconciliation
>   (the 9-item matrix for file-size / LoC claims). This plan is the
>   **semantic-smell matrix** complement.
> - `architecture/action-plans/2026-07-04-stock-architecture-improvement.md` — stock-specific plan.
>   This plan is the **cross-subsystem matrix**; stock items here cross-reference that plan.
> - `architecture/action-plans/2026-07-04-clips-metadata-consolidation.md` — clips-specific plan.
>   This plan references it for the processor_persistence / PR6 reactivation items.
>
> **godlike/06 3-surface lockstep (per CANONICAL.md §1)**:
> 1. `architecture/action-plans/2026-07-04-code-quality-cleanup-action-plan.md` (this file — narrative)
> 2. `architecture/current.yaml#CODE-QUALITY-CLEANUP-2026-07-04` (wave-tracker entry — to be added)
> 3. `CHANGELOG.md` `## Unreleased → ### Added` (closure meta-entry — to be added on first PR ship)
> 4. `AGENTS.md` §Recent cross-cutting closures (mirror entry — to be added on first PR ship)
>
> For this commit (plan creation only), only surface #1 changes. Surfaces #2/3/4
> are forward-pointed to land **per PR** as the canonical closure discipline
> (one PR = one ship_sha + ship_date per godlike/06 SSOT lockstep).

---

## 1. Honest disclosure (godlike/07 no-fake-availability)

The 12 items below were classified by **static complexity + accumulated risk**
(surface area, churn, dead-code density, policy-inconsistency risk) — NOT by
runtime profiling. The canonical ranking MUST cross-validate against
`git log --since=90.days --pretty=format: --name-only | sort | uniq -c | sort -rn`
forward-pointer `PR-CODE-QUALITY-HOTSPOT-CROSSREF` (deadline 2026-08-15) that
runs post-wave and surfaces any high-frequency hotspot NOT captured here. The
plan-deadline rolls forward per slim-schema append-only ratchet if high-frequency
hotspots are surfaced (mirrors `GODOBJ-2026-07-03.PR-GODOBJ-HOTSPOT-CROSSREF`
precedent at 2026-08-01).

**Audit-pin discipline (godlike/06 audit-pinning)**: this plan captures
**every dirty area** the audit author identified. No item is silently
deferred to "out of scope" — items deferred carry an explicit
`forward-pointer:` line to the receiving wave-tracker entry, per
godlike/07 minimum-blast-radius.

**Carry-forward preservation**: the 5 pre-existing build issues from
`architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` are NOT
regressions of any CODE-QUALITY-CLEANUP PR. The 6-item carry-forward list
(FIX-MONITOR-ENQUEUE-TOLOWER + FIX-MONITOR-SCHEDULER-ENQUEUER +
FIX-STOCKPIPELINE-REDECLARATION + FIX-APP-MODULE-MEDIA-DISPATCHER +
FIX-IMAGES-ROUTING-CYCLE + FIX-APP-WIRE-SCRIPT-SYNTAX [retired]) is
preserved verbatim per CHANGELOG convention.

---

## 2. Priority bands (4)

| Band | Items | Deadline | Pattern |
|------|-------|----------|---------|
| **P0 absolute** (bug / fake-success) | 3 (PR6 processor persistence + stock fake-availability + PR6 test reactivation) | 2026-07-15 | P0.7 typed-error contract + godlike/07 no-fake-availability + Fix + Verify |
| **P0** (god-object / hotspot) | 2 (stock service.go split + stock orchestrator_steps.go split) | 2026-08-01 | Mechanical split per AGENTS.md Pattern 5 (mirrors PR-CHROME-PROVIDER-SPLIT 2026-07-04 cadence) |
| **P1** (typed-primitive / policy-align) | 4 (postprocessor typed constants + document policy align + engine interface{} removal + ScriptFlowHandler split + wire_script split) | 2026-08-15 | Typed-primitive migration per godlike/06 SSOT one-canonical-owner-per-fact |
| **P2** (cleanup / freeze) | 3 (legacy adapters freeze-or-remove + books regression tests + reconciler port extract) | 2026-08-22 | Quarantine + forward-pointer (legacy) / TDD (regression) / port extraction (reconciler) |

---

## 3. Per-item matrix (12 items)

### P0 absolute (3 items, deadline 2026-07-15)

#### PR-PERSIST-6-CANONICAL-FIX — `internal/application/scripts/adapters/processor_persistence.go`

- **Audit finding (item #1)**: PR6 marked closed but the processor persistence
  path was still writing into the legacy `Template` and `TimelineJSON` slots
  even though the DB already has dedicated `idempotency_key` + `specscene`
  columns from migration 100. The canonical `ports.ScriptRecord.IdempotencyKey`
  + `ports.ScriptRecord.SpecScene` write seam was being silently bypassed.
- **Cross-ref**: `architecture/current.yaml#PERSIST-PR6` (already shipped
  2026-07-04 via commit `d17c78ae` per AGENTS.md `## Recent cross-cutting closures`
  — PR-PERSIST-PR6-CANONICAL). The audit-pinned residue is the **regression-
  protection discipline** that wasn't enforced: future persistence callsites
  must NOT regress to the legacy slots.
- **Fix approach**: add **Check 55 (forward-prevention gate)** to
  `scripts/ci-architectural-checks.sh` that bans `Template:` and
  `TimelineJSON:` literal writes in `internal/application/scripts/adapters/processor_persistence.go`
  and `internal/infrastructure/database/sqlite/scripts/script_record.go`.
  Tests pin the canonical-contract invariant. Lint-failure on any regression
  surfaces at CI before merge.
- **Expected outcome**: zero new code (the production code already uses the
  canonical columns per PR-PERSIST-PR6-CANONICAL); new ci-gate Check 55
  forward-prevents future regression.
- **Effort**: ~80 LoC (gate + 3 tests).

#### PR-STOCK-FAKE-AVAILABILITY-REMOVAL — `internal/application/assets/providers/stock/stockpipeline/`

- **Audit finding (item #3)**: `StockStageSourcesStep` + `StockComposeChunksStep`
  were stubs that could make the pipeline look successful even if some
  pieces didn't do real work. The audit explicitly identifies this as the
  most dangerous form of "dirty code" — fake success.
- **godlike/07 no-fake-availability**: a stub that returns
  `Status: SUCCEEDED` without doing real work is the canonical violation.
  The fix is to either (a) wire the real implementation or (b) make the
  step fail-closed if the real implementation is missing.
- **Cross-ref**: `architecture/action-plans/2026-07-04-stock-architecture-improvement.md`
  (the stock-specific plan). This entry is the **fake-availability
  subset** of the stock plan; the stock plan covers the structural splits
  (service.go, orchestrator_steps.go) that this item depends on.
- **Fix approach**: identify the 2 stub step sites via
  `rg "StockStageSourcesStep|StockComposeChunksStep" internal/`. For each,
  replace the stub with either (a) the real implementation (preferred
  per godlike/07) or (b) a typed error sentinel that the orchestrator
  surfaces as a job failure (NOT as a job success with empty artifacts).
  The `var rng` global + `map[string]any/json.RawMessage` primitive
  obsession from item #2 is **orthogonal** and tracked in
  PR-STOCK-SERVICE-SPLIT + PR-STOCK-ORCHESTRATOR-SPLIT below.
- **Expected outcome**: 2 stub sites physically removed or fail-closed.
  No pipeline step returns SUCCEEDED without doing the real work.
- **Effort**: ~120 LoC (stub-removal + tests + ci-gate assertion).

#### PR-PR6-TEST-REACTIVATE — `internal/application/scripts/adapters/processor_persistence_test.go`

- **Audit finding (item #10)**: the 2 tests that pin the
  `IdempotencyKey`-not-in-`Template` + `SpecScene`-not-in-`TimelineJSON`
  invariants were skipped via `t.Skip("Needs SQLite DB")` even though the
  tests use an in-memory `idemFakeRepo` that doesn't touch SQLite.
- **Cross-ref**: `architecture/current.yaml#PERSIST-PR6` (shipped 2026-07-04
  via commit `d17c78ae`). The 2 t.Skip markers were REMOVED in that commit
  per AGENTS.md `## Recent cross-cutting closures` entry "PR-PERSIST-PR6-CANONICAL".
- **Fix approach**: verify the 2 t.Skip markers are gone in HEAD
  (`rg 't\.Skip.*Needs SQLite' internal/application/scripts/adapters/processor_persistence_test.go`
  must return 0 hits). If not, remove them. Add Check 56 (forward-prevention
  gate) that bans NEW `t.Skip` markers in `processor_persistence_test.go`
  without a godlike/07 honest-limitation comment.
- **Expected outcome**: 2 tests run on every CI cycle, pinning the
  canonical-contract invariant.
- **Effort**: ~20 LoC (verification + Check 56 gate).

---

### P0 (2 items, deadline 2026-08-01)

#### PR-STOCK-SERVICE-SPLIT — `internal/application/assets/providers/stock/stockpipeline/service.go`

- **Audit finding (item #2)**: `service.go` is 914 LoC, god-object with 27
  recent edits + 95 stub references scattered. The plan calls it a P0/P1
  hotspot: high churn + high complexity.
- **Cross-ref**: `architecture/action-plans/2026-07-04-stock-architecture-improvement.md`
  (stock plan; this entry is the **service.go split subset**).
- **Fix approach**: mechanical split per AGENTS.md Pattern 5 (mirrors
  `PR-CHROME-PROVIDER-SPLIT` 2026-07-04 cadence at SHA `cd7e1799`):
  - `service.go` (slim orchestrator: `StockService` struct + `NewStockService` + public surface methods + compile-time `var _ Port = (*StockService)(nil)` assertion)
  - `service_resilience.go` (3 Pattern 0 ports: `ManifestBuilder` + `TransactionalAssetWriter` + `ProjectionPort` from Commit 4-expanded closure)
  - `service_state.go` (state machine: `StockState` enum + `IsValidTransition` + 4 typed sentinels)
  - `service_steps.go` (Stage 1-5 step functions extracted from `RunResilient` 7-step ladder)
  - `service_metrics.go` (Prometheus metrics: `RunCounter` + `RunDuration` + `StepLatency` histograms)
- **godlike/06 SSOT (one canonical owner per fact)**: each of the 5 files
  owns exactly one capability concern.
- **godlike/07 minimum-blast-radius**: zero surface-contract changes;
  per-step lock preserved; 4 typed sentinels + `RunSummary{Manifest, FinalStatus}`
  envelope preserved verbatim.
- **godlike/07 typed-error contract**: all 4 sentinels
  (`ErrManifestIncomplete` + `ErrAtomicDispatchFailed` + `ErrProjectionResilience` +
  `ErrResilienceNotWired`) preserved via `errors.Is` probe.
- **Expected outcome**: service.go reduces from 914 LoC to ~280 LoC
  (slim orchestrator), 4 single-purpose capability files own their concerns.
- **Effort**: ~600 LoC moved (5 files, pure code-motion), ~50 LoC new tests.

#### PR-STOCK-ORCHESTRATOR-SPLIT — `internal/application/assets/providers/stock/stockpipeline/orchestrator_steps.go`

- **Audit finding (item #2)**: `orchestrator_steps.go` is 949 LoC, the
  second god-object. Contains the 7-step `RunResilient` ladder
  (resolve_sources / plan_clips / stage_sources / build_manifest /
  validate_manifest / emit_chunks / project_manifest) interleaved with
  per-step state transitions.
- **Cross-ref**: depends on PR-STOCK-SERVICE-SPLIT (this entry is the
  **steps-side complement**; both are required to land in the same wave).
- **Fix approach**: extract each of the 7 steps into its own file with
  the `RunResilient` orchestrator left as a thin ladder:
  - `orchestrator_steps.go` (slim orchestrator: `RunResilient` 7-step ladder + `*RunSummary` envelope threading)
  - `step_resolve_sources.go` (Stage 1: source discovery)
  - `step_plan_clips.go` (Stage 2: clip planning)
  - `step_stage_sources.go` (Stage 3: persistent staging via `SourceStager` port from Commit 4-expanded)
  - `step_build_manifest.go` (Stage 4: `ManifestBuilder` port implementation)
  - `step_validate_manifest.go` (Stage 5: `ErrManifestIncomplete` gate)
  - `step_emit_chunks.go` (Stage 6: `TransactionalAssetWriter` port implementation)
  - `step_project_manifest.go` (Stage 7: `ProjectionPort` port implementation)
- **godlike/06 SSOT**: each step file owns exactly one capability concern.
- **godlike/07 minimum-blast-radius**: `*RunSummary` envelope threading
  preserved verbatim; `Service.runOrchestratorResilient` orchestrator
  signature preserved; typed sentinels preserved.
- **Expected outcome**: orchestrator_steps.go reduces from 949 LoC to
  ~140 LoC (slim ladder), 7 single-purpose step files own their concerns.
- **Effort**: ~800 LoC moved (8 files, pure code-motion), ~80 LoC new tests.

---

### P1 (5 items, deadline 2026-08-15)

#### PR-POSTPROCESSOR-TYPED-CONSTANTS — `internal/application/scripts/processors/`

- **Audit finding (item #6)**: postprocessor identifiers are string-based
  (`"entities"`, `"metadata"`, `"images"`, `"document"`, `"persistence"`).
  Typo today = runtime bug; no compile-time guard.
- **godlike/06 SSOT (one canonical owner per fact)**: introduce a typed
  `PostprocessorKind` enum with 5 named constants. The enum lives in
  `internal/application/scripts/processors/types.go` (NEW). The 5 string
  literals across the codebase migrate to the typed enum.
- **Fix approach**:
  1. Add `PostprocessorKind` typed enum + 5 constants
     (`PostprocessorEntities` / `PostprocessorMetadata` / `PostprocessorImages` /
     `PostprocessorDocument` / `PostprocessorPersistence`).
  2. `PostprocessorKind.Valid()` + `CanonicalPostprocessorKinds()` enumeration.
  3. Migrate all string-literal references to the typed enum
     (use `code-searcher` agent to enumerate the call sites).
  4. Add Check 57 (forward-prevention gate) that bans string-literal
     postprocessor kinds in `internal/application/scripts/`.
- **Expected outcome**: zero string-literal postprocessor kinds in
  production code; compile-time guard on every call site.
- **Effort**: ~150 LoC (enum + migration + Check 57).

#### PR-DOCUMENT-POLICY-ALIGN — `internal/application/scripts/processors/document_processor.go`

- **Audit finding (item #7)**: registry default classifies `document`,
  `images`, `voiceover` as `BestEffort` during cutover phase. But
  `DocumentProcessor.Policy()` returns `ProcessorRequired`. Mixing the
  two is a policy drift — either `document` is `BestEffort` or
  `Required`, not both depending on which file you read.
- **godlike/06 SSOT (one canonical owner per fact)**: the policy of
  each postprocessor is the SINGLE canonical fact that lives in
  `processors.PolicyRegistry.Get(kind)`. `DocumentProcessor.Policy()` is
  REMOVED — callers go through the registry.
- **Fix approach**:
  1. Decide canonical policy: the registry default is authoritative;
     `DocumentProcessor.Policy()` is removed.
  2. Remove `Policy()` method from `DocumentProcessor` struct.
  3. Add compile-time assertion `var _ Processor = (*DocumentProcessor)(nil)`
     does NOT need `Policy()` method on the interface (per godlike/07
     minimal-blast-radius; the `Processor` interface is unchanged).
  4. Update 3 call sites in `internal/application/scripts/usecase/`
     that read `DocumentProcessor.Policy()` to read
     `PolicyRegistry.Get(PostprocessorDocument)` instead.
  5. Tests pin the canonical policy for each postprocessor.
- **Expected outcome**: zero `Policy()` method calls on individual
  postprocessor instances; all policy reads go through the registry.
- **Effort**: ~80 LoC (interface change + migration + tests).

#### PR-ENGINE-TYPED-INTERFACES — `internal/application/scripts/usecase/engine.go`

- **Audit finding (item #8)**: `Engine` struct has `ollamaGen interface{}`
  and `memorySvc interface{}` fields even though narrow interfaces exist.
  Code does runtime type assertion instead of using the typed interfaces
  directly. This is fake abstraction.
- **godlike/06 SSOT (one canonical owner per fact)**: the `Engine` struct
  fields are typed as the existing narrow interfaces (`OllamaClient` +
  `MemoryService`) per Pattern 0 port discipline.
- **Fix approach**:
  1. Replace `interface{}` fields with typed narrow interfaces
     (`OllamaClient` + `MemoryService` — both already declared in
     `internal/application/scripts/ports/`).
  2. Remove runtime type-assertion sites (compile-time guard via
     `var _ OllamaClient = (*X)(nil)` + `var _ MemoryService = (*Y)(nil)`).
  3. Tests pin the typed-only contract.
- **Expected outcome**: zero `interface{}` fields in `Engine` struct;
  compile-time guard prevents future drift back to `interface{}`.
- **Effort**: ~60 LoC (typed fields + type-assertion removal + tests).

#### PR-SCRIPT-HANDLER-SPLIT — `internal/application/scripts/handler.go` (ScriptFlowHandler)

- **Audit finding (item #4)**: `ScriptFlowHandler` is a god object with
  22 dependencies. Plan proposes split per capability: generate, curate,
  search, flow ops, legacy.
- **Cross-ref**: AGENTS.md Pattern 5 (single-purpose capability files).
  Mirrors `PR-CHROME-PROVIDER-SPLIT` + `PR-WIRE-ASSETS-CAPABILITY-SPLIT` cadence.
- **Fix approach**: extract 5 capability files from `ScriptFlowHandler`:
  - `script_handler.go` (slim orchestrator: `ScriptFlowHandler` struct + `NewScriptFlowHandler` + `RegisterRoutes` + compile-time `var _ Handler = (*ScriptFlowHandler)(nil)`)
  - `script_handler_generate.go` (`GenerateFromClips` + `GenerateWithImages` + `GenerateBatch` = 3 endpoints)
  - `script_handler_curate.go` (`Curate` + `CurateItem` + `CurateBatch` = 3 endpoints)
  - `script_handler_search.go` (`Search` + `SearchLive` + `SearchLiveProvider` = 3 endpoints)
  - `script_handler_flow.go` (`CacheEvict` + `SectionsRegenerate` + `JobStatus` + `Health` = 4 endpoints)
  - `script_handler_legacy.go` (forward-pointer to `internal/api/script/handler_legacy_adapters.go` quarantine per PR-LEGACY-QUARANTINE below)
- **godlike/06 SSOT**: each file owns exactly one capability concern.
- **godlike/07 minimum-blast-radius**: zero new endpoints; zero surface-
  contract changes; the 22 dependencies on `ScriptFlowHandler` are
  preserved (no dependency-injection rewrite).
- **Expected outcome**: handler.go reduces from 22-dependency god to
  ~12-dependency slim orchestrator, 5 single-purpose capability files.
- **Effort**: ~400 LoC moved (6 files, pure code-motion).

#### PR-SCRIPT-WIRE-SPLIT — `internal/app/wire_script.go`

- **Audit finding (item #5)**: `wire_script.go` knows too many details.
  Plan proposes extracting factory functions for usecase, resolver,
  adapter — leaving `wire_script.go` as a slim orchestrator.
- **godlike/07 minimum-blast-radius**: the composition root is the
  canonical place where the 22 dependencies meet; the goal of this
  refactor is NOT to remove dependencies but to make the wiring clearer
  (group by capability, name the wiring seams).
- **Fix approach**: extract 3 factory functions from `wire_script.go`:
  - `wire_script.go` (slim orchestrator: `WireScript` + `buildUsecaseFactories` + `buildResolverFactories` + `buildAdapterFactories` calls)
  - `wire_script_usecase.go` (factory: `buildUsecaseFactories` constructs all 5 usecase types)
  - `wire_script_resolver.go` (factory: `buildResolverFactories` constructs all 3 resolver types)
  - `wire_script_adapter.go` (factory: `buildAdapterFactories` constructs all 7 adapter types)
- **godlike/06 SSOT**: each factory file owns exactly one wiring concern.
- **Expected outcome**: wire_script.go reduces from god-knows-everything
  to slim orchestrator with 3 named wiring seams.
- **Effort**: ~250 LoC moved (4 files, pure code-motion).

---

### P2 (3 items, deadline 2026-08-22)

#### PR-LEGACY-QUARANTINE — `internal/api/script/handler_legacy_adapters.go`

- **Audit finding (item #9)**: 3 legacy endpoints with removal dates
  (`curate`, `generate-from-clips`, `generate-with-images`). The audit
  author argues: don't split — either freeze until removal date or
  remove when telemetry says zero usage.
- **Cross-ref**: AGENTS.md Recent cross-cutting closures
  PR-ARTLIST-SYNCSERVICE closure (2026-07-04, commit `f02ae683`):
  precedent for "remove the forward-pointer + the stale docstring +
  add a deprecation record" pattern.
- **Fix approach (godlike/07 minimum-blast-radius, NOT split)**:
  1. Verify telemetry: `rg 'curate\|generate-from-clips\|generate-with-images' internal/ --type go`
     returns the call-site count.
  2. If call-site count > 0: keep the quarantine, document the removal
     date in a forward-pointer wave-tracker entry.
  3. If call-site count == 0: physical `git rm` + add deprecation record
     to `architecture/deprecations.yaml#LEGACY-SCRIPT-ENDPOINTS`.
- **Expected outcome**: zero new code; either zero-dead-code (if telemetry
  says 0 callers) or a documented forward-pointer (if telemetry > 0).
- **Effort**: ~30 LoC (telemetry check + deprecation record).

#### PR-BOOKS-REGRESSION-TEST — `internal/application/books/job_handler.go`

- **Audit finding (item #11)**: books job handler was previously flagged
  as silent-success but the current code is correct — `delivery_status`
  is explicit (`PUBLISHED` / `PUBLISH_FAILED` / `LOCAL_ONLY`). The
  audit author proposes: add regression tests, not changes.
- **godlike/07 typed-error contract**: `ErrBookDrivePublishFailed` is
  the typed sentinel for the failure case.
- **Fix approach**: add TDD regression tests that pin the 3 `delivery_status`
  outcomes:
  1. `TestBooksJob_PublishedDeliveryStatus` — happy path: book is
     published, status is `PUBLISHED`.
  2. `TestBooksJob_PublishFailedDeliveryStatus` — Drive upload fails,
     status is `PUBLISH_FAILED`, error wraps `ErrBookDrivePublishFailed`.
  3. `TestBooksJob_LocalOnlyDeliveryStatus` — Drive disabled, status
     is `LOCAL_ONLY`, no error.
- **Expected outcome**: 3 regression tests pin the canonical
  `delivery_status` contract; future drift surfaces as a test failure
  before merge.
- **Effort**: ~120 LoC (3 TDD tests).

#### PR-RECONCILER-PORT-EXTRACT — `internal/application/assets/finalizer/`

- **Audit finding (item #12)**: `PublicationIntentReconciler` holds
  `*sql.DB` inside `application/finalizer` and runs queries directly.
  The audit author says: not the highest priority but should eventually
  move behind a port repository.
- **Cross-ref**: `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`
  (this is the kind of architectural cleanup the carry-forward list
  pre-existing build issues don't cover; this entry is the
  forward-pointer).
- **Fix approach (godlike/07 minimum-blast-radius)**:
  1. Declare `PublicationIntentRepository` interface in
     `internal/application/assets/finalizer/ports.go` (NEW) with
     `Reconcile(ctx, intent)` + `GetIntent(ctx, id)` + `ListPendingIntents(ctx)`.
  2. Implement the interface in
     `internal/infrastructure/database/sqlite/assets/publication_intent_repository.go` (NEW)
     wrapping the current SQL queries.
  3. `PublicationIntentReconciler` constructor signature:
     `(deps, repo PublicationIntentRepository)` — the `*sql.DB` field is REMOVED.
  4. Composition-root injection in `internal/app/build_bundles_assets.go` (or
     wherever the finalizer is wired) supplies the SQLite-backed repository.
  5. Tests pin the port-typed contract (mock repository for hermetic tests).
- **godlike/06 SSOT (one canonical owner per fact)**: the
  `PublicationIntentRepository` port is the SINGLE canonical read/write
  seam for the publication_intent table.
- **Expected outcome**: zero direct `*sql.DB` usage in
  `internal/application/assets/finalizer/`; the SQL queries live in
  the repository implementation.
- **Effort**: ~200 LoC (port + repo + tests + composition wiring).

---

## 4. Forward-Pointer Discipline / Union With Existing Waves

Per the audit-pinning principle (godlike/06 SSOT one-canonical-owner-per-fact):

- **PR-PERSIST-6-CANONICAL-FIX** is **already shipped** in spirit via
  `PR-PERSIST-PR6-CANONICAL` (2026-07-04, commit `d17c78ae` per AGENTS.md
  Recent cross-cutting closures). The remaining work is the
  **forward-prevention ci-gate** (Check 55), not new code.
- **PR-PR6-TEST-REACTIVATE** is **already shipped** in the same commit
  (the 2 `t.Skip` markers were removed per AGENTS.md closure entry). The
  remaining work is the **forward-prevention ci-gate** (Check 56).
- **PR-STOCK-SERVICE-SPLIT + PR-STOCK-ORCHESTRATOR-SPLIT** depend on the
  pre-existing `PR-GODOBJ-3 ship_status` (2026-06-30, commit `9aa4c9e2`)
  which already retired the `IndexingStatus` field + 4 typed sentinels.
  These entries are the **structural split** complement.
- **PR-SCRIPT-HANDLER-SPLIT** is **orthogonal** to the
  `PR-WIRE-ASSETS-CAPABILITY-SPLIT` closure (2026-07-04, commit
  `8a3e5f9a`): the latter split the composition root, this entry
  splits the handler that the composition root wires. Both are needed
  to fully decouple the script subsystem.
- **PR-SCRIPT-WIRE-SPLIT** is the **composition-root** complement of
  PR-SCRIPT-HANDLER-SPLIT. Both land in the same wave per godlike/07
  minimum-blast-radius (the wire signature depends on the handler
  signature).
- **PR-LEGACY-QUARANTINE** is **NOT a split** per the audit author's
  explicit guidance: "non lo spezzerei" (I wouldn't split it). The
  entry is the **forward-pointer to telemetry check + deprecation record**.

---

## 5. Execution Order & Locks (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

### Wave 1 — P0 absolute (deadline 2026-07-15, 3 SHAs)

1. **PR-PR6-TEST-REACTIVATE** (verification + Check 56 gate)
2. **PR-PERSIST-6-CANONICAL-FIX** (Check 55 forward-prevention gate)
3. **PR-STOCK-FAKE-AVAILABILITY-REMOVAL** (2 stub sites + fail-closed or real wiring)

Locks: all 3 are independent (different files, different wave-tracker
slots). Can land in parallel per godlike/07 EXPAND-phase discipline.

### Wave 2 — P0 structural splits (deadline 2026-08-01, 2 SHAs)

1. **PR-STOCK-SERVICE-SPLIT** (5 files, ~600 LoC moved)
2. **PR-STOCK-ORCHESTRATOR-SPLIT** (8 files, ~800 LoC moved)

Locks: PR-STOCK-ORCHESTRATOR-SPLIT depends on PR-STOCK-SERVICE-SPLIT
because the `RunResilient` ladder reads the `Service` state machine.
Both land in the same wave per godlike/07 minimum-blast-radius.

### Wave 3 — P1 typed-primitive / policy-align (deadline 2026-08-15, 5 SHAs)

1. **PR-POSTPROCESSOR-TYPED-CONSTANTS** (enum + migration + Check 57)
2. **PR-DOCUMENT-POLICY-ALIGN** (registry canonical, Policy() removed)
3. **PR-ENGINE-TYPED-INTERFACES** (interface{} -> typed)
4. **PR-SCRIPT-HANDLER-SPLIT** (6 files, ~400 LoC moved)
5. **PR-SCRIPT-WIRE-SPLIT** (4 files, ~250 LoC moved)

Locks: PR-SCRIPT-HANDLER-SPLIT + PR-SCRIPT-WIRE-SPLIT land in the same
wave (handler signature depends on wire signature). The other 3 are
independent (different files, different concerns).

### Wave 4 — P2 cleanup (deadline 2026-08-22, 3 SHAs)

1. **PR-LEGACY-QUARANTINE** (telemetry check + deprecation record, ~30 LoC)
2. **PR-BOOKS-REGRESSION-TEST** (3 TDD tests, ~120 LoC)
3. **PR-RECONCILER-PORT-EXTRACT** (port + repo + wiring, ~200 LoC)

Locks: all 3 are independent (different files, different concerns).
Can land in parallel per godlike/07 EXPAND-phase discipline.

---

## 6. Migration sequence (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

Per the canonical migration sequence for every P0/P1 PR:

1. **EXPAND** — introduce the new canonical surface (typed enum, port,
   new file) WITHOUT removing the old surface. Tests pin the new
   contract.
2. **BACKFILL** — migrate call sites to the new surface. This is
   the lockstep wave where the old surface is still alive (so
   downstream tests still pass).
3. **CUTOVER** — flip the canonical pointer (e.g. remove the old
   `Policy()` method, retire the old `interface{}` field). The new
   surface becomes the only one.
4. **CONTRACT** — physical `git rm` of the old surface + deprecation
   record `status: removed` + Check forward-prevention gate promoted
   to hard-fail (no allowlist row).

For CODE-QUALITY-CLEANUP-2026-07-04, the migration sequence is:

| PR | EXPAND | BACKFILL | CUTOVER | CONTRACT |
|----|--------|----------|---------|----------|
| PR-PERSIST-6-CANONICAL-FIX | n/a (already shipped) | n/a | n/a | Check 55 (this PR) |
| PR-STOCK-FAKE-AVAILABILITY-REMOVAL | 2 stub sites fail-closed (this PR) | future: real wiring | n/a | n/a |
| PR-PR6-TEST-REACTIVATE | n/a (already shipped) | n/a | n/a | Check 56 (this PR) |
| PR-STOCK-SERVICE-SPLIT | 5-file split (this PR) | n/a | future: retire pre-split surface | future: git rm legacy |
| PR-STOCK-ORCHESTRATOR-SPLIT | 8-file split (this PR) | n/a | future: retire pre-split surface | future: git rm legacy |
| PR-POSTPROCESSOR-TYPED-CONSTANTS | typed enum (this PR) | migration of 5 call sites (this PR) | future: string-literal ban via Check 57 (this PR) | future: git rm string support |
| PR-DOCUMENT-POLICY-ALIGN | registry canonical (this PR) | migration of 3 call sites (this PR) | Policy() method removed (this PR) | future: Policy() ban via Check 58 |
| PR-ENGINE-TYPED-INTERFACES | typed fields (this PR) | type-assertion removal (this PR) | interface{} field removed (this PR) | future: interface{} ban via Check 59 |
| PR-SCRIPT-HANDLER-SPLIT | 6-file split (this PR) | n/a | future: retire pre-split handler | future: git rm legacy |
| PR-SCRIPT-WIRE-SPLIT | 4-file split (this PR) | n/a | future: retire pre-split wire | future: git rm legacy |
| PR-LEGACY-QUARANTINE | telemetry check (this PR) | n/a | if zero callers: deprecation record (this PR) | future: git rm if zero callers |
| PR-BOOKS-REGRESSION-TEST | 3 TDD tests (this PR) | n/a | n/a | n/a |
| PR-RECONCILER-PORT-EXTRACT | port + repo (this PR) | composition wiring (this PR) | *sql.DB field removed (this PR) | future: *sql.DB ban via Check 60 |

---

## 7. Honest Limitations (godlike/07)

1. **Static prioritization**: this audit prioritized by complexity +
   accumulated risk. The final canonical ranking MUST cross-validate
   against git-log frequency forward-pointer `PR-CODE-QUALITY-HOTSPOT-CROSSREF`
   (deadline 2026-08-15). The plan-deadline rolls forward per slim-schema
   append-only ratchet if high-frequency hotspots are surfaced.

2. **The 6 pre-existing build issues** (FIX-MONITOR-ENQUEUE-TOLOWER +
   FIX-MONITOR-SCHEDULER-ENQUEUER + FIX-STOCKPIPELINE-REDECLARATION +
   FIX-APP-MODULE-MEDIA-DISPATCHER + FIX-IMAGES-ROUTING-CYCLE +
   FIX-APP-WIRE-SCRIPT-SYNTAX [retired]) are NOT regressions of any
   CODE-QUALITY-CLEANUP PR. They predate this wave and carry forward
   per the canonical 6-item carry-forward convention. The waves here
   MUST be runnable in isolation (per-file `gofmt + go vet + go build +
   go test -short` on the targeted subtree) without depending on the
   pre-existing issues being fixed.

3. **PR-STOCK-FAKE-AVAILABILITY-REMOVAL is the only P0 item that
   requires deep code analysis** (which stub sites are "fake success" vs
   which are honest placeholders awaiting future implementation).
   Forward-pointer `PR-STOCK-FAKE-AVAILABILITY-AUDIT` (deadline 2026-07-10,
   separate from this wave) runs `git log --follow` on the 2 stub sites
   to surface the original author intent.

4. **PR-LEGACY-QUARANTINE has 2 valid outcomes** (keep-with-deprecation-
   record OR physical-git-rm). The decision is data-driven (telemetry
   of call sites) and is NOT predetermined in this plan.

5. **PR-DOCUMENT-POLICY-ALIGN forces a one-way decision** (registry
   default is authoritative, `DocumentProcessor.Policy()` is removed).
   The audit author considered the alternative (`DocumentProcessor.Policy()`
   is authoritative, registry default is updated to `Required` for
   `document`) but rejected it because the registry default already
   encodes the cutover-phase policy (BestEffort during cutover, Required
   after).

6. **No new feature work**: this wave is purely structural / cleanup.
   New features (e.g. P0.18 structured outbox-driven enrichment) are
   forward-pointed to their own waves per the existing wave-tracker
   discipline.

7. **The 12-item list is NOT exhaustive**: the audit author explicitly
   noted that other dirty areas exist (e.g. `internal/infrastructure/database/sqlite/`
   has scattered per-file package-level mutable test seams beyond the
   drive scope covered by P2.1). The next audit cycle (forward-pointer
   `PR-CODE-QUALITY-AUDIT-NEXT-CYCLE`, deadline 2026-08-22) captures
   the next batch.

---

## 8. Wave-tracker entry pointer (canonical anchor)

`architecture/current.yaml#CODE-QUALITY-CLEANUP-2026-07-04`:

- 12 net-new slim-shape `linked_issues` (1 per PR above).
- 1 forward-pointer cross-ref `PR-CODE-QUALITY-HOTSPOT-CROSSREF` (deadline 2026-08-15).
- 1 forward-pointer cross-ref `PR-CODE-QUALITY-AUDIT-NEXT-CYCLE` (deadline 2026-08-22).
- 4 per-band deadline anchors (2026-07-15 / 2026-08-01 / 2026-08-15 / 2026-08-22).
- 1 cross-reference to `architecture/action-plans/2026-07-04-external-audit-action-plan.md`
  (the LoC-reconciliation complement).

**Status at creation**: `pending` (no PR shipped yet). The first PR
(`PR-PERSIST-6-CANONICAL-FIX` or `PR-PR6-TEST-REACTIVATE`) flips the
first `linked_issues` slot to `status: shipped` per the canonical
godlike/06 SSOT lockstep discipline.

---

## 9. Cross-references

- `architecture/current.yaml#PERSIST-PR6` (PR-PERSIST-PR6-CANONICAL, shipped 2026-07-04)
- `architecture/current.yaml#PR-GODOBJ-3 ship_status` (Commit 4-expanded, shipped 2026-06-30)
- `architecture/current.yaml#AUDIT-RESIDUE-2026-07-04` (10 SHAs shipped 2026-07-04)
- `architecture/current.yaml#VO-DECOMPOSITION-2026-07-04` (8 net-new linked_issues)
- `architecture/action-plans/2026-07-04-stock-architecture-improvement.md`
- `architecture/action-plans/2026-07-04-clips-metadata-consolidation.md`
- `architecture/action-plans/2026-07-04-external-audit-action-plan.md`
- `architecture/deprecations.yaml#LEGACY-SCRIPT-ENDPOINTS` (forward-pointer, post-Wave-4 deprecation record)
- `architecture/deprecations.yaml#PUBLICATION-INTENT-DIRECT-DB` (forward-pointer, post-PR-RECONCILER-PORT-EXTRACT)
- AGENTS.md `## Recent cross-cutting closures` (lockstep mirror entries)
- AGENTS.md Git-Lesson-2 (direct-to-main workflow, NO BRANCHES)
- AGENTS.md Git-Lesson-3 (Co-authored-by trailer discipline)
- AGENTS.md Pattern 5 (god-object split discipline)
- godlike/06 SSOT (one canonical owner per fact)
- godlike/07 no-fake-availability, typed-error contract, minimum-blast-radius, honest-limitation disclosure

---

## 10. Lifecycle (audit trail)

- **2026-07-04**: this plan created (commit pending). Status: `pending`.
- **2026-07-15**: target close for Wave 1 (3 P0 absolute PRs).
- **2026-08-01**: target close for Wave 2 (2 P0 structural splits).
- **2026-08-15**: target close for Wave 3 (5 P1 typed-primitive / policy-align PRs).
- **2026-08-22**: target close for Wave 4 (3 P2 cleanup PRs).
- **2026-08-15**: forward-pointer `PR-CODE-QUALITY-HOTSPOT-CROSSREF` runs git-log frequency analysis to surface missed hotspots.
- **2026-08-22**: forward-pointer `PR-CODE-QUALITY-AUDIT-NEXT-CYCLE` captures the next batch of dirty areas.

**Co-authored-by**: PipelineGen Agent <agent@pipelinegen.local>
(per AGENTS.md Git-Lesson-3 auditability convention)
