# TICKET — clip.render: async submission/completion boundary (Wave B)

**Priority:** P0 — this is the throughput bottleneck, not RenderingGen/Chronon.
**Status:** IN PROGRESS — the continuation primitive EXISTS (see §1.1) and the
render boundary is now split into `Submit`/`Settle`. Remaining: the worker phase
dispatch + composition-root wiring (§7).
**Owner:** `internal/capabilities/cliprender` (worker) + `internal/kernel/job` (continuation) + `internal/app/wiring`.

## 1. Problem

The `clip.render` Master handler holds its worker slot while a remote render is
in flight. Today's flow, grounded in the code:

```text
Worker.Handle                                   internal/capabilities/cliprender/worker.go
  → preparer.Prepare                                        (parallel waves, bounded)
  → subtitles.Compile
  → Compile(...) → sealed ClipRenderPlanV1
  → w.renderer.Render(ctx, plan)                 internal/platform/renderinggen/queue_client.go
       → MapClipPlanToOverlayPlan
       → prefetchClipAssets
       → queue.Submit                           ← render accepted
       → waitClipQueue → WaitTerminal           ← BLOCKS the Master slot for the whole render
       → materializeArtifact                    (download + SHA-256 verify)
  → outputProber.ProbeOutput / ValidateContract
  → [overlay composite]
  → publisher.Publish
```

`ClipRenderExecutor.Render` is a single synchronous method that submits AND
waits (`waitClipQueue`), so the Master goroutine — and the job slot it owns —
is occupied for the entire remote render plus artifact download. The
`waitClipQueue` comment already admits this: *"The upstream PipelineGen slot
stays held while awaiting the downstream RenderingGen job (documented
synchronous barrier)"*.

Raising `workers: 4 → 20` only hides the symptom: 20 slots would still be held
by goroutines doing nothing while Chronon renders.

## 1.1 The continuation primitive already exists (this supersedes the earlier
"blocked" assessment)

An earlier pass concluded Wave B was blocked on a job continuation/resume
primitive. That was wrong: the repository already ships the canonical
parent/child aggregation surface, adopted by `script.generate` and
`voiceover.generate`:

* `internal/kernel/job/job.go` — `StatusWaitingChildren`,
  and `parent_state.go` (`ParentState` state machine:
  `dispatching → waiting_children → aggregating → succeeded|failed_terminal`).
* `internal/kernel/job/parent_link.go` — `ParentLink` + `InjectParentLink`,
  so the parent/child relationship travels IN the payload and survives a
  process restart (no process-local memory).
* `internal/capabilities/jobs/job_queries.go` —
  `Service.ListAwaitingAggregation(ctx, parentType, limit)`, a generic
  `parentType` query (so it serves `clip.render` without a new query).
* `internal/platform/sqlite/jobs/lifecycle_finalize.go` —
  `FinalizeAggregateParent` (no-lease CAS) re-finalises a parent after its
  worker released the lease, including flipping `SUCCEEDED → FAILED` when every
  child definitively failed.
* `internal/capabilities/scripts/jobs/parent_aggregator.go` — the reference
  implementation: a 30 s background poller that reads parents with
  `parent_state=waiting_children`, inspects children, and finalises.
