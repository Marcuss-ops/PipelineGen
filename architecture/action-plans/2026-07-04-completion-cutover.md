# Completion Cutover P0 — Action plan (2026-07-04)

> **Anchor**: `architecture/current.yaml#COMPLETION-CUTOVER-P0-2026-07-04`
> **Trigger**: 9-item Italian P0 audit posted by `Marcuss-ops` to the orchestrator on 2026-07-04 (user-pasted in chat on the same day).
> **Wave-track rule** (godlike/06 one-owner-per-fact): every per-item PR must add its canonical SHA to the matching `linked_issues[].shipped_sha` slot of the wave-tracker entry. Once all 9 flip to `status: shipped`, this entry flips to `status: done / exit_signal: true` per godlike/07 §Exit gate.

---

## Verdict → fix map (per P0 audit item, ordered by structural priority)

### P0-Structural (godlike/06 SSOT violations — MUST land in atomic per-item PR)

| # | Audit title | linked_issue | Owner capability | Deadline | Why structural |
|---|------------|--------------|------------------|----------|----------------|
| 4 | `completion.Service` may upload twice | `P0-COMPL-4-PUBLISH-DEDUPE` | `internal/application/jobs/completion` | 2026-07-25 | Single canonical publish seam: today `ArtifactPreparation.Prepare()` calls `publisher.Publish(...)` AND `completion.publishOne()` calls it AGAIN inside the same TX. Per godlike/06 one-owner-per-fact, a folder write cannot have two canonical writers — collapse to `ArtifactPreparation` as the lone publish seam; remove `Publisher` from `completion.Service`. |
| 5 | Two competing completion services | `P0-COMPL-5-SINGLE-BACKBONE` | `internal/application/jobs/finalizer` + `internal/application/jobs/completion` | 2026-08-15 | `JobFinalizer::CompleteWithArtifacts` AND `CompleteWithArtifactsService::Complete` today BOTH write `jobs.status=SUCCEEDED`. godlike/06 SSOT requires 1 canonical writer per fact. Both must share the same tx runner / lease fence / `(idempotency, result writer, outbox)` triple. Reduce old `JobFinalizer` to adapter shim; new Service is the sole writer. |

### P0-Cutover wiring (cross-package composition root + wire-format rename)

| # | Audit title | linked_issue | Owner capability | Deadline |
|---|------------|--------------|------------------|----------|
| 1 | Runner calls `tools.Complete` even when ArtifactManifest exists | `P0-COMPL-1-MANIFEST-DECISION` | `internal/application/jobs/worker` | 2026-07-25 |
| 2 | Stage→Drive path not wired into production composition root | `P0-COMPL-2-COMPOSITION-WIRE` | `internal/app` + `internal/application/assets/delivery` + `internal/application/jobs/completion` | 2026-08-01 |
| 3 | HTTP contract calls staged artifacts `PublishedArtifacts` (rename + Sender-side converter) | `P0-COMPL-3-ARTIFACT-CONTRACT` | `internal/api` + `internal/infrastructure/remote/jobbrokerclient` + `internal/application/jobs/completion` | 2026-08-01 |

### P0-Infrastructure hardening

| # | Audit title | linked_issue | Owner capability | Deadline |
|---|------------|--------------|------------------|----------|
| 6 | Workspace cleaned up even when completion fails (no recovery for retry) | `P0-COMPL-6-WORKSPACE-RETENTION` | `internal/application/jobs/worker` + `internal/infrastructure/jobs/local` | 2026-08-01 |
| 7 | Two different workspace paths (`/tmp/pipelinegen/creator` vs `/tmp/pipelinegen/jobs/<jobID>/output`) | `P0-COMPL-7-WORKSPACE-PATH-OWNER` | `internal/application/jobs/worker` + `internal/app` | 2026-08-01 |
| 8 | Old staged upload not atomic + no SHA-256 check (`.part` / fsync / size / sha256 mismatch / atomic rename) | `P0-COMPL-8-STAGED-UPLOAD-ATOMIC` | `internal/infrastructure/remote/jobbrokerclient` | 2026-08-15 |
| 9 | New typed upload protocol has no server-side routes (Creator-side typed commands ready but `WorkersBrokerHandler` still mounts the legacy routes) | `P0-COMPL-9-UPLOAD-ROUTES-LIVE` | `internal/infrastructure/remote/jobbrokerclient` + `internal/app` | 2026-08-15 |

