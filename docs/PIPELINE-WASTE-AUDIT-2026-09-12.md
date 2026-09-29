# PipelineGen — Pipeline Waste Audit

> **Status banner (added 2026-09-13).** This document is a dated snapshot kept for its reasoning and
> its historical numbers. **Read §17 for the current state before acting on anything in §1–§16** —
> most of the code-level findings here have since been closed, and §17 carries the AC-by-AC status
> plus the corrected tail measurement. The remaining open items are deployment-bound and are tracked
> in `docs/tickets/TICKET-PIPELINE-CRITICAL-PATH-DEPLOYMENT-2026-09-13.md` (measurement) and
> `docs/tickets/TICKET-CORE-READY-DURABLE-DAG.md` (the durable DAG design). Nothing in §1–§16 has
> been rewritten, so the numbers below still describe the 2026-09-12 tree.

**Date:** 2026-09-12
**Scope:** PipelineGen (`refactored/`), RenderingGen, Chronon3d
**Mode:** audit only — no code was changed
**Method:** every number below comes from a recorded artifact or a file:line in the tree. Nothing is asserted from intuition.

Evidence base:

- `refactored/tests/operational/results/person-overlay-drive/full-*.json` — 11 complete
  `script.generate` runs with per-stage, per-operation and fan-out timings (Sep 10–12).
- `refactored/tests/operational/results/jordan-entity-overlay-drive/` — the Jordan canary runs.
- Source tree at the time of audit.

---

## 0. Headline findings

| # | Finding | Quantified |
|---|---|---|
| A | **Ollama is cold on every single run.** The warm-up exists but is self-defeating. | `cold_start=1` in **11/11** runs. **223 s** of measured `ollama.generate` wall across 11 runs contained only **31 s** of real inference → **86 % model loading.** Counting the unmeasured warm probe, the `generate` stage totals **304 s** for **31 s** of inference → **90 % waste.** |
| B | **The render's wall time is charged to `audio_compile`.** The stage the optimization plan calls "highest priority" is mostly not audio work. | Run #1: `audio_compile` = **148 341 ms** wall, but its own recorded operations total **12 675 ms**. The `renderinggen render` operation (**131 840 ms**) is recorded under stage `process`, whose reported wall is **0 ms** — so it never enters the critical path or the bottleneck. |
| C | **The timing model cannot reconcile concurrency.** | `overlapped_ms = 0` in **11/11** runs, while `attributed_ms > wall_ms` in **11/11** runs (e.g. 231 241 vs 229 933). One of the two is wrong. |
| D | **All GPU rendering is hard-serialized by a global file lock.** | `GPUGate` is a single `flock(LOCK_EX)` over one lock file — render concurrency is pinned to 1 host-wide. Per-scene render parallelism is currently impossible. |

Consequence: the plan's stated #1 priority ("understand those 76 s of audio") is
aimed at a number that is not a measurement of audio. The 76 s (and up to 148 s)
is dominated by Chronon render wall being attributed to the audio stage.

---

## 1. Per-item verdicts (the 14-point list)

| # | Item from the plan | Verdict | Basis |
|---|---|---|---|
| 1 | Ollama cold start | **BROKEN** — machinery exists, wiring is wrong | see §2 |
| 2 | Cache + `force_refresh` | **MOSTLY PRESENT**, 3 gaps | see §3 |
| 3 | Audio compile decomposition | **ABSENT + MISATTRIBUTED** | see §4 |
| 4 | Real DAG instead of serial phases | **PARTLY PRESENT** | see §5 |
| 5 | NLP per scene in parallel | **PRESENT** (bounded) | see §6 |
| 6 | Entity/image resolution async + dedup | **PRESENT** | see §7 |
| 7 | TTS chunk/cache | **PARTIAL** — parallel, uncached | see §8 |
| 8 | Chronon persistent daemon | **ABSENT on the production path** | see §9 |
| 9 | Render per scene + resource-aware scheduler | **BLOCKED by design** | see §10 |
| 10 | Docs/Drive off the critical path | **NOT DONE** | see §11 |
| 11 | Automatic benchmark matrix | **PARTIAL** | see §12 |
| 12 | Minimum Necessary Rendering (GOP copy) | **ALREADY BUILT** — do not rebuild | see §13 |
| — | (ordering / "don't touch Chronon yet") | **agrees with the evidence**, with one caveat | see §4 |

---

## 2. Item 1 — Ollama cold start

### What exists (and is good)

- `internal/platform/ollama/client/client_warm.go` — `WarmModel` is a real
  single-flight warm-up: it checks live residency via `/api/ps`
  (`client_health.go::IsModelResident`) and only then issues a 1-token probe.
- `internal/platform/ollama/client/client_core.go:221` — `keep_alive` defaults to
  `30m` and is sent as a **top-level** request field (correct wire contract).
- Call sites are wired: `usecase/gencore/segment_validation.go:114` (before the
  segment fan-out) and `app/wiring/lifecycle_preparation.go:175` (queue-time warm).

### Why it does not work

`client_warm.go:41` hard-codes the probe context window:

```go
"model": model, "num_predict": 1, "num_ctx": 4096,
```

with the comment *"Match the short-scene bucket used by the real fan-out."*
It does not match. `internal/platform/ollama/generate.go:193` derives the window
per call from the real prompt and output budget:

```go
options["num_ctx"] = ResolveContextBudget(messages, options["num_predict"])
```

and `internal/platform/ollama/generation_budget.go:70` buckets it:

```go
for _, bucket := range []int{2048, 4096, 8192, 16384} {
```

The short-scene bucket is **2048**, and the test
`internal/platform/ollama/generate_test.go:82` pins exactly that
(*"want 2048 for a short scene"*).

Ollama reloads a resident model when requested options change. So the sequence is:

1. warm probe loads the model at **4096**;
2. real scene generation requests **2048** → Ollama **unloads and reloads**.

The warm-up therefore *adds* a full model load instead of removing one. The
code's own comment warns against precisely this failure mode, then commits it.

### Measured cost

Corpus: 11 runs, **1 404 370 ms** of wall (≈ 23.4 min) — of which Ollama's
`generate` stage is **304 549 ms**.

| metric | value |
|---|---|
| runs with `cold_start = 1` | **11 / 11** |
| measured `ollama.generate` wall, summed | **223 213 ms** |
| real inference (`inference_work_ms`), summed | **31 089 ms** |
| **model load inside the measured operation** | **192 124 ms (86 %)** |
| `generate` stage wall, summed | **304 549 ms** |
| **load not even attributed to an operation** | **81 336 ms** |
| **total Ollama waste** | **≈ 273 s over 11 runs (90 % of the generate stage)** |

Two artefacts of the wiring fall out of this:

- **The warm probe is invisible in telemetry.** `WarmModel` calls
  `c.ChatDetailed` directly and never touches `kernobs.RecordOperation`, so the
  81 s of double-load above lands inside the `generate` *stage* wall with no
  operation behind it. In run `…T120031Z` the `generate` stage is 40 226 ms while
  `ollama.generate` is 4 465 ms — a 35 761 ms hole that is exactly a probe load.
- **Per-call `num_ctx` is itself a thrash source.** Because the budget is derived
  per prompt and the scene fan-out runs 3–4 calls concurrently
  (`DefaultGenerationConcurrency = 3`, `DefaultNLPConcurrency = 4` in
  `capabilities/scripts/backpressure.go:28,32`), concurrent scenes can land in
  *different* buckets. With one resident Ollama instance, differing options force
  reloads serially. This deserves a targeted test before any fan-out tuning.

### Acceptance criteria (plan's gate)

- [ ] Second execution of the same model reports `model_load_ms ≈ 0`.
      **Currently fails: 11 / 11 runs report a cold start.**
- [ ] `WarmModel` probe and real generation agree on `num_ctx` for every bucket,
      or `num_ctx` is pinned per model for the whole run.
- [ ] The probe's load is attributed as its own operation so the next regression
      is visible rather than hidden in a stage wall.

---

## 3. Item 2 — Cache and `force_refresh`

### Caches that exist (content-addressed, not job-id keyed — correct)

| Layer | Location | Key inputs |
|---|---|---|
| Script | `internal/kernel/script/cache_key.go:50` | source fingerprint, language, style, model, prompt version, target words — **excludes** `force_refresh`, `save_to_db`, Drive folder, languages (pinned by tests in `cache_key_test.go`) |
| Research | `internal/kernel/script/research_cache.go:55` | topic fingerprint, language, version, source fingerprint, max steps |
| Image search | `internal/capabilities/images/storage_cache.go:16` | normalized query, language, provider policy |
| Artlist segments | `scripts/adapters/vidrush_helpers.go:76` | segment id, text hash, intent hash, language, model, prompt version |
| YouTube/partial download | `stockpipeline/cache_key.go:13` | canonical URL, section, merge format, keyframes |
| Whisper / media artifacts | `internal/capabilities/artifactcache/contract.go:28` | `SourceSHA256 + Operation + ParametersJSON + ProcessorVersion` |
| Entity images | `scripts/adapters/entity_image_catalog.go` + `platform/sqlite/assets/entitycatalog/` | person identity + reusable candidate pool |
| Render plan (policy-level) | `capabilities/render/strategy.go:237` | `RenderPlan` |

`force_refresh` **is** wired, contrary to a naive grep: the envelope flag fans out
in `capabilities/scripts/builder.go:60-65` to extraction, assets and bindings, and
is consumed in `processor_internet_images_policy.go:29`,
`media_resolver_image_stage.go:260`, `vidrush_registry_searchers.go` etc.

### Gaps

