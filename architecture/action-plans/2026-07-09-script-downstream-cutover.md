# SCRIPT-DOWNSTREAM-CUTOVER-2026-07-09 — FASE-2 Downstream cutover wave (deferred Option 2 from SCRIPT-PIPELINE-DECOUPLING)

> **Canonical surface (godlike/06 SSOT lockstep, per CANONICAL.md §1)**:
> - Surface 1/4 (canonical narrative): `architecture/action-plans/2026-07-09-script-downstream-cutover.md` (this file)
> - Surface 2/4 (closure meta-entry): `CHANGELOG.md ## Unreleased > ### Documentation`
> - Surface 3/4 (audit-pin mirror): `AGENTS.md ## Recent cross-cutting closures`
> - Surface 4/4 (wave-tracker slot): **DEFERRED** per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer (deadline 2026-08-15). The canonical SOLE closure record this commit ships is surfaces 1/2/3 per the SCRIPTCONTRACT-2026-07-08 + SCRIPT-PIPELINE-DECOUPLING-2026-07-09 audit-pin precedents.

---

## §0 Honest scope-lock (godlike/07 NO-FAKE-AVAILABILITY)

This wave cuts **three** capabilities (Voiceover / Scene Images / Document) from the inline postprocessor path into separate async sibling jobs. The deferred Option 2 from the 2026-07-09 audit was: keep the 5 capabilities inline for Option 1 (1-wave cutover), cut to async for Option 2 (4-phase cutover). This action plan authorises Option 2.

The canonical typed envelope surfaces (`internal/domain/script/downstream.go`) — `DownstreamRequest` + `AssetRequirements` + `VoiceoverRequirements` + `ImagesRequirements` + `DownstreamKind` enum + `OutputDestination` + `ManifestV2{NoInlineAssets bool, Items []DownstreamRequest}` + `NewManifestV2()` ctor — are already live on `origin/main` per the Step 11A canonical-types commit. The composition root currently registers DocumentProcessor/ImageProcessor/VoiceoverProcessor as inline postprocessors (`internal/app/wire_script_postprocess.go`). The async job emission infra (`SceneImageJobEmitter` emitting `job.TypeImagesGenerate` children + `Dispatcher.Enqueue` via Pattern 9 + `VoiceoverFanoutVoiceoversUseCase` for voiceover parent-child fanout) is already live.

| IN scope | OUT of scope |
|---|---|
| Voiceover sibling dispatch from `ManifestV2` | Entities / Metadata / Translation / ClipBindings / StockAssociation (stay inline) |
| Scene-Images sibling dispatch from `ManifestV2` | `media_assets`/`asset_versions` writes (asset_finalizer_tx stays intact) |
| Document sibling dispatch from `ManifestV2` | Asset index/outbox wiring (forward-pointer `PR-DOWNSTREAM-OUTBOX-INDEX`) |
| Parent-blocks-until-children-resolve semantic on `Required=true` children | Operator sunset CLI for legacy inline compose (forward-pointer `PR-CLI-INLINE-SUNSET`) |
| Feature-flag-gated cutover (`cfg.Features.Downstream{Voiceover,Images,Document}`) per capability | CD-time config validator (forward-pointer `PR-CONFIG-CD-VALIDATOR`) |

**Verdict**: this is the FASE-2 cutover Option 2 from the audit. The canonical typed contracts are already in place; the cutover needs only the dispatcher wiring + parent-block semantic + per-capability feature flag + the legacy 410-Gone for unmigrated callers.

---

## §1 Goal (godlike/06/07 alignment)

Move the **three heaviest** postprocessors (Voiceover + Images + Document) out of the synchronous `script.generate.Run` path and into **async sibling jobs** triggered after `script.generate` completes the persistence step. The parent `script.generate` job **blocks until all `Required=true` children resolve** (godlike/07 NO-FAKE-AVAILABILITY — no silent parent-SUCCEEDED while a required sibling is still RUNNING or FAILED).

The wave flips `status: shipped + exit_signal: true` ONLY WHEN:

