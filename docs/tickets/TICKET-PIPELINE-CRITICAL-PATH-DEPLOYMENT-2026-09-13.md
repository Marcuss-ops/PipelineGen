# TICKET — PipelineGen `script.generate` critical path: deployment-bound remainder

> **Dated artifact — paths reflect the tree at the time of writing.** Retained
> while the ticket is open (see `AGENTS.md` § Documentation rule); verify any
> referenced path against the current tree before acting.

**Date:** 2026-09-13
**Status:** OPEN — blocked on a real GPU/Chronon/RenderingGen deployment
**Parent audit:** `docs/PIPELINE-WASTE-AUDIT-2026-09-12.md`
**Predecessor:** the code-level acceptance criteria (§2.3, §3, §6.2, §7, §8) are closed; this
ticket is everything that cannot be closed from a source checkout alone.

## Why this ticket exists

The audit's §15 order was followed. Items 0–7 that are decidable in code are done. What is
left all requires either a live deployment (GPU, Chronon, RenderingGen queue, Drive) or a
benchmark run whose evidence must be preserved. Nothing below should be started by editing
code and hoping: each item names the measurement that decides it.

## The measured critical path this ticket is about

Source: `tests/operational/results/person-overlay-drive/full-person-overlay-drive-20260912T200028Z-31642.json`
(the last recorded full `script.generate` run with the post-split stage model).

Run wall **50,188 ms**; `attributed_ms` 50,115; `overlapped_ms` 4,642; `unattributed_ms` 73.
Bottleneck stage `scene_analysis`, bottleneck operation `nlp.extract`, **23.86 %**.

| stage | wall ms | fan-out work ms | calls | note |
|---|---|---|---|---|
| `scene_analysis` | 11,973 | 19,048 | 4 | critical path, NLP-bound |
| `overlay_render` | 11,160 | 6,227 | 4 | **wall > work: serialized / queue wait** |
| `tts` (voiceover branch) | 10,639 | 12,532 | 3 | overlaps NLP |
| `post_writer_finalize` | 9,346 | 16,580 | 10 | parallel, Drive-bound |
| `document.publish` | 5,089 | 5,080 | 1 | Google Docs API |
| `generate` | 4,392 | 3,942 | 1 | Ollama |

Read the tail as a block: `document.publish` + `post_writer_finalize` + `finalize` ≈ **15.6 s
(31 % of wall)** run **after** the render is already complete.

## Open acceptance criteria

### D1 — Docs/Drive off the critical path (audit §11)

- **Criterion:** `render_complete` is emitted before Docs/Drive publication begins, and
  Docs/Drive work appears as post-processing outside the completion path.
- **Current placement:** `runner_execution_pipeline.go` runs
  `audioCompile() → persist() → documents() → complete()`. Google Docs publication is the
  last phase, so a `RUNNING` job has no document and only previously-completed runs are
  visible in the Drive folder. This is the user-visible symptom.
- **Precedent to copy:** `clip.render` already has configurable async Drive delivery over the
  PostgreSQL media outbox (`clip_async_drive_enabled: true`, consumer + pending/dead-letter
  metrics). `script.generate` does not use it.
- **Measurement that decides it:** the same E2E payload must show `document.publish` and
  `post_writer_finalize` no longer inside the job's completion wall, with the published
  artifacts still reconciled (0 missing, 0 duplicated) after the outbox drains.
- **Risk to settle first:** moving publication post-completion changes what "SUCCEEDED"
  guarantees. The outbox must be durable before the terminal flip, exactly as the clip path
  does.

### D2 — Render serialization / GPU gate (audit §9, §10)

- **Criterion:** `overlay_render` wall reconciles with its work; render concurrency > 1 is
  either proven beneficial or explicitly rejected with numbers.
- **Evidence of loss:** 4 calls, 6,227 ms of work, **11,160 ms of wall**, max single call
  5,312 ms → roughly 5 s that is not render work.
- **Two candidate causes, kept separate:**
  1. `platform/overlays/gpu_gate.go:34` is a single host-wide
     `flock(LOCK_EX|LOCK_NB)`. It serializes the *local* overlay handler path
     (`app/wiring/overlay_handlers.go`, `rendering_runtime.go`); confirm whether it is on
     this run's path before touching it.
  2. The RenderingGen queue wait (`render_queue.go`, event-driven long poll with a 250 ms
     polling fallback). This is remote and cannot be fixed from this repo.
- **Measurement that decides it:** `capabilities/scripts/render_concurrency_benchmark_test.go`
  at concurrency 1/2/3/4 with the gate relaxed, recording wall, accumulated work, NVENC
  contention and VRAM. If >1 does not win, the gate stays and this closes as
  "rejected with evidence".

### D3 — Chronon warm daemon on the production path (audit §9)

- **Criterion:** cold render vs warm #1 vs warm #2 show startup/preparation near zero on the
  warm runs.
- **Current state:** `platform/overlays/renderer.go` still spawns one short-lived CLI process
  per overlay (`exec.CommandContext(binary, --plan, …, --output, …)`), so Vulkan device,
  pipeline cache, glyph atlas and encoder infrastructure are rebuilt every render. The warm
  daemon already exists next door in RenderingGen
  (`internal/chronon/ipc.go`, `cmd/batch-render-presets`, default 3 daemons over UNIX
  sockets) and ships a cold-vs-warm benchmark
  (`daemon_vs_cli_benchmark_integration_test.go`).
- **Measurement that decides it:** the existing RenderingGen cold/warm benchmark, run against
  the production binding, with the artifact SHA preserved.

### D4 — Warm/cold evidence for the whole pipeline (audit §3, §6, §12)

- **Criterion (now unblocked in code):** a warm rerun with identical inputs must skip the
  creative phases. The certification scripts no longer hard-code `force_refresh: true`
  (warm is the default; `FORCE_REFRESH=true` restores a cold run; `FORCE_REFRESH_MEDIA`
  controls only the media_plan sub-flags), so this is finally measurable — it has never been
  measured.
- **Criterion:** 1 / 3 / 10 scene timings show sub-linear growth **with a warm model**.
- **Criterion:** one command produces the scenes × languages × cache × renderer grid, and the
  results corpus is indexed/aggregated instead of a flat directory of timestamped files.
- **Measurement that decides it:** preserve the warm run's `full-*.json` next to the cold
  one; do not delete `status-*.json` from `tests/operational/results/`.

## Explicitly out of scope here

- Any renderer-level work (GOP copy, text preparation, NVENC tuning). The audit §13 shows the
  GOP-copy machinery already exists; it is not today's bottleneck.
- Removing `Chronon3d/build` (72 GB) and other disk weight: tracked separately, not a
  correctness or throughput item.

## Definition of done

Each of D1–D4 closes with a preserved artifact (report JSON or benchmark output), a stated
before/after wall time, and a before/after correctness gate. No item closes on reasoning
alone.
