# TICKET — clip.render critical path: audit verdicts + P0.5 settle-pool guardrail

> **Dated artifact — paths reflect the tree at the time of writing.** Verify any
> referenced path against the current tree before acting.

**Date:** 2026-09-13
**Status:** the code-level remainder is CLOSED, including the item-3
locator-first artifact path (§2.7). What is left is deployment-bound (live
GPU/Chronon/RenderingGen/Drive measurement and the production object-store
endpoint).
**Scope:** `POST /api/clips/render` end to end (PipelineGen `refactored/` +
RenderingGen + Chronon3d).
**Predecessor:** `docs/CLIP-RENDER-DEMOLITION-2026-09-12.md`,
`docs/tickets/TICKET-CLIP-RENDER-ASYNC-COMPLETION.md`.

## 0. Why this ticket exists

A fresh eight-item audit of `POST /api/clips/render` listed a sequence of
remaining bottlenecks. Re-checking each one against the current tree shows that
**six of the eight were already closed by the 2026-09-13 clip.render audit**; the
audit text predates that pass. Only **P0.5 (dedicated settle pool)** was still
genuinely open, and it is landed here. The seventh item (final-artifact
round-trip) and the eighth (final demolition) remain, and both are deployment /
cross-repo work rather than a missing abstraction.

The purpose of this document is to stop the same items from being re-opened:
each verdict below names the file that decides it.

## 1. Verdict per item (evidence, not assumption)

| # | Item from the audit | Verdict | Decisive evidence |
|---|---|---|---|
| 1 | Chronon can still start in CLI mode per job | **CLOSED** | `renderinggen/internal/config/config.go::applyDefaults`: the `gpu-vulkan-native` profile defaults `Chronon.Mode = "ipc"`; every shipped GPU config (`renderinggen/config.yaml`, `infra/native/*`, `infra/docker/worker-config*`) declares `mode: ipc`. An explicit `mode: cli` remains a deliberate cold-spawn opt-out (pinned by `TestGPUVulkanNativeProfilePreservesExplicitCLITransport`). |
| 2 | `settle` occupies a Master worker while waiting RenderingGen | **LANDED HERE (§2)** | New `job.PayloadNotMatch` + `ClaimNextMatchingExcluding`, `jobs.clip_render_settle_workers` config, and a dedicated settle pool in `lifecycle_job_runner.go`. The general pool now EXCLUDES `render_phase=settle`. |
| 3 | Artifact round-trip: RenderingGen store → PG download → local staging copy | **LANDED HERE (§2.7)** | `Settle` is locator-only (no download): `RenderOutcome` carries `StorageKey/ArtifactURL/ContentType`; the Drive intent carries them too; the uploader streams object-store → Drive via `drive/uploader_source.go`. Local staging survives only when a consumer materialized bytes (`Materialize`). |
| 4 | Double certification/probe (RenderingGen + PipelineGen) | **CLOSED** | `worker_completion.go` gates on `OutputProbeFromCertified(outcome)`; the local `RustOutputProber` and the `OutputProber` port were DELETED. `grep -rn 'OutputProber\|ProbeOutput' internal --include=*.go` returns only tombstones/comments. |
| 5 | Sidecar subtitles still force synchronous Drive | **CLOSED** | `ports.go::ClipRenderDriveDeliveryRequest.Sidecar` makes the delivery intent a BUNDLE (video + optional ASS); the synchronous branch and `ClipAsyncDriveEnabled` were DELETED (`types_misc.go`, `cliprender_publisher.go`). |
| 6 | Transcript default can still run ASR; SQLite media fallback; per-clip folder resolve | **CLOSED** | `request.go` defaults `TranscriptModeReuse` and DELETED `reuse_or_generate` (unknown modes fail closed); `clip_render_runtime.go::newClipRenderMediaResolver` fails closed when `MediaPostgres == nil` (no SQLite read surface); `drive/folder_manager.go` `EnsureFolder` is singleflight-deduped per `(parent, name)`. |
| 7 | Blocking `Render()` still alive for localization | **CLOSED** | `localization/adapters/render.go` drives `a.renderer.Submit(...)` then `a.renderer.Settle(...)`; the `RenderExecutor.Render` blocking form is deleted from `ports.go`. |
| P2 | Drive folder resolved per clip in a batch | **CLOSED (in-process)** | `folder_manager.go` singleflight keyed `(parentID, name)` coalesces concurrent resolves; the worker resolves once per job and the publisher receives the resolved leaf. |
| P3 | Very verbose logging publisher | **CLOSED** | per-phase publication trace is Debug; canonical events are accepted/completed/failed (`CLIP-RENDER-DEMOLITION-2026-09-12.md` §2). |

