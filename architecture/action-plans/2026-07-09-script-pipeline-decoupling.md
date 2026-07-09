# SCRIPT-PIPELINE-DECOUPLING-2026-07-09 — Single-Source-of-Truth cleanup wave

> **Canonical surface (godlike/06 SSOT lockstep, per CANONICAL.md §1)**:
> - Surface 1/4 (canonical narrative): `architecture/action-plans/2026-07-09-script-pipeline-decoupling.md` (this file)
> - Surface 2/4 (closure meta-entry): `CHANGELOG.md ## Unreleased > ### Refactor` (mirror under §11)
> - Surface 3/4 (audit-pin mirror): `AGENTS.md ## Recent cross-cutting closures`
> - Surface 4/4 (wave-tracker slot): **DEFERRED** per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer (deadline 2026-08-15). The canonical SOLE closure record this commit ships is surfaces 1/2/3 per the SCRIPTCONTRACT-2026-07-08 audit-pin precedent.

---

## §0 Honest status snapshot (godlike/07 NO-FAKE-AVAILABILITY)

The codebase has 12 documented inconsistencies between **wire-shape comments** (godlike-style SSOT language in godoc + plan-builders + registries) and **runtime execution semantics**. The drift is concentrated in 4 packages:

| Package | Files | Drift surface |
|---------|-------|---------------|
| `internal/application/scripts/adapters` | `processor_names.go:42` (`ProcessorTranslation` const declared) + `postprocessor_composite.go:94-104` (`defaultPolicyByName` map missing translation) + `generation_normalizer.go:225-265` (`applySafetyDefaults` forces GenerateDocument=true, SaveToDB=true, OutputFmt="json") + `postprocessor_document.go:21` (`PipelineResult.Scenes []SceneImage` misnamed) | **12 drift points** in 4 sibling files |
| `internal/application/scripts/usecase` | `generation_plan_builder.go:135-180` (no conditional `len(out.Languages)>0` for `ProcessorTranslation` append) + `postprocessing.go` (intent: postprocess orchestration future extraction) | **1 drift point** (buildPostprocessorList truth vs CanonicalProcessorNames truth) |
| `internal/app` | `wire_script_postprocess.go:84-95` (godoc claims Persistence-first REGISTRATION; but execution order is `entities → clip_search → metadata → translation → clip_bindings → stock → voiceover → images → document → persistence` per `processor_names.go:62-81`) | **2 drift points** (godoc vs plan execution) |
| `internal/application/scripts/adapters` | `processor_translation.go:104-107` (`input.SpecScene = translated.SpecScene; input.Text = translated.Text` are LOCAL-COPY writes — `input ProcessInput` is passed BY VALUE) | **1 drift point** (false propagation) |

Verdict: the canonical SSOT language is correct; the runtime is selectively correct; 6 substantial gaps remain.

---

## §1 Goal (godlike/06/07 alignment)

Bring the script-pipeline end-to-end contract into **single-source-of-truth alignment** so the SSOT language in godoc matches the runtime execution semantics:

1. **One defined direction** for `GenerateVoiceover`/`GenerateSceneImages`/`GenerateDocument` (inline until downstream jobs land).
2. **One owner of execution order** in `buildPostprocessorList` (matches `CanonicalProcessorNames()` byte-for-byte).
3. **Translation reaches the plan** when `out.Languages != []` (currently silently absent from `buildPostprocessorList`).
4. **TranslationProcessor propagates the returned `TranslatedText` + `TranslatedSpecScene`** into the next postprocessor's input (currently mutates a local copy that the caller never sees).
5. **All canonical names have a default policy** (`defaultPolicyByName` covers 8 of 9 names; `ProcessorTranslation` is absent).
6. **Bool fields replaced by tri-state `Toggle`** so "caller did not set" is distinguishable from "caller set explicitly false".
7. **`Scenes` field renamed to `SceneImages`** with legacy JSON tag `"scenes"` preserved (fixes developer confusion without breaking JSON wire shape).
8. **`AssetStatus` enum centralizes image binding state** so empty URL never becomes "generated".
9. **`OutputFmt` splits into `ModelOutputMode` + `WireOutputMode`** to align with the canonical LLM contract (plain prose → JSON via us).
10. **One preflight surface** replaces the current `postprocessor_preflight.go` (HTTP) + Registry runtime split.

The plan is **strictly orthogonal to** the existing SCRIPTCONTRACT-2026-07-08 wave (PR-1 Persistence reorder is unchanged; PR-2 preflight is preserved and EXTENDED with the new ProcessorCapabilityRegistry in PR-10 of this plan).

---

## §2 Migration sequence (godlike/07 EXPAND → BACKFILL → CUTOVER → CONTRACT)