1. All 5 PRs reach `status: shipped` on `origin/main` (per git-log `--grep=PR-SCRIPT-DOWNSTREAM-`).
2. The per-capability `cfg.Features.Downstream{Voiceover,Images,Document}` flags can be flipped to `true` without any operator-visible behavior change for `script.generate` callers (the new path produces the same wire shape).
3. `bash scripts/ci-architectural-checks.sh --self-check` exits 0 (canonical 18-assertion pipeline smoke does not regress).
4. `go test -short ./...` exits 0 with the pre-existing 6-item voiceover + app build-issue carry-forward per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` UNCHANGED (NOT regressions of this wave).

---

## §2 Migration sequence (godlike/07 EXPAND → BACKFILL → CUTOVER → CONTRACT)

5 PRs in 4 phases. Each lands **directly on `main`** per AGENTS.md Git-Lesson-2 (no branches, no `--no-ff`, no `--force`). Per-capability phasing is mandatory; combining the 3 capabilities into one atomic cutover creates a god-PR that the per-capability wave boundaries can never cleanly pin against.

### Phase A (deadline 2026-07-22) — NEW-mode write seam

#### PR-1 — `PR-SCRIPT-DOWNSTREAM-WRITE-SEAM` (Phase A, foundation)
**Goal:** the `script.generate` handler WRITES a `ManifestV2{NoInlineAssets: true, Items: []DownstreamRequest}` envelope to disk as part of the persistence step. The envelope is ignored by the postprocessor walk (handlers still run inline). The seam is **additive**: no production behavior change; the canonical SOLE new surface for the downstream dispatcher.

**Surface (2 files, ~+80 LoC):**
- `internal/application/scripts/usecase/generation_plan_builder.go::buildPlanResult`: add `manifestV2 := scriptpkg.NewManifestV2()` (always, no caller opt-out) + populate `manifestV2.Items` from the 3 capability flags (`out.GenerateVoiceover/GenerateSceneImages/GenerateDocument`) using the `NewDownstreamRequest{Voiceover,Images,Both}` helpers when the corresponding processor is in the plan. Wire-shape: each DownstreamRequest.ItemRef = `item.ID` (canonical per-item identifier); each Required = `out.{}` corresponding Toggle-as-bool (caller-explicit-true OR caller-explicit-false ignored — see godlike/07 precedence chain: caller-ToggleFalse beats the safety default of ToggleTrue so Required=false for caller-disabled siblings).
- `internal/application/scripts/jobs/generation_handler.go::HandleJob`: at the END of the persistence step (BEFORE the post-processor walk), write the populated `manifestV2` JSON to a new `scripts/{scriptID}/manifest_v2.json` row in the SQLite scripts table via `scriptsRepo.SaveManifestV2(ctx, scriptID, manifestV2)`.

**godlike/06 SSOT one-canonical-owner-per-fact:** `ManifestV2` lives ONLY at `internal/domain/script/downstream.go`; `NewManifestV2()` is its canonical constructor; `SaveManifestV2` (repository method) lives ONLY at `internal/infrastructure/database/sqlite/scripts/repository.go`; the 3 `NewDownstreamRequest{Voiceover,Images,Both}` helpers in `downstream.go` are the canonical per-capability envelope constructors.

**godlike/07 minimum-blast-radius:** zero behavior change for callers — the inline postprocessors still run; the manifest_v2.json is an **observation artifact** only (no consumer yet). Operator dashboards can verify the write seam by inspecting the manifest_v2.json file in any script.generate output dir.

**Anti-regression test (NEW file):** `internal/application/scripts/usecase/downstream_manifest_emit_test.go` (~150 LoC):
- `TestBuildPlanResult_PopulatesManifestV2VoiceoverImagesDocument`: 3-capability request (Voiceover + Images + Document, English single locale) asserts `manifestV2.NoInlineAssets == true` + `len(manifestV2.Items) == 3` + per-kind DownstreamKind matches expected + ItemRef == item.ID + Required == true (caller explicit toggle).
- `TestBuildPlanResult_OmitsCapabilitiesNotInPlan`: 1-capability request (Voiceover only) asserts `len(manifestV2.Items) == 1` + the Voiceover DownstreamRequest present + Images/Document absent.
- `TestBuildPlanResult_CallerDisabledSetsRequiredFalse`: caller sends `GenerateVoiceover=false` (ToggleDisabled), asserts `manifestV2.Items[0].Required == false`.
- `TestBuildPlanResult_PreservesOutputDestination`: per-item folder_id from caller request propagates to the corresponding DownstreamRequest.OutputDest.FolderID.

**Required migration (MINOR):** `scripts_repository.go::SaveManifestV2` (NEW ~30 LoC, +5 lines to the migration 100 `scripts` table columns). Migration is gated on user spec literal "+1 TEXT column" (acceptable PR-1 migration; not in user-explicit-out-of-scope).

---

### Phase B (deadline 2026-08-01) — Per-capability dispatcher fan-out

Per-capability phasing: IMAGE first (canonical infra already exists), then VOICEOVER, then DOCUMENT.

#### PR-2 — `PR-SCRIPT-DOWNSTREAM-IMAGE-DISPATCHER` (Phase B, item 1)

**Goal:** the `script.generate` handler reads the `manifest_v2.json` written in PR-1, iterates `manifestV2.Items` with `Kind == DownstreamVoiceover/DownstreamImages/DownstreamBoth`, and for each Image-capable item emits a `job.TypeImagesGenerate` child via `SceneImageJobEmitter.EmitSceneImageJob(ctx, EmitSceneImageCommand{...})`. The child ActiveKey is `scriptdownstream:<parentScriptID>:<itemRef>:images` (per the canonical idempotency key derivation rule).

**Surface (3 files, ~+150 LoC):**
- `internal/application/scripts/downstream/dispatcher.go` (NEW file): `Dispatcher` struct + `EmitChildren(ctx, manifestV2, parentJobID, scriptID) (jobs map[string]string /*kind → jobID*/, error)` helper. Iterates Items + emits one child per Images-capable kind.
- `internal/application/scripts/downstream/dispatcher_test.go` (NEW file): 4 hermetic TDD tests pinning the canonical contract:
  - `TestDispatcher_ImagesOnly_EmitsOneChild`: manifest_v2.json with 1 Images item → returns map{"images": "<childJobID>"}; the `Emitter.EmitSceneImageJob(ctx, cmd)` mock is called EXACTLY 1x with the typed command fields (Prompt from item.SpecScene + count from `ImagesRequirements.Count` ×1).
  - `TestDispatcher_VoiceoverOnly_NoImagesEmitted`: Voiceover-only manifest → returns empty map + `Emitter.EmitSceneImageJob` NEVER called.
  - `TestDispatcher_BothKinds_EmitsOneImage`: DownstreamBoth item → returns map{"voiceover": <jobID>, "images": <childJobID>}.
  - `TestDispatcher_ActiveKeyDeterministic`: 2 invocations with the same manifestV2 + parentScriptID emit children with the **same** ActiveKey — verifies the `scriptdownstream:<parentScriptID>:<itemRef>:images` derivation rule byte-equivalent across the canonical SOLE Idempotency-key helper.
- `internal/application/scripts/jobs/generation_handler.go::HandleJob`: after the persistence step, synthesize the Dispatcher with the composition-injected `SceneImageJobEmitter` (already wired pre-wave), call `dispatcher.EmitChildren(ctx, manifestV2, parentJobID, scriptID)`, append the returned jobIDs to the parent `child_job_ids` array stored on the result map.

**godlike/06 SSOT:** `Dispatcher` is the canonical SOLE owner of the "emit one child per Image-capable downstream item" fact; `SceneImageJobEmitter.EmitSceneImageJob` is the canonical SOLE wired seam; the ActiveKey helper lives ONLY at `downstream/idempotency.go` (NEW sibling, ~25 LoC helper function `IdempotencyKey(parentScriptID, itemRef, kind string) string`).

**godlike/07 minimum-blast-radius:** the 3 inline processors still run (PR-1 didn't remove them; the canonical composition root hasn't changed yet). The emit-children loop is **purely additive** — non-required children emit alongside the inline path; the parent's result_map records both `child_job_ids` (new) AND the inline output (preserved). Required = false children emit warnings on emit failure (the canonical EmitSceneImageJob returns `(jobID, error)`); a caller-error path that explicitly opted into downstream can NEVER silently lose image requests.

**Anti-regression test (NEW file):** `internal/application/scripts/downstream/scene_orchestrator_downstream_test.go` (~280 LoC, 8 sub-tests):
- `TestSceneImageJobEmitter_DownstreamCli_EmitsPerPrompt`: each prompt in ImagesRequirements generates its own child.
- `TestSceneImageJobEmitter_DownstreamCli_DiscardsWhenNil`: nil Emitter → typed `ErrDownstreamEmitterNil` typed sentinel (godlike/07 fail-closed).
- `TestSceneImageJobEmitter_DownstreamCli_ActiveKeyStable`: idempotency key byte-stable across N retries for same (parent, item, kind).
- `TestSceneImageJobEmitter_DownstreamCli_PropagatesParentJobID`: cmd.ParentJobID == canonical script.generate parent job ID.
- `TestSceneImageJobEmitter_DownstreamCli_PropagatesScriptID`: cmd.ScriptID == canonical script.id.
- `TestSceneImageJobEmitter_DownstreamCli_ImagesPerItem`: DownstreamImages with ImagesRequirements.Count=3 produces 3 child jobs.
- `TestSceneImageJobEmitter_DownstreamCli_EmissionFailure_ReportsWarning`: emit err → typed sentinel `ErrDownstreamEmitFailure` + warning in result.
- `TestSceneImageJobEmitter_DownstreamCli_RaceRecovery`: when 2 emits collide on the same ActiveKey within the parent's retry window, the broker's FindActiveByKey returns the existing job ID (no duplicate emit).

---

#### PR-3 — `PR-SCRIPT-DOWNSTREAM-VOICEOVER-DISPATCHER` (Phase B, item 2)

**Goal:** like PR-2 but for the Voiceover downstream kind. Lifts the `FanoutVoiceoversUseCase` parent-child fanout pattern into the script-level Dispatcher. Emits `job.TypeScriptVoiceoverSibling` child jobs (new canonical typed entry — the existing `voiceover.generate` job is preserved for backward compat with `/api/media/voiceover/generate` invocations).

**Surface (3 files, ~+120 LoC):**
- `internal/domain/job/job.go`: add canonical `const TypeScriptVoiceoverSibling = "script.voiceover_sibling"` (typed one-canonical-literal-decl pattern; cross-references the existing `voiceover.generate_item` sibling surface).
- `internal/application/scripts/downstream/dispatcher.go` (extended from PR-2): add the Voiceover emit branch (mirrors the voiceover.fanout.go per-item loop). Projects `VoiceoverRequirements{Provider, VoiceID, Pace, StylePreset}` into the child payload envelope (`ScriptVoiceoverChildPayload{ParentJobID, ScriptID, ItemRef, Required, Provider, VoiceID, Pace, StylePreset, FolderID}`).
- `internal/application/voiceover/jobs/script_sibling_handler.go` (NEW file): the worker for `TypeScriptVoiceoverSibling` — runs the existing TTS flow (`voiceover.processInternalCommand`) + persists via `voiceover.Finalizer.Finalize` (the canonical FASE 4 surface).

**godlike/06 SSOT one-canonical-owner-per-fact:** `TypeScriptVoiceoverSibling` const is the canonical SOLE owner of the `"script.voiceover_sibling"` literal; the Dispatcher in `internal/application/scripts/downstream/dispatcher.go` is the canonical SOLE emit point; `script_sibling_handler.go` is the canonical SOLE worker for this jobType.

**godlike/07 minimum-blast-radius:** the existing `voiceover.generate` job handler at `internal/application/voiceover/jobs/generate_handler.go` is UNCHANGED (renders the same wire shape for `/api/media/voiceover/generate` callers); the new sibling flow is a parallel pathway that script.generate uses for the inline→async cutover. Operator dashboards can compare the two pathways side-by-side during the feature-flag window.

**Anti-regression test (NEW file):** `internal/application/scripts/downstream/voiceover_sibling_fanout_test.go` (~250 LoC, 6 sub-tests):
- `TestDispatcher_VoiceoverEmit_ActiveKeyStable`: idempotency key byte-stable across N retries.
- `TestDispatcher_VoiceoverEmit_ProjectsVoiceID`: `cmd.VoiceID == item.VoiceoverRequirements.VoiceID`.
- `TestDispatcher_VoiceoverEmit_ProjectsProvider`: `cmd.Provider == item.VoiceoverRequirements.Provider`.
- `TestDispatcher_VoiceoverEmit_ProjectsPaceStyle`: Pace + StylePreset propagation.
- `TestDispatcher_VoiceoverEmit_FolderIDPropagation`: cmd.FolderID == item.OutputDest.FolderID.
- `TestWorker_RunsTTS_FinalizesViaCanonical`: handler routes through voiceover.processInternalCommand + voiceover.Finalizer.Finalize (NOT direct calls; compile-time pin).

---

### Phase C (deadline 2026-08-08) — Document + feature flags

#### PR-4 — `PR-SCRIPT-DOWNSTREAM-DOCUMENT-DISPATCHER + CONFIG-GATES` (Phase C, item 3 + feature flags)

**Goal:** like PR-2/PR-3 but for the Document downstream kind. 1:1 sibling mapping (Document doesn't batch like Voiceover per language). Add the explicit `cfg.Features.Downstream{Voiceover,Images,Document}` feature flags so the composition root can opt in/out at deployment time. When `Downstream{Voiceover,Images,Document}` is `true` AND the composition caller hasn't explicitly opted out via `cfg.Features.InlineDownstream=true`, the canonical `wire_script_postprocess.go::registerScriptPostProcessors` SKIPS the inline Voiceover/Images/Document registration (the downstream Dispatcher emits siblings instead).

**Surface (5 files, ~+200 LoC):**
- `internal/domain/job/job.go`: add `const TypeScriptDocumentSibling = "script.document_sibling"` (canonical typed literal).
- `internal/application/scripts/downstream/dispatcher.go`: add the Document emit branch (mirrors Images branch shape but single child). Projects `OutputDestination{FolderID, DocumentTitle}` into the child payload envelope.
- `internal/application/scripts/documents/script_sibling_handler.go` (NEW file, ~+80 LoC): worker for `TypeScriptDocumentSibling` — calls the canonical `usecase.NewDocumentsService(root.Drive.DocClient, log, cfg.Drive.ScriptsGenFolder())` + the existing `DocumentProcessor`'s renderDoc logic (lifted to package-level helper so the worker doesn't import `adapters/document_processor.go`).
- `internal/platform/config/types_features.go`: add 3 new feature flags — `DownstreamVoiceover bool`, `DownstreamImages bool`, `DownstreamDocument bool`, `InlineDownstream bool` (default false; faithful-to-audit forward-pointer CL-002). YAML keys + env vars wired per the existing feature-flag pattern (`yaml:"downstream_voiceover" env:"VELOX_FEATURE_DOWNSTREAM_VOICEOVER"`, etc.).
- `internal/app/wire_script_postprocess.go::registerScriptPostProcessors`: each of the 3 inline registration blocks (Document, Image, Voiceover) is now gated on `!cfg.Features.Downstream{Voiceover,Images,Document}` AND `cfg.Features.InlineDownstream OR ...`. The composition-time validator at `wire_script_adapters.go::validateRequiredProcessors` is relaxed (the new downstream gateway means inline processors MAY be absent without failing composition).

**godlike/06 SSOT:** the feature-flag struct lives ONLY at `internal/platform/config/types_features.go`; the composition-time gate is the canonical SOLE decision surface.

**godlike/07 minimum-blast-radius:** legacy `cfg.Features.Downstream* = false` + `cfg.Features.InlineDownstream = true` (the canonical default) → behavior is byte-equivalent to PR-3 + earlier (inline processors still register). The canonical REQUIRED semantic for callers that didn't migrate: setting `DownstreamVoiceover=true` requires operator-side deploy of the 3 sibling worker handlers (PR-3 + PR-2 + PR-4 each ship a dedicated handler per capability). A canonical `boot-time validator` ensures all 3 handlers are registered when the first feature flag is true.

**Anti-regression test (NEW file):** `internal/application/scripts/downstream/document_sibling_emit_test.go` (~250 LoC, 6 sub-tests) — mirrors the voiceover_sibling_fanout_test.go shape but Document-specific:
- `TestDispatcher_DocumentEmit_ActiveKeyStable`: `scriptdownstream:<parentScriptID>:<itemRef>:document` byte-equivalent across N retries.
- `TestDispatcher_DocumentEmit_DocumentTitlePropagation`: cmd.DocumentTitle == item.OutputDest.DocumentTitle.
- `TestDispatcher_DocumentEmit_FolderIDPropagation`.
- `TestDispatcher_DocumentEmit_SingleChildPerItem` (Document doesn't fan out — 1 child per item).
- `TestWorker_RunsRenderDoc_FinalizesViaCanonical`.
- `TestConfigGate_InlineDownstreamFalse_DisablesInlineRegistration`: config flip disables the inline registration block (composition surface contract).

---

### Phase D (deadline 2026-08-15) — Contract cleanup

#### PR-5 — `PR-SCRIPT-DOWNSTREAM-CONTRACT-CLEANUP` (Phase D, final flip)

**Goal:** flip the canonical defaults — `cfg.Features.Downstream{Voiceover,Images,Document}` defaults to `true`; the legacy inline registration is fully unwired from the production composition root (a forward-pointer `FASE-2.1-DOWNSTREAM-FREEZE` analog to `FASE-2.1-VOICE-FREEZE` keeps the dead code behind a `// FROZEN-DOWNSTREAM-LEGACY:` marker so per-line git blame remains clean). The legacy `applySafetyDefaults=true` callers receive an HTTP `410-Gone` with a `LegacyDeprecationPayload` JSON envelope + `Deprecation` header pointing at the canonical `/api/script/generate-from-clips` route (mirrors the SCRIPTCONTRACT-2026-07-08 + PR-script-legacy-contract precedent).

