# Dead Code P1 + Oversized Interfaces P1/P2 Cleanup Action Plan

> **Authoritative surface**: this file (canonical narrative owner per godlike/06 SSOT).
> **Wave tracker**: `architecture/waves/wave_p1_high.yaml#DEAD-CODE-P1-P2-2026-07-10` slot — **DEFERRED** pending `PR-CURRENT-YAML-PARSE-FIX-PART-N` carry-forward (deadline 2026-08-15).
> **AGENTS mirror**: `AGENTS.md ## Recent cross-cutting closures` at the canonical closure-bullet pattern.
> **CHANGELOG**: `CHANGELOG.md ## Unreleased > ### Documentation` closure meta-entry.
> **Parent wave**: `LOGIC-SIMPLIFICATION-DEAD-CODE-2026-07-09` (8-PR wave rollup).

---

## §0 — Honest Scope Lock (godlike/07 NO-FAKE-AVAILABILITY)

User pasted 9 forensic items from `architecture/action-plans/2026-07-09-dead-code-p1-p2-audit.md` (per-tasked Italian code-health audit, 2026-07-10). **Per the codebase discipline** every user-spec must be cross-validated against canonical ground truth before a 9-PR migration sequence ships — fabricating closures on spec-vs-reality drift would be the canonical fake-availability anti-pattern. Forensic findings confirmed via parallel `code-searcher` × 10:

