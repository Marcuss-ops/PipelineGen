# 2026-07-04 — Script‑Gen & Qdrant Stabilization Action Plan

> Companion to `architecture/current.yaml#SCRIPT-QDRANT-STABILIZATION-2026-07-04` (NEW wave‑tracker entry, this commit registers it). Synthesizes the prior 4‑axis methodology‑driven analyses on `internal/application/scripts/` (script‑gen, 17.006 LoC across 80 production files) and the Qdrant‑related surface (≈10.000 LoC scattered across cmd/api/application/infrastructure).

---

## TL;DR

| Bucket | Fascia | n. PRs | Band‑aligned deadline |
|---|---|---|---|
| Script‑Gen | FASCIA 1 (high ROI / low risk) | **4** | 2026‑07‑25 |
| Script‑Gen | FASCIA 2 (typed‑seam + phase split) | **5** | 2026‑08‑15 |
| Script‑Gen | FASCIA 3 (godfile splits + dead‑code elimination) | **4** | 2026‑08‑22 |
| Qdrant | FASCIA 1 (facade narrow + nil‑close + audit) | **4** | 2026‑07‑25 |
| Qdrant | FASCIA 2 (composition‑root decisions) | **3** | 2026‑08‑01 |
| Qdrant | FASCIA 3 (long‑tail cleanup) | **3** | 2026‑08‑22 |

**Total: 23 PRs**, all per‑file auto‑sufficient, each lands directly on `main` per AGENTS.md Git‑Lesson‑2. Each carries its own `gofmt + go vet + go build + go test -short` acceptance on the touched subtree AND a `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>` trailer per AGENTS.md Git‑Lesson‑3.

---

## Honest Disclosure (godlike/07 §no‑fake‑availability)

1. Analyses are **static snapshots** at commit `d478c394` (current `origin/main` tip).  No runtime cross‑validation against the current production build (6 pre‑existing build issues predate the analyses — see §7).
2. Each PR's expected `delta_LoC` is approximate (hand‑rolled estimate; commit‑level diff after landing supersedes).
3. **Session boundary**: this action plan is a synthesis of two prior 4‑axis analyses (script‑gen + Qdrant). The AGENTS.md §godlike/06 SSOT discipline required forward‑cite any opinion already tracked under `EXTERNAL-AUDIT-2026-07-04` rather than duplicate the slot (§5 explains the rename conventions).
4. Per‑file commit granularity: a single PR = 1 file + its test (godlike/07 minimal‑blast‑radius). Wave‑tracker entries are wave‑ratchet, not fixed‑counter (per godlike/08 zero‑baseline rule).

---

## §1 — Script‑Gen Fascia 1 (4 PRs, deadline 2026‑07‑25)

Per‑PR: file target, delta LoC, acceptance test on the touched subtree, godlike/07 contract.

### 1.1 PR‑SCRIPT‑NOOP‑GODLIKE
- **Owner capability:** `internal/application/scripts`
- **Surface:** `adapters/compat_adapters.go` (~62 LoC; 2 noopFns: `noopEntityExtractionAdapter.ExtractEntities` + `noopMetadataGenerationAdapter.GenerateMetadata`)
- **Delta:** -2 fake‑availability surfaces. Replace `return &scriptpkg.EntityResult{}, nil` and `return nil, nil` with `return nil, fmt.Errorf("%w: …", scriptpkg.ErrPostprocessFailed)`.
- **Acceptance test:** new `adapters/compat_adapters_test.go` exercising both noops via the `ProcessorPost` panic‑recovery path; assert `errors.Is(err, scriptpkg.ErrPostprocessFailed)`.
- **godlike/07 contract:** End the silent‑success pattern that was proposed for closure under ARCH‑§Active‑Concerns #11 (June 2026 PR‑VO cleanup); same semantic lands here.

### 1.2 PR‑HASHUTIL‑SCRIPT‑USAGE
- **Owner capability:** `internal/application/scripts`
- **Surface:** `adapters/processor_persistence.go::computeIdempotencyKey` (`crypto/sha256 + encoding/hex` inline)
- **Delta:** -3 imports; route through canonical `pkg/hashutil.SHA256String(tuple)[:16]`.
- **Acceptance test:** `processor_persistence_test.go` byte‑stability across 1000 same‑input runs (existing test surface unchanged unless a test name needs addendum).

