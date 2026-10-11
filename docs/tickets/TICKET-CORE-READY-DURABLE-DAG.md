# TICKET — CORE_READY durable DAG: `CORE_READY → {Docs, finalization} → COMPLETED`

> **Dated artifact — paths reflect the tree at the time of writing.** Retained
> while the ticket is open (see `AGENTS.md` § Documentation rule); verify any
> referenced path against the current tree before acting.

**Date:** 2026-09-13
**Status:** CUTOVER LANDED 2026-10-11 — steps 1–5 are implemented and covered by hermetic tests
(step 1 boundary + resume prerequisite earlier; steps 2–5 in this pass: staged completion strategy,
docs child + handler registration, enqueue at CORE_READY with inline skip, post-core completion port,
aggregator-owned terminal flip). The explicit hermetic no-duplicate-Doc retry criterion is now covered by
`TestPublishRunDocuments_NoDuplicateDocOnRetry`. REMAINING (live-only): one clean end-to-end run proving
no duplicate Drive object, Docs∥finalization overlap wall time, and the measured core_ready_ms→terminal distance.
**Parent:** `docs/PIPELINE-WASTE-AUDIT-2026-09-12.md` §11,
`docs/tickets/TICKET-PIPELINE-CRITICAL-PATH-DEPLOYMENT-2026-09-13.md` (item D1)

## 1. Target

```text
overlay_render ───────┐
audio_publish ────────┤
persist ──────────────┘
          ↓
       CORE_READY
        ↙      ↘
 Docs publish      artifact/Drive finalize
        ↘      ↙
     POSTPROCESS DONE
          ↓
       COMPLETED
```

`SUCCEEDED` / `COMPLETED` keeps its meaning — every requested artifact is published. `CORE_READY`
is exposed separately as `core_ready=true` / `current_stage=CORE_READY`, never as a rename of the
terminal state.

Historical target from the pre-cutover 50.2 s run: core availability was expected in the 35–40 s
band instead of at the terminal flip. The current measured live tail remains open below.

## 2. Landed (step 1 + the prerequisite)

| what | where |
|---|---|
| `StageCoreReady` milestone, deliberately outside `stageOrder` | `capabilities/scripts/model_run.go` |
| `IsCoreCompletable` (scenes + per-language text, says nothing about Docs) | `capabilities/scripts/model_run.go` |
| `IsRunCompletable` = core **and** every requested document, so `COMPLETED` is not weakened | `capabilities/scripts/model_run.go` |
| `ResumeFrom` maps `CORE_READY` → `StagePublishingDocuments` instead of replaying the run | `capabilities/scripts/model_run.go` |
| the boundary is emitted at the `persist()` seam: `CORE_READY` stage, `core_ready_ms` KPI, durable `CurrentStage = CORE_READY` | `capabilities/scripts/runner_execution.go::markCoreReady` |
| `CoreReadyMs` KPI field + `core_ready_ms` milestone case | `kernel/observability/report.go`, `kernel/observability/run.go` |
| **prerequisite:** a resume adopts the checkpointed `Result` | `capabilities/scripts/runner_execution.go::start` |

The prerequisite matters more than it looks. `Repository.Get` already returned the checkpointed
`Result`, but nothing adopted it, while every phase before the resume index is skipped
(`stageSkipped`). A resume from `PUBLISHING_DOCUMENTS` therefore continued against an empty result
and published nothing. Without the fix, "crash between `CORE_READY` and publish is recoverable" —
one of the spec's own acceptance gates — was false.

`markCoreReady` stays silent in two cases, both intentional:

- no post-processing leg is deferred (docs disabled) — it would be a second name for `COMPLETED`;
- the core contract does not hold — the milestone must never advertise a core that is not durable.

## 3. Historical blockers found during recon (superseded by §12 cutover)

The following analysis records the pre-cutover state from 2026-09-13; it is
retained as design history, not as the current implementation status. Steps 1–5
were subsequently implemented as documented in §12.

### B1 — one job cannot be both a worker-spine artifact producer and a parent of children