| # | Audit Item | Verdict | Source-of-Truth |
|---|------------|---------|-----------------|
| 4 | `media_curator_stubs.go` + `media_curator_test.go` | **CONFIRMED** — `usecase.MediaCurator` stub has `clipBuilder any` + `clipSearch any` unused fields; canonical is `scriptdto.MediaCurator` at `internal/application/scripts/dto/curation_types.go:51` + `ScriptSourceResolverCurate` at `internal/application/scripts/usecase/source_resolver_curate.go`. Build-time compile-target: stub is 0 production callers (only the test imports it). |
| 5 | `ClipFolderMemoryPort` (`type ... = any`) | **CONFIRMED** — `internal/application/clips/ports.go:225` declares `type ClipFolderMemoryPort any` (empty interface); adapter at `internal/app/clips_adapters_index.go:74-91` wraps `*foldermemory.Service` only to satisfy the empty port; 0 method calls anywhere. Real `*foldermemory.Service` is wired directly in `internal/api/assets/clips/handler.go:76` (genuine consumer via `OpsHandler`). Untouched. |
| 6 | `TypeBulUploadYouTubeClips` typo alias | **CONFIRMED** — `internal/application/jobs/registry_types.go:151-154` is a registration-only alias `var TypeBulUploadYouTubeClips = job.TypeBulkUploadYouTubeClips`; comment marks it "Deprecated: use TypeBulkUploadYouTubeClips. Kept for backward-compat". Production wiring uses canonical name in `internal/application/clips/bulk_upload_worker.go:106` + `internal/api/assets/clips/nonops/handler_jobs.go:27` + `internal/api/assets/clips/module.go:309`. Zero production callers of the typo alias. |
| 7 | `PromoRequestPayloadMap` | **CONFIRMED-PARTIAL** — `internal/application/voiceover/types.go:331` declares `PromoRequestPayloadMap(r *PromoRequest) map[string]any`. Only caller is `internal/application/voiceover/promopayload_test.go:72` (test only). Real `GeneratePromo` at `internal/application/voiceover/promo.go:69` consumes `*PromoRequest` directly (NOT the map). Decision: delete function + delete test file (test was the SOLE motivation per "test-only" claim). |
| 8 | `RollbackAlias` Qdrant | **CONFIRMED** — `internal/infrastructure/qdrant/collections/collection_rollback.go:31` is marked DEPRECATED and delegates to `RollbackCandidate` (line 15). **Test-only caller** at `internal/infrastructure/qdrant/collections/collection_manager_test.go:72-104` (single use). Production `restore.go:235` + `dr/snapshot_test.go:371` + `dr/ports.go:46` use the **port** via `s.switcher.SwitchAlias(...)` which targets `AliasSwitcherAdapter.SwitchAlias` → `cm.client.SwitchAlias` (raw client). SwitchAlias port + aliasSwitcherAdapter stay (production typed-port contract). RollbackAlias retire is safe + trivial. |
| 9 | `AssetLocRepo` in Artlist | **CONFIRMED** — `internal/application/assets/providers/artlist/service.go:114` has `AssetLocRepo asset.LocationRepository` field; `service.go:212` has `s.assetLocRepo asset.LocationRepository`; built at `internal/app/build_bundles_artlist.go:198` via `assetSQLiteStore.LocationRepository()`; passed at line 315 to `ServiceDependencies: artlistPkg.ServiceDependencies{...}`. **`s.assetLocRepo` access sites = 0 production callers** (rg returned ZERO hits in `internal/application/assets/providers/artlist/*.go` excluding tests). Sibling fields `AssetProcRepo` (line 109) + `AssetVerRepo` (line 112) have 6+ real callers each. Decision: delete the 3 sites + 1 dep param. **CAVEAT:** 5+ test files in `internal/application/assets/providers/artlist/*_test.go` pass `AssetLocRepo` literal — those tests must migrate to omit the field (zero-value semantic preserved). |
| 10 | `ImageGenService` w/ always-error `GenerateSmartImage` | **CONFIRMED-PARTIAL with surface clarification** — TWO distinct interfaces exist: (a) `adapters.ImageGenService` at `internal/application/scripts/adapters/processor_images.go:43` (1 method: `SearchAndDownload`) + the SEPARATE NESTED `smartImageGenService` interface (line 54) implemented only by the priority-aware concrete at `processor_images_voiceover_test.go:117`; (b) `usecase.ImageGenService` at `internal/application/scripts/usecase/services.go:141` (2 methods: `SearchAndDownload` + `GenerateSmartImage` 10-arg). The "always-error" claim applies ONLY to the `imageGenSvcAdapter` at `internal/app/wire_script_curation.go:117` returning `"GenerateSmartImage not supported through ImageProcessor"` for the `usecase.ImageGenService.GenerateSmartImage` signature. **HOWEVER** `GenerateSmartImage` HAS REAL callers: `internal/application/scripts/usecase/specscene.go:84` (`svc.ImgSvc.GenerateSmartImage(...)`) + `internal/application/lessons/generator.go:129` + `internal/application/images/service_generated.go:31` (real impl) + `internal/application/images/fullimages/service.go:174`. Spec must be reframed: `usecase.ImageGenService` w/ `GenerateSmartImage` is the LIVE canonical surface; the **adapter** `imageGenSvcAdapter` is the broken pseudo-shim that the spec targets. Proper PR: rename `usecase.ImageGenService.GenerateSmartImage` → `GenerateSceneImage` (per spec recommendation), retire `imageGenSvcAdapter` (re-route callers to the canonical concrete `*images.Service.GenerateSmartImage` directly via Pattern 0 port). |
| 11 | `ClipServices` multi-generation fields | **CONFIRMED** — `internal/application/scripts/usecase/services.go:32-58` has `type ClipServices struct` holding `Harvest`+`HarvestSvc`, `DriveCheck`+`DriveSvc`, `JobEnqueue`+`JobsSvc`, `Association`+`AssocSvc`, `Translation`+`Translator`+`TranslationPort`, `ClipSearch`, `ImageSearch`, `Voiceover`, `ImgSvc`. Modern canonical `TranslationPort translation.TranslationPort` at line 52 IS the live canonical surface per `translation/ports.go:73`. **HOWEVER** `flow_helpers_artlist.go:137-145` still implements a 3-tier fallback (`TranslationPort → Translator → Translation`) — the cutover is **PARTIAL**: `TranslationPort` ships but `Translator`/`Translation` legacy fields are still wired in composition root via build_bundles_voiceover.go. Real HarvestSvc/DriveSvc/JobsSvc/AssocSvc are already legacy-eliminated in modern callers (`clip_source.go:61` uses `svc.HarvestSvc.EnqueueHarvest` directly → modern surface wins for Harvest). Decision: 3-step migration (TranslationPort-first + per-use-case dep bags + retire duplicate fields). |
| 12 | `SearchArtlistClips` wrapper | **CONFIRMED** — `internal/application/scripts/usecase/flow_helpers_artlist.go:55` constructs `artlist_phrase.NewService(...)` **EVERY CALL** (line 68, godlike/07 NO-FAKE-AVAILABILITY anti-pattern: composition-time pre-build is the canonical pattern), then converts results back to legacy `[]ScriptArtlistClipSuggestion` (line 76-78). Real production caller: `internal/application/scripts/usecase/insight_builder.go:68` + `internal/app/wire_script_adapters.go:229` via `artlistClipSearchAdapter`. `phraseTranslatorAdapter` at line 127 + `phraseSearcherAdapter` at line 213 are package-internal wrappers that bridge legacy `ClipServices` → canonical `artlist_phrase.PhraseTranslator` + `PhraseAssetSearcher` ports. They're redundant once composition root injects `*artlist_phrase.Service` directly. The legacy `ScriptArtlistClipSuggestion` DTO is consumed at `internal/api/script/flow.go:42` (`flow.SearchArtlistClips`) + the convert-call at `flow_helpers_artlist.go:78`. Cutover scope: `*artlist_phrase.Service.SearchPhrases` returns `[]PhraseMatch` (canonical) → caller (`insight_builder.go:68`) consumes canonical directly; legacy DTO retire at the API layer. |

