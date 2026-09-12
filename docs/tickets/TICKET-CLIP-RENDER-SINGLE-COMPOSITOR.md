# TICKET — clip.render: one compositor (Chronon single pass) (Wave C)

**Priority:** P1.
**Status:** IMPLEMENTED (single-pass path is the default) — the only remaining
step is the GPU artifact certificate that gates DELETING the legacy compositor.
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
* `Worker` resolves the overlay **before** sealing when single-pass is enabled
  (`WithSinglePassOverlay`), and skips the post-render composite entirely.
* Composition root: `singlePassOverlayEnabled` — **default enabled**; set
  `PIPELINEGEN_CLIP_SINGLE_PASS_OVERLAY=0` to revert to the legacy compositor
  without a code change (logged at wiring time).

### 2.3 Contract bug found by the cross-repo test

The producer originally emitted `"preset_id": ""` / `"motion_id": ""`. The
published `overlay-plan.v1` schema declares both with `minLength: 1`, so those
items are now **omitted** when empty. This was caught by feeding PipelineGen's
real mapper output through RenderingGen's compiler + schema validator
(`pipelinegen_overlay_contract_test.go`), not by inspection.

## 3. Remaining — the deletion gate

### 3.0 CI gate (LANDED)

`scripts/ci/check_clip_render_cutover.sh` now runs
`check_ffmpeg_overlay_compositor_callers`: it fails on any
`NewFFmpegOverlayCompositor` reference outside the three permitted sites (the
composite pass, its constructor wrapper and the composition-root wiring).
Verified in both directions — the gate passes on the current tree and fails on
a simulated new caller. It keeps passing with zero hits after the deletion, so
it permanently forbids the regression.

The composition root also logs a `WARN` when single-pass is disabled, so a
deployment left on the deprecated second-transcode path is visible instead of
being discovered from a benchmark.

### 3.1 Delete only when certified

Do NOT delete the FFmpeg compositor until:

1. A **GPU artifact certificate** compares the single-pass output against the
   legacy composited output on a real host: geometry, fps/timebase, color, GOP,
   audio sync and overlay placement (the audit's golden equivalence step).
2. The **segment-scale caveat** is confirmed: Chronon renders a video layer into
   the canvas box (video layers carry no per-layer `fit`), so an overlay segment
   rendered at the output contract is identity-composited, while a segment with
   a DIFFERENT aspect ratio is scaled rather than letterboxed (the legacy
   `scale=…:force_original_aspect_ratio=decrease,pad=…` behaviour). Confirm the
   segment is always produced at the assembly contract, or add the letterbox.
3. `PIPELINEGEN_CLIP_SINGLE_PASS_OVERLAY=0` is no longer needed in production.

### 3.2 Exact deletion change set (mechanical, once certified)

1. `internal/app/wiring/registry_internal_modules.go`: remove the
   `worker.WithOverlayCompositor(...)` call and the `singlePassOverlayEnabled`
   helper + `PIPELINEGEN_CLIP_SINGLE_PASS_OVERLAY` env var (single-pass becomes
   unconditional); drop the now-unused encoder args.
2. `internal/capabilities/cliprender/worker.go`: drop the
   `singlePassOverlay` condition, make overlay resolution unconditional when an
   overlay is declared, delete the legacy composite block, require only the
   `OverlaySegmentResolver`.
3. `internal/capabilities/cliprender/worker_options.go`: delete
   `WithSinglePassOverlay` and `WithOverlayCompositor`.
4. `ports.go`: delete `OverlayCompositor`, `OverlayCompositeInput`,
   `OverlayCompositeResult`.
5. `adapters/`: delete `FFmpegOverlayCompositor` (composite pass) and its
   `NewFFmpegOverlayCompositor` entry in `constructors.go`; KEEP
   `OverlaySegmentResolver`.
6. `worker_single_pass_overlay_test.go`: delete the legacy-path test; keep the
   single-pass tests (they assert `renderer.plan.Overlay` and must not reference
   the removed option).
7. Drop the `composite` parameter from `renderedResult` and its callers.
8. `scripts/ci/check_clip_render_cutover.sh`: the allowed-file list becomes
   moot (zero hits still passes); remove the entries for tidiness.

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
- [ ] GPU artifact equivalence certificate (legacy vs single-pass) recorded.
- [x] CI gate prevents a new FFmpeg overlay compositor caller.
- [ ] No production caller of `NewFFmpegOverlayCompositor` remains.
- [ ] `FFmpegOverlayCompositor` deleted (change set in §3.2).