12 PRs in 4 phases. Each lands **directly on `main`** per AGENTS.md Git-Lesson-2 (no branches, no `--no-ff`, no `--force`).

### Phase A (deadline 2026-07-15) — P0 Definitions

#### PR-1 — `PR-SCRIPT-DECOUPLE-DECISION-LOCK`
**Goal:** Lock the "inline-for-now" direction in `OutputSpec` godoc. Remove the misleading "deprecated / no effect" language because the inline processors are still active.

**Surface (1 file, ~5 LoC godoc change):** `internal/application/scripts/adapters/generation_normalizer.go:225-265` (the `applySafetyDefaults` godoc block) + `internal/domain/script/types.go` (the `OutputSpec` struct godoc).

**Change:** rewrite the godoc to state: *"GenerateVoiceover/GenerateSceneImages/GenerateDocument are ACTIVE inline flags. They route processors in `buildPostprocessorList`. Future cuts to downstream jobs will be tracked via the FASE-2 Spina Dorsale wave (separate from this plan)."*

**godlike/07 minimum-blast-radius:** pure documentation. No behavioral change. No new symbols. **Compatibility:** `applySafetyDefaults` STILL forces these to true on safety-default path (intentional, see PR-3 below).

---

#### PR-2 — `PR-SCRIPT-DECOUPLE-TRANSLATION-DEFAULT-POLICY`
**Goal:** Register `ProcessorTranslation` in `defaultPolicyByName` so `ValidateRequested` doesn't flag it as unknown when it enters the plan (PR-5).

**Surface (2 files, +~10 LoC):** `internal/application/scripts/adapters/postprocessor_composite.go:94-104` (add `ProcessorTranslation: ProcessorBestEffort`) + NEW `internal/application/scripts/adapters/postprocessor_composite_test.go::TestDefaultPolicy_CoversAllCanonicalProcessorNames`.

**Change:** the new `TestDefaultPolicy_CoversAllCanonicalProcessorNames` iterates `CanonicalProcessorNames()` and asserts every entry has a value in `defaultPolicyByName`. The new entry uses `ProcessorBestEffort` (translation is enrichment-only, NOT a hard gate).

**godlike/07 minimum-blast-radius:** 1 new line in the existing map + 1 new test function (~30 LoC). **Compatibility:** all 9 canonical processors have policy assignments; future canonical name additions without policy assignments will FAIL the test (forward-prevention lock).

---

#### PR-3 — `PR-SCRIPT-DECOUPLE-TOGGLE-TRISTATE`
**Goal:** Replace boolean `OutputSpec` fields with a tri-state `Toggle` enum so "caller did not set" is distinguishable from "caller set explicitly false".

**Surface (8 files, +~120 LoC span):**
- `internal/domain/script/types.go`: add `type Toggle int` (const `ToggleUnset = 0`, `ToggleTrue = 1`, `ToggleFalse = 2`) + add `ToBool(bool) Toggle` constructor + `ResolveUnset(Toggle) Toggle` precedence resolver.
- Same file: change `OutputSpec.GenerateDocument`, `OutputSpec.GenerateSceneImages`, `OutputSpec.GenerateVoiceover`, `OutputSpec.SaveToDB` from `bool` to `Toggle`.
- `internal/application/scripts/adapters/generation_normalizer.go::applySafetyDefaults` (lines 225-265): replace the 2 `if !item.Output.GenerateDocument { item.Output.GenerateDocument = true }` blocks with `ResolveUnset(item.Output.GenerateDocument, ToggleTrue)`. Resolution chain: caller explicit `ToggleFalse` > everything else (it survives all layers); caller explicit unset OR preset > config > safety default `ToggleTrue`.
- `internal/application/scripts/usecase/generation_plan_builder.go::buildPostprocessorList` (lines 154-167): replace `if out.GenerateVoiceover` with `if out.GenerateVoiceover == scriptpkg.ToggleTrue || out.GenerateVoiceover == scriptpkg.ToggleUnset && (caller-explicit indicator)`. Ergonomic helper: `isTruthy(out.SaveToDB)` returns true for `ToggleTrue` OR `ToggleUnset`.
- HTTP request unmarshalers in `internal/api/script/`: convert JSON `true`/`false`/absent → `ToggleTrue`/`ToggleFalse`/`ToggleUnset`.

**godlike/07 minimum-blast-radius:** ~120 LoC; every bool field becomes a `Toggle` enum value with backward-compat JSON wire shape (`true`/`false`/`omitempty`).