### 1.3 PR‑SCRIPT‑METADATA‑SHADOW‑COLLAPSE
- **Owner capability:** `internal/application/scripts`
- **Surface:** `usecase/postgen_usecase.go::BuildMetadataLanguages` (shadow stub) — already documented in `dto/metadata.go`
- **Delta:** -1 shadow func; re‑import from `dto.BuildMetadataLanguages` canonical.
- **Acceptance test:** Existing `dto/language_helpers_test.go` continues; verify no postgen caller drifts.

### 1.4 PR‑FLOW‑HELPERS‑DUPLICATE‑REMOVAL
- **Owner capability:** `internal/application/scripts`
- **Surface:** `api/script/flow.go::ResolveRecommendedDriveFolder` (API‑side duplicate)
- **Delta:** -1 duplicate func; canonical lives at `usecase/flow_helpers.go::ResolveRecommendedDriveFolder`.
- **Acceptance test:** `flow_helpers_test.go` byte‑stability across 1 canonical + 1 deleted site (compile‑time pins).
- **SSOT note:** This MUST land before PR‑FLOW‑HELPERS‑GODFILE‑SPLIT (§2.3) so the duplicate is collapsed BEFORE splitting.

---

## §2 — Script‑Gen Fascia 2 (5 PRs, deadline 2026‑08‑15)

### 2.1 PR‑ENGINE‑TYPED‑SEAM  *(dependency‑gating)*
- **Owner:** `internal/application/scripts/usecase`
- **Surface:** `engine.go::Engine.ollamaGen interface{}` + `memorySvc interface{}` → typed Go interfaces `scriptOllamaGenerator` + `memoryGateChecker` (already declared in the same file).
- **Delta:** -2 interface{} fields; runtime type‑assertions become compile‑time `var _ scriptOllamaGenerator = (*ollama.Generator)(nil)` pins.
- **Acceptance test:** `engine_test.go` (11 commits/90gg, already red every push) — fully typed contract after PR; no new tests vanish.

### 2.2 PR‑ENGINE‑PHASE‑SPLIT
- **Owner:** `internal/application/scripts/usecase`
- **Surface:** `engine.go::Generate` 5‑phase inline (memory‑gate + cache‑replay + prompt‑build + ollama‑invoke + decode)
- **Delta:** -150 LoC from `Generate` body; 4 private helpers (`g.memoryGate`, `g.cacheReplay`, `g.buildPrompt`, `g.invokeAndDecode`, `g.stampProvenance`).
- **Acceptance test:** 6 TDD tests pin each phase (pre‑existing engine_test.go minus the public surface).

### 2.3 PR‑FLOW‑HELPERS‑GODFILE‑SPLIT
- **Owner:** `internal/application/scripts/usecase`
- **Surface:** `usecase/flow_helpers.go` 810 LoC (largest file in script‑gen)
- **Delta:** split into 5 single‑purpose files (text‑language / translate‑helpers / doc‑helpers / discovery‑helpers / flow_helpers‑orchestrator).
- **Requires FIRST:** PR‑FLOW‑HELPERS‑DUPLICATE‑REMOVAL (§1.4) to land.
- **Acceptance test:** existing tests (3 test files, ~12 tests) keep byte‑stable imports.

### 2.4 PR‑METADATA‑BATCH‑TRANSLATE
- **Owner:** `internal/application/scripts/dto`
- **Surface:** `dto/metadata.go::GenerateVideoMetadata` quadratic N×M LLM fan‑out (`for _, tag := range enTags { TranslateTextWithModel(...) }`)
- **Delta:** -1 nested loop; introduce batch‑translation helper `(*MetadataTranslator).TranslateManyWithModel(ctx, items, lang, model)`.
- **Acceptance test:** 1 byte‑stability test (single LLM call replaced with batched call) — `dto/metadata_test.go`.

