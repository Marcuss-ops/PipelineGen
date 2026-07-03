# CUTOVER-COMPLETE-WITH-ARTIFACTS — Action Plan

**Date:** 2026-07-04
**Author:** PipelineGen Agent
**Scope:** Canonical progression (18 Azioni across 4 tracks) to migrate artifact-producing jobs to `CompleteWithArtifacts` and retire the legacy `tools.Complete` bypass.
**Status:** in_progress (`architecture/current.yaml#CUTOVER-COMPLETE-WITH-ARTIFACTS`)
**Anchor:** Azione 6 shipped 2026-07-04 (`2bf7e865` on `origin/main`)

---

## 1. Cross-cutting Schema & Governance Mapping

*   **Wave-Tracker Mapping**: Each Azione maps 1:1 to a `linked_issue` inside `architecture/current.yaml#CUTOVER-COMPLETE-WITH-ARTIFACTS`. Flipping an Azione's ticket to `status: shipped` progresses the wave. The tracker's `exit_gate` is satisfied when all 18 linked issues are shipped AND the telemetry confirms 0 fallback traces.

*   **Deprecation Record**: `architecture/deprecations.yaml#REMOTE-COMPLETE-LEGACY` registers the formal YAML deprecation schema representing the phase-out of `tools.Complete` for artifact-yielding jobs (Azione 16c). The CUTOVER phase replaces `tools.Complete(...)` for artifact-producing jobTypes via Azione 7 (registry-driven branch in `internal/app/runner.go`).

*   **CONTRACT Finalization**: The CONTRACT phase (final lock + physical removal of the legacy compatibility fallback) is gated by Azioni 1-5 being observed in production for ≥2 weeks without the `tools.Complete` fallback firing. The post-removal date aligns with the Wave 14 mega-package split gate (Q3 2026), same deadline as `DRIVE-005-FIELDS` sibling.

*   **godlike/06 SSOT Discipline**: Each Azione has exactly one `owner_capability` tag (single canonical owner per fact). The `WithArtifactsService` (Azione 6) is the canonical owner of "completed a job with published assets"; the pre-existing `CompleteJobService` (P0 Commit 7) remains canonical for the artifact-free path.

*   **godlike/07 Typed-Error Contract**: All failure paths return typed sentinels reachable via `errors.Is`. The chained calls in Azione 6 (CAS, InsertResult, PersistArtifact, Outbox, AssetLocations) share the same SQLite TX — any partial failure rolls back the entire batch.

---

## 2. Bands & Deadlines

| Band | Azioni | Deadline | Migration Phase |
|------|--------|----------|-----------------|
| **P0 absolute** | 1, 2, 3, 4, 5, 6, 7, 8, 16a, 16b, 16c | 2026-08-15 | EXPAND → CUTOVER |
| **P1 mechanical** | 9, 10, 11, 12, 14, 15 | 2026-08-22 | BACKFILL |
| **P0/P1 mixed (audit)** | 13 | 2026-08-15 (P0) / 2026-08-22 (P1) | EXPAND |

P0 absolute = azioni that block E2E verification (Track A + 16 governance). P1 mechanical = azioni that depend on P0 being available (Track B + Track C ops). Band 13 (VLM gate audit) crosses the cut because Client.IsEnabled() enforcement can land independently of the artifact chain.

---

## 3. Azioni Inventory (By Track)

### TRACK A — Artifact Path (P0 absolute, Deadline 2026-08-15)

#### Azione 1: Staged Resolver
- **Description**: Local staging lookup mapping abstract `artifact_id` → concrete local path prior to verification.
- **Target File(s)**: `internal/application/assets/staged/resolver.go` (NEW package)
- **godlike/06 SSOT Rationale**: Single source of truth for `artifact_id → local_path` mapping, decoupling metadata from local filesystem volatility.
- **godlike/07 Typed-Error Contract**: `ErrStagedArtifactMissing` sentinel; `errors.Is` compatible.
- **Migration Phase**: EXPAND
- **Test Surface**: 3 TDD tests (happy path; not-found → typed sentinel; idempotency same artifactID = same path).
- **Cross-file Ripple**: Feeds Azione 2 (Verifier) and Azione 3 (Publisher).
- **Acceptance Criteria**: `ResolveStagedAsset(ctx, artifactID)` compiles + tests pass + emits typed sentinel without wrapping generic errors.