**Anti-regression test (new file):** `internal/application/scripts/adapters/toggle_resolver_test.go` (~80 LoC, 6 sub-cases):
1. `ToggleUnset + safety → ToggleTrue` (caller didn't say, system chose).
2. `ToggleFalse survives all layers` (caller said NO; preserved as NO).
3. `ToggleTrue stays true` (caller said YES; preserved).
4. `ToggleUnset + preset → preset value`.
5. `ToggleUnset + preset + config → preset wins over config` (canonical precedence chain).
6. `ToggleFalse + preset → ToggleFalse wins` (caller > preset).

**Compatibility:** migration via JSON `true`/`false`/absent preserves wire compatibility for legacy clients.

---

#### PR-4 — `PR-SCRIPT-DECOUPLE-EXECUTION-ORDER-OWNER`
**Goal:** Make `buildPostprocessorList` the **single owner** of execution order; eliminate the header-godoc/code-runtime divergence in `wire_script_postprocess.go`.

**Surface (2 files):** `internal/application/scripts/usecase/generation_plan_builder.go::buildPostprocessorList` (rewrite to iterate `adapters.CanonicalProcessorNames()` as the BACKBONE + apply per-flag `if` branches to determine membership) + the goddoc-replacing comment block in `internal/app/wire_script_postprocess.go::registerScriptPostProcessors` godoc (lines 80-87).

**Canonical ordering (matches `CanonicalProcessorNames()` exactly):**
```
entities → clip_search → metadata → translation → clip_bindings →
stock_association → voiceover → images → document → persistence
```

**`buildPostprocessorList` (canonical shape):**
```go
// Pattern: iterate the canonical CLOSED SET; for each, decide
// membership based on plan.OutputSpec + plan.SourceSpec.
//
// CRITICAL: order is determined by CanonicalProcessorNames().
// The `// Some conditional comments` only decide MEMBERSHIP,
// never ORDER. Adding a new processor = adding to both
// CanonicalProcessorNames() AND the conditional block here.

members := make(map[adapters.ProcessorName]bool)
if out.ExtractEntities == ToggleTrue || out.ExtractEntities == ToggleUnset {
  members[adapters.ProcessorEntities] = true
  members[adapters.ProcessorClipSearch] = true  // ordered dependency
}
if out.GenerateMetadata == ToggleTrue || out.GenerateMetadata == ToggleUnset {
  members[adapters.ProcessorMetadata] = true
}
if len(out.Languages) > 0 {
  members[adapters.ProcessorTranslation] = true  // PR-5 enables
}
members[adapters.ProcessorClipBindings] = true  // unconditional
members[adapters.ProcessorStockAssociation] = true  // unconditional
if out.GenerateVoiceover == ToggleTrue || out.GenerateVoiceover == ToggleUnset {
  members[adapters.ProcessorVoiceover] = true
}
if out.GenerateSceneImages == ToggleTrue || out.GenerateSceneImages == ToggleUnset {
  members[adapters.ProcessorImages] = true
}
if out.GenerateDocument == ToggleTrue || out.GenerateDocument == ToggleUnset {
  members[adapters.ProcessorDocument] = true
}
if out.SaveToDB == ToggleTrue || out.SaveToDB == ToggleUnset {
  members[adapters.ProcessorPersistence] = true
}

// CRITICAL: stable sort by CanonicalProcessorNames() position.
sorted := make([]adapters.ProcessorName, 0)
for _, name := range adapters.CanonicalProcessorNames() {
  if members[name] { sorted = append(sorted, name) }
}
return sorted
```

**Anti-regression test:** `internal/application/scripts/usecase/generation_plan_builder_test.go`: add 4 sub-cases `TestBuildPostprocessorList_OrderMatchesCanonicalProcessorNames` that generates an `OutputSpec` with every flag `ToggleTrue` and asserts `buildPostprocessorList` returns the same order as `CanonicalProcessorNames()`.

**godlike/07 minimum-blast-radius:** rewrites one function (~50 LoC). Adds 4 test sub-cases (~60 LoC).

---

### Phase B (deadline 2026-07-22) — P0 Wiring

#### PR-5 — `PR-SCRIPT-DECOUPLE-TRANSLATION-IN-PLAN`
**Goal:** `buildPostprocessorList` conditionally appends `ProcessorTranslation` when `len(out.Languages) > 0`.

**Surface (1 file, +3 LoC):** `internal/application/scripts/usecase/generation_plan_builder.go::buildPostprocessorList` (the membership decision block).

**Change:** insert the 3-line conditional from PR-4 above. Membership gate = `len(out.Languages) > 0`. Slot placement follows `CanonicalProcessorNames()` position 4 (between metadata and clip_bindings).

**Anti-regression test:** `internal/application/scripts/usecase/generation_plan_builder_test.go::TestBuildPostprocessorList_AddsTranslationWhenLanguagesPresent` (locks the rule with `[]string{"it", "en"}` and asserts "translation" present at position 4 of the returned slice).

---

#### PR-6 — `PR-SCRIPT-DECOUPLE-TRANSLATION-PROPAGATION-FIX`
**Goal:** Repair the false-propagation bug. `TranslationProcessor.Process` mutates a value-passed struct (local copy); the caller's `input` never sees the translation. Fix: add `TranslatedText` + `TranslatedSpecScene` fields to `PostProcessResult`; have `TranslationProcessor.Process` populate them; have `mergePostProcessResult` (the canonical merge site inside `Run`) RESPECTFULLY apply them to the next iteration's `input`.

**Surface (4 files):**
- `internal/application/scripts/adapters/postprocessor_document.go::PostProcessResult` (the struct definition, ~+8 LoC: `TranslatedText string` + `TranslatedSpecScene scriptpkg.SpecSceneOutput`).
- `internal/application/scripts/adapters/processor_translation.go::Process` (rewrite final return: `return &PostProcessResult{Changed: true, TranslatedText: translated.Text, TranslatedSpecScene: translated.SpecScene, Warnings: tWarnings}, nil`; REMOVE the buggy `input.SpecScene = ...; input.Text = ...` lines).
- `internal/application/scripts/adapters/postprocessor_composite_run.go::mergePostProcessResult` (or wherever `Run` orchestrates the loop — verify the exact file per postprocessor_unification wave split) (~+15 LoC: at the head of each loop iteration, IF `result.TranslatedText != "" || result.TranslatedSpecScene.Scenes != nil` → apply to `input.Text` + `input.SpecScene` BEFORE invoking the next processor).
- `internal/application/scripts/adapters/postprocessor_image.go` (verify if any merge counterpart needs an update; conservative: mirror the same fix if downstream processors observe `input.Text` reading instead of `input.Text`.

**godlike/07 minimum-blast-radius:** 1 new field per struct (additive), 1 rewired merge seam (named seam), removes 2 buggy lines. **Anti-regression test:**

- `processor_translation_test.go`: add `TestTranslationProcessor_PropagatesTranslatedTextAndSpecSceneViaResult` (asserts `result.TranslatedText == "translated text"` + `result.TranslatedSpecScene.Scenes != nil`).
- `postprocessor_composite_run_test.go`: add `TestMergePostProcessResult_AppliesTranslatedTextToNextInput` (calls `mergePostProcessResult(&input, &{TranslatedText: "T", TranslatedSpecScene: S})`; asserts `input.Text == "T"` + `input.SpecScene == S` BEFORE the next processor sees it).

This fix is **load-bearing for downstream voiceover/images/document in non-English source**: PR-6 enables them to observe translated SpecScene when `len(out.Languages) > 1` per PR-5's user-requested translation.

---

### Phase C (deadline 2026-07-29) — P1 Truth-recovery

#### PR-7 — `PR-SCRIPT-DECOUPLE-RENAME-SCENES`
**Goal:** Rename `PipelineResult.Scenes` → `PipelineResult.SceneImages` (Go-side developer clarity) while PRESERVING the JSON wire key `"scenes"`.

**Surface (3 files):**
- `internal/application/scripts/adapters/postprocessor_document.go` (2 field renames: `PipelineResult.Scenes` → `SceneImages`, `PostProcessResult.SceneImages` → keeps name; JSON tag stays `"scenes,omitempty"`).
- All callers (~30 sites per audit estimate): update Go references. Mechanical codemod via `sed` + `goimports`-equivalent.
- `internal/application/scripts/jobs/` (cross-package reader): update if any caller.

**godlike/07 minimum-blast-radius:** field rename + JSON tag preservation = NO wire-shape change for clients. **Anti-regression test:** `pipeline_result_json_test.go::TestPipelineResult_ScenesFieldJSONWireKeyIsStillScenes` (asserts JSON marshalling produces `"scenes"` key, NOT `"scene_images"`).

---

#### PR-8 — `PR-SCRIPT-DECOUPLE-ASSET-STATUS-ENUM`
**Goal:** Centralize binding status state. Empty URL never becomes "generated".

**Surface (3 files):**
- `internal/domain/script/types.go`: add `type AssetStatus string` const set (Generated/Failed/Skipped/Pending).
- `internal/application/scripts/adapters/processor_images.go::Process` (replace the partial-failure path that does `images = append(images, SceneImage{Index: i, Text: sceneText})` — empty URL — with `SceneImage{Index: i, Text: sceneText, Status: AssetStatusFailed}`).
- `internal/application/scripts/adapters/processor_image.go::mergePostProcessResult` (or wherever merge codepath runs): add the empty-URL guard — `if result.SceneImages[i].URL == "" { result.SceneImages[i].Status = AssetStatusFailed }`.

**Anti-regression test:** `processor_images_test.go::TestImageProcessor_PartialFailure_DoesNotMarkEmptyURLAsGenerated` (asserts 3-image run with 1 backend failure → all 3 SceneImages have Status ∈ {Generated (when URL non-empty), Failed (when URL empty)}; NEVER status="Generated"+URL="").

---

#### PR-9 — `PR-SCRIPT-DECOUPLE-OUTPUTFMT-MODEL-WIRE-SPLIT`
**Goal:** Split `OutputSpec.OutputFmt` ("json") into `ModelOutputMode` (canonical LLM contract = plain prose) + `WireOutputMode` (canonical API envelope = JSON we build locally). Eliminates the applySafetyDefaults json-forcing.

**Surface (4 files):**
- `internal/domain/script/types.go`: add `ModelOutputMode string + WireOutputMode string` to `OutputSpec`. Default `ModelOutputMode=OutputModePlainText` (the canonical LLM contract per LLM-PLAIN-TEXT-CONTRACT, see `architecture/waves/wave_p3_low_and_audit.yaml`). Default `WireOutputMode=OutputModeJSON` (canonical envelope).
- `internal/application/scripts/adapters/generation_normalizer.go::applySafetyDefaults` (lines 240-255): REMOVE the `item.Output.OutputFmt = "json"` block — `ModelOutputMode` defaults handle it.
- `internal/infrastructure/ai/ollama/types/types.go::OutputMode`: deprecation comment — `OutputModeScriptV1` is the legacy "ask model for JSON" path; `OutputModePlainText` is the canonical new path (per LLM-PLAIN-TEXT-CONTRACT wave).
- `internal/application/scripts/usecase/engine_generate.go`: ensure the request body sets `format: modelOutputMode` (with `OutputModePlainText` → no `format` key).

**Anti-regression test:** `output_spec_split_test.go` (~120 LoC, 6 sub-cases):
1. Default constructor → `ModelOutputMode=plain, WireOutputMode=json`.
2. Caller explicit `ModelOutputMode=plain` survives normalization.
3. Caller explicit `ModelOutputMode=json` is rejected with `ErrModelOutputModeRejected` (legacy migration path is forward-closed).
4. Wire layer marshalling produces canonical JSON envelope regardless of model output mode.
5. End-to-end: plain prose model output → usecase parses → JSON envelope has all 4 canonical fields (text, specscene, metadata, entities).
6. `output_fmt` wire field is DEPRECATED but still accepted in legacy requests (backward compat for 1 wave).

---

### Phase D (deadline 2026-08-08) — P2 Cleanup

#### PR-10 — `PR-SCRIPT-DECOUPLE-PROCESSOR-CAPABILITY-REGISTRY`
**Goal:** Single capability registry replaces the current two preflight surfaces (HTTP `postprocessor_preflight.go:35-80` + Registry runtime preflight at `postprocessor_composite.go:148-185`).

**Surface (3 files, +~150 LoC):**
- NEW `internal/application/scripts/capabilities/registry.go`: `ProcessorCapabilityRegistry` struct with fields `Name ProcessorName`, `Policy ProcessorPolicy`, `RequiresCompositionCaps []Capability` (e.g. `VoiceoverService`, `ImageService`, `DocClient`, `ScriptRepository`, `EntityExtractor`, `MetadataGenerator`, `TranslationBackend`), `IsInline bool`, `IsDownstream bool` (future flag), `EnabledByOutputFlag string` (the spec field that opts it in).
- NEW `internal/application/scripts/capabilities/registry_test.go`: 8 sub-cases (one per canonical processor) pin the canonical capability row + 1 regression test (`TestCapabilityRegistry_CoversAllCanonicalProcessorNames`).
- Migrate `internal/api/script/postprocessor_preflight.go` to consult `capabilities.ProcessorCapabilityRegistry` instead of hardcoded `voiceover/images/document` checks.
- Migrate `internal/application/scripts/adapters/postprocessor_composite.go::ValidateRequested` to consult the same registry.

**godlike/07 minimum-blast-radius:** the registry is NEW; existing code becomes a thin reader. Each migration is `lookup(registry, name)` instead of `if/else`. ~50 LoC savings net across migrated files.

---

#### PR-11 — `PR-SCRIPT-DECOUPLE-SOURCE-RESOLVER-VALIDATOR`
**Goal:** Composition-time `ValidateSourceResolvers(sourceReg, enabledFeatures)` confirms every enabled-source-route has its resolver wired. Service refuses to boot otherwise.

**Surface (2 files):**
- `internal/application/scripts/source/validator.go::ValidateSourceResolvers` (NEW, ~40 LoC): reads `enabledFeatures` map (e.g. `CatalogFeature=true, SearchFeature=true, CurateFeature=true, ClipSourceBuilderFeature=true`) and asserts the corresponding `sourceReg.Has(adapter.SourceCatalog/SourceSearch/SourceCurate/SourceClips)` returns true; returns `ErrSourceResolverMissing` typed sentinel otherwise.
- `internal/app/wire_script_resolvers.go` (call site): invoke `ValidateSourceResolvers` after `sourceRegistry.Register()` calls; on error, abort composition boot with the typed sentinel.

**Anti-regression test:** `source_validator_test.go::TestValidateSourceResolvers_EnabledFeatureMissingResolver_ReturnsErrSourceResolverMissing` (3 sub-cases: all-enabled-happy, one-missing-typed-error, nil-registry-typed-error).

---

#### PR-12 — `PR-SCRIPT-DECOUPLE-ASSET-SEARCH-PORT-CUTOVER`
**Goal:** Complete the unification of `ClipSearchPort` + `StockSearchPort` into `AssetSearchPort`. Retire the legacy wrappers (already unified internally per `PR-POSTPROCESSOR-UNIFICATION-PHASE-3` PR-RESOLVER-PORT-EXTRACT; just need to flip the public API).

**Surface (2 files):**
- `internal/application/scripts/ports/asset_search_port.go::ClipSearchPort`: replace with `type ClipSearchPort = AssetSearchPort` (Go type alias — pure back-compat, zero runtime change).
- `internal/application/scripts/ports/asset_search_port.go::StockSearchPort`: same.

Three advantages:
1. Caller code that still imports `ClipSearchPort` keeps compiling (alias preserves byte-identical signatures).
2. Call sites can opt-in to `AssetSearchPort` incrementally (zero migration pressure).
3. Future PR-CLIPS-STOCK-PORT-RETIRE removes the aliases after a sustained 7-day zero call-site observation.

**godlike/07 minimum-blast-radius:** 2 type aliases = ~4 LoC. **Anti-regression test:** `asset_search_port_aliases_test.go::TestClipSearchPort_AliasIsAssetSearchPort + TestStockSearchPort_AliasIsAssetSearchPort` (compile-time `var _` pin via reflection on the type name; locks the alias survives).

---

## §3 Per-PR execution checklist (godlike/07)

For EACH PR (§2):
1. `gofmt -l internal/application/scripts/... internal/app/...` → exit 0.
2. `go vet ./internal/application/scripts/... ./internal/app/...` → exit 0.
3. `go build ./...` (full project) → exit 0.
4. `go test -short -count=1 -run 'Test*PR_number*' ./internal/application/scripts/...` → PASS.
5. `bash scripts/ci-architectural-checks.sh --self-check` → exit 0 (baseline preserved; no NEW violations).
6. Direct-to-main per Git-Lesson-2 + `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>` trailer per Git-Lesson-3.
7. Race-protect pre-push: `git fetch origin && git log --oneline HEAD..@{u}` empty.

---

## §4 Verification gates (godlike/06/07)

### Pre-PR-1 verification (the day this plan lands)
- `bash scripts/ci-architectural-checks.sh --self-check` → exit 0 (baseline established).
- `gofmt -l` → clean (baseline).
- `go vet ./internal/application/scripts/... ./internal/app/...` → exit 0 (baseline).
- `rg '^Processor[A-Z]' internal/application/scripts/adapters/processor_names.go` → 9 matches (closed set).
- `rg '^var [A-Z]\w+ = ' internal/application/scripts/usecase/generation_plan_builder.go` → confirms `buildPostprocessorList` is the SOLE conditional postprocessor appender (NOT `merge-elsewhere`).

### Post-per-PR verification
- Targeted TDD: `go test -short -count=1 -run 'TestPR_<n>_' ./internal/application/scripts/... ./internal/app/...` → PASS.
- Full project: `go test -short ./...` PASS (every PR lands in isolation; pre-existing carry-forward failures reproduce unchanged per AGENTS.md "Pre-existing build issues" convention).

---

## §5 Pre-regression anti-test surface (godlike/07 forward-collection)

Per the audit's 9-test surface, here are the canonical anti-regression tests this plan produces (each is the spec for ONE PR's test file):

| Audit test name | Plan PR | File |
|-----------------|---------|------|
| `TestBuildPostprocessorList_DoesNotAddDeprecatedInlineArtifacts` | PR-1 (goddoc) + PR-4 (membership semantics) | `generation_plan_builder_test.go` |
| `TestBuildPostprocessorList_AddsTranslationWhenLanguagesPresent` | PR-5 | `generation_plan_builder_test.go` |
| `TestDefaultPolicy_CoversAllCanonicalProcessorNames` | PR-2 | `postprocessor_composite_test.go` (NEW) |
| `TestTranslationProcessor_PropagatesTranslatedSpecScene` | PR-6 | `processor_translation_test.go` |
| `TestExecutionOrder_PersistenceBeforeDriveSideEffects` | (PR-SCRIPTCONTRACT-2026-07-08 PR-1 — already shipped) | (reuse) |
| `TestOutputSpec_ExplicitFalseNotOverriddenBySafetyDefault` | PR-3 | `toggle_resolver_test.go` (NEW) |
| `TestImageMerge_DoesNotMarkEmptyURLAsGenerated` | PR-8 | `processor_images_test.go` |
| `TestPipelineResult_NoAmbiguousScenesField` | PR-7 | `pipeline_result_json_test.go` (NEW) |
| `TestSourceRegistry_EnabledSourceTypesHaveResolvers` | PR-11 | `source_validator_test.go` (NEW) |

---

## §6 Honest scope-lock (godlike/07)

**Carry forward unchanged** (per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YUILD-ISSUES-2026-07-04`, the canonical 6-item list):
- `monitor/enqueue.go::tolower` drift (OUT OF SCOPE)
- `monitor/scheduler.go::ENQUEUER` typed-port resolution (OUT OF SCOPE)
- `stockpipeline/run_upload.go::Orchestrator` redeclaration (OUT OF SCOPE)
- `app/module_media.go::MODULES` literal slice (OUT OF SCOPE)
- `images/routing` package import cycle (OUT OF SCOPE)
- `app/wire_script.go` (registry scripts entry) syntax drift (july 2026 RETIRED per `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` carry-forward)