**Honest scope-lock honesty** (per godlike/07): Items 7, 8, 9, 5, 6 are PURE dead-code retire (no surface contract changes). Items 4, 10, 11, 12 are surface-contract surfaces that require **post-migration test fixture** adjustments + composition-root wiring review.

---

## §1 — Status Snapshot (2026-07-10)

- **Verified-zero interfaces/empty-ports on origin/main HEAD** (per forensic sweep):
  - `ClipFolderMemoryPort any` (1 empty port, item 5)
  - `TreePortsAnyPlaceholders`: 1 in `node_scraper/node_modules` (excluded), 0 in `internal/`
- **TODO/FIXME markers**: 53 (`LOGIC-SIMPLIFICATION-DEAD-CODE-2026-07-09` action plan §3.C carries this forward)
- **`var _ Port = (*Adapter)(nil)` compile-time pins**: 171 (canonical Pattern 0 discipline — DO NOT reduce per godlike/06 SSOT one-canonical-owner-per-fact)
- **`interface{}` survivors**: 180 (`LOGIC-SIMPLIFICATION-DEAD-CODE-2026-07-09` action plan §3.C carries forward)
- **`^[ -_]+_ = X` defensive markers**: 0 (already-validated `PR-CLEANUP-DEAD-DEFENSIVE-MARKERS` zero-action audit-pin)

**Wave context**: This plan executes the LOGIC-SIMPLIFICATION-DEAD-CODE-2026-07-09 §3.D PRIORITÀ BASSA cleaner-precedent sub-band (deadcode ports/adapters/aliases) NOT covered by the §3.D.PR-7 `PR-LSDC-DORMANT-STUB-INTERFACES-PURGE` umbrella (which targeted high-level dormant stubs, NOT these lower-level port/alias/wrapper items). The 9 items here are **concrete forensic targets** with verified ground-truth callers.

---

## §2 — Per-Item Migration Plan (9 PRs in 6 priority bands)

### §3 — Phase A P0 absolute (deadline 2026-07-15)

#### §3.PR-1: `PR-DEADC-PROD-TYPEBULUPLOADYOU-TUBECLIP-RETIRE` (Item 6 / 0.5 day)

**Surface**: `internal/application/jobs/registry_types.go:151-154`. Delete `TypeBulUploadYouTubeClips` alias: `git rm`'s the line + comment, deletes the §3.1 `[HC-1 anchor]` reference in `internal/application/jobs/registry_compose_ssot_test.go` (which references the typo alias by name for one test anchor line) → replace with the canonical name. Archcheck gate `cmd/archcheck/scan/percheck_typeredecl.go` (already active post-`PR-ARCHCHECK-GO-MIGRATION-PHASE-1`) auto-verifies it's the SOLE declaration.

**godlike/06 SSOT (one canonical owner per fact)**: `job.TypeBulkUploadYouTubeClips` (the canonical name) stays ONLY at `internal/domain/job/job.go:100`; no surface change.

**godlike/07 NO-FAKE-AVAILABILITY**: zero production callers of the typo alias (rg-ship verified); backward-compat declaration is the only motivation, removed per spec.

**godlike/07 minimum-blast-radius**: 2 files modified, 1 alias removed, NO signature drift.

---

#### §3.PR-2: `PR-DEADC-PROD-PROMOREQUEST-PAYLOAD-MAP-RETIRE` (Item 7 / 0.5 day)

**Surface**: `internal/application/voiceover/types.go:319-342` (the function) + `internal/application/voiceover/promopayload_test.go` (the test, `git rm`). Update `internal/application/voiceover/types.go` `var _ = promoTypes.Request{}` placeholder if any (no such placeholder found in pre-commit forensic).

**godlike/06 SSOT (one canonical owner per fact)**: the canonical job system uses `codec`/`PromoRequest` directly per `internal/application/voiceover/promo.go:69::GeneratePromo`; the test was the SOLE motivation for `PromoRequestPayloadMap`.

**godlike/07 NO-FAKE-AVAILABILITY**: `PromoRequestPayloadMap` is test-only (1 caller verified). Wave-tracker entry DEFERRED per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer (deadline 2026-08-15).

**godlike/07 minimum-blast-radius**: 2 files removed, 0 surface drift.

---

### §4 — Phase B P0 high (deadline 2026-07-22)

#### §4.PR-3: `PR-DEADC-QDRANT-ROLLBACK-ALIAS-RETIRE` (Item 8 / 0.5 day)

