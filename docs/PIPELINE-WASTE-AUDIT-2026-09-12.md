# PipelineGen — Pipeline Waste Audit

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
- [ ] The stage field is present in the serialized operation report.
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

- [ ] Overlap is computed and non-zero on the parallel fan-out path.
- [ ] `wall_ms` is the authority; stage sums are explicitly allowed to exceed it
      and are reported as `work_ms`, not `wall_ms`.

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
- [ ] The two gates' capacities are documented in config rather than implicit.

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
