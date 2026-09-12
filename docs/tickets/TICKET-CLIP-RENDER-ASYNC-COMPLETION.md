# TICKET — clip.render: async submission/completion boundary (Wave B)

**Priority:** P0 — this is the throughput bottleneck, not RenderingGen/Chronon.
**Status:** OPEN (blocked on a job-continuation primitive that does not exist yet).
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

- [ ] `clip.render` submission returns and releases its Master slot as soon as
      the RenderingGen job id is durably persisted.
- [ ] Remote render state survives a PipelineGen restart: a restarted process
      resumes completion and never re-submits a render that is already running.
- [ ] Completion is event-driven (no aggressive polling) and idempotent under
      duplicate/at-least-once delivery.
- [ ] Publication/certification/probe run in the continuation, not the initial
      job.
- [ ] Crash/restart recovery test: kill mid-render → restart → exactly one
      published artifact, no duplicate Drive object, no lost render.
- [ ] Before/after throughput report at fixed concurrency proving the slot is
      not occupied while Chronon renders.
- [ ] Guardrail (dedicated clip-render budget) landed and documented as
      temporary.