**Surface (4 files, ~+150 LoC):**
- `internal/platform/config/types_features.go`: flip 3 default values to `true` (one-line change per flag).
- `internal/app/wire_script_postprocess.go`: REMOVE the 3 inline registration blocks entirely (the downstream Dispatcher from PR-2/PR-3/PR-4 is the canonical surface now).
- `internal/api/script/handler_legacy_inline_deprecation.go` (NEW file, ~+60 LoC): canonical `LegacyInlineDeprecationPayload` struct + `StatusGoneDeprecated = "deprecated_inline"` const + `applyGone(c)` helper + 410 response wrapper that mirrors the `handler_legacy_deprecation.go` (SCRIPTCONTRACT-2026-07-08) shape byte-for-byte.
- `internal/application/scripts/usecase/pipeline.rs` (or equivalent): the canonical composition-time gate at `enterPipeline()` reads `cfg.Features.InlineDownstream`. If true → 410-Gone per the legacy contract; if false → canonical NEW-mode pipeline (the 5-phase dispatcher fan-out + persistence).

**godlike/06 SSOT one-canonical-owner-per-fact:** the 410-Gone contract lives ONLY at `handler_legacy_inline_deprecation.go`; the LegacyDeprecationPayload struct lives ONLY at `handler_legacy_deprecation.go` (the SCRIPTCONTRACT precedent); the feature-flag defaults live ONLY at `types_features.go`; the composition root is the canonical SOLE wiring surface.