## 2. LANDED — P0.5 dedicated clip.render settle pool

A `clip.render` settle continuation owns a worker slot for the whole remote
render (`render_phase=settle` → `WaitTerminal`). On one shared pool, N clips in
flight pin N general slots doing nothing but waiting. The split needed an
EXCLUSION, not just a positive match: an absent `render_phase` is the
back-compatible submit phase, so a positive `render_phase=submit` matcher would
have stranded historical unscoped payloads, while scoping only the settle pool
would leave the general pool still claiming settle jobs.

### 2.1 Contract: payload exclusion

`internal/kernel/job/payload_match.go`

* `PayloadNotMatch` — excludes jobs whose payload carries ANY listed key=value.
* `ExcludesPayload(payload, notMatch)` — exact complement of `MatchesPayload`
  over the same scalar projection. A malformed/empty payload carries none of the
  excluded pairs, so it is NOT excluded (the default pool must never lose a job
  to a decode failure).
* `PayloadExcludingClaimer` — the optional store capability, embedding
  `PayloadScopedClaimer` because a phase pool still needs positive scoping.

### 2.2 Store

`internal/platform/sqlite/jobs/repository_claims.go`
`ClaimNextMatching` and the new `ClaimNextMatchingExcluding` share one
`claimNextScoped` body: bounded QUEUED candidate window, payload hydration, then
`MatchesPayload` AND `ExcludesPayload`, then the same CAS `Start()` claim. An
empty match+exclude is the historical unscoped `ClaimNext`.

### 2.3 Worker + runner

`internal/capabilities/jobs`
`WorkerDeps.PayloadNotMatch` / `Worker.notMatch` /
`RunnerConfig.PayloadNotMatch` → `buildWorkers`. `Worker.claimNext` routes to
`ClaimNextMatchingExcluding` when an exclusion is set and **fails closed** if the
store cannot honour it (never silently widened).

### 2.4 Wiring

`internal/platform/config/jobs.go` — `clip_render_settle_workers`
(`VELOX_CLIP_RENDER_SETTLE_WORKERS`, default 16; `0` disables the split and
restores the single unscoped pool).

`internal/app/wiring/lifecycle_job_runner.go`

```text
Jobs.ClipRenderSettleWorkers > 0  AND  Features.ClipRenderEnabled
        │
        ├── general pool   JobTypes=nil, PayloadNotMatch{render_phase: settle}
        └── settle pool    JobTypes=[clip.render], PayloadMatch{render_phase: settle}
                           Workers = ClipRenderSettleWorkers
```

Both pools share the registry, broker port, job ledger, observer and resource
sampler; the step starts both under the same `job-runner` lifecycle step.

### 2.5 Tests

* `kernel/job/payload_match_test.go` — `TestExcludesPayload` (incl. malformed +
  empty payload not excluded), `TestValidatePayloadNotMatch`.
* `platform/sqlite/jobs/repository_claims_matching_test.go` —
  `TestClaimNextMatchingExcluding_SkipsExcludedPhase` (skips a higher-priority
  settle and leaves it claimable by the settle pool),
  `_OnlyExcludedJobsReturnsNil`, `_EmptyBothIsUnscoped`.