`architecture/waves/wave_p1_high.yaml` parse error (L5557+ per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward; forward-pointer `PR-CURRENT-YAML-PARSE-FIX-PART-N`, deadline 2026-08-15). **This plan's wave-tracker slot is DEFERRED** — the canonical SOLE closure record this commit ships is the action plan + CHANGELOG + AGENTS mirror per the SCRIPTCONTRACT-2026-07-08 precedent.

**Do NOT touch** (orthogonal):
- `VO-DECOMPOSITION-2026-07-04` (voiceover subsystem hardening — 4 forward-pointers pending).
- `DRIVE-AS-CENTRAL-CAPABILITY-2026-07-07` (FASE A→E closure — `delivery.Publisher` canonical).
- `QDRANT-CHAIN-VERIFY-2026-07-04` (12-gate Qdrant DoD finale).
- `GODOBJ-2026-07-03` (12-file godobject kill list).

**Out of scope for non-immediate follow-ups:**
- PR-12's alias RETIRE (forward-pointer `PR-CLIPS-STOCK-PORT-RETIRE-2026-08-22`, GATED on a 7-day zero call-site observation).
- Postprocessor orchestration extraction (godlike/07 GodObject decomposition deadline 2026-08-15, NOT in scope).
- Migration of `applySafetyDefaults` to typed resolver surface (forward-pointer `PR-APPLY-SAFETY-TYPED-2026-08-22`).