Historically, `registry_script.go:12` declared `TypeScriptGenerate` as
`ArtifactOwnershipWorkerSpine` + `FinalizationStrategyCompleteWithArtifacts`, while the registry
had no strategy for publishing core artifacts then deferring the remaining work.
A docs child therefore required `script.generate` to act as both a
worker-spine artifact producer and a parent (`ScriptParentState=waiting_children`,
`ListAwaitingAggregation`, `FinalizeAggregateParent`). The pre-cutover registry
had no staged ownership/finalization strategy. This blocker was resolved in the
2026-10-11 cutover by adding `CompleteWithArtifactsThenDefer` and migrating
`TypeScriptGenerate`; see §12. This historical analysis is not a current
architectural blocker.

### B2 — the Docs-vs-finalization overlap is not expressible in the current phase order

Before the cutover, `Worker.runJob` dispatched inline Docs publication and
then called `finalizeJob`, so the two legs were sequential. The cutover moves
Docs to a durable child, making overlap architecturally possible; actual
Docs/finalization overlap and wall-time savings remain unverified until a live
E2E run. See §12 and the deployment-bound ticket.

### B3 — Completion gate and durability (historical)

The pre-cutover concern was that `IsRunCompletable` had no production caller
and the Docs leg lacked a durable child. The current cutover routes completion
through `CompletePostCoreRun`; the aggregator refuses the terminal flip until
all required artifacts are durable, while child retries resume from the run
snapshot. Local tests cover refusal, retry, redrive and restart behavior. Live
Drive durability remains a deployment acceptance item.

## 4. Historical remaining steps (superseded by §12 cutover)

The numbered list below was the pre-cutover plan. It is retained for traceability;
none of these code steps should be reopened as unimplemented without new evidence.

1. **New completion strategy** (B1). Introduce the ownership/strategy pair that allows a phase-1
   spine publication followed by phase-2 child aggregation, with the validator case and a single
   central switch. Migrate `TypeScriptGenerate` in one place.
2. **Docs child job.** New `TypeScriptDocsSibling` (or `script.docs_publish`) type + registry entry
   + handler. The child receives `run_id` plus a reference to the checkpointed snapshot — never a
   copy of the result. It must read any missing voiceover Drive link from the **durable** DB state
   and retry/fail typed, never from the process-local `voiceoverPublishDrainer`.
3. **Enqueue at `CORE_READY`.** The runner enqueues the child right after `markCoreReady`, so Docs
   and the worker's finalization start together instead of chaining.
4. **`POSTPROCESSING` state** and the terminal flip owned by the aggregator, with `COMPLETED`
   reachable only once every required artifact (including documents) is persisted.
5. **Retire the process-local drain** from the Docs path so a restart between `CORE_READY` and
   publish loses nothing.

## 5. Acceptance gates (from the spec, made executable)

- [x] `CORE_READY` does not wait for Google Docs/Drive — **landed**: the deferred child is
      enqueued after the durable `persist()` boundary; inline document publication is skipped.
- [x] `COMPLETED` still means every required artifact is persisted — **landed**: `CompletePostCoreRun`
      and the parent aggregator fail closed until the requested documents and core are durable.
- [x] Crash/restart between `CORE_READY` and publish is recoverable — landed: `ResumeFrom`
      re-enters at `PUBLISHING_DOCUMENTS`, adopts the checkpoint result, and the deferred child
      reads the durable run snapshot rather than process-local state (steps 2–5; hermetic tests in
      `docs_deferred_test.go` and `docs_deferred_aggregator_test.go`).
- [x] No duplicate Doc on retry — `TestPublishRunDocuments_NoDuplicateDocOnRetry` verifies two
      retries do not republish an already-durable language, returns the full existing document set,
      and includes a positive control that publishes when the document is absent.
- [x] No goroutine is required for durability — the durable broker child, run snapshot and
      aggregator own recovery; the completion path does not depend on the process-local drain
      (cutover step 5, `runner_docs_publish.go`).
- [x] Docs work is handed to a durable child and the completion aggregator can finalize independently — cutover landed (§12), locally covered by deferred-child and aggregator tests.
- [ ] Live acceptance: prove Docs and finalization overlap on one real E2E payload and measure `core_ready_ms`→terminal; see the deployment-bound ticket.