**godlike/07 NO-FAKE-AVAILABILITY:** a future operator who flips `cfg.Features.InlineDownstream=true` accidentally deploys BOTH code paths (the canonical godlike/07 fail-closed contract prevents this — `PR-CONFIG-CD-VALIDATOR` forward-pointer documents the canonical CD-time gate that asserts the two flags are never both true in production).

**godlike/07 minimum-blast-radius:** PR-5 is a one-line config default flip + 110 LoC of 410-Gone wiring + the canonical forward-pointer archival of the dead inline registration. The dead code is preserved (for git blame audit-ability + future sunset CLI) under the `// FROZEN-DOWNSTREAM-LEGACY:` marker — called from `wire_script_postprocess_legacy.go` (renamed archive file) which is compiled only when `cfg.Features.InlineDownstream=true` AND the OS-built tag `fase21_inline_archive_enabled=1` (a build-time gate that defaults to false in production).

**Anti-regression test (NEW file):** `internal/application/scripts/downstream/parent_aggregator_required_failure_test.go` + `internal/api/script/handler_legacy_inline_deprecation_test.go` (~300 LoC, 7 sub-tests):
- `TestParentAggregator_RequiredChildFailure_PropagatesFailed`: 3 children, 1 Required=true fails → parent = `Status=Failed` + the failure documented in result_map.
- `TestParentAggregator_NonRequiredChildFailure_EmitsWarning`: same shape, 1 Required=false fails → parent = `Status=SUCCEEDED` + warning in result_map.
- `TestParentAggregator_RequiredChildrenAllSucceed_ParentSucceeds`.
- `TestParentAggregator_BlocksUntilRequiredChildrenResolve`: parent state transitions "Persistence → ChildrenPending → Completed" with child ordering encoded.
- `TestHandler_LegacyInlineDeprecation_Returns410AndBody_PinContract`: HTTP POST to legacy route returns 410 + canonical body shape (mirrors `TestLegacyGenerateFromClips_Returns410AndBody_PinContract`).
- `TestHandler_LegacyInlineDeprecation_DeprecationHeader_Present`: `Deprecation` header set on every 410 response (mirrors the SCRIPTCONTRACT precedent).
- `TestHandler_LegacyInlineDeprecation_PayloadReferencesCanonicalRoute`: body points at `/api/script/generate-from-clips`.