---

## §7 Cross-references (godlike/06 SSOT umbrella)

| Surface | Reference |
|---------|-----------|
| Existing pipeline contract | `architecture/action-plans/2026-07-08-script-pipeline-contract.md` (4-PR wave; complements, not supersedes) |
| Postprocessor unification | `architecture/action-plans/2026-07-08-postprocessor-unification.md` (Phase 1-4 wave; PR-6 here extends Phase 2 Pattern) |
| LLM plain-text contract | `architecture/waves/wave_p3_low_and_audit.yaml#LLM-PLAIN-TEXT-CONTRACT` (PR-9 here aligns the new OutputFmt with the LLM contract) |
| Voiceover propagation | `architecture/waves/wave_p1_high.yaml#VO-DECOMPOSITION-2026-07-04` (uses Phase C PR-7+PR-8 surfaces for canonical status verbs) |
| Drive canonical | `architecture/waves/wave_p1_high.yaml#DRIVE-AS-CENTRAL-CAPABILITY-2026-07-07` (PR-4 here does NOT touch Publisher — orthogonal) |
| Code-quality cleanup | `architecture/waves/wave_p1_high.yaml#CODE-QUALITY-CLEANUP-2026-07-04` (12 dirty areas band; PR-7 PR-8 here fix 2 of the 12) |
| Refactor wave-3 | `architecture/action-plans/2026-08-08-refactor-checklist-action-plan.md` (5-PR wave; PR-12 here aligns with PR-ASSETS-SEARCH-PORT-RETIRE forward-pointer) |