## 6. Definition of done

The code cutover and hermetic gates are landed (§12). Full acceptance still requires one live E2E
payload reporting the `core_ready_ms`→terminal distance, proving Docs/finalization overlap, and
reconciling Drive artifacts without loss or duplication.

## 7. Measured baseline (2026-09-13)

Reproduce with:

```bash
bash tests/operational/measure_core_ready_tail.sh
```

This is a historical pre-cutover measurement procedure. Its old-run corpus and tail baseline
remain useful for comparison, but do not certify the current deferred Docs path; rerun it on a
post-cutover live run to close the remaining acceptance criteria.

### The run that emitted the real boundary

`person-overlay-drive/full-person-overlay-drive-20260913T084243Z-18144.json`
(`job_1789288964299429537_f06e9fdd`, Ada Lovelace person overlay, `en`, docs enabled) — the only
artifact in the corpus that contains the `CORE_READY` stage.

| quantity | value |
|---|---|
| wall | 102 595 ms |
| boundary on critical path | `… audio_publish → persistence → **CORE_READY** → document → complete_finalize → post_writer_finalize` |
| `core_ready_ms` (wall − tail) | **90 974 ms** |
| tail to terminal flip | **11 621 ms (11.3% of the run)** |
| ↳ `document` (Google Docs leg) | 5 975 ms (`document.prepare` 2 + `document.publish` 5 959) |
| ↳ `complete_finalize` | 12 ms |
| ↳ `post_writer_finalize` (artifact/Drive finalization) | 5 634 ms |

Independent cross-check from the wall-clock event timeline: `job_running` 08:42:44Z, `job_completed`
08:44:27Z → 103 s total; 08:44:27Z − 11.621 s = **08:44:15.4Z**, i.e. the core was available ~91.0 s
into the run. That matches `wall − tail` to within a second.

So the render, the final audio and the canonical script result were already durable **11.6 s before**
the job flipped to `SUCCEEDED`, split roughly evenly between the Google Docs leg and the worker
finalization leg.

### Corpus distribution (38 recorded E2E runs)

| metric | min | median | max |
|---|---|---|---|
| tail ms | 9 120 | 11 649 | 83 208 |
| core ms | 33 101 | 94 971 | 308 224 |
| tail % of wall | 3.5% | 11.3% | 39.2% |

The tail is close to a flat cost (~9–13 s) rather than a fraction of the run, which is why it looms
largest on short runs: the fastest recorded run (44 022 ms) spends 24.8% of its life after the core
is durable. The two worst outliers are not Docs at all — they are pathological
`post_writer_finalize` legs of 44 045 ms and 78 623 ms (produce 39.2% and 24.3% tails), which makes
that leg worth profiling independently of the Docs work.

### Note on where the boundary is observable

`core_ready_ms` is recorded in the observability report (`KPIs.CoreReadyMs`,
`kernel/observability/report.go`) and logged at the boundary, but it is **not** serialized into the
job result artifact. In an artifact the boundary is only visible as the zero-duration `CORE_READY`
stage appended to the critical path, which is what the script reads. If the KPI is meant to be
auditable from a stored run without a log grep, the artifact needs to carry it.

> **RESOLVED in this pass** — `TimingSummary` now projects `CoreReadyMs`
> (`json:"core_ready_ms"`), so the boundary travels with the artifact. See §10.

---

## 8. Root cause of the tail outliers — the Drive upload path

Before building the durable child it was worth knowing *what* the tail actually is. It is not the
Docs leg. It is the Drive leg, and it is not a tuning problem.

### 8.1 Two runs are 6–14x the norm, and both Drive legs inflate together

`post_writer_finalize` is normally ~5.6 s. Two runs stand out:

| run | post_writer_finalize | slowest single call | tail | tail % |
|---|---|---|---|---|
| `jordan-…20260911T114528Z-28836` | 78 623 ms | 78 468 ms | 83 208 | 39.2% |
| `jordan-…20260912T123227Z-24434` | 44 045 ms | 43 904 ms | 50 204 | 24.3% |

The decisive detail is that in the **same run** the final-audio upload spikes too, while it lives in
a different stage:

| run | `drive.upload` (stage `audio_publish`) | slowest `post_writer_finalize` call |
|---|---|---|
| `…114528Z-28836` | 80 947 ms | 78 468 ms |
| `…123227Z-24434` | 38 994 ms | 43 904 ms |
| healthy run | 5 055 ms | ~5 600 ms |

Two *independent* Drive operations inflating to the same order of magnitude in the same run is not a
slow file. It is a shared resource stalling.

### 8.2 It is throughput collapse, not file size

`drive.upload` measures the final-audio upload. Across the corpus:

| run | upload ms | audio MB | KB/s |
|---|---|---|---|
| `…114528Z-28836` | 80 947 | 7.04 | **89** |
| `…123227Z-24434` | 38 994 | 7.00 | **184** |
| `…114125Z-16503` | 40 283 | **0.87** | **22** |
| healthy `jordan` runs | ~5 800–6 500 | ~7.1 | **~1 150** |
| healthy `person` run | 5 055 | 0.93 | ~186 |

A **0.87 MB** file taking 40 s at 22 KB/s rules out size entirely. Corpus distribution:
`n=39, min 3 937 ms, p50 5 548 ms, p90 9 079 ms, max 80 947 ms` — a long, thin tail, not a bimodal
size effect.

### 8.3 Mechanism — everything Drive serializes behind one token refresh

The upload path is structurally exposed:

1. **Non-resumable by default.** `platform/drive/uploader_put.go:480` —
   `resumableUploadThreshold = 16 * 1024 * 1024`. Anything under 16 MB takes
   `call.Media(f).Context(ctx)`: a **single-shot, all-or-nothing** upload with no chunking and no
   resume point. Every generated artifact in this corpus (0.87–7.34 MB) is far below the threshold,
   so the most robust path is never used for the files that matter most.
2. **No client timeout.** `platform/drive/auth.go:93` — `oauth2.NewClient(ctx, httpSource)` sets no
   `http.Client.Timeout`. Nothing bounds a stalled request except the caller's context, so a stall is
   paid in full rather than failing fast into the retry policy.
3. **The token source is a process-wide serialization point.** `platform/drive/token.go:24` —
   `refreshingTokenSource.Token()` takes `r.mu.Lock()` and holds it across **both** the refresh call
   and `SaveToken` (a disk write), returning only afterwards. `NewGoogleHTTPClient` then wraps it in
   `oauth2.TokenSource(...)` (= `ReuseTokenSource`), whose mutex is held across the refresh too. So a
   single slow/stalled refresh or `SaveToken` **blocks every concurrent Drive request in the
   process** for its whole duration. The refresh HTTP call itself carries no timeout either.

That mechanism is exactly the signature observed in 8.1: unrelated Drive operations spiking together.

Retry is *not* the explanation: `pkg/retry.DefaultOptions()` is 3 attempts with 1 s → 2 s backoff
(~3 s total), far too small for 80 s. The 80 s is genuine in-request time. Note also that
`finalize/drive_publish` reports 4–9 **calls** with work far exceeding the stage wall (180 850 ms of
work in a 78 623 ms wall), so internal retry attempts are summed into `work_ms` while the call count
stays low — retries are invisible in the operation counts.

### 8.4 What would confirm it, and the fixes that follow

Confirm from the existing logs, not new instrumentation: `refreshingTokenSource` already emits
`"Refreshed OAuth token saved successfully"` (debug) and `"Failed to save refreshed token"` (warn).
If a refresh timestamp coincides with an outlier's Drive leg, the mechanism is proven.

Candidate fixes, cheapest first — none implemented here, because each is a behavioural change to the
live Drive path that deserves its own measurement:

- Move `SaveToken` **outside** the mutex (it is best-effort persistence, explicitly non-fatal on
  error) so a slow disk write cannot gate every Drive request.
- Give the token refresh and the upload request a bounded timeout so a stall fails fast into the
  retry policy instead of consuming the run.
- Route generated audio/video artifacts through the resumable path (lower the threshold for this
  class, or make it explicit per artifact kind) so a stall resumes from the last confirmed byte.