---

## §3 Per-PR execution checklist (godlike/07)

For EACH PR (§2):
1. `gofmt -l internal/application/scripts/... internal/app/... internal/domain/job/... internal/platform/config/...` → exit 0.
2. `go vet ./internal/application/scripts/... ./internal/app/... ./internal/domain/job/...` → exit 0.
3. `go build ./cmd/server/ ./cmd/worker/` → exit 0.
4. `go test -short -count=1 -run 'TestPR_<n>_' ./internal/application/scripts/downstream/... ./internal/domain/script/...` → PASS.
5. `bash scripts/ci-architectural-checks.sh --self-check` → exit 0 (no NEW violations).
6. Direct-to-main per AGENTS.md Git-Lesson-2 + `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>` trailer per AGENTS.md Git-Lesson-3.
7. Race-protect pre-push per AGENTS.md Git-Lesson-4: `git fetch origin && git log --oneline HEAD..@{u}` empty.

---

## §4 Verification gates (godlike/06/07)

### Pre-PR-1 verification (the day this plan lands)
- `bash scripts/ci-architectural-checks.sh --self-check` → exit 0 (baseline).
- `python3 -c 'import yaml; yaml.safe_load(open("internal/domain/script/downstream.go"))` exits 0 (the canonical contract parses).
- `rg 'ManifestV2' internal/domain/script/` confirms the type is declared + the 3 helpers are present.
- `rg 'SceneImageJobEmitter' internal/application/scripts/scene_orchestrator.go` confirms the canonical image-emit infra already live.