#### Azione 2: Verifier
- **Description**: Recompute SHA-256 + size against stage metadata.
- **Target File(s)**: `internal/application/assets/verification/verified.go` (NEW file)
- **godlike/06 SSOT Rationale**: Single canonical owner of "verified artifact" fact (12-field typed envelope mirrors a subset of `finalization.PublishedArtifact`).
- **godlike/07 Typed-Error Contract**: `ErrStagedChecksumMismatch` + `ErrStagedSizeMismatch` typed sentinels.
- **Migration Phase**: EXPAND
- **Test Surface**: 4 TDD tests (match + mismatch per each of 2 dimensions).
- **Cross-file Ripple**: Feeds Azione 3 (Publisher).
- **Acceptance Criteria**: `VerifyStagedArtifact(ctx, *StagedArtifact)` returns `(VerifiedArtifact, error)` typed; hash via `pkg/hashutil`; size via `os.Stat`.

#### Azione 3: Publisher
- **Description**: Route verified artifacts through `ArtifactPreparation.Prepare → ArtifactPublisherAdapter.Publish`.
- **Target File(s)**: `internal/application/assets/completion/publish_verified.go` (NEW file; alongside P1.4/2 `complete_job_service.go`)
- **godlike/06 SSOT Rationale**: Single canonical owner of "publish verified artifact" path; idempotency-key byte-stable via `ArtifactIdempotencyKey(jobID, artifactID, sha256)`.
- **godlike/07 Typed-Error Contract**: `ErrAlreadyPublished` short-circuit on byte-stable idem-key match.
- **Migration Phase**: EXPAND
- **Test Surface**: 5 TDD tests (happy + 401→retry + duplicate→idempotent + sha256 final mismatch + error propagation).
- **Cross-file Ripple**: Feeds Azione 6 (CompleteWithArtifacts) + Azione 5 (Broker).
- **Acceptance Criteria**: Output `PublishedArtifact` carries 15-field expanded envelope (mirrors the typed `finalization.PublishedArtifact` shape).

#### Azione 4: Jobbrokerclient Wire
- **Description**: `func (c *Client) CompleteWithArtifacts(ctx, cmd appjobs.CompleteWithArtifactsCommand) error` via `POST /internal/v1/jobs/:id/complete-with-artifacts`.
- **Target File(s)**: `internal/infrastructure/remote/jobbrokerclient/client.go` (MODIFY — remove fake "not supported over remote protocol" return).
- **godlike/06 SSOT Rationale**: Single canonical wire for `CompleteWithArtifacts` between Sender and local broker.
- **godlike/07 Typed-Error Contract**: HTTP 4xx/5xx via typed `errors.As(googleapi.Error)`; SignalEnd on retryable.
- **Migration Phase**: EXPAND
- **Test Surface**: 2 TDD tests (happy-path + URL-param-wins-body).
- **Cross-file Ripple**: Compile-time pin `var _ appjobs.CompletionPort = (*Client)(nil)`.
- **Acceptance Criteria**: Compile-time drift detection via the assertion; URL param `:id` wins body `cmd.JobID` when both present.

#### Azione 5: Broker Signature
- **Description**: Extend `Broker.CompleteWithArtifacts(*appjobs.FinalizationAck, error)` to populate `AssetIDs` in HTTP response (was previously `error`-only).
- **Target File(s)**: `internal/infrastructure/jobs/local/broker.go::CompleteWithArtifacts` + `stubBroker` + `stubLeaseBroker` + `remote jobbrokerclient` + `handler_workers.go::CompleteWithArtifacts`.
- **godlike/06 SSOT Rationale**: Single canonical owner of "broker ACK surface for artifact completion" fact.
- **godlike/07 Typed-Error Contract**: Existing typed sentinels preserved at the broker seam.
- **Migration Phase**: CUTOVER (signature change is a typed-port breaking change gated by Azione 6 completion).
- **Test Surface**: 6 TDD tests (1 happy + 5 stub-bearing).
- **Cross-file Ripple**: All callers updated in lockstep (compile-time drift = build failure).
- **Acceptance Criteria**: `resp.AssetIDs` populated from `ack.AssetIDs`; no surface regression in stub brokers.