- Make internal retries visible: `drive_publish` reporting 1 call for a 80 s transfer that retried
  hides the very signal an operator needs.

---

## 9. Step 2–5 assessment — the completion contract is the blocker

The single blocker named in §3 is real and load-bearing, and one additional landmine was found.

### 9.1 The constraint

`capabilities/jobs/registry_types.go` — `CompletionDeclaration.Validate()` switches on ownership and
*forces* the pair:

```go
case ArtifactOwnershipNone, ArtifactOwnershipApplication:
    -> must be FinalizationStrategyLegacyComplete
case ArtifactOwnershipWorkerSpine:
    -> must be FinalizationStrategyCompleteWithArtifacts
```

`TypeScriptGenerate` is registered as `WorkerSpine` + `CompleteWithArtifacts`
(`registry_script.go`). There is no vocabulary for "publish my core artifacts, then hand the deferred
legs to a child". The two existing strategies are *terminal choices*, not staged ones, so the durable
DAG cannot be expressed without adding one.

### 9.2 The landmine — a test encodes the wrong equivalence

The production projection is ownership-based (`registry_capabilities.go:190`,
`ProducesArtifactsMap()` keys on `ArtifactOwnership == ArtifactOwnershipWorkerSpine`). But
`registry_completion_policy_test.go::TestCompose_CompletionPolicyHasOneCanonicalProjection` asserts:

```go
want := reg.FinalizationStrategy(jobType) == FinalizationStrategyCompleteWithArtifacts
_, got := artifactTypes[jobType]
```

It equates "produces artifacts" with "strategy is exactly CompleteWithArtifacts". A new
*complete-with-artifacts-and-defer* strategy under `WorkerSpine` ownership would keep the production
projection correct (ownership unchanged) while failing this test. That is at least a **loud** failure
rather than a silent one, but the implementer must know: adding the strategy requires updating this
rule to be ownership-derived in lockstep, or the gate will read as a false regression.
`TestRegistry_CompletionDeclarationAcceptsCanonicalPolicies` also enumerates the three accepted pairs
exhaustively, so the new pair must be added there deliberately.

### 9.3 Why nothing was implemented here

Adding the new enum value without the child job, the `POSTPROCESSING` state and the retiring of the
`voiceoverPublishDrainer` barrier produces **two authorities on the terminal** — strictly worse than
the current state, and precisely what §5 warns against. It would also add unused vocabulary, which is
the same class of dead surface removed in §10 (`DirectDriveRoot`). Both are deliberate omissions, not
oversights. Steps 2–5 remain the next unit of work, and §9.1–9.2 are the exact touchpoints.

---

## 10. Landed in this pass (2026-09-13, second pass)

| change | evidence |
|---|---|
| `core_ready_ms` is projected into `TimingSummary`, so a stored run is auditable without a log grep | `kernel/observability/timing_summary.go`; `TestTimingSummary_CarriesCoreReadyMs`, `TestTimingSummary_CoreReadyMsZeroWhenBoundaryNotReached` |
| the `overlay` child-folder segment has ONE canonical owner | `finalization.OverlayChildFolder` (`capabilities/finalization/types_drive_layout.go`), consumed by `renderinggen/overlay_artifact_publisher.go`, `renderinggen/overlay_receipt.go`, `platform/drive/artifact_publisher_adapter.go` and `app/wiring/overlay_handlers.go`. The artifact **source key** `"overlay"` and the `safeName` filename fallback are deliberately NOT merged — they are different facts |
| dead `DirectDriveRoot` removed | `direct_drive_root` appeared exactly once in the tree (its own struct tag), with zero production producers; field + publisher branch + the test that existed only to exercise it are gone. `TestArtifactPublisherAdapter_Publish_HappyPath` already covers the surviving no-subpath path |
| the measurement is now a gate | `tests/operational/measure_core_ready_tail.sh --gate`, exposed as `make gate-core-ready-tail` |

### The gate

```bash
make gate-core-ready-tail                        # corpus, all invariants
make gate-core-ready-tail MAX_TAIL_MS=30000      # add a tail ceiling
make gate-core-ready-tail CORE_READY_ENFORCE_PROJECTION=1   # require core_ready_ms on new runs
make gate-core-ready-tail RESULTS_DIRS=tests/operational/results/<run-dir>
```