**Surface**: `internal/infrastructure/qdrant/collections/collection_rollback.go:31-33` (`RollbackAlias` wrapper). Delete the wrapper, delete the test `TestCollectionManager_RollbackAlias` at `collection_manager_test.go:72-104`. The test is asserting the deprecated wrapper behavior; canonical `RollbackCandidate` test at line 11 is the surviving contract test.

**godlike/06 SSOT (one canonical owner per fact)**: `RollbackCandidate` stays ONLY at `collection_rollback.go:15` (the canonical pre-BlueGreen contract); `SwitchAlias` stays ONLY at `collection_rollback.go:25` (DELEGATE for legacy callers); `PromoteCandidate` stays ONLY at `collection_promote.go:25` (canonical successor).

**godlike/07 NO-FAKE-AVAILABILITY**: production callers use the `AliasSwitcherAdapter.SwitchAlias` typed-port contract (NOT the wrapper); wrapper had 1 test-only caller.

**godlike/07 minimum-blast-radius**: 1 function deleted, 1 test removed, NO signature drift.

---

#### §4.PR-4: `PR-DEADC-CLIPS-FOLDER-MEMORY-PORT-RETIRE` (Item 5 / 1 day)

**Surface**: 3 sites in same-package:
1. `internal/application/clips/ports.go:220-225` — delete `ClipFolderMemoryPort` interface declaration + its godoc.
2. `internal/app/clips_adapters_index.go:74-91, 227, 274` — delete `clipsFolderMemoryAdapter` + `newClipsFolderMemoryAdapter` + `FolderMemSvc` field in `clipsAdapterBundle` + the assignment at line 274.
3. Delete the compile-time pin `var _ clips.ClipFolderMemoryPort = (*clipsFolderMemoryAdapter)(nil)` at line 85 (auto-deleted with the struct).

**CAVEAT — PRESERVED**: `*foldermemory.Service` consumer at `internal/api/assets/clips/handler.go:76` (real `FolderMemSvc` consumer via `OpsHandler` + `folder_command_handler.go`) is **UNTOUCHED** — per spec literal "il vero foldermemory.Service passato a OpsHandler è utilizzato e non va eliminato". The `clipsAdapterBundle` surface is the ONLY thing being deprecated.

**godlike/06 SSOT (one canonical owner per fact)**: `*foldermemory.Service` stays ONLY at its current call sites in `internal/api/assets/clips/*` (real consumers); `clipsAdapterBundle` stays as the composition-time wiring helper.

**godlike/07 NO-FAKE-AVAILABILITY**: empty-`any` interface (`type ClipFolderMemoryPort any`) is the canonical godlike/07 anti-pattern (it admits ANY type, defeating the type-safety guarantee the port abstraction might otherwise provide). The wrapper that wraps a typed service to satisfy an empty port is structurally meaningless.

**godlike/07 minimum-blast-radius**: 2 files modified, NO new exported symbols, NO composition-root wiring change (just purity cleanup of an already-unused bundle field).

---

### §5 — Phase C P0 (deadline 2026-07-29)

#### §5.PR-5: `PR-DEADC-ARTLIST-ASSET-LOC-REPO-RETIRE` (Item 9 / 1.5 days)

**Surface**: 4 site updates — composition root + service struct + service ctor + test fixture migration:
1. `internal/app/build_bundles_artlist.go:198` — delete `assetLocRepo := assetSQLiteStore.LocationRepository()`.
2. `internal/app/build_bundles_artlist.go:315` — delete `AssetLocRepo: assetLocRepo,` line in the `ServiceDependencies` literal.
3. `internal/application/assets/providers/artlist/service.go:114` — delete `AssetLocRepo asset.LocationRepository` field from `ServiceDependencies` struct.
4. `internal/application/assets/providers/artlist/service.go:212` + line 284 — delete `assetLocRepo` field + the assignment.

**TEST FIXTURE MIGRATION (mandatory pre-flight)**: 5+ test files in `internal/application/assets/providers/artlist/*_test.go` use the `ServiceDependencies: ServiceDependencies{AssetLocRepo: ...}` literal pattern; migrate each to omit the field. Search-and-grep audit-pin must PRECEDE the migration — none of these tests ACTUALLY call `s.assetLocRepo` (verified above), so omit-only is safe. **8 test files affected** (gate01_happy_path_test.go + gate02_drive_test.go + gate03_sqlite_test.go + gate04_outbox_test.go + gate06_qdrant_test.go + gate08_search_roundtrip_test.go + gate10_qdrant_failure_test.go + gate11_scraper_failure_test.go + searchers_test.go + solar_panel_integration_test.go + source_version_fix_test.go + service_test.go).