### 2.5 PR‑CURATE‑IMMUTABLE‑CONSTRUCTOR
- **Owner:** `internal/application/scripts/usecase`
- **Surface:** `source_resolver_curate.go::SetClipSearchPort` runtime mutable setter
- **Delta:** -1 method; constructor takes `(clipBuilder, clipSearch) both required`; remove the `SetClipSearchPort` surface.
- **Acceptance test:** `wire_script.go:285` `curateResolver.SetClipSearchPort(&clipSearchPortAdapter{port: clipSearchPort})` migrates to a constructor‑only wire.
- **Acceptance test:** `source_resolver_curate_test.go` covers 4 path‑orthogonal branches; constructor call asserted once.

---

## §3 — Script‑Gen Fascia 3 (4 PRs, deadline 2026‑08‑22)

### 3.1 PR‑SCRIPT‑CONSOLIDATE‑STUBS
- **Owner:** `internal/application/scripts/usecase`
- **Surface:** 5 Phase 1b stubs: `section_regen.go::Regenerate`, `flow_helpers.go::Enqueue` (line 809), `postgen_usecase.go::BuildMetadataLanguages` shadow + others, `media_curator_stubs.go` (entire file), `adapters/source_registry_test.go` inline comment
- **Delta:** -2 files + -20 LoC; collapse residual Phase‑1b sentinels into a single sentinella (file `…/_phase1b.go` doc‑only) OR eliminare se nessun consumer.
- **Acceptance test:** rg audit verifies ZERO production‑consumer of `section_regen.go::Regenerate`; if hit, implement the stub rather than delete.

### 3.2 PR‑APPLYPRESET‑MAP
- **Owner:** `internal/application/scripts/adapters`
- **Surface:** `generation_normalizer.go::ApplyPreset` 8‑case switch (5 pass‑through + 2 with overrides + 1 default)
- **Delta:** -1 switch; replace with `map[Preset][]FieldOverride` data‑driven dispatch.
- **Acceptance test:** `normalizer_plan_tests_test.go` 5+ tests preserved byte‑stable.

### 3.3 PR‑POSTPROCESSOR‑REGISTRY‑SPLIT
- **Owner:** `internal/application/scripts/adapters`
- **Surface:** `postprocessor_registry.go` 692 LoC
- **Delta:** split into 4 capability files: `registry.go` (struct) + `preflight.go` (preflight gate) + `policy.go` (ProcessorPolicy) + `runner.go` (ppReg.Run).
- **Acceptance test:** existing registry tests cover pre‑flight + apply‑policy; splits preserve every visible name.

### 3.4 PR‑LEGACY‑ADAPTERS‑PROFILE‑SHRINK
- **Owner:** `internal/api/script`
- **Surface:** `handler_legacy_adapters.go` 641 LoC
- **Delta:** collapse `deriveClipIDs` + `resolveAliases` + `toEnvelope` (3 O(N) scans on the same data) into a single `buildLegacyClipReferences(req)` (1 pass); isolate 3 endpoint legacy adapters into separate files.
- **Acceptance test:** `handler_legacy_adapters_test.go` 14 tests cover the new path byte‑stable (with deprecation counter still incrementing).

---

## §4 — Qdrant Fascia 1 (4 PRs, deadline 2026‑07‑25)

### 4.1 PR‑RUNTIME‑FACADE‑NARROW  *(dependency‑gating)*
- **Owner:** `internal/infrastructure/qdrant`
- **Surface:** `runtime.go::QdrantRuntime` exposes 8 fields. Per‑Analysis §2.2.1 "facade overdose".
- **Delta:** -4 fields; reduce to `(Schema, Writer, Searcher, Client)`. `Manager/Health/Cleaner/Mapper` are constructed on‑demand via methods (lazy + memoise).
- **Acceptance test:** `runtime_test.go` + composition_test.go `frozenQdrantClientSites=1` constraint still passes.

### 4.2 PR‑DIAG‑QDRANT‑NIL‑CLOSE
- **Owner:** `internal/app` (composition root) + `internal/api/assets/diagnostics`
- **Surface:** `wire_assets.go::diagIndexHealthAdapter{qdrant: nil, collectionName: ""}` literal — godlike/07 nil‑propagation smell.
- **Delta:** fail‑closed at composition: when `root.Process.QdrantClient != nil` but the adapter wraps a nil, return `ErrDiagnosticsNotConfigured` typed error.
- **Acceptance test:** composition test asserts the typed error surface; not a 503 (which is the current nil‑propagation).