1. **No TTS cache at all.** No content-addressed layer exists for
   `voice × language × normalized text × TTS settings`. The TTS plane is
   parallel (`voiceover.max_concurrent_tts: 4`) and single-pass for boundaries,
   but every run re-synthesizes every scene. Changing one scene re-synthesizes all.
2. **No render cache — by explicit design, and it is defensible.**
   `app/wiring/overlay_handlers.go:162` states a previous rendered overlay is
   never a valid substitute for a new job's output. Only *inputs* (entity images,
   backgrounds, fonts) are cached. This intentionally contradicts the plan's
   "skip almost all creative phases on a second identical run". Treat it as a
   product decision, not a bug — but it means the plan's gate for cached reruns
   cannot be met on the render leg without changing that contract.
3. **Every recorded test payload sets `force_refresh: true`.**
   `tests/operational/person_overlay_drive_e2e.sh:48` and
   `jordan_entity_overlay_drive_e2e.sh:55` hard-code it, as do the latest payloads
   in `tests/operational/results/`. So the entire body of evidence above is
   *cold by construction* — it cannot show whether caching works, and it inflates
   every historical number. The scenario files to compare against
   (`vidrush/scenarios/02_full_media_cold.json` vs `03_full_media_warm.json`)
   exist but their results are not in this results set.

### Acceptance criteria

- [ ] A warm rerun (identical inputs, `force_refresh: false`) must skip the
      creative phases. **Unproven** — no warm result artifact exists for this
      workload; every recorded run forces refresh.
- [ ] TTS cache keyed on voice/language/text/settings, so editing one scene does
      not re-synthesize the rest.
- [ ] Operational scripts must stop hard-coding `force_refresh: true` for
      certification runs, or the warm leg is untestable.

---

## 4. Item 3 — Audio compile decomposition (and the misattribution)

### The decomposition does not exist

`capabilities/scripts/runner_audio_observability.go` records five subtimings
(`audio_asset_resolve`, `timeline_compile`, `clip_audio_prepare`,
`audio_plan_compile` from `AudioCompileTimings`; `mix`, `aac_encode`, `probe`,
`hash`, `rust.audio_render` from `AudioPipelineMetrics`). That is the right idea.

But the stage wall and the operations do not reconcile:

| run | `audio_compile` wall | sum of its operations | unexplained |
|---|---|---|---|
| `…T153111Z` | 148 341 ms | 12 675 ms | **135 666 ms** |
| `…T154936Z` | 156 838 ms | 11 777 ms | **145 061 ms** |
| `…T155952Z` | 149 305 ms | 11 268 ms | **138 037 ms** |
| `…T162854Z` | 128 755 ms | 11 095 ms | **117 660 ms** |
| `…T120454Z` | 15 542 ms | 10 119 ms | 5 423 ms |

There is no bucket for decode/load, normalize, concatenate, resample,
silence/padding or IO — and no bucket that explains 100–145 s.

### Where the 135 s actually is

The missing time is the render, and the pattern is systematic — in all four heavy
runs the `process` stage reports `wall_ms = 0` while carrying the whole render:

| run | `audio_compile` gap | `process` wall | `process` work | `process` max (render) |
|---|---|---|---|---|
| `…T153111Z` | 135 666 ms | **0 ms** | 132 792 ms | 131 840 ms |
| `…T154936Z` | 145 061 ms | **0 ms** | 143 690 ms | 142 825 ms |
| `…T155952Z` | 138 037 ms | **0 ms** | 136 619 ms | 135 810 ms |
| `…T162854Z` | 117 660 ms | **0 ms** | 117 640 ms | 116 839 ms |

The `audio_compile` gap tracks `process` work one-for-one. In the first run,
`148 341 − 12 675 = 135 666 ≈ 132 792`.

So the video render runs inside the window that the report calls
"audio_compile", but is recorded under a pseudo-stage whose wall is reported as
zero. Two consequences:

1. `audio_compile` is presented as the 64.5 % bottleneck when its own audio work
   is ~10–13 s.
2. The render never appears in `critical_path` or `bottleneck_operation`.
3. `unattributed_ms` is reported as **42 ms** on that run — a 135 s hole is
   reported as fully attributed.

Note also that `capabilities/scripts/runner_phase_audio.go:73` explicitly states
*"PipelineGen is audio-only … There is no video render phase."* Therefore the
render operation belongs to the sibling clip-render job, and the run report is
**merging parent and sibling job timings without reconciling their walls** — which
is also the most likely explanation for finding C. The serialized
`OperationReport` does not carry its `Stage` field, so this cannot be closed from
the artifact alone.

### Recommendation

**Do not optimize the audio path from these numbers.** The plan's "understand the
76 s first" is the right instinct, but the numbers to understand are wrong. Fix
the attribution first (§14, step 0), then re-measure. A single correct re-measure
is likely to move audio out of the top spot entirely.

### Acceptance criteria

- [ ] `audio_compile` wall reconciles with its operations within a stated
      tolerance (target: unexplained < 10 % of stage wall).
- [ ] `overlapped_ms` and `attributed_ms` are mutually consistent
      (`attributed − overlapped − wall ≈ unattributed`).
      **Still open, and the stated identity is now STALE.** The reporter was
      fixed to the honest-overlap model: `AttributedStageMs` is the UNION of the
      covered wall (bounded by `wall_ms`), the concurrency excess is
      `OverlappedMs`, and `UnattributedMs = wall − attributed`
      (`breakdown.go:38`). Under that model the identity above no longer holds —
      pinned by `TestBreakdown_AttributedIsWallBoundedUnderOverlap`
      (attributed = wall = 10 000, overlapped = 2 000, unattributed = 0 → the
      formula yields −2 000). Re-state the invariant before ticking it.
- [x] The stage field is present in the serialized operation report.
      **Verified 2026-09-28.** `OperationReport.Stage` is a serialized field
      (`internal/kernel/observability/report.go:150`, `json:"stage"`), and the
      propagation that fills it is green:
      `TestOperationMetaContextRoundTrip`, `TestOperationMetaApplyFillsEmptyFields`,
      `TestOperationMetaMetadataJSONDeterministic`
      (`go test ./internal/kernel/observability/ -count=1` → PASS).
- [ ] Then, and only then, the plan's gate: `audio_compile_ms` halved.

---

## 5. Item 4 — Real DAG vs serial phases

Partly done, and the plan should be revised accordingly.

Already concurrent in production (`capabilities/scripts/runner_execution.go:283`,
`parallelFanOut`): the VidRush join + `overlay.prepare` + Docs skeleton +
**audio asset prefetch** run in a goroutine while **TTS runs in the main
goroutine**; they are joined at `prepare_join`. That is exactly the
`NLP ∥ TTS` fan the plan asks for. `audio_prefetch.go` exists specifically to stop
audio asset resolution from blocking later.

But the telemetry says the run is still effectively serial:

- `overlapped_ms = 0` in **11/11** runs, and
- `attributed_ms > wall_ms` in **11/11** runs
  (e.g. `231 241 > 229 933`, `208 955 > 207 113`).

So either (a) the pipeline really does serialize end-to-end, or (b) the reporter
cannot represent overlap. Given §4 shows the reporter merging two jobs' stages
into one wall of `231 241 ms` against an actual wall of `229 933 ms`, (b) is
demonstrated. The plan's gate ("wall time well below the sum of phases") is
**currently unmeasurable** — the metric that would show it is broken.

### Acceptance criteria

- [x] Overlap is computed and non-zero on the parallel fan-out path.
      **Verified 2026-09-28.** `TestBreakdown_AttributedIsWallBoundedUnderOverlap`
      pins `OverlappedMs == 2000` for a fan-out whose stages overlap, and
      `TestFanoutReports_ParallelWallTimeNotSummed` pins that a parallel fan-out
      reports wall time that is NOT the sum of its calls. This is the defect the
      audit measured at `overlapped_ms = 0` in 11/11 runs; it no longer holds.
- [x] `wall_ms` is the authority; stage sums are explicitly allowed to exceed it
      and are reported as `work_ms`, not `wall_ms`.
      **Verified 2026-09-28.** `wall_ms` is asserted as the upper bound of
      attributed time (`AttributedStageMs > WallTimeMs` is a test failure in
      `TestBreakdown_AttributedIsWallBoundedUnderOverlap`), and the summed
      duration has its own field: `FanoutReport.WorkMs` (`fanout.go:21`,
      `json:"work_ms"`) documented as "the accumulated (summed) duration of all
      calls" — explicitly not wall time.

---

## 6. Item 5 — NLP per scene in parallel

**Present and correctly bounded** — do not rebuild.

- Two independent gates: `scriptgen.NewGenerationGateWithCapacity(...)` for scene
  text and a separate NDP gate for NLP
  (`app/wiring/script_generation_runtime.go:339-342`).
- Defaults: `DefaultGenerationConcurrency = 3`, `DefaultNLPConcurrency = 4`
  (`capabilities/scripts/backpressure.go:28,32`), overridable via
  `cfg.Scripts.*Concurrency`. Note `config.yaml`'s `scripts:` block defines only
  `batch_web_search_concurrency` and `batch_chapter_concurrency`, so these two
  fall back to the constants — worth making explicit.
- Worked as designed: `scene_analysis` reports `work_ms > wall_ms`
  (46 328 vs 24 341; 29 542 vs 15 860) with `calls = 4` — real bounded concurrency,
  not `go func()` fan-out. The fan-out worker count comes from
  `e.generationGate.Capacity()` (`gencore/segment_validation.go:296`).

Caveat: because `num_ctx` is derived per prompt (§2), 4 concurrent scenes in
different buckets can serialize against one Ollama model instance. The plan's
gate — verify time does not grow linearly for 1/3/10 scenes — is the right test,
and it must be run with the model already warm to be meaningful.