* `capabilities/jobs/worker_payload_not_match_test.go` — routing, fail-closed on
  a non-excluding store, match-only behaviour unchanged, runner propagation.
* `app/wiring/lifecycle_job_runner_test.go` — `TestSettleWorkerBudget` (feature
  off / budget ≤ 0 / positive), builder fail-closed without a root.

### 2.6 Repair landed alongside: the cliprender test package compiled again

The 2026-09-13 prober deletion was incomplete: `internal/capabilities/cliprender`
no longer compiled for tests (`go vet ./...` failed repo-wide), so the
certification consolidation in item 4 could not be verified at all.

```text
certify_test.go                 → tested ReconcileCertifiedFacts /
                                  ErrCertifiedFactsMismatch, neither of which
                                  exists any more
worker_test.go / bench_harness_test.go / worker_single_pass_overlay_test.go
                                → wired WithOutputProber(...), a method deleted
                                  with the OutputProber port
```

Repaired to match the shipped architecture:

* `certify_test.go` now tests the REAL projectors — `OutputProbeFromCertified`
  (every certified dimension + container/profile normalization + fail-closed
  `ErrUncertifiedOutput`), the legacy flat-summary path, `RenderOutcomeFacts`
  resolution, and the contract gateway (wrong GOP / colour range now fail).
* the fake render boundaries inject a RenderingGen-shaped certified `Facts`
  (`certifiedTestOutcome`), which is what a real boundary always returns; the
  `WithOutputProber` wiring and the `fakeOutputProber` / `benchProber` types are
  gone.
* `TestWorker_RecordsRunReportStages` now expects the real serial critical path
  `clip.prepare → clip.render → clip.probe → clip.publish`.

Gate: `go test ./internal/capabilities/cliprender/...` green,
`go test -run TestScenario` (the canonical benchmark matrix) green, and
`go vet ./...` clean.

## 2.7 LANDED — locator-first artifact (item 3, code half)

Before this change, one 500 MB clip moved its bytes six times: RenderingGen
object store → download into the job workspace (hashing), workspace → durable
staging copy, staging → Drive. `Settle` **required** the download, and
`delivery.Publisher` **required** a `LocalPath`. The certified locator
(`storage_key` + `artifact_url` + `sha256` + `size` + facts) already existed in
the RenderingGen artifact; it simply was not the canonical artifact identity.

The render boundary now certifies the locator and never writes the video to disk:

```text
Settle        → RenderOutcome{StorageKey, ArtifactURL, ContentType, SHA256, SizeBytes, Facts}
Publish       → ClipRenderDriveDeliveryRequest{LocalPath: "", StorageKey, ArtifactURL, ContentType, …}
outbox        → delivery.PublishRequest{SourceURL, ContentType, SizeBytes}
uploader      → drive.httpObjectSource{Read/ReadAt/Close}  →  Drive
```

* `capabilities/cliprender/ports.go` — `RenderOutcome` gains the locator fields;
  `RenderArtifactMaterializer` is the OPTIONAL lazy-fetch capability;
  `RenderPublishInput` carries the locator; the delivery intent carries
  `storage_key/artifact_url/content_type` with `local_path,omitempty`.
* `platform/renderinggen/queue_client.go` — `Settle` decodes the certified facts
  and returns the locator (`OutputPath == ""`). `Materialize(ctx, outcome, dest)`
  is the lazy half: downloads into `dest`, verifying size + certified digest
  while streaming. Fail-closed on a nil outcome, no URL, or no destination.
* `capabilities/cliprender/adapters/cliprender_publisher.go` — the synchronous
  Drive branch is DELETED (publication is unconditionally asynchronous);
  `publishAsyncDrive` stages locally **only** when `in.OutputPath != ""`.
* `capabilities/delivery/types.go` + `platform/drive/publisher*.go` —
  `PublishRequest`/`PutFileRequest` gain `SourceURL` + `ContentType`.