### 4.3 PR‑RECONCILE‑PROGRESS‑SPLIT
- **Owner:** `internal/application/qdrant/reconciler`
- **Surface:** `service.go::Reconcile` 5‑phase inline, 601 LoC
- **Delta:** extract 4 private helpers (`attachSnapshots`, `applyMetrics`, `attachRepair`, `attachReportWriter`); `Reconcile` becomes the orchestrator (~150 LoC).
- **Acceptance test:** existing reconciler tests (5+ invariants) keep byte‑stable.

### 4.4 PR‑ALIASES‑SHIM‑AUDIT  *(dependency‑gating)*
- **Owner:** `internal/infrastructure/qdrant`
- **Surface:** `aliases.go` 137 LoC; 25+ `var X = X.NewX` re‑exports.
- **Delta:** `rg 'qdrant\.New(ReconcilerService|Clients|…)' cmd/ internal/app/` → for each unreferenced re‑export, remove (`aliases.go` shrinks).
- **Acceptance test:** composition‑level smoke — `go build ./…` exits 0 after each removed line.

---

## §5 — Qdrant Fascia 2 (3 PRs, deadline 2026‑08‑01)

> **SSOT discipline:** these PRs reference slots ALREADY tracked in `architecture/current.yaml#EXTERNAL-AUDIT-2026-07-04.linked_issues[]`. Per godlike/06 SSOT (one canonical owner per fact), the NEW wave‑tracker `SCRIPT-QDRANT-STABILIZATION-2026-07-04` only FORWARD‑CITES them; it does NOT duplicate the slots.

### 5.1 PR‑QDRANT‑RUNTIME‑FACADE‑OPT‑A (forward‑cite `EXTERNAL-AUDIT-2026-07-04.linked_issues[PR-QDRANT-FINAL-DECISION]`)
- **Required prerequisite:** §4.1 PR‑RUNTIME‑FACADE‑NARROW lands first (facade shape determines the decision).
- **Owner:** `internal/app` (composition root)
- **Surface:** `composition.go::ProcessBundle` collapses 8 Qdrant fields into 1 `QdrantRuntime *qdrant.QdrantRuntime`. The 11 reader sites migrate.
- **Delta:** 8 read‑sites touched (`wire_assets.go:91`, `wire_script.go:247`, `wire_script_postprocess.go:151`, `wire_script_sources.go:64`, `wire_services.go:165‑168`, `qdrant_readiness.go:238`, …).
- **Acceptance test:** `composition_test.go::TestComposition_FrozenClientConstructionSites` (=1) unchanged; canonical compile‑time pin per reader site.

### 5.2 PR‑QDRANT‑MAINT‑PER‑MODE (forward‑cite `EXTERNAL-AUDIT-2026-07-04.linked_issues[PR-QDRANT-MAINT-PER-MODE]`)
- **Owner:** `cmd/admin`
- **Surface:** `qdrant_maintenance.go` per‑mode split into 3 mode‑specific files (audit / repair‑locators / delete‑invalid) + 4 helper files.
- **Delta:** ~700 LoC → ~9 thin files; CLI surface unchanged.
- **Acceptance test:** rg tests on `cmd/admin/qdrant_maintenance*` paths.

### 5.3 PR‑DR‑RETENTION‑STUB‑CHECK
- **Owner:** `internal/application/qdrant/dr`
- **Surface:** `retention.go` thin wrapper around `qdrant.CollectionManager.CleanupWithConfig`
- **Delta:** rg audit for stub‑test sites (`RetentionExecutor` consumers).  If zero, drop the wrapper. Otherwise, retain.
- **Acceptance test:** `dr/retention_test.go` (if it exists) AND rg shows ZERO stub site → deletion PR; ELSE retain.

---

## §6 — Qdrant Fascia 3 (3 PRs, deadline 2026‑08‑22)