#### Azione 6: CompleteWithArtifactsService ✅ DONE
- **Description**: Sender-side atomic 5-step single-TX chain.
- **Target File(s)**: `internal/application/jobs/completion/complete_with_artifacts_service.go` (NEW) + `complete_with_artifacts_service_test.go` (NEW).
- **godlike/06 SSOT Rationale**: Single canonical owner of "completed a job with published assets" fact; sits alongside `complete_job_service.go`.
- **godlike/07 Typed-Error Contract**: 3 new sentinels (`ErrCompleteWithArtifactsNotConfigured` / `ErrCompleteWithArtifactsRequestMissingFields` / `ErrRemoteArtifactLocationMismatch`); reuses 4 sentinels from C7.
- **Migration Phase**: EXPAND (shipped as commit `2bf7e865` on origin/main, 2026-07-04).
- **Test Surface**: 6 TDD tests (5 mandated + 1 nil-receiver bonus) — 14/14 PASS (8 C7 preserved + 6 Azione 6).
- **Cross-file Ripple**: `TxContext` extended with 7th method `InsertAssetLocations`; `AssetLocationEntry` typed struct added.
- **Acceptance Criteria**: ✅ DONE — 14 PASS, gofmt/go vet/build/test clean, surfaced at CHANGELOG `## Unreleased → ### Fixed`.

#### Azione 7: Runner Terminal Branch
- **Description**: Registry-driven branch in `internal/app/runner.go::cycle finale` (no hard-coded `job.Type=="script.generate"`).
- **Target File(s)**: `internal/app/runner.go` (MODIFY).
- **godlike/06 SSOT Rationale**: Single canonical termination seam driven by `registry.Definition(job.Type).ProducesArtifacts + RequireManifest`.
- **godlike/07 Typed-Error Contract**: Reuses typed error surfaces from Azione 5/6.
- **Migration Phase**: CUTOVER
- **Test Surface**: 2 tests (1 artifact-producing + 1 legacy), forcing distinct paths.
- **Cross-file Ripple**: `Check 53 + 54` enforced in `scripts/ci-architectural-checks.sh` after this commit.
- **Acceptance Criteria**: `internal/app/runner.go` reads `registry.Definition(jobType)` for the branch — zero hard-coded `job.Type == "script.generate"` strings remaining.

#### Azione 8: E2E Test
- **Description**: Full happy-path + idempotency-retry coverage.
- **Target File(s)**: `internal/app/completion_e2e_test.go` (NEW).
- **godlike/06 SSOT Rationale**: Single canonical E2E coverage for the CUTOVER chain.
- **godlike/07 Typed-Error Contract**: 0 path-locale in the final response JSON.
- **Migration Phase**: EXPAND (test surface runs against the live chain in Azione 6 + Azione 5 stub).
- **Test Surface**: 1 E2E subtest with 4 assertions (0 duplicates at retry + 1 PublishedArtifact + 1 asset_location + JobFinalizer SUCCEEDED).
- **Cross-file Ripple**: Uses in-process `httptest.Server` + SQLite in-memory + mock Drive.
- **Acceptance Criteria**: `go test ./internal/app/ -run TestCompletionE2E -count=1` exits 0.

---

### TRACK B — Workflow Layered on Top (P1 mechanical, Deadline 2026-08-22)

