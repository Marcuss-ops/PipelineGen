# TICKET — CORE_READY durable DAG: `CORE_READY → {Docs, finalization} → COMPLETED`

**Date:** 2026-09-13
**Status:** PARTIALLY LANDED — step 1 + the resume prerequisite are in; steps 2–5 are blocked on a
central completion-contract change (see §3)
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

Measured target: with the recorded 50.2 s run, core availability should land in the 35–40 s band
instead of at the terminal flip.

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

## 3. Blockers found during recon (why steps 2–5 are not a mechanical edit)

### B1 — one job cannot be both a worker-spine artifact producer and a parent of children

`registry_script.go:12` declares `TypeScriptGenerate` as
`ArtifactOwnershipWorkerSpine` + `FinalizationStrategyCompleteWithArtifacts`, and
`registry_types.go:80-90` validates the pair: `worker_spine` **requires** `complete_with_artifacts`,
and `none`/`application_transaction` **require** `legacy_complete`. There is no strategy that means
"publish my own core artifacts now, then hand the rest to a child".

A docs child job would require script.generate to also be a parent
(`ScriptParentState=waiting_children`, `ListAwaitingAggregation`, `FinalizeAggregateParent` —
`scripts/jobs/parent_aggregator.go`). Today that aggregation model belongs to the **batch** path
(`TypeScriptGenerateItem` children); the single-item `/api/script/generate` job does everything
inline and completes via the spine. Mixing the two in one job type is exactly the "two authorities
for one fact" the repo's discipline forbids.

**This is the "cambiare l'ownership in un solo punto centrale" the spec names.** It requires a new
`ArtifactOwnership`/`FinalizationStrategy` pair (e.g. `worker_spine_then_children`), a validator
case, and a defined two-phase terminal (core artifacts published → flip to `waiting_children` →
aggregator finalizes). That is an architectural decision, not an edit.

### B2 — the Docs-vs-finalization overlap is not expressible in the current phase order

`Worker.runJob` (`capabilities/jobs/worker_execution.go:334`) is:

```text
Dispatch(jobCtx)                      ← Runner.Execute: …, persist, documents()
finalizeJob(finalizationCtx)          ← post_writer_finalize: Drive publish + terminal flip
```

Docs publication happens **inside** `Dispatch`; the ~9 s of artifact/Drive publication happens
**after** it, in `finalizeJob`. They are strictly sequential, so the current architecture cannot
overlap the 5.1 s Docs publish with the 9.3 s finalization — no ordering tweak inside the runner
can do it. The overlap requires the docs work to leave `Dispatch`, i.e. B1.

### B3 — `IsRunCompletable` had no production caller

It is only referenced by tests, so the split in §2 changed no behavior. Harmless, but it also means
the terminal state was never gated by the contract in production; any future gate must be added
deliberately.

### B4 — `JobFinalizer` retries are the only current durability for the tail

`finalizeJobArtifactPath` retries transient storage contention 5×/exponential
(`worker_finalize_paths.go`), and the run-level retry re-enters via `ResumeFrom`. There is no
durable outbox for the Docs leg specifically; with §2's resume fix, the run checkpoint is the
durable snapshot a child would receive — which is the natural input for step 2 and is now correct.

## 4. Remaining steps

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

- [x] `CORE_READY` does not wait for Google Docs/Drive — **landed**: the boundary is recorded at
      `persist()`, before `documents()`.
- [x] `COMPLETED` still means every required artifact is persisted — **landed**:
      `IsRunCompletable` = core + documents, and the terminal state is unchanged.
- [x] Crash/restart between `CORE_READY` and publish is recoverable — **landed** for the resume
      half: `ResumeFrom` re-enters at `PUBLISHING_DOCUMENTS` and the result is adopted from the
      checkpoint. The child-job half arrives with step 2.
- [ ] No duplicate Doc on retry — largely already true (per-language idempotent checkpoint inside
      `runDocumentPhase`; `UpsertDocument` keyed by run+language); needs an explicit test once the
      child exists.
- [ ] No goroutine is required for durability — blocked on steps 2–4.
- [ ] Docs and finalization run concurrently — blocked on B1/B2.

## 6. Definition of done

Steps 1–5 land with the gates in §5 green, and the measured distance between `core_ready_ms` and
the terminal flip is reported for the same E2E payload. Until then, `core_ready_ms` makes the tail
measurable, which is the precondition for claiming any improvement.

## 7. Measured baseline (2026-09-13)

Reproduce with:

```bash
bash tests/operational/measure_core_ready_tail.sh
```

The script derives the boundary from the recorded critical path: everything strictly after the
`persistence` entry is post-core work. Runs recorded before the marker was introduced are
reconstructed by the same rule, and the reconstruction is cross-checked against the stage sum
(`document + complete_finalize + post_writer_finalize`); it must agree within the run's own
`unattributed_ms`. All 38 recorded runs reconcile.

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