### 6.1 PR‑ALIASES‑FULL‑REMOVAL  *(requires §4.4)*
- **Owner:** `internal/infrastructure/qdrant`
- **Surface:** `aliases.go` deletion; all consumers import canonical (`collections.NewCollectionManager`, `search.NewSearcher`, …)
- **Delta:** -137 LoC. File header renamed to `archived_do_not_use.go` with `// Archived: zero consumers as of YYYY-MM-DD. Re-add via git history if a fallback pattern emerges.`
- **Acceptance test:** `go build ./…` exits 0; `rg 'qdrant\.New(Clients|ReconcilerService|…)'` returns 0 hits outside `internal/infrastructure/qdrant/*` test files.

### 6.2 PR‑SCHEMA‑REGISTRY‑TYPE
- **Owner:** `internal/infrastructure/qdrant/verification`
- **Surface:** `DefaultSchemaRegistry` mutable global → typed `*SchemaRegistry` with method `Resolve(version)`.
- **Delta:** +50 LoC; -mutability.
- **Acceptance test:** `verification/schema_registry_test.go` swaps literal direct map construction.

### 6.3 PR‑SEARCH‑BACKEND‑SEMANTIC‑UNIT‑TEST
- **Owner:** `internal/app`
- **Surface:** `search_backend_semantic.go::semanticSearchBackend::compileSemanticFilters` (62 LoC, pivotal for Wave 30 fan‑out)
- **Delta:** +1 file `search_backend_semantic_test.go` with 4 TDD tests pinning lifecycle ACTIVE + workspace isolation + filter invariants + empty filters.
- **Acceptance test:** tests cover `compileSemanticFilters` directly (no qdrant call).

---

## §7 — Cross‑cutting pre‑existing build issues

Carried forward as `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` (6‑item list) PLUS the 3 disclosed in `d478c394` (PR-LIFECYCLE-CAPABILITY-DISABLED-SENTINEL ship):

| ID | File | Description | Deadline |
|---|---|---|---|
| FIX‑MONITOR‑ENQUEUE‑TOLOWER | `internal/application/assets/monitor/enqueue.go` | `strings.ToLower` undefined | 2026‑07‑15 |
| FIX‑MONITOR‑SCHEDULER‑ENQUEUER | `internal/application/assets/monitor/scheduler.go` | `NewUnboundJobEnqueuer` undefined | 2026‑07‑15 |
| FIX‑STOCKPIPELINE‑REDECLARATION | `run_upload.go` | File MISSING from disk (retired god‑object wave) | 2026‑07‑25 |
| FIX‑APP‑MODULE‑MEDIA‑DISPATCHER | `internal/app/module_media.go~line334` | Obsolete `MutationsDispatcher` literal in `clips.Deps` struct | 2026‑07‑25 |
| FIX‑IMAGES‑ROUTING‑CYCLE | `internal/application/images/routing/` | Structural DTO‑relocation per `e52005cc` | 2026‑08‑01 |
| FIX‑APP‑WIRE‑SCRIPT‑SYNTAX | `internal/app/workerruntime/{preflight.go, run.go}` | Actual broken location (post `d478c394` re‑audit) | 2026‑08‑01 — REVISIT‑AND‑RETIRE |
| FIX‑COMPOSITION‑OUTBOX‑BUNDLE‑TEST | `composition_test.go:774` | `BuildOutboxBundle` missing `VoiceoverCleanupDriver` arg | 2026‑07‑25 |
| FIX‑WORKER‑REGISTRY‑MOCK | `worker_registry_e2e_test.go:140+526+572+814+835` | `*mockBroker` missing `CompleteWithArtifacts` | 2026‑07‑25 |
| FIX‑CREATOR‑RUNTIME‑IMPORT | `creator_runtime_test.go:31` | Unused `go/ast` import | 2026‑07‑25 |

All 9 predated PR‑LIFECYCLE‑CAPABILITY‑DISABLED‑SENTINEL (`d478c394`) and carry forward per the canonical `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` 6‑item convention extended in‑place.

---

## §8 — Dependency Graph (critical‑path blockers)

The 4 dependency blockers I would surface for any agent sequencing this work:

```
PR-ENGINE-TYPED-SEAM (§2.1) ─────→ PR-ENGINE-PHASE-SPLIT (§2.2)
                                     (typed seam must land first;
                                      phases re-couple to typed seam pattern)

PR-FLOW-HELPERS-DUPLICATE-REMOVAL (§1.4) ─→ PR-FLOW-HELPERS-GODFILE-SPLIT (§2.3)
                                             (collapse duplicate first;
                                              then split)

PR-RUNTIME-FACADE-NARROW (§4.1) ───→ PR-QDRANT-RUNTIME-FACADE-OPT-A (§5.1)
                                      (facade shape pre-decides the
                                       ProcessBundle 8-fields collapse)

PR-ALIASES-SHIM-AUDIT (§4.4) ─────→ PR-ALIASES-FULL-REMOVAL (§6.1)
                                     (audit must precede full deletion;
                                      per godlike/08 zero-baseline rule)
```

---

## §9 — Migration sequence per godlike/07

For each per‑file PR:
1. **EXPAND** — canonical surface live + ci‑gate forward‑preventive (Check compatible with prior gates).
2. **BACKFILL** — migrate pre‑PR callers to consume the new typed surface.
3. **CUTOVER** — pre‑PR callers now off‑canonical; pre‑PR surfaces physically `git rm`.
4. **CONTRACT** — tightened‑gate (zero‑allowlist, fail‑closed).

Per‑PR granularity: the script‑gen+Qdrant analyses are biased toward EXPAND (canonical typed surface) + thin BACKFILL migrations within Fascia 2; CUTOVER lands when 100% of consumers have migrated (per‑file build cycle); CONTRACT is intentional‑later‑phase.

---

## §10 — Wave‑tracker anchor + SSOT lockstep

NEW wave‑tracker entry (this section is the canonical anchor for the 23 net‑new PR slots):

```
architecture/current.yaml#SCRIPT-QDRANT-STABILIZATION-2026-07-04
  status: in_progress
  exit_signal: false
  owner: architecture
  deadline: 2026-08-22
  linked_issues: 23 net-new slots (one per PR in §1–§6 above)
  forward_cite: EXTERNAL-AUDIT-2026-07-04 (renamed §5.1–5.2 forward-cites)
```

**Per godlike/06 SSOT**: this anchor does NOT modify `EXTERNAL-AUDIT-2026-07-04`. The forward‑cited §5.1 and §5.2 only modify their `linked_issues[]` slots when *that* PR ships; the canonical slot is owned there.

---

## Honest Limitation (final disclosure)

The 23 PRs above represent the synthesis of two static analyses on `d478c394`. They are **Recommended** for any agent valuing the methodology‑output noise floor below zero — but this is NOT a runtime‑validated implementation roadmap.

In particular:
- **Dependency graph (§8)** is based on logical ordering, not measured compilation order. A future agent may discover additional blockers (e.g. fan‑out race in the reconciler).
- **LoC deltas** are hand‑rolled estimates; commit‑level `git log --stat` after each PR is the authoritative tracker.
- **Cross‑cut impact** (e.g. PR‑ALIASES‑FULL‑REMOVAL may reveal a 100‑file surface reach‑through) is speculative until rg audit at PR creation.

Per `godlike/07 §no‑fake‑availability`, do NOT treat this plan as truth; treat it as a forward‑pointer enumerable.

---

## Cross‑reference

- `architecture/current.yaml#SCRIPT-QDRANT-STABILIZATION-2026-07-04` — canonical wave‑tracker anchor (NEW, this plan introduces it).
- `architecture/current.yaml#EXTERNAL-AUDIT-2026-07-04` — pre‑existing audit‑derived wave‑tracker (9 slots, forward‑cited by §5.1 and §5.2).
- `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` — 6‑item (now extended to 9‑item) carry‑forward build‑issue tracker.
- `architecture/current.yaml#GODOBJ-2026-07-03` — god‑object decomposition wave tracker — orthogonal (different concern; this plan's §3.3 PR‑POSTPROCESSOR‑REGISTRY‑SPLIT is mechanical, NOT a godobject split).
- `AGENTS.md §Git-Lesson-2/3/4/5` — canonical git workflow (direct‑to‑main, Co‑authored‑by trailer, byte‑equivalent‑replay acceptance).
- `AGENTS.md §godlike/06` — one canonical owner per fact (SSOT).
- `AGENTS.md §godlike/07` — no fake availability + typed‑error contract.
- `AGENTS.md §godlike/08` — zero‑baseline rule for transitional re‑export shims.