* `internal/capabilities/jobs/queue/claim.go` — claiming is filtered by an
  explicit `types []string` capability list, so a job type can be given a
  DEDICATED pool by configuration (the audit's P0.5 guardrail) without core
  changes.

So the boundary is reachable on existing contracts, with no migration and no
new job type (the phase travels in the payload, so
`architecture/ownership/jobs.yaml` still declares exactly one `clip.render` job
with one `RegisterHandler` binding — `percheck_job_ownership` is untouched).

## 2. Required design

**Submission boundary (fast):**

```text
Worker.Handle
  → prepare + compile + seal plan
  → prepare assets (prefetch)
  → submit RenderingGen job
  → PERSIST render state durably
  → RETURN  (slot released; job reports "submitted / awaiting remote render")
```

**Completion boundary (continuation):**

```text
RenderingGen terminal event
  → clip.render.complete continuation job
  → materialize artifact (reusing the certified SHA-256 from the download)
  → probe / ValidateContract
  → overlay composite (or the Wave C single-pass replacement)
  → publish + commit
  → COMPLETED
```

### 2.1 Persisted remote-render state

PipelineGen must persist at least:

```text
clip_render_id
renderinggen_job_id
correlation_id
artifact locator (storage key + expected SHA-256 + size)
attempt
state
```

So a PipelineGen restart does not lose an in-flight render. Suggested states:

```text
PREPARING
SUBMITTED
REMOTE_RENDERING
ARTIFACT_READY
PUBLISHING
COMPLETED
FAILED
```

### 2.2 Completion transport

Preference order (as audited):

1. **RenderingGen terminal event → PipelineGen continuation** (preferred; keeps
   the Master fully free).
2. Dedicated waiter pool.
3. Short-poll continuation job (last resort — reintroduces polling).

`WaitTerminal` is already event-driven (`queue_client.go` delegates to the
queue's `GET /jobs/{id}/wait` long poll), so the completion signal can reuse it
inside a waiter/continuation instead of inventing aggressive polling.

### 2.3 What must move behind the completion boundary

Making only `Render()` async is not enough. These must run in the
continuation/completion worker, not the initial Master job:

* artifact materialization (`materializeArtifact`)
* hash verification (already certified during download — reuse it)
* output probe (`outputProber.ProbeOutput` + `ValidateContract`)
* overlay compositing (today `w.overlayCompositor.Composite`)
* Drive publication + asset commit (`w.publisher.Publish`)
* result projection (`renderedResult`, `finalizeMetrics`, RunReport stages)

## 3. Blocking gap: no continuation primitive

`internal/kernel/job` has **no** continuation/resume/rehydrate primitive (no
`Continuation`, no resume entrypoint). The states above also need durable
storage: there is no `clip_render_state` table today. Implementing Wave B
therefore requires, in order:

1. A durable render-state store (SQLite table + migration) owned by
   `internal/capabilities/cliprender` behind a narrow port.
2. A terminal-event consumer that maps `renderinggen_job_id → clip_render_id`
   and enqueues an idempotent continuation job.
3. Splitting `ClipRenderExecutor.Render` into `Submit` (returns the remote job
   identity) and `Complete` (materialize + return the certified `RenderOutcome`).
4. Worker support for "resume from persisted state" (idempotent re-entry).

## 4. P0.5 guardrail (temporary, while Wave B is open)

Assign `clip.render` to a **separate pool/budget** so four slow renders cannot
starve every other Master job.

Reality check: `JobDefinition.ConcurrencyKey` exists
(`internal/kernel/job/job_definition.go:257`) but is **never enforced anywhere**
in the codebase, and `clip.render` is not even a member of
`CanonicalJobDefinitions` (`internal/kernel/job/canonical_definitions.go`).
So the guardrail requires either (a) implementing ConcurrencyKey enforcement,
or (b) a dedicated clip-render pool in the Master runner.

This guardrail is a temporary mitigation; it is **not** a substitute for the
async redesign.

## 5. Acceptance criteria

- [x] `clip.render` submission returns and releases its Master slot as soon as
      the RenderingGen job id is durably persisted. — worker.go dispatches on
      `render_phase`; MEASURED by benchmark scenario 2 (§8.2): the submit
      handler occupies 1 ms while the blocking (pre-split) pipeline occupies
      63 ms for the same 60 ms render.
- [ ] Remote render state survives a PipelineGen restart: a restarted process
      resumes completion and never re-submits a render that is already running.
      — the continuation contract is in place (CAS-addressed resume document,
      deterministic `run_id`, idempotent submit); the kill-mid-render test
      still needs a live RenderingGen.
- [x] Completion is event-driven (no aggressive polling) and idempotent under
      duplicate/at-least-once delivery. — the settle phase is a normal job
      continuation (redelivery re-derives the same `run_id`); scenario 3 (§8.2)
      measures the completion tail with and without event-driven finalisation.
- [x] Publication/certification/probe run in the continuation, not the initial
      job. — worker.go `settle` calls the probe/publish tail and the `submit`
      phase returns immediately after `EnqueueContinuation`; scenario 2 shows
      the submit phase does not track the render.
- [ ] Crash/restart recovery test: kill mid-render → restart → exactly one
      published artifact, no duplicate Drive object, no lost render.
- [x] Before/after throughput report at fixed concurrency proving the slot is
      not occupied while Chronon renders. — `make bench-cliprender` (§8).
- [ ] Guardrail (dedicated clip-render budget) landed and documented as
      temporary. — the benchmark models it (`WaiterPool`); the configuration
      change itself is deployment work.

## 6. LANDED — the render-boundary split + the continuation contract

### 6.1 `ClipRenderExecutor` split into `Submit` / `Settle`

`internal/platform/renderinggen/queue_client.go`

* `Submit(ctx, plan) error` — the PRE-RENDER half: validate, map the plan onto
  `renderinggen.overlay-plan.v1`, resolve + prefetch content-addressed assets,
  enqueue the remote job. It returns once the job is ACCEPTED and makes ZERO
  status calls (proved by test: a queue that counts `Get` observes 0 calls).
* `Settle(ctx, plan) (*RenderOutcome, error)` — the POST-SUBMIT half: wait for
  the terminal state, require the certified Chronon artifact, download it
  (hashing in the same streaming pass) and project the outcome. This is the
  ONLY place that blocks on RenderingGen.
* `Render` is now literally `Submit` + `Settle`, so every existing caller keeps
  byte-identical behaviour (the pre-existing executor tests still pass).

Resumable by construction: the remote job id is the sealed plan's deterministic
`RunID`, so a `Settle` running after a restart addresses the same remote render
with no process-local state, and a replayed `Submit` is idempotent
(409 → `ErrJobExists`, already handled).

### 6.2 The continuation contract

`internal/capabilities/cliprender/continuation.go`

* `RenderPhase` (`submit` | `settle`) read from the payload key
  `render_phase`; an ABSENT key is `submit` (back-compatible), an UNKNOWN value
  is a typed error — never a silent fallback, so a typo can never silently
  re-run submission (which would re-prepare and re-upload the clip's assets).
* `RemoteRenderState` — the audit's explicit 7 states (`PREPARING`, `SUBMITTED`,
  `REMOTE_RENDERING`, `ARTIFACT_READY`, `PUBLISHING`, `COMPLETED`, `FAILED`)
  replacing the implicit "the handler is blocked, so a render is in flight".
* `Submission` — the durable record (render job id + plan digest + correlation
  id + state + attempt), JSON-serializable, fail-closed when incomplete.
* `ContinuationRef` + `Continuation` — the small handoff payload: the
  submission plus the CONTENT ADDRESS (digest + size) of the resume document,
  NOT the document itself.
* `ResumeDocument` — the content-addressed document the settle phase loads
  (sealed plan + normalized request + resolved contract/transcript/subtitle
  artifact + resolved folder id), with `Attributes` binding it to the
  submission it resumes (run id + plan digest) and failing closed on drift.
* `ContinuationEnqueuer` port + `ActiveKeyFor(renderJobID, attempt)` so a
  retried submit cannot fan out two settle jobs for the same render.
* `ParentStateWaitingChildren` — the SAME wire value scripts/voiceover write, so
  the existing `ListAwaitingAggregation` / `FinalizeAggregateParent` machinery
  finds and re-finalises a `clip.render` parent without a migration.

### 6.2.1 Why the resume inputs travel as an address, not inline

The settle phase must NOT re-run preparation (that would re-resolve and
re-upload the clip's assets), so it needs the preparation results. Inlining
them would put a large, duplicated, unverifiable blob in the job payload —
which the Master persists in `result_json` and the broker copies on every
retry. Instead the submit phase writes ONE deterministic document into the
canonical CAS and ships only its address:

* the payload stays small and cheap to retry;
* the settle phase VERIFIES the document against the address it was handed, so
  a truncated or drifted document is detected rather than acted on;
* a redelivered continuation addresses the same bytes (idempotent), and a
  retried submit re-derives the same digest.

`internal/platform/renderinggen/continuation_store.go` implements the port over
`internal/platform/cas` (`CASContinuationStore`): `PutResumeDocument` validates
before writing, and `GetResumeDocument` enforces BOTH the declared size and the
declared digest (via the `internal/kernel/digest` SSOT — the archcheck
`percheck_digest_sha256_ban` gate caught the first draft importing
`crypto/sha256` directly) before decoding.

### 6.3 Tests

* `clip_executor_async_test.go` — `Submit` never waits (0 status calls),
  `Settle` certifies + projects (and fails closed without an artifact), and the
  blocking `Render` is exactly one submit + one settle.
* `continuation_test.go` — phase parsing (absent/empty/unknown/case), the
  `Submission` fail-closed rules, the state vocabulary + terminal set, the
  `ContinuationRef` address rules, the `Continuation` JSON round-trip (which
  also asserts the payload NEVER inlines the sealed plan), the `ResumeDocument`
  digest/run-id binding, and the enqueue idempotency key.
* `continuation_store_test.go` — a real `cas.Store` with the real `LocalStore`
  stager: put/get round-trip, digest determinism (the property that makes a
  retried submit idempotent), and fail-closed on an unknown address, a size
  drift, a malformed address, and an invalid document (which is rejected
  BEFORE anything is written).

## 7. REMAINING — worker phase dispatch + wiring (the exact change set)

1. `internal/capabilities/cliprender/worker.go` — dispatch on the phase read by
   `ParseRenderPhase`:
   * `submit`: keep the existing prepare → subtitles → overlay → `Compile` →
     seal path, then call `AsyncRenderExecutor.Submit`, persist the
     `Submission`, set `parent_state = waiting_children` in the job result,
     call `ContinuationEnqueuer.EnqueueContinuation`, and RETURN without
     probing or publishing. Fail closed (return the enqueue error) when no
     continuation port is wired — a submission whose continuation was lost is a
     render nobody will ever collect.
   * `settle`: `DecodeContinuation` from the payload, call
     `AsyncRenderExecutor.Settle`, then reuse the EXISTING probe → publish tail
     verbatim. No preparation runs.
2. Carry the resume inputs in the continuation payload so the settle phase does
   NOT re-prepare (re-preparing would re-resolve and re-upload assets): the
   sealed plan (already carried), plus the normalized request, the resolved
   contract + transcript, the subtitle artifact, and the resolved publish
   folder id. This makes the continuation payload large; a durable blob/side
   reference is the cleaner follow-up once the run workspace is content
   addressed.
3. `internal/app/wiring` — wire the `ContinuationStore` (a `cas.Store` built
   once) and satisfy `ContinuationEnqueuer` with
   `job.Service.Enqueue(TypeClipRender, ...)` + `job.InjectParentLink`, behind
   an explicit switch. **DECISION (2026-09-12): default OFF**, opt-in via env
   var, until validated against a live RenderingGen — the same staging the
   single-pass overlay cutover used.
4. Guardrail (P0.5): claim filtering is already by explicit type list
   (`internal/capabilities/jobs/queue/claim.go`), so the settle phase can be
   given a dedicated pool; `ConcurrencyKey` exists but is never enforced, which
   is why the pool — not the key — is the guardrail to land.
5. Aggregator: mirror
   `internal/capabilities/scripts/jobs/parent_aggregator.go` to finalise the
   `clip.render` parent from the settle child's outcome (reusing
   `ListAwaitingAggregation` + `FinalizeAggregateParent`), which is what makes
   the parent's final result truthful instead of stuck at
   `waiting_children`.
6. Crash/restart + before/after throughput evidence per §5 — the throughput
   half LANDED 2026-09-12 (§8); the crash/restart half still needs a live
   RenderingGen.

## 8. LANDED — the canonical throughput benchmark (2026-09-12)

`make bench-cliprender` (scenarios in `internal/capabilities/cliprender/bench_*_test.go`)
answers §5's throughput criterion headlessly. The harness drives the REAL
`Worker.Handle` in both phases, the real continuation contract, the real
`ParentAggregator` and the real kernel RunReport, and models the two things
that bound clip throughput: N Master slots and L RenderingGen GPU lanes. Every
remote cost (render, artifact download, Drive publish, asset materialization,
ASR) is a latency parameter, so a scenario is a controlled experiment rather
than a hardware lottery. It is CI-safe (no server, GPU, Drive or Whisper) and
`-race`-clean.

### 8.1 The scenarios

| # | Scenario | What it proves |
|---|---|---|
| 1 | worker scaling 1/2/4/8 | wall scales with workers while workers ≤ lanes and plateaus past the lane ceiling; concurrency never exceeds min(workers, lanes) |
| 2 | submit vs settle occupancy | the remote render duration moves the SETTLE slot and not the SUBMIT slot |
| 3 | parent completion latency | 30 s cadence vs the shipped 2 s default vs event-driven finalisation |
| 4 | SHA cache-hit | N clips from one source cost ONE full-file hash and ONE download |
| 5 | shared CAS | two processes over one root materialise the bytes once |
| 6 | transcript reuse | `generate` = one ASR pass per clip; `reuse_or_generate`+`persist` = one per source |
| 7 | Chronon source seek | in-repo: the sealed plan is source-relative and a minute-25 window is expressed and validated exactly. Decode cost is Chronon-owned and needs the live stack |
| 8 | cold vs warm | a warm process over the same CAS pays neither download nor a second read |
| 9 | RenderingGen saturation | sweep the SETTLE producers 1/2/4/8 over 2 lanes: one producer leaves half the GPU idle, two saturate both lanes, and more producers buy no throughput while the queue grows |
| 10 | E2E 1/10/50 clips | the full metric report to re-run after every performance change |

### 8.2 Measured (this host, `-run TestScenario`, 2.7 s total)

Slot occupancy — scenario 2, 8 clips, 8 lanes:

| pipeline | handler wall for a 60 ms render | after +50 ms of remote render |
|---|---|---|
| blocking (pre-split) | 63 ms (the slot tracks the render) | — |
| async submit | 1 ms | 1 ms (**unchanged**) |
| async settle | 10 ms @10 ms render | 60 ms (the wait is where we put it) |

Parent completion — scenario 3, 6 clips, 200 ms measured cadence, projected
linearly (the parent flips on the first tick after its child):

| completion mode | p50 | p95 |
|---|---|---|
| 30 s cadence (historical) | ~29 400 ms (projected) | — |
| 2 s cadence (shipped default) | ~1 960 ms (projected) | — |
| event-driven (finalise on settle completion) | **0 ms** | **1 ms** |

Worker scaling — scenario 1, 24 clips × 10 ms render, lanes = 8:
wall 261 / 129 / 68 / 36 ms at 1/2/4/8 workers, with lanes_used == workers.
At lanes = 2 the wall plateaus at 128 ms from 2 workers on, and concurrency
never exceeds 2 — the GPU, not the pool, is the bound.

RenderingGen saturation — scenario 9, 24 clips × 10 ms render, lanes = 2
(the producer axis is the SETTLE pool: with the async split the submit pool
never holds a lane):

| settle producers | lanes used | lane utilisation | queue p95 | rate |
|---|---|---|---|---|
| 1 | 1 | 48.4 % | 0 ms | 5 527 clips/min |
| 2 | 2 | 95.3 % | 0 ms | 10 841 clips/min |
| 4 | 2 | 97.7 % | 10 ms | 11 092 clips/min |
| 8 | 2 | 97.9 % | 33 ms | 10 933 clips/min |

Two producers are enough to saturate both lanes; beyond that the rate
plateaus and only the queue wait grows. The starved control (2 clips over 8
lanes) reports 24.3 % utilisation, so idle lanes are detected rather than
always reported busy.

Canonical E2E — scenario 10, 4 workers, 64 settle slots, 8 lanes:

| clips | wall | rate | p50 | p95 | lane utilisation |
|---|---|---|---|---|---|
| 1 | 8 ms | 6 754 clips/min | 8 ms | 8 ms | 11.9 % |
| 10 | 15 ms | 39 882 clips/min | 12 ms | 14 ms | 58.7 % |
| 50 | 46 ms | 64 314 clips/min | 29 ms | 43 ms | 88.6 % |

The rate rises with batch size (fixed startup is amortised) and the p95 stays
bounded, which is the steady-state KPI to watch: clips/minute, not the wall of
one clip.

Waste elimination — scenarios 4/5/6/8, one source, 10–20 clips:

| metric | naive | measured |
|---|---|---|
| full-file source hashes (20 clips) | 20 | **1** |
| source downloads (20 clips) | 20 | **1** |
| downloads, 2 batches × 10 clips on a shared CAS root | 20 | **1** |
| warm-process downloads / extra reads | N | **0 / 0** |
| ASR passes (10 clips, 20 ms each) | 10 (246 ms) | **1 (24 ms)** |

### 8.3 What this does NOT measure

- Real GPU utilisation, NVENC/NVDEC, VRAM and real decode cost. The lane
  simulator is a queuing model of RenderingGen, not a GPU profiler; the
  in-harness `gpu_lane_utilization_pct` is lane occupancy, not GPU busy %.
- Chronon source seek (scenario 7). Segment decode cost is a Chronon property;
  the skipped live test documents the exact procedure and the fields to read
  from `RenderMetricsV2` (`decode_ms`, `frames`, `render_wall_ms`,
  `bytes_read`).
- Crash/restart recovery, which needs a live RenderingGen to kill.

Real-stack runs reuse the same report schema: `VELOX_BENCH_WRITE_REPORT=1`
persists the JSON artifacts under `tests/operational/results/cliprender-bench/`
(off by default so a normal run never dirties the working tree).