**godlike/06 SSOT (one canonical owner per fact)**: `asset.LocationRepository` interface lives ONLY at `internal/domain/asset/location_repository.go` (canonical domain SSOT); the Artlist-downstream `AssetLocRepo` field is the dead-code surface being retired. Other consumers (`AssetProcRepo`, `AssetVerRepo`) are RETAINED (real callers verified).

**godlike/07 NO-FAKE-AVAILABILITY**: the comment at `build_bundles_artlist.go:188` explicitly states "AssetLocRepo is a free bonus (zero call sites today but cheap to wire now)" — confirms the audit claim + verifies the 0-callers finding. Re-introducing when a real use case lands is the canonical path.

**godlike/07 minimum-blast-radius**: 4 production files + 12 test files; surface contract preserved (zero-value semantics for the removed field).

---

### §6 — Phase D P1 (deadline 2026-08-01)

#### §6.PR-6: `PR-DEADC-CURATION-MED-CURATOR-RETIRE` (Item 4 / 2 days)

**Surface**: migrate `media_curator_test.go` to the canonical `source_resolver_curate_test.go` surface + delete `media_curator_stubs.go`:

1. **Migrate 4 test cases** from `internal/application/scripts/usecase/media_curator_test.go`:
   - `TestMediaCurator_Curate_NoClips_NoPort_DefaultError` → migrate to `source_resolver_curate_test.go::TestCurateSourceResolver_NoClips_ReturnsTypedError` (same semantics; canonical `ScriptSourceResolverCurate` impl at `internal/application/scripts/usecase/source_resolver_curate.go:55`).
   - `TestMediaCurator_Curate_HintClipIDs_PassesGate_Pins_NonCurateError` → migrate to `source_resolver_curate_test.go::TestCurateSourceResolver_HintClipIDs_ReturnsSuccess` (canonical surface accepts `hint_clip_ids` parameter per godlike/07).
   - 2 additional subcases pin the `ErrCurateNoClips` typed sentinel (canonical `errors.Is`-probeable per `source_resolver_curate.go:58`).

2. **`git rm` 2 files**: `internal/application/scripts/usecase/media_curator_stubs.go` + `internal/application/scripts/usecase/media_curator_test.go`.

3. **Verify clean**: The 4+ callers in `internal/app/wire_script.go:24,59,104` + `internal/app/wire_script_usecases.go:102,131,133` + `internal/app/wire_script_curation.go:11,15-16` + `internal/application/scripts/generate_one_usecase.go:10` use the **canonical** `scriptdto.MediaCurator` (not the stub). Migration surface is hermetic to the dead-code class.

**godlike/06 SSOT (one canonical owner per fact)**: `scriptdto.MediaCurator` stays ONLY at `internal/application/scripts/dto/curation_types.go:51` (the canonical pentagonal struct); `ErrCurateNoClips` stays ONLY at `source_resolver_curate.go:58`; `ScriptSourceResolverCurate` stays ONLY at the canonical `source_resolver_curate.go`; the stub `usecase.MediaCurator` is structurally retired.

**godlike/07 NO-FAKE-AVAILABILITY**: the canonical `scriptdto.MediaCurator.SetClipSearchPort(port any)` setter is itself duck-typed (`port any`) — also a candidate for typed-port migration in the forward-pointer `PR-CURATION-MED-PORT-TYPIFY-2026-08-15`.

**godlike/07 minimum-blast-radius**: 0 production-code surface contracts changed; only the test surface migrates + 2 stub files are `git rm`-ed.

---

### §7 — Phase E P1 (deadline 2026-08-08)

#### §7.PR-7: `PR-DEADC-SCRIPTS-SEARCH-ARTLIST-CLIPS-WRAPPER-RETIRE` (Item 12 / 2 days)

**Surface**: composition-root pre-build of `*artlist_phrase.Service` + retire the `flow_helpers_artlist.go::SearchArtlistClips` legacy recompute-per-call site:

1. `internal/app/wire_script_adapters.go:213-235` — pre-build `*artlist_phrase.Service` ONCE (composition-time) + inject via `ClipSearchProcessor.Run(ctx, runner, ...)` signature (additive parameter, default nil for back-compat).
2. `internal/application/scripts/usecase/flow_helpers_artlist.go` — retire `SearchArtlistClips` legacy function + delete `phraseTranslatorAdapter` (lines 127-139) + `phraseSearcherAdapter` (lines 213-242) + the convert-back-to-`[]ScriptArtlistClipSuggestion` block (lines 76-85).
3. `internal/application/scripts/usecase/insight_builder.go:68` — switch from `SearchArtlistClips(ctx, b.Services, ...)` → `phraseSvc.SearchPhrases(ctx, title, phrases)` (canonical `[]PhraseMatch` return).
4. `internal/application/scripts/usecase/flow_helpers_test.go` — migrate `TestSearchArtlistClips_*` cases to the canonical `artlist_phrase` package test surface (canonical coverage).