---

## §8 Wave-flip criterion (godlike/06/07)

The wave flips to `status: shipped + exit_signal: true` ONLY WHEN:
1. All 12 PRs reach `status: shipped` (single canonical surfacing per PR via git-log `--grep=PR-SCRIPT-DECOUPLE-`).
2. `bash scripts/ci-architectural-checks.sh` exits 0 with `Check 64` (already shipped) AND the future `Check 67` (forward-pointer `PR-CHECK-67-CAPABILITY-REGISTRY`, to ship alongside PR-10) active.
3. `go test -short ./...` exits 0 (full project pre-existing carry-forward failures reproduce unchanged per AGENTS.md "Pre-existing build issues" convention — NOT regressions of any per-PR closure).

---

## §9 Lifecycle audit-trail + Co-authored-by

| Stamp | Action | Actor |
|-------|--------|-------|
| 2026-07-09 | Marcuss-ops 12-issue Italian audit pasted to orchestrator | Marcuss-ops |
| 2026-07-09 | Action plan authored (this file) | PipelineGen Agent |
| 2026-07-09 | Action plan committed + pushed to origin/main (direct-to-main per AGENTS.md Git-Lesson-2) | PipelineGen Agent |
| 2026-07-09 (Phase A) | PR-1 → PR-2 → PR-3 → PR-4 land on main (4 atomic commits) | PipelineGen Agent |
| 2026-07-22 (Phase B) | PR-5 → PR-6 land on main (2 atomic commits) | PipelineGen Agent |
| 2026-07-29 (Phase C) | PR-7 → PR-8 → PR-9 land on main (3 atomic commits) | PipelineGen Agent |
| 2026-08-08 (Phase D) | PR-10 → PR-11 → PR-12 land on main (3 atomic commits) | PipelineGen Agent |