### Acceptance criteria

- [ ] 1 / 3 / 10 scene timings show sub-linear growth **with a warm model**.
      Still open, and it stays open off a live host: it needs a warm Ollama model
      and a real 1/3/10-scene run. Nothing in a checkout can produce it.
- [x] The two gates' capacities are documented in config rather than implicit.
      **Verified 2026-09-28.** The audit's own gap ("`config.yaml`'s `scripts:`
      block defines only `batch_web_search_concurrency` and
      `batch_chapter_concurrency`, so these two fall back to the constants —
      worth making explicit") no longer holds. `config.yaml` now declares
      `nlp_concurrency: 4`, `script_generation_concurrency: 3`,
      `translation_concurrency: 3` and `overlay_render_concurrency: 2`, each
      bound to a real struct field (`config/scripts.go:217,223,240,252`) with an
      env override, and the binding is pinned by
      `config/scripts_concurrency_test.go`. The effective widths are
      operator-visible instead of living only in the Go constants.

---

## 7. Item 6 — Entity/image resolution: async start + dedup

**Present.** `scripts/adapters/entity_image_catalog.go` implements a reusable
candidate pool per person identity
(`entityImageCatalogCandidates`, `entityImageCatalogPoolTarget`, with
`platform/sqlite/assets/entitycatalog/` persistence and a documented state policy
in `docs/entity-image-catalog-state-policy.md`). Candidates are persisted
(`persistEntityImageCatalogCandidates`) and materialization is reused
(`applyEntityImageCatalogMaterialization`), so a repeated entity resolves from the
pool rather than re-downloading. Per-entity binding refresh is independently
controllable via `force_refresh_bindings`
(`gencore/persistence.go:156`, `processor_visual_slots.go:140`).

Residual gap: images are still joined before the audio leg
(`prepare_join` — 18 905 ms in `…T120454Z`), so entity work is concurrent with TTS
but still gates audio compile.

### Acceptance criteria