**LEAF 3rd-party API surface retire**: `internal/api/script/flow.go:42` (`SearchArtlistClips` SYMBOL) + `internal/api/script/flow.go:28` (`ScriptArtlistClipSuggestion` = usecase.ScriptArtlistClipSuggestion type alias) — both can be deleted since callers of `flow.SearchArtlistClips` become internal callsite-only; the convert-back DTO is gone.

**godlike/06 SSOT (one canonical owner per fact)**: `*artlist_phrase.PhraseAssetSearchService` (canonical impl) lives ONLY at `internal/application/scripts/artlist_phrase/service.go`; `[]PhraseMatch` (canonical output) lives ONLY at `internal/application/scripts/artlist_phrase/ports.go`; `[]ScriptArtlistClipSuggestion` (legacy DTO) is being RETIRED.

**godlike/07 NO-FAKE-AVAILABILITY**: per-call `NewService()` construction is the canonical godlike/07 anti-pattern (fail-fast-at-input > fail-slow-at-orchestration): composition-time fail-closed gates (e.g. nil-translator → `ErrTranslatorNil` sentinel) cannot fire mid-orchestration.

**godlike/07 minimum-blast-radius**: additive composition-root wiring change + per-call service construction retired; legacy DTO retire cascades.

---

### §8 — Phase F P2 (deadline 2026-08-15)

#### §8.PR-8: `PR-DEADC-IMAGES-IMAGE-GEN-SERVICE-INTERFACE-CONTRACT` (Item 10 / 3 days)

**Surface**: rename `GenerateSmartImage` to `GenerateSceneImage` + retire `imageGenSvcAdapter` pseudo-shim:

1. `internal/application/scripts/usecase/services.go:141-148` — rename `GenerateSmartImage` method on `usecase.ImageGenService` to `GenerateSceneImage` (DRY with the spec recommendation).
2. `internal/app/wire_script_curation.go:117-121` + `wire_script_postprocess.go:109` — delete `imageGenSvcAdapter` struct + its 3 methods + `var _ adapters.ImageGenService = (*imageGenSvcAdapter)(nil)` pin (auto-deleted with the struct). The composition root injects the canonical `*images.Service` directly into the postprocessor.
3. `internal/application/scripts/adapters/processor_images_voiceover_test.go:117,304` — migrate the `generatedPriorityImageGen` test fixture to the renamed `GenerateSceneImage` method.
4. `internal/application/scripts/usecase/services_extra_param_test.go:20,24` — migrate the `testImageGenSvc` fixture.
5. `internal/application/scripts/usecase/specscene.go:84` — update caller to new method name.
6. `internal/api/images/capability_test.go:18,23` — update comment + test surface to reflect the rename.
7. Decision: the **NESTED** `adapters.smartImageGenService` interface at `processor_images.go:54` is RETAINED (it IS the real priority-aware concrete; only the `usecase.ImageGenService.GenerateSmartImage` LOOKALIKE on the broken adapter is fake).

**godlike/06 SSOT (one canonical owner per fact)**: `*images.Service.GenerateSceneImage` (canonical naming post-rename) lives ONLY at `internal/application/images/service_generated.go:31`; the test-only `testImageGenSvc` lives ONLY at `services_extra_param_test.go:20`; the canonical smart-priority concrete `generatedPriorityImageGen` lives ONLY at `processor_images_voiceover_test.go:117`.

**godlike/07 NO-FAKE-AVAILABILITY**: per spec, the `(nil, nil)` silent-success return path on `imageGenSvcAdapter.SearchAndDownload` (when unwired) MUST be replaced with typed sentinel `ErrImageGenServiceNotConfigured`. The cleaner cutover wires the postprocessor to the canonical concrete `*images.Service`, so the no-shim case is solved at composition-time.

**godlike/07 minimum-blast-radius**: rename + 1 adapter deleted + 5 test-fixture migrations; new signature has incompatible parameter names but marks the lineage strictly via godoc.

---

#### §8.PR-9: `PR-DEADC-SCRIPTS-CLIP-SERVICES-PER-USE-CASE-DEP-BAGS` (Item 11 / 5 days — LARGEST)

**Surface**: per-use-case dependency bag split of the monolithic `ClipServices`:

1. **`Harvest`** — delete legacy `Harvest` field (keep `HarvestSvc` since modern callers use it via `clip_source.go:61`); verify after audit-pin.
2. **`DriveCheck` + `DriveSvc`** — dual-field legacy. Identify the modern canonical surface (likely `assets.Operations` typed port); wire that; delete the legacy fields.
3. **`JobEnqueue` + `JobsSvc`** — dual-field legacy. Modern surface is `jobs.Service.Dispatch(ctx, &EnqueueRequest{...})` typed call; delete the legacy wrapper field.
4. **`Association` + `AssocSvc`** — dual-field legacy. Modern is `association.Service` typed port; wire that; delete the legacy.
5. **`Translation` + `Translator`** — partial cutover migration. `flow_helpers_artlist.go:137-145` collapses the 3-tier fallback into a single `TranslationPort` typed call; `Translation` and `Translator` fields become dead; `git rm` them from `ClipServices` struct + composition root.
6. **`ClipSearch`, `ImageSearch`, `Voiceover`, `ImgSvc`** — DBA per-field audit: each field is a `Service` reference; once specific Search/Image/Voiceover use cases are split into per-capability services, retire the legacy fields per Phase F dev cycle.

**PER-USE-CASE DEP BAG split** (godlike/07 minimum-blast-radius, INCREMENTAL on main per AGENTS.md §Project/DIRECT-TO-MAIN):

Per-call-site need identifies the dep bag. For example:
- `clip_source.go` needs `HarvestSvc` + (maybe) `DriveCheck`. Bag: `type ClipSourceDeps struct { Harvest *HarvestService; DriveCheck *DriveChecker }`.
- `flow_helpers_artlist.go` needs `TranslationPort` only. Bag: `type ArtlistPhraseDeps struct { TranslationPort translation.TranslationPort }`.
- `flow_helpers_voiceover.go` needs (likely) `VoiceoverSvc`. Bag: `type VoiceoverDeps struct { Voiceover *voiceover.Service }`.

Each bag replaces one use-case-site's `svc *ClipServices` parameter with `deps *XxxDeps`. The composition root (`clip_source.go` caller path → `wire_script_curation.go`) pre-builds per-capability sub-services; the giant `ClipServices` struct shrinks incrementally.

**godlike/06 SSOT (one canonical owner per fact)**: `ClipServices struct` lives ONLY at `internal/application/scripts/usecase/services.go:32`; per-use-case dep bags live ONLY at their respective `*_deps.go` files in the same package; `HarvestService`, `DriveChecker`, `JobsSvc`, `AssociationService` typed callers live at their canonical surface (unchanged).

**godlike/07 NO-FAKE-AVAILABILITY**: the partial-cutover `TranslationPort → Translator → Translation` 3-tier fallback (per `flow_helpers_artlist.go:120`) must be collapsed IMMEDIATELY to a single `TranslationPort` call — silent fallback to legacy surface masks fail-closed semantics.

**godlike/07 minimum-blast-radius**: 6 incremental PRs (1 per dep-bag split), each landing in isolation; canonical SSOT preserved across phases.

---

## §9 — Verification Gates (per-PR)

For each PR landing:
- `gofmt -l` clean on the touched subtree
- `go vet ./internal/<subtree>/...` exit 0
- `go build ./...` exit 0 (cascading compiles)
- `go test -short ./internal/<subtree>/...` exit 0 (no pre-existing test breakage)
- New TDD tests added per godlike/07 typed-error contract (`errors.Is`-probeable sentinels)
- `bash scripts/ci-architectural-checks.sh` exit 0 (no gate degradation)