---

## Co-authored-by trailer (mandatory, per AGENTS.md Git-Lesson-3)

```
Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>
```

---

## §10 Honest cross-walk vs pre-existing waves (godlike/07 transparency)

This plan is **complementary**, not a substitute for any pre-existing wave:

| Pre-existing wave | What this plan adds vs it |
|--------------------|--------------------------|
| SCRIPTCONTRACT-2026-07-08 (PR-1 reorder, PR-2 hard preflight, PR-3 CI gate, PR-4 TDD) | This plan extends preflight surface (PR-10) + adds Translation propagation (PR-6) + adds truth-recovery (PR-7-9) |
| Postprocessor-unification (Phase 1-4) | This plan adds new fields (TranslatedText/TranslatedSpecScene) that PR-6 Phase 2 left forward-pointed |
| LLM-PLAIN-TEXT-CONTRACT | PR-9 here aligns `ModelOutputMode=plain` as the canonical default (LLM-PLAIN-TEXT-CONTRACT already declares the contract; PR-9 fixes the wire field naming) |
| Drive-as-Central-Capability | PR-4 here does NOT touch `delivery.Publisher` — orthogonal persistence/registry focus |
| Code-quality-cleanup (12 dirty areas) | PR-7 here renames `Scenes` → `SceneImages` (resolves "PipelineResult.Scenes in realtà sono immagini" audit item #9) and PR-8 here centralizes AssetStatus (resolves "Image binding può diventare falso 'generated'" audit item #8) |

---

## §11 Forward-pointers (godlike/07 honest scope-lock)

- `PR-SCRIPT-DECOUPLE-CAPABILITY-REGISTRY-CI-GATE-CHECK-67` (deadline 2026-08-15): forward-prevention archcheck gate that fails CI when any non-canonical `ProcessorCapability` struct field appears (similar to `Check 64` precedent).
- `PR-SCRIPT-DECOUPLE-TRISTATE-MIGRATION-GUIDE` (deadline 2026-08-22): user-facing migration guide for downstream API consumers that send explicit `false` values to `Toggle` fields.
- `PR-SCRIPT-DECOUPLE-DRIVE-EVENT-OBSERVABILITY` (deadline 2026-09-01): Prometheus counter `script_translation_total{language, success}` + `script_translation_propagation_total{skipped, reason}` — observability surface for PR-6 propagation metrics.
- `PR-SCRIPT-DECOUPLE-OUTPUTFMT-WIRE-PROM-COMPAT` (deadline 2026-09-15): backward-compat shim that accepts legacy `output_format="json"` requests while emitting the new response shape (1 wave sunset window).
- `PR-CLIPS-STOCK-PORT-RETIRE-2026-08-22` (gated on a 7-day zero call-site observation per PR-12 alias surface): physical removal of the type aliases once caller migration is observation-verified zero.