It fails closed on three things: the two tail reconstructions disagreeing beyond the run's own
`unattributed_ms` (the timing model regressed, so no tail number may be quoted); a run over
`MAX_TAIL_MS`; and — opt-in — an artifact that carries a `CORE_READY` stage without `core_ready_ms`.
The projection check is opt-in because artifacts recorded by a pre-projection binary legitimately
have the stage without the field; enforcing it on the historical corpus would fail forever.

Current corpus result: **39 runs, 39 reconciled**, tail median 11 721 ms, median 11.3% of wall.

---

## 11. Where the lever actually is

Removing the entire tail takes the marked run from 102.6 s to 91.0 s. The head dominates:
`scene_analysis` is **52 589 ms = 51.3%** of that run. The tail is the correct *honesty* fix and the
top-ranked *low-risk* win, but it is not the largest available one. Ranked by size, the remaining
levers are: the head (`scene_analysis`), then the Drive upload path in §8 (which costs both the tail
*and* `audio_publish`), then the GPU gate / Chronon process-per-render (`overlay_render`: 6 227 ms of
work behind 11 160 ms of wall). Sections §9 and §10 of the parent audit stay deployment-bound.

## 12. Cutover landed (2026-10-11)

Steps 1–5 are implemented; the blockers in §3/§9 are resolved as follows
(all hermetic suites green on the landing tree):

| step | change | where |
|---|---|---|
| 1 (strategy) | `CompleteWithArtifactsThenDefer` accepted under `WorkerSpine`; `TypeScriptGenerate` migrated | `capabilities/jobs/registry_types.go`, `registry_script.go` |
| 2 (child) | `script.docs_publish` type + handler registered in production wiring | `kernel/script` (`TypeDocsPublish`), `scripts/jobs/docs_publish_handler.go`, `app/wiring/wire_script.go` |
| 3 (enqueue) | child enqueued at `markCoreReady` (ActiveKey `docs-publish:<parent>:<run>`, idempotent re-enqueue); pipeline skips inline `documents()`/`complete()` when deferred | `scripts/runner_execution.go`, `runner_execution_pipeline.go`, `app/wiring/build_bundles_domain_scripts.go` (+ canonical `DocsPublishActiveKey`/`DocsPublishCorrelationID`) |
| 4 (terminal) | `CompletePostCoreRun` (idempotent; refuses via `ErrPostCoreNotCompletable` without every artifact); aggregator flips the broker parent only after completion | `scripts/runner_docs_publish.go`, `scripts/jobs/parent_aggregator.go` (`aggregateDocsDeferred`), `app/wiring/lifecycle_worker.go` |
| 5 (drain) | no process-local state on the completion path: the child re-reads the durable snapshot, publication stays idempotent per (run, language), broker retry resumes at `PUBLISHING_DOCUMENTS` | `scripts/runner_docs_publish.go`, `scripts/model_run.go` (`ResumeFrom`) |

Tests (all hermetic, no server/GPU/Drive): `scripts/docs_deferred_test.go`
(deferral flag, fail-closed enqueue, idempotent/refusing completion, key
canon), `scripts/jobs/docs_deferred_aggregator_test.go` (wait / complete+flip /
refused-never-flips / re-drive / exhausted-fail / retryable-wait / ok=false
gate / deferral decision table). Full `scripts/...`, `jobs/...`,
`app/wiring/...` suites green.

Verified locally after cutover:
`go test ./internal/capabilities/scripts -run 'TestDocsDeferred|TestPublishRunDocuments_NoDuplicateDocOnRetry|TestRunner_IdempotentRestart_NoDuplicateWrites' -count=1`
passes, including the explicit no-duplicate-Doc retry test and positive control.

Live-only remainder (needs a real Docs/Drive deployment, unreachable from a
checkout — same class as the clip.render restart-mid-render criterion): prove no
duplicate Drive object across the real outbox/publication boundary, measure
Docs ∥ finalization overlap on one E2E payload, and report
`core_ready_ms`→terminal distance.