* `platform/drive/uploader_source.go` (new) — `httpObjectSource` implements
  `io.Reader` (one GET) and `io.ReaderAt` (bounded HTTP Range GET per call), so
  the resumable Drive path works with **no local file**.
* `app/wiring/clip_render_drive_outbox.go` — an intent is valid with EITHER a
  staged `LocalPath` OR a certified `ArtifactURL`; the URL is streamed and no
  staged file is required or removed.
* `capabilities/localization/adapters/render.go` — the one production consumer
  that genuinely needs bytes materializes on demand through the optional
  `RenderArtifactMaterializer`, failing closed if the boundary cannot.

### 2.7.1 Tests

* `platform/drive/uploader_source_test.go` — Range + sequential reads against a
  local HTTP object store; `openUploadSource` fail-closed rules.
* `platform/renderinggen/clip_executor_async_test.go` — `Settle` returns an empty
  `OutputPath` and a populated locator; `Materialize` writes the exact certified
  bytes.
* `capabilities/cliprender/adapters/cliprender_publisher_test.go` — a
  locator-only render stages nothing, commits an intent with the locator, and
  fails closed with neither path nor URL.
* `app/wiring/clip_render_drive_outbox_test.go` — a locator-only intent passes
  the URL + size to the publisher with `LocalPath == ""`.
* `capabilities/localization/adapters/render_test.go` — the materializing
  consumer path.

## 3. Still open — deployment-bound

These are not missing abstractions and must not be "fixed" by editing code and
hoping.

### 3.1 Item 3 — local artifact round-trip (code landed; live gate remains)

The locator-first path is implemented (§2.7): RenderingGen's `storage_key +
artifact_url + sha256 + size + facts` is committed as the canonical artifact and
the outbox streams object-store → Drive. What is still deployment-bound:

* the production object store must serve **HTTP Range** requests to the
  PipelineGen outbox host (the resumable Drive path is `io.ReaderAt`-backed), and
  must be reachable with the credentials the outbox runs under;
* RenderingGen's `artifact_url` must be a stable, directly-readable locator at
  settle time (not a short-lived signed URL that can expire before the outbox
  drains).

Measurement that decides it: for a 500 MB artifact, the `render → publish` wall
shows **no local write of the video at all** on the canonical path, with the same
certified SHA published to Drive.

### 3.2 Item 8 — final demolition of the CLI opt-out (CODE LANDED)

RenderingGen `config.go::validate` now rejects `mode: cli` for the
`gpu-vulkan-native` profile (`<profile> requires chronon.mode=ipc`), the default
remains `ipc`, no shipped GPU config declares `cli`, and the obsolete
`TestGPUVulkanNativeProfilePreservesExplicitCLITransport` was replaced by
`TestGPUVulkanNativeProfileRejectsCLITransport`. **The live 1/10/50 cold+warm
certification** of the IPC daemon on the production host is still the
deployment gate that confirms the enforced transport is healthy — the code no
longer permits the CLI opt-out, so that measurement validates the daemon, not
the selection logic.

### 3.3 Cross-process folder resolve

The singleflight is in-process. A batch split across processes can still resolve
the same leaf folder more than once. The cheap fix is upstream (resolve at
batch/script level and pass the resolved leaf id), not a distributed lock.

## 4. Definition of done

Items 2 and 3's code halves are done. Item 2: the general pool can no longer
claim a settle continuation, the dedicated pool owns it, and both behaviours are
pinned by tests that run without a GPU. Item 3: `Settle` no longer downloads the
artifact, the certified locator is committed and streamed to Drive, and only a
consumer that needs bytes calls `Materialize` — all pinned by tests with a local
HTTP object store. Item 8 (CLI demolition) and cross-process folder resolution
close only against a live GPU/Chronon/RenderingGen/Drive deployment with a
preserved measurement.