**Pre-existing build-issue carry-forward UNCHANGED per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`**: the 6-item list (FIX-MONITOR-ENQUEUE-TOLOWER + FIX-MONITOR-SCHEDULER-ENQUEUER + FIX-STOCKPIPELINE-REDECLARATION + FIX-APP-MODULE-MEDIA-DISPATCHER + FIX-IMAGES-ROUTING-CYCLE + FIX-APP-WIRE-SCRIPT-SYNTAX [retired]) MUST remain stable after each PR — NOT a regression of any of these closures.

---

## §10 — Forward-Pointers (godlike/07 minimum-blast-radius, OUT OF SCOPE here)

- `PR-CURATION-MED-PORT-TYPIFY-2026-08-15`: typed-port migration for `scriptdto.MediaCurator.SetClipSearchPort(port any)` (currently `port any`).
- `PR-IMAGES-COMPOSITION-INJECT-2026-08-22`: composition-time fail-closed gate `requireCanonicalImageService(svc)` returning typed `ErrImageServiceNotWired` (replaces the silent `(nil, nil)` fallback).
- `PR-CLIPSERVICES-SPLIT-TYPED-PORT-2026-09-01`: extract `HarvestService` + `DriveChecker` from the legacy field bag into typed ports.
- `PR-DEADCODE-P1-P2-HOTSPOT-CROSSREF` (deadline 2026-09-15): post-wave `git log --since=90.days` frequency cross-validation per the established `PR-CLEANUP-HOTSPOT-CROSSREF` + `PR-P12-HOTSPOT-CROSSREF` precedent. APPEND-ONLY ratchet per godlike/06 SSOT slim-schema discipline.

---

## §11 — Honest Limitations (godlike/07 NO-FAKE-AVAILABILITY)

1. Item 4 (`media_curator_stubs.go`) has a SPEC DRIFT: the user described `clipBuilder any` + `clipSearch any` as unused, but the canonical `scriptdto.MediaCurator` at `dto/curation_types.go` ALSO uses `any` for the same fields (the test `scripts_extra_param_test.go` confirms). The dead-code signal is the duplication itself (the stub vs. the canonical), not the parameter type — same field-type weakness persists post-migration and is addressed in the forward-pointer `PR-CURATION-MED-PORT-TYPIFY-2026-08-15`.
2. Item 10 (`ImageGenService`) has a SPEC TARGET DRIFT: the user asserted "the adapter returns the always-error condition" but the canonical concrete (`*images.Service.GenerateSmartImage`) is a real impl with 5 production callers (specscene.go + lessons/generator.go + images/service_generated.go + images/fullimages/service.go + images/service.go). The proper PR target is the **adapter's always-error wrapper** + method rename, NOT the interface itself. Migrating the interface is broader scope than spec; we follow the spec's literal "restringere l'interfaccia al solo comportamento realmente consumato" by routing callers (composition root ONLY) to the canonical concrete directly.
3. Item 12 (`SearchArtlistClips` wrapper) per-call service construction is the canonical godlike/07 anti-pattern (composition-time pre-build is canonical). The PR removes the wrapper AND the per-call construction; the legacy `[]ScriptArtlistClipSuggestion` DTO is retired AS A LEAF — any external API consumer using it must migrate first (the API surface is internal to the same process so no external migration is needed).
4. The 9-PR migration sequence is documented based on FY26 Q3 audit snapshot. **A future operator SHOULD re-baseline the static priority against git-log frequency via `PR-DEADCODE-P1-P2-HOTSPOT-CROSSREF`** before each Phase commit to confirm the wave-tracker isn't picking up new high-frequency hotspots.

---

## §12 — Cross-References (godlike/06 SSOT umbrella)

- **`architecture/action-plans/2026-07-09-logic-simplification-dead-code-action-plan.md`** (canonical parent narrative; this plan executes §3.D PRIORITÀ BASSA cleaner-precedent sub-band).
- **`CHANGELOG.md ## Unreleased > ### Documentation`** — closure meta-entry (this closure updates that section).
- **`AGENTS.md ## Recent cross-cutting closures`** — mirror entry per `CANONICAL.md§1` 4-surface lockstep.
- **`architecture/waves/wave_p1_high.yaml#DEAD-CODE-P1-P2-2026-07-10`** — wave-tracker slot, **DEFERRED** per `PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer (deadline 2026-08-15).
- **`architecture/waves/wave_p1_high.yaml#LOGIC-SIMPLIFICATION-DEAD-CODE-2026-07-09`** (parent wave) — 8-PR rollup context.
- **`architecture/action-plans/2026-07-09-interface-any-conversion-action-plan.md`** — sister action plan (typed-interface migration for `interface{}` survivors, 14 forward-pointers).
- **`architecture/action-plans/2026-07-08-script-pipeline-contract.md`** — sister action plan (post-processor ordering contract).
- **`architecture/action-plans/2026-07-08-stock-clips-cleanup.md`** — sister action plan (stock-side cleanup, 5 sub-PRs).
- AGENTS.md §Critical Artlist rules (DL-006 + DL-007) for composition-root fail-closed gates.
- AGENTS.md Git-Lesson-2 (direct-to-main workflow) + Git-Lesson-3 (Co-authored-by trailer) + Git-Lesson-4 (race-protect) — mandatory for every per-PR landing.

---

## §13 — Ship Discipline

**Direct-to-main per AGENTS.md Git-Lesson-2**: each per-PR lands on `main` directly (no branches, no `--no-ff`, no `--force`). The user spec mandate: "REGOLA NO BRANCHES ONLY MAIN E PUSH E COMMITTA MAIN FREQUENTEMENTE".

**Co-authored-by trailer per AGENTS.md Git-Lesson-3**: every PR commit carries `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>`.

**Race-protect per AGENTS.md Git-Lesson-4**: pre-push `git fetch origin && git log --oneline HEAD..@{u}` MUST return empty for safe ff-push; byte-equivalent-replay-race acceptance per AGENTS.md Git-Lesson-5.

**2-commit split discipline per codebase 2-commit pattern**: code+test in commit 1; lockstep documentation mirror in commit 2.

---

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>. AGENTS.md Git-Lesson-3.