#### Azione 10: SceneImageJobEmitter
- **Description**: 1-method port for emitting child `images.generate` jobs from `script.generate`.
- **Target File(s)**: `internal/application/scripts/scene_orchestrator.go` (NEW port + EmitSceneImageJob method).
- **godlike/06 SSOT Rationale**: Single canonical owner of "scene-image child job emitted from script.generate" fact.
- **godlike/07 Typed-Error Contract**: `(jobID, error)` typed return; `errors.Is` on emit failure.
- **Migration Phase**: EXPAND (uses Pattern 9 `Dispatcher.Enqueue`).
- **Test Surface**: 4 TDD tests (compile-time pin + happy + nil-receiver + typed envelope).
- **Cross-file Ripple**: Wired in `app/composition.go::BuildBundleScript`.
- **Acceptance Criteria**: `var _ port.SceneImageJobEmitter = (*Emitter)(nil)` compile-time pin.

#### Azione 11: Workflow State Machine
- **Description**: Typed 6-state machine with 2 terminal sinks.
- **Target File(s)**: `internal/application/scripts/workflow_state_machine.go` (NEW).
- **godlike/06 SSOT Rationale**: Single canonical owner of "workflow lifecycle state" fact.
- **godlike/07 Typed-Error Contract**: Forward-only via `Validated()`; `FAILED`/`DEAD_LETTERED` as sticky sinks.
- **Migration Phase**: EXPAND
- **Test Surface**: 6 TDD tests (1 legal forward + 2 illegal backward → typed error + 3 retry idempotency).
- **Cross-file Ripple**: Persisted in `workflow_state` table with `UNIQUE(workflow_id, state)`.
- **Acceptance Criteria**: Forward transition `SCRIPT_READY → IMAGES_PENDING` works; backward `→ SCRIPT_READY` raises typed sentinel.

#### Azione 12: Google Doc Template
- **Description**: Render scene with `[]*asset.Asset` inputs; look up asset_locations for `webViewLink`.
- **Target File(s)**: `internal/application/scripts/templates/google_doc_template.go::render_scene` (MODIFY).
- **godlike/06 SSOT Rationale**: Single canonical owner of "Google Doc rendering for finalized assets" fact; manual concatenation forbidden (audit-pin).
- **godlike/07 Typed-Error Contract**: `ErrImageNotFinalized` typed sentinel.
- **Migration Phase**: EXPAND
- **Test Surface**: 3 TDD tests (2 finalized rendered + 1 non-finalized → typed error + 0 path-locale in render output).
- **Cross-file Ripple**: DB lookup in `asset_locations` for canonical `webViewLink`.
- **Acceptance Criteria**: `render_scene([]*asset.Asset)` NEVER concatenates manually; asserts `status==FINALIZED`.

---

### TRACK C — Operational (P1 mechanical, Deadline 2026-08-22)

#### Azione 9: Recovery Script
- **Description**: Recover lost PNG fixtures as completed artifacts in script.generate workflow.
- **Target File(s)**: `scripts/recovery/recover_apollo_cosmos_pngs.sh` (NEW).
- **godlike/06 SSOT Rationale**: Single canonical recovery command for already-completed artifacts.
- **godlike/07 Typed-Error Contract**: Idempotent — second run yields zero new artifacts.
- **Migration Phase**: BACKFILL
- **Test Surface**: Smoke run on `data/media/google-slides/{cosmos,apollo}.png` with assert.
- **Cross-file Ripple**: Documents `rebuild doc` final command without UPDATE of old job.
- **Acceptance Criteria**: Says "Google Doc aggiornato senza ri-creare scene".

#### Azione 14: Dead Letter Script
- **Description**: Sweep stale-token lease rows into DEAD_LETTERED + audit log.
- **Target File(s)**: `scripts/admin/dead_letter_stale_token_jobs.sh` (NEW) + `docs/operations/dead-letter-stale-token.md` (NEW).
- **godlike/06 SSOT Rationale**: Single canonical owner of "stale-token lease handling".
- **godlike/07 Typed-Error Contract**: Idempotent (`UPDATE` is no-op on rows already DEAD_LETTERED).
- **Migration Phase**: BACKFILL
- **Test Surface**: Smoke run against test DB with mixed old/new fingerprints.
- **Cross-file Ripple**: `dead_letter_audit` table INSERT.
- **Acceptance Criteria**: Idempotent re-runs return zero new rows.