---

## Execution order (per godlike/07 EXPAND → BACKFILL → CUTOVER → CONTRACT)

1. **PR-0 (this commit, 2026-07-04)** — `chore(architecture)` — wave-tracker anchor (`current.yaml#COMPLETION-CUTOVER-P0-2026-07-04`) + this action plan + `CHANGELOG.md ### Added` entry. **Zero semantic change.**
2. **PR-1 (~2026-07-15, ~11 days)** — `fix(remote)` — `P0-COMPL-4-PUBLISH-DEDUPE`. Migration step 1 of structural EXPAND. Remove `Publisher Publisher` field from `internal/application/jobs/completion/complete_with_artifacts_service.go::Deps`; collapse canonical publish to `ArtifactPreparation.Prepare` only. Backed by `TestArtifactPreparation_PublishInvokedOncePerArtifact` (typed `errors.Is` on single-publish invariant).
3. **PR-2 (~2026-07-22)** — `refactor(jobs)` — `P0-COMPL-5-SINGLE-BACKBONE`. Migration step 2 of structural CUTOVER. Extract `CompleteJobTxRunner` (already in `internal/domain/remote/complete_job.go` from the C7 closure) as the shared composition seam. `JobFinalizer.CompleteWithArtifacts` is reduced to an adapter that delegates to `CompleteWithArtifactsService.Complete`. Physical deletion of the legacy file is CUTOVER-phase (separate PR, forward-pointer ticket `PR-JOB-FINALIZER-CONTRACT`).
4. **PR-3 (~2026-07-25)** — `feat(worker) + feat(jobs)` — `P0-COMPL-1-MANIFEST-DECISION`. Add `tools.CompleteWithArtifacts(*ArtifactManifest)` worker-side facade that gates on `uploadedManifest != nil`. Legacy `tools.Complete(...)` path becomes the manifest-empty branch.
5. **PR-4 (~2026-07-29)** — `feat(composition)` — `P0-COMPL-2-COMPOSITION-WIRE` + `P0-COMPL-3-ARTIFACT-CONTRACT`. Single composition-root bundle wires the full chain (`StagedResolver → VerifiedArtifactProjector → ArtifactPreparation → Drive Publisher → WithArtifactsService`). Wire-format rename `PublishedArtifacts` → `StagedArtifacts` on `complete-with-artifacts` request DTO + Sender-side `StagedArtifact → PublishedArtifact` converter. Backed by `TestCompleteWithArtifacts_SenderConvertsStagedToPublished` round-trip test.
6. **PR-5 (~2026-08-05)** — `feat(worker) + feat(jobs)` — `P0-COMPL-6-WORKSPACE-RETENTION` + `P0-COMPL-7-WORKSPACE-PATH-OWNER`. `WorkspaceRetentionPolicy` port with `OnSuccess` / `OnRetryableFailure(TTL)` / `OnPermanentFailure` so the retry can recover. Runner-side `WorkspaceOutputDir()` knob (config-driven, no `os.TempDir()` in adapters).
7. **PR-6 (~2026-08-10)** — `feat(remote)` — `P0-COMPL-8-STAGED-UPLOAD-ATOMIC`. `.part` + fsync + size verify + sha256 verify + atomic rename + 9-state state machine replacing the legacy "ready" stamp without relitigating bytes. `UploadState` extended from 6 → 9 typed values: STAGED / PUBLISHING / PUBLISHED added. `FinalizeUpload` returns SOMETHING only after the atomic rename succeeds AND the bytes match the request's `ContentHash`.
8. **PR-7 (~2026-08-13)** — `feat(remote) + feat(handlers)` — `P0-COMPL-9-UPLOAD-ROUTES-LIVE`. Server-side handlers mounted at `/jobs/:jobID/uploads/{prepare,file,finalize}`. Legacy `/worker-assets/uploads/*` deprecation record `DRIVE-UPLOAD-CUTOVER-CONTRACT` opens post-CUTOVER (separate PR, forward-pointer).

