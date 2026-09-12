# TICKET — clip.render: one compositor (Chronon single pass) (Wave C)

**Priority:** P1.
**Status:** DONE — single-pass is the ONLY path. The legacy FFmpeg overlay
compositor, its port API, worker options, composition-root wiring and env
switch are DELETED; the CI gate forbids their return. The only open item is the
GPU artifact certificate for the single-pass output itself (see §3.3), which is
evidence, not a code change.
**Owner:** `internal/capabilities/cliprender` (worker + adapters) + `RenderingGen/renderinggen` (overlay compiler).

## 1. Problem (what this replaced)

When a request declared an overlay, the clip was encoded **twice**: Chronon
produced `rendered-clip.mp4`, then an FFmpeg compositor blended the overlay
segment and re-encoded the whole file:

```text
Chronon transcode → FFmpeg overlay compositor → second transcode
```

## 2. Implemented single-pass path

```text
Chronon (single pass):
  base video + subtitles + overlay segment (timed video layer)
        ↓
   single encode
```

### 2.1 RenderingGen — a first-class timed video-overlay kind

* `internal/overlay/registry.go`: `KindVideoOverlay` (`video_overlay`, with the
  `video` / `rendered_overlay` synonyms) plus the `VIDEO_OVERLAY` /
  `RENDERED_OVERLAY` templates and `behaviorVideo`. Deliberately preset-less
  and family-less: the segment carries its own pixels and the producer owns the
  window.
* `internal/overlay/semantic_compile.go`: `compileVideoOverlayLayer` lowers the
  item to a **video layer** whose `Source` is the content-addressed segment and
  whose `[StartFrame, StartFrame+DurationFrames)` is the declared window. It is
  appended after the source layer, so it composites on top. Fail-closed: a
  video overlay without an asset, with an unsupported `fit`, or with an
  out-of-range opacity is a compile error.

Why this is correct without a new renderer feature: Chronon's `VideoNode`
samples the segment at `frame - layer_start` (`video_node.hpp`) and the
compiler gates the layer with `active = frame ∈ [start, end)`
(`typed_program_sampler.cpp`). A timed video layer therefore plays the
segment's first frame at the declared start and is composited only inside the
window — the same semantics the FFmpeg path expressed with
`overlay=...:enable='between(t,start,end)'`.

### 2.2 PipelineGen — the overlay travels in the sealed plan

* `ClipRenderPlanV1.Overlay` (`PlanOverlay`) + `CompileInput.Overlay`
  (`PlanOverlayInput`): the resolved segment (path + sha256 + size) and its
  `[StartMS, EndMS)` window are sealed into the plan, so the overlay is part of
  the plan digest. Validation is fail-closed (missing segment, empty/negative
  window, window past the clip duration).
* `MapClipPlanToOverlayPlan` emits exactly one semantic item
  (`kind: video_overlay`, `template_id: VIDEO_OVERLAY`) with the window and the
  segment's `asset_refs`.
* `overlayPlanAssets` + `prefetchClipAssets` stage the segment from its local
  path, deduplicated by content digest.
* `Worker` resolves the overlay **before** sealing (unconditionally: a declared
  overlay is always a sealed timed layer) and never composites post-render.
* Composition root: the resolver is the only overlay adapter wired; there is no
  compositor, no toggle and no env switch — the single-pass path is the only
  path (see §3.3).

### 2.3 Contract bug found by the cross-repo test

The producer originally emitted `"preset_id": ""` / `"motion_id": ""`. The
published `overlay-plan.v1` schema declares both with `minLength: 1`, so those
items are now **omitted** when empty. This was caught by feeding PipelineGen's
real mapper output through RenderingGen's compiler + schema validator
(`pipelinegen_overlay_contract_test.go`), not by inspection.

## 3. Deletion — DONE

### 3.1 What was deleted (executed change set)

1. `internal/app/wiring/registry_internal_modules.go`: removed the
   `worker.WithOverlayCompositor(...)` call and the `singlePassOverlayEnabled`
   helper + the `PIPELINEGEN_CLIP_SINGLE_PASS_OVERLAY` env var (single-pass is
   now unconditional); dropped the now-unused encoder args and the `strconv` /
   `strings` imports.
2. `internal/capabilities/cliprender/worker.go`: dropped the
   `singlePassOverlay` field and condition, made overlay resolution
   unconditional when an overlay is declared, deleted the legacy composite
   block (composite call + second post-composite probe + `StageClipOverlay`
   recording). The worker now requires only `OverlaySegmentResolver`.