### Post-per-PR verification
- Targeted TDD: `go test -short -count=1 -run 'TestPR_<n>_' ./internal/...` → PASS.
- Full project: `go test -short ./...` PASS (every PR lands in isolation; pre-existing carry-forward failures reproduce unchanged per AGENTS.md "Pre-existing build issues" convention).

### Post-wave verification (after PR-5 ships)
- `bash scripts/ci-architectural-checks.sh --self-check` exits 0 + Check 64 (postprocessor-order gate) still active + the NEW `Check 70` (forward-gate `PR-CHECK-70-DOWNSTREAM-CHILD-REGISTRATION`, ships with PR-5) reports zero hits across the codebase.
- The canonical 18-assertion pipeline smoke (per `tests/operational/script_e2e_pipeline_smoke.sh`) PASSes after operator flips `cfg.Features.DownstreamVoiceover=true & cfg.Features.DownstreamImages=true & cfg.Features.DownstreamDocument=true` simultaneously.

---

## §5 Anti-regression test surface (godlike/07 forward-collection)

Per the thinker's 6-test surface design, here are the canonical anti-regression tests this wave produces (each is the spec for ONE PR's test file):

| Audit test name | Plan PR | File |
|-----------------|---------|------|
| `TestBuildPlanResult_PopulatesManifestV2VoiceoverImagesDocument` (+ 3 sub-cases) | PR-1 | `downstream_manifest_emit_test.go` (NEW) |
| `TestSceneImageJobEmitter_DownstreamCli_EmitsPerPrompt` (+ 7 sub-cases) | PR-2 | `scene_orchestrator_downstream_test.go` (NEW) |
| `TestDispatcher_VoiceoverEmit_ActiveKeyStable` (+ 5 sub-cases) | PR-3 | `voiceover_sibling_fanout_test.go` (NEW) |
| `TestDispatcher_DocumentEmit_ActiveKeyStable` (+ 5 sub-cases) | PR-4 | `document_sibling_emit_test.go` (NEW) |
| `TestParentAggregator_RequiredChildFailure_PropagatesFailed` (+ 6 sub-cases) | PR-5 | `parent_aggregator_required_failure_test.go` (NEW) + `handler_legacy_inline_deprecation_test.go` (NEW) |