#### Azione 15: Migration 121
- **Description**: Rename `AssetStatus::Ready → Staged` with 6-month BC alias + warning.
- **Target File(s)**: `migrations/sqlite/121_rename_ready_to_staged.sql` (NEW) + `internal/domain/asset` enum (MODIFY).
- **godlike/06 SSOT Rationale**: Single canonical owner of "asset lifecycle state v2" fact.
- **godlike/07 Typed-Error Contract**: BC alias `AssetStatus_Ready = AssetStatus_Staged` for 6 months + warning log on alias access.
- **Migration Phase**: BACKFILL
- **Test Surface**: 5 TDD tests (rename + INSERT/SELECT round-trip + BC alias + warning log assertion).
- **Cross-file Ripple**: All `rg asset_status_ready transfer_state_ready` references updated.
- **Acceptance Criteria**: `ALTER` is backward safe (NULL check guard); BC alias removed after 6 months.

---

### TRACK D — Audit / Config / Governance (P0/P1 mixed)

#### Azione 13: VLM Gate Audit
- **Description**: `Client.IsEnabled()` gate respected by all call sites in `internal/`.
- **Target File(s)**: `config/vlm.yaml` (MODIFY) + audit `rg` + fix bypass callers.
- **godlike/06 SSOT Rationale**: Single canonical owner of "VLM-enabled flag" fact.
- **godlike/07 Typed-Error Contract**: `errors.Is` on `ErrVLMDisabled`.
- **Migration Phase**: EXPAND (P0) + BACKFILL (P1 for audit-pin enforcement)
- **Test Surface**: Smoke test: 0 log lines at `/vlm/autotag/analyze-file` with `enabled=false`.
- **Cross-file Ripple**: All `vlm` config consumers updated.
- **Acceptance Criteria**: `bash scripts/ci-architectural-checks.sh` exposes VLM gate; pass on `vlm.enabled=false` config.

#### Azione 16a: Action Plan Doc (this file)
- **Description**: Canonical narrative action plan tying Azioni 1-15 + governance together.
- **Target File(s)**: `architecture/action-plans/2026-07-04-CUTOVER-COMPLETE-WITH-ARTIFACTS.md` (THIS file).
- **Acceptance Criteria**: ✅ THIS COMMIT — file lands on `origin/main` via direct-to-main flow per AGENTS.md Git-Lesson-2.

#### Azione 16b: Wave-Tracker Entry
- **Description**: `architecture/current.yaml#CUTOVER-COMPLETE-WITH-ARTIFACTS` entry with 18 `linked_issues`.
- **Target File(s)**: `architecture/current.yaml` (APPEND entry).
- **godlike/06 SSOT**: Per-Azione linked_issue = canonical owner_capability tag.
- **Acceptance Criteria**: Tracker entry pins all 18 Azioni; status flips to `done` only when all 18 are `shipped`.

#### Azione 16c: Deprecation Record (REMOTE-COMPLETE-LEGACY)
- **Description**: YAML deprecation schema for `tools.Complete` artifact-producing retirement.
- **Target File(s)**: `architecture/deprecations.yaml` (APPEND record).
- **godlike/06 SSOT**: Single canonical owner for "legacy complete retirement" fact.
- **godlike/07 Typed-Error Contract**: 12-field schema enforced by `scripts/archcheck/deprecations_validator.go`.
- **Acceptance Criteria**: Validator passes (no duplicate IDs; removal_date ≤ CUTOVER parity); tracked to `architecture/current.yaml#CUTOVER-COMPLETE-WITH-ARTIFACTS`.

---

## 4. Acceptance Criteria per Band

*   **P0 absolute (Azioni 1-8 + 16)**: 
    - All targeted packages pass `gofmt && go vet && go build && go test -short`.
    - `scripts/ci-architectural-checks.sh` Check 53/54 forward-prevent after Azione 7 lands (bans raw-string `job.Type == "script.generate"` patterns).
    - Tracker's `exit_gate` = `CUTOVER-COMPLETE-WITH-ARTIFACTS.status == done && exit_signal == true`.

*   **P1 mechanical (Azioni 9-15)**:
    - All targeted scripts/migrations compile clean.
    - Smoke runs against local fixtures succeed.
    - Telemetry counters for legacy fallbacks trend to zero before CONTRACT.