---

## Cross-package impact map (per godlike/06 one-owner-per-fact)

| linked_issue | Owner capability | Cross-package owns | SSOT boundary |
|--------------|------------------|--------------------|---------------|
| #1 | worker | `tools.go::CompleteWithArtifacts` (new) | broker+finalizer invariants |
| #2 | composition | New composition bundle wiring | registration SSOT gate per FASE-4 |
| #3 | api + remote + completion | Wire-format DTO rename | `complete_artifacts.go` typed envelope (`internal/application/jobs/completion`) |
| #4 | completion | `ArtifactPreparation.Prepare` is sole publish seam | `delivery.Publisher.Publish` is the godlike/06 SSOT |
| #5 | finalizer + completion | Shared `tx-runner` + `lease` + `outbox` triple | `internal/domain/remote/complete_job.go` |
| #6 | worker + local broker | `WorkspaceRetentionPolicy` port + Runner hooks | `tools.go` SSOT |
| #7 | worker + app | Runner-side `WorkspaceOutputDir()` knob | `internal/infrastructure/jobs/local` adapter loses `os.TempDir()` reference |
| #8 | remote | `.part` upload protocol + 9-state machine | `internal/infrastructure/remote/jobbrokerclient/client.go` |
| #9 | remote + app | 3 NEW HTTP handlers + composition wiring | `internal/infrastructure/remote/jobbrokerclient/client.go` + `internal/app/registry_internal_modules.go` |

---

## Per-item contracts (godlike/07 typed-error surface)

New typed sentinel errors added per item (godlike/07 typed-error contract; all `errors.New(...)`):

- **PR-1 (`P0-COMPL-4-PUBLISH-DEDUPE`)**: `ErrDoublePublishReplaced` typed sentinel. Surfaced when the audit-pattern (Prepare+Publish double-call) is detected by a Compose-time probe; logged+dropped, no fake-availability.
- **PR-2 (`P0-COMPL-5-SINGLE-BACKBONE`)**: `ErrConcurrentCompletion` typed sentinel + errors.Is probe on the shared composition seam; forbids both backbones from writing `SUCCEEDED` in the same TX.
- **PR-5 (`P0-COMPL-6-WORKSPACE-RETENTION`)**: `ErrWorkspaceAlreadyCleaned` + `ErrWorkspaceTTLExpired` typed sentinels; the latter surfaces the audit's "retry cannot recover" hole at the Operator dashboard.
- **PR-6 (`P0-COMPL-8-STAGED-UPLOAD-ATOMIC`)**: `ErrStagedUploadPartCorrupt` + `ErrStagedUploadShaMismatch` typed sentinels in the new 9-state machine.

---

## CI-gate promotion (godlike/07 forward-prevention, NO transitional allowlists)

Each PR adds ONE Check N+1 in `scripts/ci-architectural-checks.sh` per godlike/07 forward-prevention discipline (no fake-availability). The 4 forward-prevention gates planned (deadlines staggered to land after each PR):

- **Check 54** (after PR-1, 2026-07-25): `rg 'JobFinalizer\.CompleteWithArtifacts' internal/application/...` returns zero hits outside `internal/application/jobs/finalizer/job_finalizer.go`. Locks the sole-writer SSOT.
- **Check 55** (after PR-4, 2026-07-29): `rg 'delivery\.Publisher\.Publish' internal/application/jobs/completion/` returns at-most-N calls per artifact (deduplication pin: 1, never 2). Locks single-publish seam.
- **Check 56** (after PR-5, 2026-08-05): `rg 'os\.TempDir\(' internal/infrastructure/jobs/local/` returns zero. Locks runner-side path ownership.
- **Check 57** (after PR-6, 2026-08-10): `rg 'Status == "ready"' internal/infrastructure/remote/jobbrokerclient/` returns zero. Locks atomic-upload invariant: "ready" pre-Drive-publish is a typed-error sentinel, not a stamp.