**Total: 28 hermetic TDD cases** across 6 NEW test files + 1 NEW compile-time pin (`var _ jobbatcher.Enqueuer = (*Dispatcher)(nil)`).

---

## §6 Honest scope-lock carry-forward (godlike/07)

Carry forward unchanged per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` (the canonical 6-item list):
- `monitor/enqueue.go::tolower` drift (OUT OF SCOPE)
- `monitor/scheduler.go::ENQUEUER` typed-port resolution (OUT OF SCOPE)
- `stockpipeline/run_upload.go::Orchestrator` redeclaration (OUT OF SCOPE)
- `app/module_media.go::MODULES` literal slice (OUT OF SCOPE)
- `images/routing` package import cycle (OUT OF SCOPE)
- `app/wire_script.go` (registry scripts entry) syntax drift (july 2026 RETIRED per `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` carry-forward)
- `internal/application/assets/providers/artlist/search_strategy.go` redeclaration (NEW canonical 7th item per `architecture/waves/wave_p1_high.yaml#FIX-ARTLIST-SEARCH-STRATEGY-RECLARATION-2026-07-09`)

The pre-existing `architecture/waves/wave_p1_high.yaml` parser error (per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward; forward-pointer `PR-CURRENT-YAML-PARSE-FIX-PART-N`, deadline 2026-08-15) is unchanged. **This plan's wave-tracker slot is DEFERRED** — the canonical SOLE closure record this commit ships is the action plan + CHANGELOG + AGENTS mirror per the SCRIPTCONTRACT-2026-07-08 + SCRIPT-PIPELINE-DECOUPLING-2026-07-09 precedents.

**Do NOT touch** (orthogonal): GODOBJ-2026-07-03 (godobject kill list); VO-DECOMPOSITION-2026-07-04 (voiceover subsystem hardening); DRIVE-AS-CENTRAL-CAPABILITY-2026-07-07 (FASE A→E closure); QDRANT-CHAIN-VERIFY-2026-07-04 (12-gate Qdrant DoD finale); CODE-QUALITY-CLEANUP-2026-07-04 (12 dirty areas band); REFACTOR-WAVE-3-2026-08-08 (5 PR refactor wave rollup).

---

## §7 Cross-references (godlike/06 SSOT umbrella)