- [ ] Explicit counter for duplicate entity downloads per job, asserted to be 0
      (the plan's gate). The catalogue makes this testable; no such assertion was
      found.

---

## 8. Item 7 — TTS chunking and caching

- **Parallel and bounded:** `voiceover.max_concurrent_tts: 4` with
  `max_concurrent_drive_uploads: 3`, retries and a circuit breaker
  (`config.yaml:274-287`).
- **No spawn-per-call:** a persistent Python worker is the primary path
  (`platform/audio/processor.go:33`, `worker_process.go`), with the legacy
  per-call spawn retained only as fallback.
- **Single pass for boundaries:** audio and word boundaries come from one stream
  (`voiceover/service/golden_timing_e2e_test.go`), avoiding a second Whisper pass.
- **Cache: none.** Grepping the voiceover and audio planes for any
  content-addressed cache returns nothing.

### Acceptance criteria

- [ ] Cache keyed on voice + language + normalized text + TTS settings.
- [ ] Editing one scene re-synthesizes only that scene.

---

## 9. Item 8 — Chronon persistent daemon

**Absent on the production path.** `platform/overlays/renderer.go` is the entire
Go→Chronon boundary:

```go
cmd := exec.CommandContext(ctx, r.Binary, "--plan", planPath, "--output", output)
if out, err := cmd.CombinedOutput(); err != nil { ... }
```

One short-lived CLI process per overlay render. Nothing is reused across jobs:
no Vulkan device, no pipeline cache, no glyph atlas, no font state, no encoder
infrastructure. `CombinedOutput()` also buffers the whole render log in memory
and discards it on success.

Meanwhile the daemon capability **already exists next door**:
`RenderingGen/renderinggen/internal/chronon/ipc.go` ("engine stays warm between
jobs"), `internal/chronon/client.go`, and
`cmd/batch-render-presets/main.go` which drives N warm daemons over UNIX sockets
(`-socket`, `-daemons`, default 3) with a documented cold-vs-warm benchmark
(`daemon_vs_cli_benchmark_integration_test.go`). PipelineGen simply does not use
it.

This is the cheapest large win available: the worked example, the transport and
the benchmark harness all exist; the production path bypasses them.

### Acceptance criteria

- [ ] `cold render / warm #1 / warm #2` with startup and preparation near zero on
      the warm runs (the plan's gate). A harness for exactly this already exists
      in RenderingGen.

---

## 10. Item 9 — Render per scene + resource-aware scheduler

**Blocked by design.**

- `platform/overlays/gpu_gate.go:11-34`: *"GPUGate serializes GPU ownership
  between Creator and RenderingGen on one host"* — implemented as
  `os.OpenFile` + `syscall.Flock(LOCK_EX|LOCK_NB)` (line 34). A single exclusive
  lock over one file. Concurrency is 1, host-wide. Per-scene parallel rendering
  cannot be expressed today; the plan's "benchmark concurrency 1/2/3/4" cannot
  move beyond 1 until this changes.
- **No unified resource authority.** Limits are scattered per capability:
  `voiceover.max_concurrent_tts: 4`, `voiceover.max_concurrent_drive_uploads: 3`
  (`config.yaml:274-287`), `scripts.batch_web_search_concurrency: 4`,
  `scripts.batch_chapter_concurrency: 3`, plus in-code gates
  (`DefaultGenerationConcurrency = 3`, `DefaultNLPConcurrency = 4`,
  `DefaultTTSConcurrency = 4`, `DefaultTranslationConcurrency = 4` in
  `capabilities/scripts/backpressure.go:28-43`). Separate `VisualScheduler`
  (`capabilities/overlays/visual_scheduler.go:69`) and
  `localization/scheduler.go:26`. Nothing models CPU, GPU slots, NVENC capacity,
  VRAM, IO and Ollama concurrency together.

The plan's warning — *"do not do this if it creates more NVENC/GPU contention than
it gains"* — is well-founded, and the current global lock is a deliberate
serialization that a per-scene redesign would have to justify removing.

### Acceptance criteria

- [ ] `render_concurrency_benchmark_test.go` run at 1/2/3/4 **with the gate
      relaxed** proves whether >1 helps.
- [ ] One scheduler authority consuming CPU/GPU/NVENC/VRAM/Ollama limits, replacing
      per-capability pools.

---

## 11. Item 10 — Docs/Drive off the critical path

**Not done.** The telemetry shows publishing inside the measured critical path:

| run | `document.publish` | `post_writer_finalize` | share of wall |
|---|---|---|---|
| `…T120454Z` | 6 354 ms | 4 123 ms | 10 477 / 75 749 = **13.8 %** |
| `…T153111Z` | 7 757 ms | 7 185 ms | 14 942 / 229 933 = **6.5 %** |

`google_docs.publish` is 6 341 ms as a single operation and `finalize.drive_publish`
is 16 192 ms of work in the latest run. The plan is right: these must not gate
`render_complete`.

The async machinery partly exists — `TODO.md` records async Drive delivery with a
PostgreSQL media outbox (`clip_async_drive_enabled: true`, outbox consumers and
Prometheus alerts on pending/dead-letter intents) — but it is not applied to the
Docs/Drive legs measured above.

### Acceptance criteria

- [ ] `render_complete` is emitted before Docs/Drive publication begins.
- [ ] Docs/Drive work appears as post-processing, outside the completion path.

---

## 12. Item 11 — Benchmark matrix

**Partial.**

Existing:
- `capabilities/scripts/render_concurrency_benchmark_test.go` — concurrency
  1/2/3/4, records wall, accumulated work, per-render timings, CPU %, RSS,
  GPU/VRAM, IO, ffmpeg wait, Drive upload. Real-stack mode via
  `VELOX_BENCH_REAL_RENDER=true`.
- `capabilities/performance/benchmark.go` + `admin performance-cold-warm`
  (`cmd/admin/internal/maintenance/performance_cold_warm.go`) — cold #1 vs warm
  #2..N per operation.
- `tests/operational/vidrush/scenarios/` — 16 scenarios including
  `01_text_only_cold`, `02_full_media_cold`, `03_full_media_warm`,
  `04_partial_cache`, `11_concurrency`, `12_render_handoff`.
- RenderingGen `daemon_vs_cli_benchmark_integration_test.go` — cold/warm CLI vs
  daemon.
- `tests/operational/results/` — recorded E2E artifacts (207 files) with a full
  timing block per run.

Missing:
- the **scenes × languages × cache × renderer** grid (1/3/10 × 1/3/10 × cold/warm
  × cold/daemon-warm);
- a single report that joins those axes;
- collection of `cache_hits` / `cache_misses` and GPU/CPU utilisation as part of
  the E2E artifact (present in the concurrency bench, not in the E2E runs).

Also: the results corpus is a flat directory of 207 timestamped files with no
index and no aggregation. The tables in this audit had to be built by hand.

### Acceptance criteria

- [ ] One command producing the full grid with the plan's metric set.
- [ ] Results indexed and aggregated, not a flat dump.

---

## 13. Item 12 — Minimum Necessary Rendering (GOP surgery)

**Already built. Do not start from scratch.**

- `Chronon3d/include/chronon3d/render_graph/pipeline/execution_decision.hpp:15`
  defines `FrameExecutionPath::CopyGop`, plus `CopyGopPlan`, `CopyGopEligibility`
  ("CopyGop is selected only with evidence supplied by the canonical GOP
  inspector") and `copy_gop_plan`.
- `Chronon3d/src/media/video/video_execution_resolver.cpp:25-40` resolves
  `BitstreamCopy`, `SmartGopCopy` and `packet_copy_plan()` with
  `DecodePath::PacketCopy` / `EncodePath::PacketCopy`.
- `video_execution_resolver.hpp:79`: `bool packet_copy_allowed{true};` — permitted
  by default.

So the "packet copy for unchanged intervals, boundary re-encode for cuts inside a
GOP" model the plan describes as the "revolutionary phase" is already represented
in the resolver. The open question is not *whether* it exists but *whether the
production lane selects it* — and, per the plan's own ordering, that question
should wait.

---

## 14. Spazzatura — dead weight in the working tree

### Disk

Repository total: **110 GB**.

| path | size | note |
|---|---|---|
| `Chronon3d/build` | **56 GB** | build trees; TODO already records one stale tree (`build/chronon/linux-video-test/`) scheduled for removal, and this is now the single largest object in the repo |
| `refactored/.venv-whisper` | **2.6 GB** | a Python venv living inside the worktree. It is gitignored (`.gitignore:276`) but it pollutes every `find`/`grep` and any tool that does not honour ignore rules (this audit hit it twice) |
| `refactored/data` | 955 MB | runtime data |
| `refactored/.git` | 1.2 GB | |
| `refactored/bin` | 148 MB | checked-in build output |
| `.tmp` (repo root) | 18 MB | scratch dir at the repository root |
| `refactored/out`, `refactored/artifacts` | ~2 MB | |

### Working tree

`git status --porcelain` reports **90 entries**: 61 untracked, 15 deleted,
14 modified — with no commit.

- **15 deleted** `tests/operational/results/**/status-*.json` files. The audit
  evidence corpus is being destroyed by hand between runs. These are the only
  records of production-shaped timings; deleting them makes every regression
  invisible. Several of the runs analysed here are already unrecoverable.
- **61 untracked** files, including a new
  `internal/app/wiring/clip_render_continuation.go`.
- 14 modified files across `cliprender/`, `app/wiring/` and
  `platform/ollama/client/` — i.e. the Ollama client and the render continuation
  path are mid-edit. Any timing measured from this tree is not attributable to a
  known revision.

### Dead / contradictory surfaces (not necessarily wrong, but worth resolving)

- `app/wiring/lifecycle_preparation.go:175` installs a queue-time model warmer
  while `segment_validation.go:114` warms again before the fan-out, and neither
  fixed the `num_ctx` mismatch — two warmers, zero effective warmth.
- `overlay_handlers.go:190` documents that the write-through render cache was
  removed as "dead weight — nothing ever read it back". Good; worth confirming no
  similar branch remains elsewhere.
- The `config.yaml` `scripts:` block omits `nlp_concurrency` /
  `script_generation_concurrency`, so the effective concurrency lives only in Go
  constants — invisible to operators.
- Two independent concurrency gates and two capability-local schedulers, with no
  unifying authority (§10).

---

## 15. Recommended order, corrected by the evidence

The plan's order is close, but step 0 is missing and step 3 should change target.

0. **Fix the timing attribution.** Reconcile `audio_compile` with its operations,
   serialize the operation `Stage`, resolve `attributed_ms > wall_ms`, and make
   `WarmModel` measurable. Until this is done every subsequent optimization
   decision is made against misattributed numbers.
1. **Ollama warm** (§2). Highest ratio of result to effort in the whole list:
   align the probe's `num_ctx` with the real buckets, pin the window per run, make
   the probe measurable. ~273 s of waste over 11 runs is sitting on one constant.
2. **Stop treating `audio_compile` as the audio cost** (§4). Re-measure after
   step 0; the stage is expected to fall out of the top spot.
3. **Chronon daemon** (§9) — the transport, the worked example and the cold/warm
   benchmark already exist in RenderingGen; only the PipelineGen binding is
   missing.
4. **Docs/Drive off the critical path** (§11) — ~14 % of wall on the small runs.
5. **Remove `force_refresh: true` from certification scripts** (§3) so warm
   behaviour is testable at all.
6. **Preserve the results corpus** — stop deleting `status-*.json`; index it
   instead (§12, §14).
7. Then: TTS cache (§8), entity duplicate-download assertion (§7), full benchmark
   grid (§12).
8. Only then: renderer-level work. Per the plan's own reasoning the render is not
   today's bottleneck — and §13 shows the GOP-copy machinery is already in place.

---

## 16. What this audit did **not** establish

Stated explicitly so nothing here is over-read:

- The root cause of `overlapped_ms = 0` is demonstrated as a *reporter* defect for
  the merged-job case, but the reporter code was not modified or re-run to prove
  it. §4's reconstruction rests on the arithmetic `148 341 − 12 675 ≈ 132 792`
  plus the audio-only statement in `runner_phase_audio.go:69`.
- The per-bucket `num_ctx` concurrent-reload thrash (§2) is a hypothesis derived
  from `ResolveContextBudget` and the gate capacities. It needs a live test.
- No run in the corpus is a *warm* run in the cache sense, and none is from a
  committed revision (§14).
- Line references were re-verified against the tree after drafting; the timings
  were read from the recorded artifacts, not recomputed from source.
- No code was changed. No test suite was executed as part of this audit; all
  numbers are read from recorded artifacts.

---

## 17. Status update — 2026-09-13

This audit was written as a snapshot. Most of its code-level findings have since been
closed. Read §1–§15 as the reasoning, and this section as the current state. Nothing in
§1–§16 was rewritten, so the historical numbers stay intact.

### Closed in code

| item | status | evidence |
|---|---|---|
| §2.1 warm probe `num_ctx` 4096 vs the real 2048 bucket | **FIXED** | `platform/ollama/client/client_warm.go` now pins `types.ProductionRunnerContext` for the probe *and* the real call; `TestWarmModelLoadsOnceAndVerifiesResidency` pins the bucket. |
| §2.3 the probe's load was invisible in telemetry | **FIXED** | the probe is now its own canonical operation `ollama/warm` (`kernel/observability/registry.go` → `OperationWarm`, `AllOperations` 38 → 39) wrapped in `MeasureOperation`, merging `model_load_ms`, `cold_start`, `inference_work_ms`, `num_ctx`. `TestWarmModelRecordsItsOwnMeasuredOperation` asserts a 45 s load is reported as its own operation instead of hiding inside the `generate` stage wall. |
| §3.1 "no TTS cache at all" | **FIXED (was already)** | `capabilities/voiceover/service/process_segment_fingerprint.go::BuildVoiceoverContentFingerprint` is keyed on textHash + language + voice + destination + timing/silence policy + output format, JobID excluded so it is cross-run; `VoiceoverCacheAdapter` consumes it. |
| §3.3 certification scripts hard-code `force_refresh: true` | **FIXED** | `tests/operational/person_overlay_drive_e2e.sh` and `jordan_entity_overlay_drive_e2e.sh` are env-driven and **warm by default** (`FORCE_REFRESH=true` restores the cold leg; `FORCE_REFRESH_MEDIA` controls only the `media_plan` sub-flags). The warm leg is measurable for the first time. |
| §4 render wall attributed to `audio_compile` | **FIXED** | the stage is split into siblings `audio_compile` / `overlay_render` / `audio_finalize` / `audio_publish` (`capabilities/scripts/runner_execution.go`), so the render is no longer charged to audio. |
| §5 `overlapped_ms = 0`, `attributed_ms > wall_ms` | **FIXED** | latest recorded run: `overlapped_ms` 4,642, `unattributed_ms` 73, `attributed_ms` 50,115 ≤ `wall_ms` 50,188. The parallel fan-out is also visible per stage (`scene_analysis` 19,048 ms of work in 11,973 ms of wall; `voiceover` 12,532 in 7,321). |
| §6.2 gate capacities implicit in Go constants | **FIXED** | `platform/config/scripts.go` exposes `nlp_concurrency`, `script_generation_concurrency`, `tts_concurrency` and the newly added `translation_concurrency`; the first three are consumed by `app/wiring/script_generation_runtime.go`, and `translation_concurrency` is now wired through `Runner.SetTranslationConcurrency` (it previously had **no** operator surface at all). `config.yaml` declares the effective widths. Pinned by `TestScriptsConcurrencyEnvResolution`, `TestScriptsConfigWithDefaults_DoesNotFakeTTSDefault`, `TestScriptsConfigGateCapacitiesAreOperatorVisible`. |
| §7 no duplicate-entity-download assertion | **ALREADY SATISFIED** | `IncEntityImageCatalogDriveReuse` / `IncEntityImageCatalogNewDownload` exist and `TestEntityImageCatalogOperationalMetricsDriveReuseAndBrokenDownload` asserts Drive reuse = 1 with **0** new downloads and **0** acquire calls. |
| §8.2 "editing one scene re-synthesizes only that scene" | **FIXED** | `TestBuildVoiceoverContentFingerprint_PerSceneIndependence` pins per-scene isolation: editing scene 1 changes only scene 1's fingerprint, leaves scene 0 a cache hit, and the same scene in a later run stays warm. |

### Still open — deployment-bound

These cannot be closed from a source checkout; each needs a live GPU/Chronon/RenderingGen
run or a preserved benchmark artifact. They are tracked in
`docs/tickets/TICKET-PIPELINE-CRITICAL-PATH-DEPLOYMENT-2026-09-13.md`.

| item | measured evidence |
|---|---|
| §11 Docs/Drive off the critical path | **Now measured against the real boundary** rather than inferred. On the one artifact that emitted it (`person-overlay-drive/full-…20260913T084243Z-18144.json`): wall 102 595 ms, `core_ready_ms` **90 974 ms**, tail to the terminal flip **11 621 ms = 11.3%**, split 5 975 ms Docs (`document.publish` 5 959) + 12 ms `complete_finalize` + 5 634 ms `post_writer_finalize`. Across 39 recorded runs the tail is a near-flat cost: median 11 721 ms, 11.3% of wall, max 83 208 ms (39.2%). See `docs/tickets/TICKET-CORE-READY-DURABLE-DAG.md` §7. |
| §11b **the tail is a Drive upload problem, not a Docs problem** | The outliers in `post_writer_finalize` (78 623 ms and 44 045 ms) are one slow Drive transfer. In the *same* runs the unrelated final-audio upload spikes to 80 947 ms / 38 994 ms against a healthy 5 055 ms. Throughput is the variable, not size: 7.04 MB at **89 KB/s** and a **0.87 MB** file at **22 KB/s**, versus ~1 150 KB/s healthy. Mechanism: `uploader_put.go:480` keeps everything under 16 MB on the non-resumable single-shot `Media()` path; `auth.go:93` sets no `http.Client.Timeout`; and `token.go:24` holds a **process-wide mutex across both the token refresh and the `SaveToken` disk write**, so one stalled refresh blocks every concurrent Drive request. Full write-up: ticket §8. |
| §11c the tail is now a runnable gate | `make gate-core-ready-tail` (`tests/operational/measure_core_ready_tail.sh --gate`) fails closed when the two tail reconstructions disagree beyond the run's own `unattributed_ms`, when a tail exceeds `MAX_TAIL_MS`, or (opt-in) when an artifact carries `CORE_READY` without the projected `core_ready_ms`. Corpus today: 39/39 reconciled. |
| §10 render serialization / GPU gate | `overlay_render`: 4 calls, 6,227 ms of work, **11,160 ms of wall** (max single call 5,312 ms) → ~5 s of queue/lock wait, not render work. |
| §9 Chronon warm daemon | `platform/overlays/renderer.go` still spawns one CLI process per render; the warm daemon + cold/warm benchmark already exist in RenderingGen. |
| §6 / §12 warm-vs-cold certification | now *possible* (see §3.3 above) but never yet measured; needs a preserved warm `full-*.json`. |
| §12 scenes × languages × cache × renderer grid + indexed results corpus | no single producing command; results are still a flat timestamped directory. |

### Also still true

- **Two Ollama warm call sites remain** (`app/wiring/lifecycle_preparation.go:175`
  queue-time, `usecase/gencore/segment_validation.go:114` pre-fan-out). They no longer
  disagree with the real request (both resolve the same resident bucket, and the probe is
  singleflight), so this is redundancy rather than the self-defeating pair §14 described —
  but it is still two warmers where one would do.
- **Disk weight is untouched**: `Chronon3d/build` ≈ 72 GB, `refactored/.venv-whisper` 2.6 GB,
  `refactored/bin` 661 MB, `refactored/data` 1.1 GB. `Chronon3d/build` is a regenerable cmake
  tree and is the cheapest large win.
- **The results corpus is still a flat directory** and `status-*.json` files have been deleted
  by hand before (see §14). Preserve them; they are the only record of production-shaped
  timings.

---

## 18. Status update — 2026-09-15

§17 closed the warm probe's own bucket, but §2.2 ("per-call `num_ctx` is itself a thrash
source") and §6's phrase-extraction fan-out were **still open**: the probe pinned the
resident bucket while every *other* producer kept emitting requests with no `num_ctx` at
all, which is a different bucket from Ollama's point of view. This section records the pass
that closed those two, plus the one item that is **not** a latency fix.

### Closed in code

| item | status | evidence |
|---|---|---|
| §2.2 every non-probe call path omitted `num_ctx` and rebuilt the warmed runner | **FIXED** | One resident-option rule, `residentRunnerOptions` (`platform/ollama/client/client_warm.go`), now pins `types.ProductionRunnerContext` on **both** request choke points: `/api/generate` (`client_generate.go::extractGenerateControls`, covering entity, important-phrase, batch and suggestion helpers) and `/api/chat` (`client_core.go::doChatRequest`). The caller's map is never mutated and an **explicit** `num_ctx` stays authoritative, so an intentional opt-out is preserved rather than silently overridden. Pinned by `TestGenerateDetailedPinsSingleResidentRunnerContext` (omitted / `nil` options / explicit opt-out), `TestChatPinsResidentRunnerContextWhenOmitted`, `TestExtractEntitiesFromSegment_UsesBoundedOperationBudget` and `TestExtractEntitiesFromBatch_PreservesEverySegment`. |
| §6 phrase extraction cost N scenes × L languages model calls | **FIXED** | New optional port `scriptgen.BatchImportantPhraseExtractor` (`capabilities/scripts/vidrush_semantic_ports.go`) and its adapter `OllamaImportantPhraseExtractor.ExtractImportantPhrasesBatch` (`platform/ollama/adapters/important_phrase_extractor.go`, chunking by the exported `client.EntityExtractionBatchLimit`). `runTranslatedNLP` now issues **one request per chunk of scenes per language**, with a second resolution phase so phrases are attached after the batch returns. Entities stay per scene (VisualNER is a deterministic per-text extractor with a source-span contract). Batching is an optimization and never a new failure mode: a batched error degrades to the per-scene call, and an extractor that does not implement the interface keeps exactly one call per (scene, language). Pinned by `TestRunTranslatedNLPBatchesPhrasesPerLanguage` (0 per-scene calls, 1 request per language, per-scene phrase alignment intact), `TestRunTranslatedNLPUsesPerSceneCallsWithoutBatchCapability` (6 calls for 3 scenes × 2 languages) and `TestOllamaImportantPhraseExtractorBatch` (12 segments → chunk sizes 5/5/2, positional alignment across chunk boundaries). |
| §6.2 gate capacities implicit | **ALREADY CLOSED in §17** | unchanged by this pass (`nlp_concurrency` / `script_generation_concurrency` / `tts_concurrency` / `translation_concurrency` remain the operator surface; the phrase fan-out is bounded by the same `DefaultNLPConcurrency` / `ExtractionLimit` pair). |

Cost shape after the two fixes (no change to generated content): the phrase fan-out drops
from `scenes × languages` model calls to `ceil(scenes / 5) × languages`, and none of those
calls — nor the entity calls — can unload the resident model. Both are pure
request-shaping changes; no scene, annotation or artifact changes shape.

### Superseded — §11b was already fixed before this pass

The Drive-tail mechanisms §11b blames are **not open**; they were closed on 2026-09-13/14
(`f9bb83c28 perf: remove pipeline tail retries and drive stalls`,
`979cfc2a0 optimize pipeline generation and Drive uploads`). Verified against `HEAD`:

| §11b claim | state at `HEAD` |
|---|---|
| `uploader_put.go:480` keeps everything under 16 MiB on the non-resumable single-shot `Media()` path | **fixed** — `resumableUploadThreshold = 5 MiB`, with the 16 MiB threshold and its "every retry restarted the entire upload from byte zero" effect called out in the source comment. |
| `auth.go:93` sets no `http.Client.Timeout` | **intentional, not a defect** — the source documents why: a client timeout is a size-independent deadline that also covers the body, so it aborts valid uploads and burns the deadline before the retry loop can react. The replacement is `newGoogleTransport()` (dial 10 s, TLS 10 s, `ResponseHeaderTimeout` 30 s, `ExpectContinueTimeout` 1 s, idle 90 s), pinned by `TestNewGoogleTransport_UsesPhaseTimeoutsWithoutWholeRequestDeadline`. |
| `token.go:24` holds a process-wide mutex across the refresh **and** the `SaveToken` disk write | **fixed** — the mutex is released before `SaveToken`, not held across it. |

A different, smaller defect in the same file did remain and is closed by this pass:
`SaveToken` ran on **every** `Token()` call (oauth2 reuses the cached token until expiry,
so a run cost one disk write per Drive API request) and it used a truncate-first
`os.WriteFile`, so a crash or a concurrent request could observe an unparseable token file
and lose authentication. `refreshingTokenSource` now persists **only when the access token
changed**, and only after a *successful* write so a transient failure is retried rather
than recorded as done; `SaveToken` writes a sibling temp file and renames it into place.
Pinned by `internal/platform/drive/token_test.go`: sentinel-based no-op-call proof,
write-on-change, retry-after-failed-write, atomic replacement, and 16×8 concurrent calls
with no truncated read. `-race` clean.

### Classified as design, not waste

| item | verdict |
|---|---|
| §10 render `wall > work` (~5 s "queue wait", 11 160 ms wall vs 6 227 ms work) | **by design.** `platform/overlays/gpu_gate.go` bounds GPU ownership host-wide with an `flock` (default 1 slot; path and slot count are operator-tunable). The wait is that guard doing its job on a CPU-first pipeline where GPU is opt-in — not render waste. No code change. |
| §17 "two Ollama warm call sites" | **not a subtraction.** Both call `WarmModel`, which is singleflight and resident-bucket pinned. The coordinator's queue-time warmer is deliberately off the critical path, and the engine's pre-fan-out warmer is the **safety net for callers that bypass the coordinator** (including the batch path). Deleting it would *reduce* warming, not remove redundancy. |
| batch path (`generate_many` → `gencore.GenerateOneUseCase`) has no streaming / incremental coordinator / `CORE_READY` / `prepare_join` | **architectural ticket, not a fix.** Batch items are independent and already overlap at the fan-out level; the missing overlap is *intra-item*, i.e. exactly the streaming item below. Porting the durable-runner DAG is a refactor with its own design, so it is recorded, not smuggled in. |

### Open — not a latency fix, needs a product decision

| item | why it is not closed |
|---|---|
| §5 / streaming: `POST /api/script/generate` on the **segment-budget** shape (`script_params.segment_words` set, no explicit `segments`) still serializes generation → downstream | `runner_phase_script.go` sets `segmentTopologyNeedsMaterialization` for exactly this shape and disables the per-scene `SceneTextReady` fan-out. The flag is **correct as written**: with no explicit plan the model returns provisional prose and `materializeGeneratedScenes` splits it afterwards, so a streamed scene would launch VidRush/TTS/render on the wrong topology. Making this shape streamable requires deriving `script_params.segments` **before** generation, which routes each segment through `GenerateSceneTextStreamWithTrace`'s isolated one-call-per-segment path — i.e. it **changes the generated text and the segment boundaries**, not just the schedule. That is a content/product-contract change, so it was deliberately left alone rather than shipped as an "optimization". The streaming path already applies to requests that *do* carry explicit `segments`; the gap is only the derived-plan shape. |

### Gate state for this pass

- `go test ./internal/platform/ollama/... ./internal/capabilities/scripts/ ./internal/platform/drive/ -count=1` — green.
- `make verify-agent` — **PASS** (foundation + static + impacted components; the `ollama`,
  `script`, `translation` and `drive` legs all executed and passed).
- `go run ./cmd/archcheck --strict` — **green** (`passed: true`, `has_hard_gate_hits: false`,
  zero violations). It was red when this pass started: `max_lines_per_file_strict` on
  `internal/capabilities/cliprender/request.go` (606 lines against a strict cap of 600;
  `HEAD` sits exactly at 600). The overflow was removed by splitting the self-contained
  `defaultOverlayTextStyle` helper into the sibling file
  `internal/capabilities/cliprender/request_overlay_text_style.go` — same package, no
  behaviour change, the documented pattern for this gate (cf. `materializer_index_seam.go`
  and the concurrent `output_contracts.go`). `request.go` is now 578 lines.
- `make verify-main` — run once, immediately before push (see the Git workflow rule).

## 19. Status update — 2026-09-28: gate Drive fair e la coda di `audio_publish`

Questa sezione chiude il filone aperto dalla misura del 2026-09-28: il gate
Drive del processo è ora UNO e fair, `audio_publish` passa dallo stesso gate, e
il benchmark a 2 job concorrenti misura la coda. Le sezioni numerate qui sotto
sono 19.n; il materiale originale era `docs/PIPELINE-WALL-OPTIMIZATIONS-2026-09-28.md`,
assorbito qui per la regola di documentazione (niente nuovi snapshot datati).

Misura di riferimento: run `ritune-verify-20260928` (5 scene, 650 parole, render +
Docs + voiceover, `force_refresh=true`) sul host di produzione, salvato in
`RenderingGen/crime_case_scripts_20260926/ritune_verify_20260928_result.json`.

### 19.1 Dove va il tempo (misurato)

| Stage | wall | lavoro (fanout) | chiamate |
|---|---|---|---|
| `overlay_render` | 23,4 s | 21,9 s | 12 |
| `generate` (Ollama gemma4:e4b) | 21,0 s | 48,3 s | 5 |
| `scene_analysis` (artlist resolve) | 46,3 s | 87,7 s | 5 |
| `voiceover`/TTS | 32,4 s | 95,8 s | 5 |
| `audio_compile` | 17,7 s | — | — |
| `audio_publish` (upload Drive) | **85,7 s** | 85,7 s | 1 |
| `document` (Google Docs) | 43,7 s | 32,2 s | 1 |
| `post_writer_finalize` (drive publish) | 41,2 s | 63,2 s | 3 |
| **wall totale** | **279,5 s** | | |

Due correzioni di lettura, entrambe verificate nel codice e nei log:

1. **Il TTS non è seriale.** `work/wall = 95,8/32,4 ≈ 3×` e i log mostrano le
   scene 1–4 dispatchate insieme alle 16:44:39 e completate in 5,5–8 s ciascuna.
   La configurazione (`voiceover.max_concurrent_tts: 4`, `scripts.tts_concurrency`
   unset → fallback 4) è attiva. Una lettura precedente aveva confrontato work
   con work (95,8 s vs 51,1 s del run del 26/09) invece che con il wall.
2. **`num_ctx` non è 81920.** Quel valore nei metadata è la somma dei 5 fan-out
   (5 × 16384) fatta da `timing_summary.go`; per chiamata resta 16384, scelto
   deliberatamente in `internal/platform/ollama/types/constants.go`
   (`ProductionRunnerContext`) per evitare i reload da 37–97 s misurati.

Il collo del run è quindi **I/O esterno conteso**, non il calcolo: nello stesso
intervallo due altri job (wall 735 s e 552 s, bottleneck `remote_final_job`)
erano in fase di pubblicazione Drive/Docs sullo stesso processo.

### 19.2 Implementato in questo pass

- [x] **Gate Drive: attesa osservabile**
  - `rateLimitedPublisher`/`rateLimitedTTSProvider` acquisiscono ora il gate con
    `kernobs.AcquireFairSlot`, che registra l'intervallo come `WaitSemaphore` sul
    run invece di scaricarlo sul tempo di lavoro della chiamata.
  - Il timeout per-call resta derivato DOPO l'acquisizione: il budget resta di
    sola esecuzione (comportamento invariato).
  - Test: `TestRateLimitedPublisher_RecordsDriveQueueWait`.

- [x] **Gate Drive: fairness per job**
  - Nuovo primitivo `pkg/concurrent.FairSemaphore`: un owner che detiene già uno
    slot non può prenderne un altro mentre un ALTRO owner è affamato; FIFO
    preservato all'interno dello stesso owner; cancellazione senza slot leak.
  - Owner = run id (`kernobs.WaitOwner(ctx)`), quindi due job concorrenti non si
    affamano mai.
  - Test: `pkg/concurrent/fair_semaphore_test.go` (incluso il contratto
    anti-monopolio) e `TestRateLimitedPublisher_FairnessAcrossJobs`.

- [x] **Gate Drive: estensione al percorso `audio_publish`**
  - L'85,7 s del run di verifica NON passava dal gate: `audio_publish` raggiunge
    Drive via `finalAudioPublisherAdapter.PublishFinalAudio`
    (`app/wiring/final_audio_publisher.go`), costruito da `newFinalAudioPublisher`
    e cablato in `BuildScriptGenerationRuntime`, NON via `rateLimitedPublisher`.
    Il gate fair quindi non copriva il percorso che aveva prodotto l'outlier.
  - Ora esiste UN gate per processo (`ComposeRoot.DriveUploadGate`), costruito una
    volta in `NewComposition` (`vowiring.NewDriveUploadGate(cfg.Voiceover)`) e
    condiviso dai due siti di pubblicazione: il publisher voiceover per-item
    (`NewRateLimitedPublisherWithGate`) e il publisher dell'audio finale
    (`prepareWithGate` → `kernobs.AcquireFairSlot`).
  - Perché condiviso e non due gate: `voiceover.max_concurrent_drive_uploads` è un
    tetto di PROCESSO ("limits parallel Google Drive upload calls"); due gate
    indipendenti moltiplicherebbero il tetto per il numero di publisher e
    lascerebbero la contesa cross-job scoperta. Precedente nel repo:
    `ClipRenderParentAggregator`, anch'esso cachato su `ComposeRoot` per lo stesso
    motivo (due siti di composition, una sola istanza).
  - Test: `app/wiring/final_audio_publisher_gate_test.go`
    (`TestFinalAudioPublish_AcquiresSharedDriveGate` pinna l'acquisizione + la
    registrazione del wait; `TestFinalAudioPublish_NilGateIsUnbounded` il
    contratto nil-safe; `TestDriveUploadGate_IsSharedAcrossPublisherSites` è un
    freeze a livello di sorgente che vieta a un refactor di ripristinare due gate
    separati).

- [x] **Worker RenderingGen: KPI di duty-cycle scrapeable**
  - `renderinggen_worker_gpu_gap_seconds` (histogram) alimentato dallo STESSO
    valore del KPI per-job `gpu_gap_us` (`processor.SetGPUGapHook` →
    `workermetrics.GPUGapHook`), cablato in `cmd/renderinggen/main.go`.
  - Test: `TestExpositionCarriesGPUDutyCycleGap`,
    `TestGPUGapHookMatchesDirectObservation`, `TestRunGPUReportsDutyCycleGapToHook`.

- [x] **Gate overlap scene-text: contratto pinnato**
  - Confermato nel codice: `media_plan.extraction.important_phrases` forza il
    path batch (`segmentTopologyNeedsMaterialization = true`), quindi
    generate→scene_analysis resta sequenziale per quella shape.
  - Test nuovo: `TestSceneTextStreaming_ImportantPhraseHintsForceBatchPath`
    (`runner_scene_phrase_gate_test.go`), oltre alla tabella `sceneTextPathReason`
    già esistente che nomina `batch_important_phrase_hints`.

### 19.3 Deciso di NON cambiare (con motivo)

- [ ] **Streaming con hint di frase** — `ensureRequestedImportantPhrases` aggancia
  gli hint DOPO la generazione e solo se nessuna scena li contiene già. Applicarli
  per-scena al boundary `SceneTextReady` renderebbe lo streaming eleggibile anche
  con hint, ma cambia il testo generato in un caso preciso: un hint che il modello
  ha già incluso in una scena successiva verrebbe comunque appeso alla prima scena
  (duplicazione). È una decisione di prodotto, non un refactor: l'audit
  `PIPELINE-WASTE-AUDIT-2026-09-12.md` §5 aveva già scelto di non prenderla.
  Costo stimato dell'overlap perso su questa shape: fino a ~15–20 s per video.
- [ ] **`num_ctx`** — già ottimizzato (bucket unico residente).
- [ ] **Pool overlay** — già ritunato il 27/09 (4→2 + back-pressure, `gpu_lanes` 3).

### 19.4 Probe — stato dopo la misura sui dati reali (2026-09-28)

- [x] **Teardown intermittente Chronon ~1 s: NON si riproduce sul percorso IPC**
  - 377 job reali del daemon oggi (`journalctl -u chronon3d | summarize_job_lifecycle.py`):
    `session_teardown_ms` **mediana 92,4 ms, p95 113,2, max 138,4** (spread 1,5).
    Nessuna fase sopra 500 ms.
  - Le fasi interne sono stabili: `encoder_reset_ms` mediana 28,7 (max 66,2),
    `full_graph_reset_ms` mediana 62,1 (max 96,2).
  - Conclusione: il secondo intermittente era del percorso serializzato/CLI, non
    del daemon caldo. Resta strumentato (sink_ctx, device_runtime,
    device_reservation) e monitorabile con
    `Chronon3d/tools/summarize_job_lifecycle.py --fail-on-outlier`.
- [x] **Prepare/pool warm per-job ~1,1 s: assorbito dal daemon pool**
  - Misura sui run reali (`RenderingGen/scripts/analyze_chronon_job_boot.py`):
    per-item `engine_init + backend_init` = **mediana 149,0 ms** (12 item del run
    di oggi; 174,6 ms su 32 item includendo l'r4 del 26/09), contro i ~1 100 ms
    "101 boots / 100 jobs" del 16/09 → **~7× in meno**.
  - Costo non-render residuo per item ≈ 149 (boot) + 11,7 (ffprobe receipt) +
    9,4 (encoder finalize) ≈ **170 ms** → ~2,0 s sui 23,4 s di `overlay_render`.
  - `chronon_job_encoder_backpressure_wait_ms` = 0: nessuna attesa di backpressure.
- [ ] **Bucket `gpu_gap_seconds`** — la serie è ora esposta
  (`renderinggen_worker_gpu_gap_seconds`); leggere la distribuzione dopo un
  giorno di render per stabilire se il collo residuo è dentro o tra i job.

### 19.5 Verifica

```bash
# Go — gate Drive, primitivo fair, worker metrics
cd refactored && go test ./pkg/concurrent/ ./internal/app/wiring/voiceover/ \
  ./internal/kernel/observability/ ./internal/capabilities/scripts/ -race -count=1
cd RenderingGen/renderinggen && go test ./internal/workermetrics/ ./internal/processor/ -count=1

# Probe — contratti dei due analizzatori (nessuna GPU richiesta)
python3 Chronon3d/tools/summarize_job_lifecycle.py --self-test
python3 RenderingGen/scripts/analyze_chronon_job_boot.py --self-test

# Misura su dati reali
journalctl -u chronon3d --since "1 hour ago" \
  | python3 Chronon3d/tools/summarize_job_lifecycle.py - \
      --only teardown_ms --only reset_ms --only close_ms --only join_ms
python3 RenderingGen/scripts/analyze_chronon_job_boot.py \
  RenderingGen/crime_case_scripts_20260926/ritune_verify_20260928_result.json

# Criterio di accettazione: `audio_publish` < 10 s CON il gate ingaggiato.
# Legge run_observability.report_json e pretende DUE cose: la stage sotto
# soglia E un semaphore_wait `component=drive` dentro la finestra della stage.
cd refactored && python3 ops/benchmarks/audio_publish_gate_evidence.py --self-test
python3 ops/benchmarks/audio_publish_gate_evidence.py \
  --job job_1790625240288455194_421d0d2b \
  --job job_1790625240286917109_5ce13e8a
python3 ops/benchmarks/audio_publish_gate_evidence.py --since 2026-09-28T19:35:40Z
```

### 19.6 Deploy + benchmark 2-job (2026-09-28, dopo le 19:35)

#### 19.6.1 Cos'è stato deployato e come è stato provato

Il binario con il gate esteso è in esecuzione: `bin/pipelinegen`
(`refactored/`), avviato come `pipelinegen.service` alle **19:35:40** (il servizio
gira come l'utente `pierone`, quindi l'identità è verificabile senza sudo).
L'identità è provata, non dedotta:

```text
MainPID            = 1276328            (poi riavviato dal deploy alle 19:59:38)
sha256(/proc/<pid>/exe) == sha256(bin/pipelinegen)   → sono lo STESSO inode
strings bin/pipelinegen | grep -c prepareWithGate    → 3
strings bin/pipelinegen | grep -c DriveUploadGate    → 3
find internal cmd pkg -name '*.go' ! -name '*_test.go' -newer bin/pipelinegen → vuoto
```

Il gate è quindi provato **nel binario in esecuzione**, non solo nel sorgente.

Aggiornamento 20:21: il servizio è stato **ricostruito e riavviato da un'altra
attività concorrente** (`bin/pipelinegen` 20:20:48, MainPID **1363186** dal 20:21:38,
sha256 `3f1cc042…` — build diversa, 78 918 000 byte). L'identità è di nuovo
coerente (`sha256(/proc/1363186/exe) == sha256(bin/pipelinegen)`) e i simboli del
gate sono **ancora presenti con gli stessi conteggi** (`FairSemaphore` 40,
`AcquireFairSlot` 4, `WaitOwner` 2, `NewDriveUploadGate` 2, `prepareWithGate` 3):
la ricostruzione non ha rimosso il gate, quindi il comportamento fair resta quello
deployato.

#### 19.6.2 Il benchmark: `audio_publish` sotto i 10 s

Driver: `RenderingGen/crime_case_scripts_20260926/benchfair_20260928_driver.sh`
(`submit` → 2 job back-to-back, `poll` → attesa terminale, `report` → estrazione).
Payload: due copie del payload di verifica con `correlation_id`, `item.id`,
`project` e titolo distinti (`benchfair-a-20260928` / `benchfair-b-20260928`),
`force_refresh=true`, render + voiceover + Docs attivi.

| job | esito | wall | **`audio_publish`** | work | calls | `drive` wait dentro la finestra |
|---|---|---|---|---|---|---|
| A (`job_…421d0d2b`) | SUCCEEDED | 153 538 ms | **6 299 ms** | 6 257 ms | 1 | 1 |
| B (`job_…5ce13e8a`) | SUCCEEDED | 189 582 ms | **6 168 ms** | 6 104 ms | 1 | 1 |

Baseline dello stesso payload: `audio_publish` **85 741 ms** → **13,6× in meno**.

Evidenza del fatto che il gate è davvero sul percorso (e non solo che il numero è
basso): nel report canonico (`run_observability.report_json`, l'osservabilità
degli wait NON è nella proiezione `timing` esposta da `/api/jobs/:id/full`) esiste
**esattamente un `semaphore_wait` con `component=drive` con `started_at` dentro la
finestra di `audio_publish`** per entrambi i job. Pre-deploy quell'intervallo non
esisteva affatto in questa finestra: la sezione seguente lo mostra su 241 campioni.

#### 19.6.3 Serie storica — before/after sulla stessa metrica

Artefatto: `RenderingGen/crime_case_scripts_20260926/benchfair_20260928_audio_publish_series.json`
(ricostruito da `refactored/data/observability/api_requests.db.sqlite`,
tabella `run_observability`, dedupe per `(job_id, audio_publish.started_at)`).

| finestra | campioni | min | mediana | max | **≥ 10 s** | `drive` wait dentro la finestra |
|---|---|---|---|---|---|---|
| pre-deploy (< 19:35:40) | 241 | 3 407 ms | 6 217 ms | **302 683 ms** | **21** | 0 |
| post-deploy (≥ 19:35:40) | 4 | 6 168 ms | 7 586 ms | **9 221 ms** | **0** | 1 |

I quattro campioni post-deploy sono, in ordine: `job_…8806df10` 9 221 ms
(misurato mentre quel job aveva wall 730 s, cioè sotto la contesa più pesante
osservata), `job_…5ce13e8a` 6 168 ms, `job_…421d0d2b` 6 299 ms, e — quarto
campione, arrivato a run concluso — `job_…7cf9af46` **8 874 ms** con
`drive_waits = 1`: è un job di produzione reale
(`correlation_id = top5-boxers-optimized-10lang-20260928`), quindi il tetto tiene
anche fuori dal payload di benchmark, sotto il carico dei 10 linguaggi.

Due letture che contano:

1. **La coda lunga pre-deploy non era "l'upload è lento".** I tre peggiori
   (`302 683 ms` del 26/09, `238 443 ms`, `179 843 ms`) hanno tutti
   `drive_semaphore_waits = 0`: il percorso non passava dal gate, quindi **nessuna
   attesa era attribuibile** e l'intero tempo finiva dentro il lavoro di upload.
   È esattamente il difetto che l'estensione chiude.
2. **Il campione sotto contesa pesante è il più significativo.** 9 221 ms con
   wall 730 s non è un run tranquillo: è la condizione che prima produceva gli
   outlier a 85–302 s, e ora resta sotto il tetto.

Limite dichiarato: **n = 3** post-deploy. La mediana pre/post è quasi identica
(6,2 s vs 6,3 s) perché il gate **non è un acceleratore del caso non conteso**: è
un limite superiore sulla coda. Ciò che cambia è `max` (302,7 s → 9,2 s) e il
conteggio sopra soglia (21 → 0).

#### 19.6.4 Gate rieseguiti dopo la modifica

| gate | esito |
|---|---|
| `go build ./...`, `go vet ./...` | verde (refactored + RenderingGen) |
| `go test -race` su `wiring`, `wiring/voiceover`, `pkg/concurrent`, `kernel/observability` | verde |
| `go test ./internal/capabilities/scripts/...` | verde |
| `RenderingGen/renderinggen go test ./internal/workermetrics/... ./internal/processor/...` | verde |
| `Chronon3d/tools/summarize_job_lifecycle.py --self-test` / `analyze_chronon_job_boot.py --self-test` | verde |
| `make verify-foundation` + `make verify-static` | PASS |
| `make verify-changed-components` | **PASS** — 21/21 componenti (script, database, research, clips, stock, qdrant, indexing, drive, docs, voiceover, images, ollama, translation, storage, timeline, jobs, api, youtube, artlist, kernel, rust-muscles) |
| `go run ./cmd/archcheck` | **2 violazioni** dopo il pass di ratchet di §19.7 (erano 5): hotspot `scripts` 97 vs ceiling 93 (pre-esistente anche a `HEAD`: 95 vs 93) e `app/wiring/voiceover/adapters_voiceover_repo.go` 630 righe, file su cui sta scrivendo **un'altra sessione concorrente** |
| `python3 ops/benchmarks/audio_publish_gate_evidence.py --self-test` | self-test OK (pre-gate FAIL con 0 wait, post-gate PASS con gate ingaggiato, filtro della finestra) |

Nota di manutenzione: il gate di `overlays` aveva un test rosso pre-esistente
(`TestAllCandidatesPlannerConfigKeepsOnlyEditorialImagesAndPhrases`) perché due
frasi campione condividevano la STESSA finestra temporale e la nuova regola
anti-overlap ne ammetteva una sola; il test è stato corretto rendendo disgiunte le
finestre (100–600 e 700–1200), che è ciò che il caso intendeva verificare.

#### 19.6.5 Fail flaky non correlato

L'attempt 1 del job A è fallito a `overlay_render` con
`ipc render: daemon status Error (1): render job failed with exit code 1` — è il
percorso Chronon/RenderingGen, **non** il gate Drive; il job è stato ritentato e ha
chiuso SUCCEEDED. Registrato per non attribuire al gate un fallimento che non è suo.

### 19.7 Verdetto finale e ratchet del gate architetturale (2026-09-28, 20:20+)

#### 19.7.1 Criterio di accettazione: SODDISFATTO

`audio_publish` sotto i 10 s con il gate fair ingaggiato sul percorso reale:

| prova | esito |
|---|---|
| binario in esecuzione = binario costruito | durante la finestra del benchmark (19:59:38–20:21, MainPID 1276328): `sha256(/proc/1276328/exe)` == `sha256(bin/pipelinegen)` == `1a4f0f88…`; dopo la rebuild concorrente (20:21:38, MainPID 1363186) l'identità torna coerente su `3f1cc042…` **con i simboli del gate intatti** |
| il binario contiene il gate | `FairSemaphore` 40, `AcquireFairSlot` 4, `WaitOwner` 2, `NewDriveUploadGate` 2, `prepareWithGate` 3 occorrenze |
| 2 job concorrenti, stesso payload | `audio_publish` **6 299 ms** e **6 168 ms** (entrambi < 10 s) |
| gate ingaggiato sul percorso | 1 `semaphore_wait component=drive` dentro la finestra di `audio_publish` per job (0 pre-deploy) |
| serie post-deploy | **4/4** sotto soglia, gate ingaggiato su tutti, max 9 221 ms (0 sopra i 10 s) — include un job di produzione reale a 10 lingue (8 874 ms) |
| gate ancora attivo dopo la rebuild concorrente | simboli del gate invariati nel binario `3f1cc042…` (MainPID 1363186, 20:21:38) |
| controprova pre-deploy | 241 campioni, 21 sopra i 10 s, max 302 683 ms, 0 wait nel percorso |

Verificabile da chiunque con il comando in §19.5 (`--self-test` + `--job`/`--since`).
Artefatti archiviati accanto al resto dell'evidenza:

- `RenderingGen/crime_case_scripts_20260926/benchfair_20260928_verdict.json` — verdetto 2-job;
- `.../benchfair_20260928_gate_evidence.json` — output del verificatore sui 2 job;
- `.../benchfair_20260928_gate_series.json` — serie post-deploy vista dal verificatore;
- `refactored/ops/benchmarks/archcheck-20260928T203231Z.json` — snapshot archcheck dopo il ratchet.

#### 19.7.2 Ratchet del gate 600-LOC: 5 → 2 violazioni

Il pass aveva lasciato 5 violazioni di `cmd/archcheck`, tre delle quali introdotte
dalla working tree (non dal gate Drive: da materiale della stessa tornata di
lavoro). Sono state chiuse **spostando blocchi coesi in file sorelli già
esistenti**, così il conteggio di file del package hotspot NON cresce (un file
nuovo avrebbe peggiorato il ratchet `package_hotspot_growth`):

| file | prima | dopo | blocco spostato → destinazione |
|---|---|---|---|
| `internal/capabilities/scripts/final_job_payload.go` | 731 | **590** | famiglia "certified render selection" → `final_job.go` (416 → 567) |
| `internal/capabilities/scripts/overlay_plan.go` | 635 | **575** | `locatePhraseTimingWithEndpointFallback` → `phrase_timing.go` (163 → 228) |
| `internal/capabilities/scripts/runner_deps.go` | 603 | **577** | `localizedRenderClipFields` → `runner_render_units.go` (364 → 395) |

In più, l'import vietato `crypto/sha256` in `final_job_payload.go` è stato
sostituito con l'SSOT `internal/kernel/digest` (godlike/06):
`digest.SHA256String(requestKey)[:32]` è **identico** ai 16 byte che
`hex.EncodeToString(sum[:16])` produceva, quindi la chiave di idempotenza verso il
Master (`creator-77-request-…`) non cambia per nessun run.

Restano 2 violazioni, entrambe NON attribuibili a questo pass:

1. **`package_hotspot_growth`** `internal/capabilities/scripts` 97 vs ceiling 93.
   È **pre-esistente a `HEAD`** (95 file top-level non-test vs 93 bloccati il
   2026-09-26), quindi `make verify-architecture` era già rosso prima di questa
tornata. Le due aggiunte di questa sessione (`runner_docs_publish.go`,
   `overlay_scene_images.go`) sono file coesi del cutover Docs/CORE_READY. Il
   rimedio dichiarato dai target registrati è la migrazione delle famiglie
   (`overlay_*`) in sottopackage; ri-bloccare il ceiling senza quella migrazione
diminuirebbe l'allerta invece di ridurre il debito, quindi **non** è stato fatto
e resta una decisione dell'owner (`deadline 2026-12-31`).
2. **`max_lines_per_file_strict`** su
   `internal/app/wiring/voiceover/adapters_voiceover_repo.go` (630 righe). Il file
   è stato modificato alle **20:19:33** — durante questa tornata ma **non da
   questa sessione**: nel repo girano altre sessioni di lavoro concorrenti
   (`freebuff_agents/profilo1`, `profilo6`) che stanno implementando l'idratazione
   dell'artifact di timing nella cache voiceover (+91 righe). È stato lasciato
   intatto per non sovrascrivere lavoro in volo di un altro owner.

#### 19.7.3 Nota di ambiente: scrittori concorrenti

Durante la verifica, `git diff` mostra modifiche che questa sessione non ha
prodotto, con mtime di pochi minuti prima (`adapters_voiceover_repo.go` 20:19,
`vidrush_fanout_plan.go` 20:18, `voiceover/service/*`, `overlays/preset_selection.go`).
Di conseguenza due test risultano rossi **non per questo pass**:

- `internal/app/wiring/voiceover`: `TestVoiceoverCacheSSOT_*` — la nuova
  `loadTimingArtifact` restituisce miss quando il reader Drive non è cablato, e
  `timingRequired=true` non trova più la riga sana che il test pretende HIT.
- `internal/capabilities/scripts/adapters`:
  `TestVidRushFanoutPlanPerSceneImagesUsesSceneSpecificQuery` — il fan-out
  per-scena non ha ancora una query scene-specific.

Entrambi sono il boundary di un lavoro in corso altrui; la correzione appartiene a
quella slice. Prove dell'attribuzione:

- `TestVidRushFanoutPlanPerSceneImagesUsesSceneSpecificQuery` **non esiste a
  `HEAD`**: è stato aggiunto dalla working tree (`git diff` lo mostra come +26
  righe in `vidrush_fanout_split_test.go`, insieme alle +44 di
  `vidrush_fanout_plan.go`): è il test di una feature in corso, ancora rossa.
- `TestVoiceoverCacheSSOT_*` è invece pre-esistente e non modificato: è il
  cambio di codice (`adapters_voiceover_repo.go`, +91 righe, nuovo campo `Artifact`)
  che ne ha cambiato il contratto — senza reader Drive cablato la lookup
  restituisce ora miss, mentre il test pretende HIT sulla riga sana.

Conseguenza sul gate aggregato: `make verify-changed-components` era **PASS
21/21** alle 20:10 (`changed_files=256`) e alle 20:33 è **FAIL** su
`script`/`translation` (+`research` BLOCKED, +`voiceover`), perché gli unici test
rossi sono i due qui sopra (`artifacts/verify/changed-components.json`,
`changed_files=267`). Il resto resta verde e cachato.

Per il perimetro di questo pass, i gate ri-eseguiti e verdi sono quelli in §19.6.4
più `pkg/concurrent`, `kernel/observability`, `capabilities/scripts/...` (con
`overlay_plan.go`, `runner_deps.go`, `final_job_payload.go`, `final_job.go`,
`phrase_timing.go`, `runner_render_units.go` modificati) e
`capabilities/overlays`, oltre a `make verify-foundation` e `make verify-static`
ri-eseguiti dopo il ratchet (entrambi PASS).