---

## Verification (per-item + wave-level)

Per-item closure commit MUST pass targeted:
```
bash scripts/ci-architectural-checks.sh   # exits 0 with the Check N+1 rule active
go vet ./internal/<package>/<touched>/...
go build ./internal/<package>/<touched>/...
go test -count=1 -timeout=60s ./internal/<package>/<touched>/...
```

Wave-level exit_gate (per godlike/07):
- All 9 `linked_issues` flip to `status: shipped`.
- Run summary in `architecture/current.yaml#COMPLETION-CUTOVER-P0-2026-07-04.exit_gate` exits 0.
- No cross-package regressions: `rg` audit on the 9 owner capabilities returns zero NEW violations.
- Pre-existing 5-item build-issue list unchanged (out of scope per CHANGELOG forward-pointer convention).
- Check 54 + 55 + 56 + 57 forward-prevention gates PASS in `scripts/ci-architectural-checks.sh` (no transitional allowlists).

---

## Honest scoping (godlike/07)

This plan covers the 9 P0 items the user listed, with **zero semantic surface drift in PR-0** (wave-tracker anchor only).

**Pre-existing build issues** (5-item carry-forward, **OUT OF SCOPE** per AGENTS.md §"Known Issues & Fixes" + CHANGELOG convention):
- `monitor/enqueue.go` (`strings.ToLower` undefined in `isTransientEnqueueError`).
- `monitor/scheduler.go` (`NewUnboundJobEnqueuer` undefined).
- `internal/application/assets/providers/stock/stockpipeline/run_upload.go` (syntax error in legacy upload path).
- `internal/app/module_media.go` (pre-existing `clips.Deps.MutationsDispatcher` literal).
- `internal/application/images/routing` (import cycle).

The cross-package YouTube-side `IndexingStatus` forward-pointer (§12-5, deadline 2026-08-15) and the multi-package `PR-GODOBJ-*` chain stay SEPARATE concerns — their wave-tracker entries already exist (`current.yaml#29` for §12-5 + `current.yaml#GODOBJ-2026-07-03` for god-object decomposition).

The pre-existing `DRIVE-005-FIELDS` deprecation record (`status: removed` since 2026-06-30, wave-tracker anchor `current.yaml#27`) and `DRIVE-008` (`status: in_progress`, forward-pointer to `PR-DRIVE-008-CONTRACT`) carry forward unchanged — they are independent of this plan. This plan is the **CUTOVER-WRITE** side of the typed upload protocol, whereas DRIVE-005 / DRIVE-008 are the **CONTRACT-PHYSICAL-DRIVE-SURFACE** gate.

**godlike/06 SSOT cross-check**: every cross-package owns exactly one (linked_issue ↔ owner_capability) pair; no `linked_issue` orphans exist post-cleanup. The 9 items map 1:1 onto the 8 owner capabilities (worker is shared between #1+#6+#7; remote is shared between #8+#9) — the cross-package fan-out is bounded and visible in the Cross-package impact map above.

---

**Cross-reference**:
- `architecture/current.yaml#COMPLETION-CUTOVER-P0-2026-07-04` (wave-tracker anchor + 9 linked_issues + exit_gate).
- `CHANGELOG.md ## Unreleased → ### Added → COMPLETION-CUTOVER-P0 entry` (closure audit-pin).
- `architecture/action-plans/2026-07-03-godobjects-decomposition.md` (the prior precedent — narrative + per-item kill-candidate + execution order + godlike/07 honest limitations).
- `AGENTS.md §"Git-Lesson-2"` (direct-to-main, no `--force`, no PR, no topic branch).
- `AGENTS.md §"Migration Status (Brutal Care Plan)"` (canonical patterns for typed-error contracts + slim-schema forward-pointers).
- `AGENTS.md §Godlike/07` (no fake-availability, typed-error contract, slim-schema append-only ratchet discipline).