3. `internal/capabilities/cliprender/worker_options.go`: deleted
   `WithSinglePassOverlay` and `WithOverlayCompositor`.
4. `ports.go`: deleted `OverlayCompositor`, `OverlayCompositeInput`,
   `OverlayCompositeResult`, replaced by a note pointing at this ticket.
5. `adapters/`: deleted `FFmpegOverlayCompositor` (composite pass) and its
   `NewFFmpegOverlayCompositor` entry in `constructors.go`;
   `OverlaySegmentResolver` is kept.
6. `worker_single_pass_overlay_test.go`: deleted the legacy-path test; the
   remaining tests assert the SEALED plan (`renderer.plan.Overlay`) and the
   render boundary's certified digest, and never reference the removed option.
   `worker_test.go`: removed `fakeOverlayCompositor`, converted the compositor
   fail-closed cases into plan-validation fail-closed cases, and repointed the
   overlay lineage test at the sealed plan + the single render output.
7. Dropped the `composite` parameter from `renderedResult` and all callers
   (worker + `chronon_timing_result_test.go`).
8. `stage_timing.go`: deleted the dead `StageClipOverlay` (`clip.overlay`)
   stage name — the overlay cost is now part of `StageClipRender`. The
   `metrics_v2.composite_ms` projection stays: that is the renderer's own
   Chronon GPU kernel phase, unrelated to the deleted overlay blend.
9. `worker_result.go`: the overlay result block no longer carries
   `composited` / `composite_ms`; it carries `single_pass: true`,
   `start_ms`, `end_ms` and `segment_sha256` from the sealed plan.

### 3.2 CI gate (zero allowlist)

`scripts/ci/check_clip_render_cutover.sh` runs
`check_ffmpeg_overlay_compositor_callers`, which now fails on **any**
`NewFFmpegOverlayCompositor` / `FFmpegOverlayCompositor` / `OverlayCompositor`
reference in production code — the permitted-site list is gone because the
permitted count is now zero. Verified in both directions: it passes on the
current tree (`CLIP_RENDER_CUTOVER=PASS`) and fails when a simulated caller is
dropped in. Because the deleted identifiers are no longer mentioned anywhere in
production code (not even in comments), the gate needs no allowlist to stay
truthful.

### 3.3 Residual risk (evidence still owed)

The deletion removes the only runtime revert path, so the remaining caveat must
be closed by evidence rather than by a fallback:

1. A **GPU artifact certificate** for the single-pass output: geometry,
   fps/timebase, colour, GOP, audio sync and overlay placement, compared
   against the legacy composited artifact captured before this deletion.
2. The **segment-scale question**: Chronon renders a video layer into the canvas
   box (video layers carry no per-layer `fit`), so an overlay segment rendered at
the output contract is identity-composited, while a segment with a DIFFERENT
   aspect ratio is scaled rather than letterboxed (the legacy
   `scale=…:force_original_aspect_ratio=decrease,pad=…` behaviour). Confirm the
   segment is always produced at the assembly contract, or add the letterbox —
   in RenderingGen/Chronon, since the FFmpeg pass no longer exists.

## 4. Rejected alternative

`-ss/-to` + stream-copy segmentation (prefix copy → encode overlay window →
suffix copy) is technically possible but introduces keyframe-boundary,
timestamp-continuity, codec-parameter, B-frame-ordering, concat-compatibility,
audio-sync and GOP-discontinuity hazards.

## 5. Acceptance criteria

- [x] A clip with an overlay is encoded exactly once on the default path.
- [x] The overlay window lands at the declared `[start_ms, end_ms)` (compiler
      tests on both sides).
- [x] Producer payload validated against the published contract + lowered by
      the real compiler (cross-repo golden fixture).
- [ ] GPU artifact certificate for the single-pass output recorded (legacy vs
      single-pass; the legacy artifact must come from a pre-deletion capture).
- [x] CI gate prevents a new FFmpeg overlay compositor caller (zero allowlist).
- [x] No production caller of `NewFFmpegOverlayCompositor` remains (gate-verified).
- [x] `FFmpegOverlayCompositor`, its port API, options, wiring and
      `PIPELINEGEN_CLIP_SINGLE_PASS_OVERLAY` env switch deleted (§3.1).
- [x] No second-transcode path remains; the single-pass plan is the only path.