| Surface | Reference |
|---------|-----------|
| Parent decoupling plan | `architecture/action-plans/2026-07-09-script-pipeline-decoupling.md` (§0 deferred Option 2 reference; §11 forward-pointer for FASE-2 Spina Dorsale — **renamed** to **FASE-2 Downstream** per this action plan) |
| Code Health Improvement Plan | `architecture/action-plans/2026-07-09-code-health-improvement-plan.md` (sister plan; orthogonal — covers Cyclomatic/P0 import-cycles/stray-state/etc.) |
| Existing pipeline contract | `architecture/action-plans/2026-07-08-script-pipeline-contract.md` (4-PR wave; PR-1 reorder + PR-2 preflight + PR-3 CI gate + PR-4 child-doc propagation — orthogonal to the downstream cutover, complemented by this plan) |
| Postprocessor unification | `architecture/action-plans/2026-07-08-postprocessor-unification.md` (Phase 1-4 wave; PR-3 here lifts the typed `AssetSearchPort` for the downstream Dispatcher's image sibling enqueue) |
| Dead-code purge | `architecture/waves/wave_p1_high.yaml#PR-DEAD-CODE-PURGE-2026-07-25` (4 user-spec surfaces + 1 discovered bonus; OUT OF SCOPE for this wave — `SceneImageJobEmitter` was already on main pre-purge) |
| Code-quality cleanup | `architecture/waves/wave_p1_high.yaml#CODE-QUALITY-CLEANUP-2026-07-04` (12 dirty areas band; PR-1 here touches the legacy 5-folder postprocessor wiring which is partly covered by the upstream cleanup) |
| Voiceover propagation | `architecture/waves/wave_p1_high.yaml#VO-DECOMPOSITION-2026-07-04` (PR-3 here lifts the canonical FanoutVoiceoversUseCase pattern into script-level; the existing voiceover subscription continues unchanged) |
| Drive canonical | `architecture/waves/wave_p1_high.yaml#DRIVE-AS-CENTRAL-CAPABILITY-2026-07-07` (PR-4 here does NOT touch `delivery.Publisher`; just routes through OutputDestination which is routed by publishing surfaces) |
| LLM plain-text contract | `architecture/waves/wave_p3_low_and_audit.yaml#LLM-PLAIN-TEXT-CONTRACT` (orthogonal — PR-1 here emits the canonical item_ref payload without touching the LLM contract) |

---

## §8 Wave-flip criterion (godlike/06/07)

The wave flips to `status: shipped + exit_signal: true` ONLY WHEN:

1. All 5 PRs reach `status: shipped` (single canonical surfacing per git-log `--grep=PR-SCRIPT-DOWNSTREAM-`).
2. `bash scripts/ci-architectural-checks.sh --self-check` exits 0 with `Check 70` (forward-gate `PR-CHECK-70-DOWNSTREAM-CHILD-REGISTRATION`, ships with PR-5) active.
3. `go test -short ./...` exits 0 with the pre-existing 6+1-item voiceover + app + artlist build-issue carry-forward per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` UNCHANGED (NOT regressions of any per-PR closure).
4. The canonical 18-assertion pipeline smoke (per `tests/operational/script_e2e_pipeline_smoke.sh`) passes after operator flips `cfg.Features.DownstreamVoiceover=true & cfg.Features.DownstreamImages=true & cfg.Features.DownstreamDocument=true` simultaneously with `cfg.Features.InlineDownstream=false`.

---

## §9 Lifecycle audit-trail + Co-authored-by

| Stamp | Action | Actor |
|-------|--------|-------|
| 2026-07-09 | Marcuss-ops deferred-Option-2 spec | Marcuss-ops |
| 2026-07-09 | Action plan authored (this file) | PipelineGen Agent |
| 2026-07-09 | Action plan committed + pushed to `origin/main` (direct-to-main per AGENTS.md Git-Lesson-2) | PipelineGen Agent |
| 2026-07-22 (Phase A) | PR-1 NEW-mode write seam lands on main | PipelineGen Agent |
| 2026-08-01 (Phase B) | PR-2 images dispatcher ships + PR-3 voiceover dispatcher ships (2 atomic commits) | PipelineGen Agent |
| 2026-08-08 (Phase C) | PR-4 document dispatcher + feature flags ships on main | PipelineGen Agent |
| 2026-08-15 (Phase D) | PR-5 contract cleanup defaults flip + 410-Gone legacy wiring ships on main | PipelineGen Agent |

---

## Co-authored-by trailer (mandatory, per AGENTS.md Git-Lesson-3)

```
Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>
```

---

## §10 Forward-pointers (godlike/07 honest scope-lock)

- `PR-DOWNSTREAM-OUTBOX-INDEX` (deadline 2026-08-22): the downstream Dispatcher emits `voiceover.index.requested` / `image.index.requested` / `document.index.requested` outbox events via `asset.index.requested` aggregator. Future PR makes the canonical `/api/media/search` Qdrant backfill understand the 3 new sibling event types.

- `PR-CLI-INLINE-SUNSET` (deadline 2026-09-01): operator CLI `--sunset-inline-downstream` inspects the SQLite scripts table for rows where the manifest_v2.json column says `NoInlineAssets=false`; produces an enumerated list of legacy manifest rows for operator-driven one-shot cleanup.

- `PR-CONFIG-CD-VALIDATOR` (deadline 2026-08-22): CI gate that fails when `cfg.Features.InlineDownstream=true` AND any of `cfg.Features.Downstream{Voiceover,Images,Document}` is `true` (godlike/07 NO-FAKE-AVAILABILITY — never run both code paths in production).

- `PR-DOWNSTREAM-OBERVABILITY-METRICS` (deadline 2026-09-08): Prometheus counter `script_downstream_children_total{kind, status}` + gauge `script_downstream_pending_children{script_id}` — operator-facing observability surface for the 3 sibling kinds.

- `PR-CHECK-70-DOWNSTREAM-CHILD-REGISTRATION` (deadline 2026-08-15): `scripts/ci-architectural-checks.sh` forward-gate that fails when a non-canonical sibling job type literal (`script.<c>_sibling`) appears outside the canonical handlers declared in this wave. Mirrors the Check 64 forward-prevention precedent.

---

## §11 Co-authored-by trailer (mandatory, repeated for visibility)

All commits in this wave MUST include the canonical trailer:

```
Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>
```

Per AGENTS.md Git-Lesson-3 (the canonical agent-identity attribution rule for this repository).