---

## 5. Migration Sequence (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

1. **EXPAND** (P0, 2026-07-04 → 2026-08-15): Azioni 1-8 ship with canonical surfaces live + ci-gates forward-preventive. Zero behaviour change for existing pre-cutover caller paths (`tools.Complete` legacy path still active).
2. **BACKFILL** (P1, 2026-08-01 → 2026-08-22): Azioni 9-15 ship. New callers migrate from legacy to canonical paths. BC alias (Azione 15) covers transitional period.
3. **CUTOVER** (2026-09-01): Azione 7's registry-driven branch becomes the SINGLE termination path. `tools.Complete` legacy still callable but type-mismatched for artifact-producing jobTypes (Check 53/54 enforced).
4. **CONTRACT** (2026-09-26, aligned with Wave 14 mega-package split gate): physical removal of `tools.Complete` artifact-producing branch; `architecture/deprecations.yaml#REMOTE-COMPLETE-LEGACY.status: removed`.

---

## 6. Forward-pointer / Honest limitations (godlike/07)

*   **Static analysis caveat**: The P0/P1 band classification is based on dependency ordering. Final canonical ranking MUST cross-validate against `git log --since=90.days` frequency (forward-pointer entry).
*   **Pre-existing build issues carry forward unchanged**: per CHANGELOG convention, the 5-item build-issue list from prior waves is out-of-scope for any per-Azion commit. Each split commit lands in isolation on its own subtree and passes targeted gates independently.
*   **Cross-package residue**: the `IndexingStatus` typed enum at `internal/application/assets/sourcing/types.go` is a SEPARATE concern (asset-level YouTube indexing vs chunk-level stock indexing). NOT retired by this action plan — forward-pointer `PR-CrossPackage-IndexingStatus-§12-5` (architecture/current.yaml) carries the cross-package retirement.

---

## 7. Cross-references

*   **godlike/06 SSOT**: Pattern 0 port discipline; one canonical owner per fact.
*   **godlike/07 typed-error contract**: every failure path returns a typed sentinel reachable via `errors.Is`.
*   **AGENTS.md Git-Lesson-2**: Direct-to-main workflow. Each auto-sufficient commit lands directly on `main` with `Co-authored-by:` trailer. No topic branches, no `--no-ff` merge commits, no `--force`.
*   **AGENTS.md Pattern 9 (Dispatcher.Enqueue)**: Azione 10 uses this pattern for typed-payload job emission.
*   **AGENTS.md Pattern 11 (Atomic CompleteJob + idempotency)**: Azione 6 extends the P0 Commit 7 surface with the 5-step atomic TX chain.

---

## 8. Lifecycle (audit trail)

| Date | Action | Author |
|------|--------|--------|
| 2026-07-04 | Action plan doc lands (Azione 16a) | PipelineGen Agent |
| 2026-07-04 | Tracker entry lands (Azione 16b) | PipelineGen Agent |
| 2026-07-04 | Deprecation record lands (Azione 16c) | PipelineGen Agent |
| 2026-07-04 | Azione 6 ships (`2bf7e865` on `origin/main`) | PipelineGen Agent |
| (TBD) | Azioni 1-5 ship | PipelineGen Agent |
| (TBD) | Azione 7 + Check 53/54 enforcement | PipelineGen Agent |
| (TBD) | Azione 8 E2E test | PipelineGen Agent |
| (TBD) | Azioni 9-15 P1 | PipelineGen Agent |
| (TBD) | CUTOVER phase (2026-09-01) | PipelineGen Agent |
| (TBD) | CONTRACT phase (2026-09-26) | PipelineGen Agent |

---

**End of canonical action plan.** Future agents: read this file alongside `architecture/deprecations.yaml#REMOTE-COMPLETE-LEGACY` and `architecture/current.yaml#CUTOVER-COMPLETE-WITH-ARTIFACTS` for full governance context. Per `godlike/06 SSOT` discipline, this file is the canonical narrative; the tracker entry is the canonical state; the deprecation record is the canonical enforcement schema.
