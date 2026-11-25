# Overlay GPU failures and script-job latency

**Status:** OPEN  
**Priority:** P1  
**Area:** Script generation, overlay rendering, Chronon GPU worker, final artifact delivery  
**Reproduced:** 2026-11-24 on the Corrado Augias five-scene video

## Problem

The 30-minute Augias job failed four times during overlay compilation before a
successful run. The failures exposed two separate GPU issues: the Vulkan
renderer rejects image effects that are not supported by its strict native-GPU
path, and the shared GPU ran out of allocatable memory after many overlay
renders. The job's `require_gpu=false` setting did not reach overlay renders;
they still ran with `gpu_required=true`. A documented `type: none` effect was
also rejected by payload validation.

The run monitor reported `progress=0%` throughout long-running work. That hid
whether time was being spent in scene analysis, overlay queueing, final render,
or artifact delivery.

## Evidence

| Attempt | Result | Evidence |
| --- | --- | --- |
| R56 `job_1795531333360086593_64d9414f` | Failed after 9m29s | `COMPILING_AUDIO / ENQUEUE_FAILED`; native `EffectStackNode` reported unsupported GPU effect and refused software fallback. The frozen plan had 61 items. |
| R57 `job_1795532056400308118_da7559ca` | Failed after 1m45s | `require_gpu=false` was set in the request, but worker logs still said `gpu_required=true`; the same strict-GPU failure recurred. |
| R58 `job_1795532226171639535_55dbb98b` | Failed after 29s | Validator rejected `overlay_style.image.effects: [{"type":"none"}]` as unsupported, although `docs/operations/script-generate-payload-schema.md` documents that value as the way to clear effects. |
| R59 `job_1795532266551590026_421963e1` | Failed after 1m05s | Removing vignette alone exposed the remaining unsupported rounded-mask/shadow dispatch: “Vulkan alpha-mask, offset, dual-blur and source-over shadow dispatch”. |
| R60 `job_1795532356500565223_b62cdb0c` | Failed after 50s | With image effects, radius, and shadow removed, Chronon failed to allocate a 1920×1080 Vulkan image (`vmaCreateImage`, `VkResult=-2`). The GPU sample showed 15,192 MiB used and 785 MiB free on a 16,376 MiB RTX A4000. Three Chronon daemons held about 11.7 GiB and Ollama about 3.4 GiB. |
| R61 `job_1795532522511159131_b9593507` | Succeeded in 4m36s | Restarting only the job's Chronon daemon raised free GPU memory to about 5 GiB. With GPU-unsupported image effects, masks, and shadows removed, all 56 planned overlays completed on their first attempt. |

The first five attempts occupied 17m53s of wall-clock time from first submit to
last failure; their job execution durations sum to about 13m38s. R61 used a
56-item overlay plan (5 entity images, 21 images, 2 location cards, 4 mixed
chains, 5 phrase chains, and 19 phrase cards; no maps). Its 56 GPU queue waits
totalled 58.2s (1.04s average); image preparation totalled 1.3s. The overlay
render stage took 67.6s and overlapped `AUDIO_COMPILE` (68.2s).

R61 completed worker execution in 275.6s. The largest measured stage was the
remote final job at 171.0s (about 62% of worker time); scene analysis took
23.9s, final writer cleanup 7.8s, and TTS 0.5s because those assets were
reused. The end-to-end job ran from 15:02:02 to 15:06:38. A 30:17, 1080p
final was then downloaded, mixed with BGM, and uploaded to Drive. The final
Drive file is 344,515,783 bytes.

The successful plan materialized 32 of 45 requested phrase overlays, leaving
13 unfilled. This is a separate output-quality shortfall that the generation
flow should report and handle deliberately.

## Required fixes

1. Validate the complete overlay render plan against the selected GPU backend
   before dispatch. Report the exact unsupported effect and either select a
   supported fallback or fail immediately with a typed actionable error.
2. Make `require_gpu` semantics explicit and propagate the setting to every
   overlay child job. If overlays are always GPU-only, reject an incompatible
   request before starting TTS or media work.
3. Reconcile the `type: none` schema documentation with the validator and add
   a contract check so documented payloads pass preflight.
4. Establish a per-device GPU memory budget across Chronon instances. Prevent
   multiple daemons from independently consuming nearly all VRAM, reclaim
   retained Vulkan surfaces between items, and surface low-memory conditions
   before the renderer returns `VkResult=-2`.
5. Report phase-level progress and timings for scene analysis, overlay queue
   wait, overlay render, final render, final artifact transfer, and Drive
   publication. Do not report 0% while completed stages and overlays are
   visible in durable job state.
6. Enforce or clearly report the requested phrase-overlay count. For this run,
   13 of 45 requested phrases were missing despite a successful final render.

## Acceptance criteria

- The five-scene, 56-overlay Augias payload completes twice consecutively on
  the configured GPU without manual daemon restarts or failed overlay retries.
- Unsupported GPU effects fail during preflight with the field and capability
  named, or render through an explicitly permitted fallback.
- The `require_gpu` behavior and the `type: none` contract agree across the
  payload schema, validator, worker request, and logs.
- Per-stage progress includes queue wait and final artifact delivery; the
  published run reports timings that reconcile with total worker duration.
- A request for 45 short phrases either materializes 45 or returns an explicit
  shortfall before publication.

## Remediation progress (2026-11-24)

The main PipelineGen source now has these protections in place:

- `effects: [{"type":"none"}]` is accepted as the explicit empty image-effect
  stack by `internal/kernel/script/overlay_style_spec.go`; this fixes the
  schema/validator mismatch in the current source tree.
- Narration Markdown emphasis markers are stripped before voiceover work is
  constructed in `internal/capabilities/scripts/runner_phase_voiceover.go`.
- Phrase-budget shortfall is written as a preflight warning before final-job
  handoff in `internal/capabilities/scripts/finaljob/final_job_payload_builder.go`.
- The original-clip `no_voiceover` contract is materialized before voiceover
  fan-out in `runner_scene_materialization.go`.
- The remote final-job artifact download already records duration, byte count,
  and cache hit; Drive upload uses the resumable uploader implementation.

The ticket remains open. This review did not verify deployment parity, and the
major GPU items remain: overlay child jobs do not yet carry the request's
`require_gpu` policy into RenderingGen, the Go preflight does not validate the
complete Chronon effect stack against the selected runtime capabilities, and
VRAM admission is still per-process rather than coordinated across all
Chronon/Ollama users of a device. A real two-run Augias GPU acceptance render
is still required.
