# Modern Documentary pack (2026-11-14)

Five native `chronon.render-plan.v3` looks distilled from the supplied modern
documentary references (Vox / LEMMiNO / MagnatesMedia grammar). Design and
timing live in `include/chronontemplate/web/ModernDocumentaryPack.hpp` and
`src/chronontemplate/web/ModernDocumentaryPack.cpp`; renderer semantics stay in
Chronon3D. There is no browser, DOM, screenshot plate or live server in this
path.

## Recipes

| id | visual contract | primitives exercised |
| --- | --- | --- |
| `web_modern_vox_highlight_v1` | Paper article, cyan multiply highlighter growing from its left edge, cutout subject, rack focus, dust bokeh | `Multiply`, shape `scale_x`/`position_x`, `camera_samples`, thin-lens DoF, particle environment |
| `web_modern_split_glitch_v1` | Split composition, halftone photo, procedural torn energy seam, rounded UI card in Z, RGB/slice glitch | `halftone`, procedural path seam + `glow`, `rounded_rect`, `chromatic_aberration`, `mesh_warp`, `scanlines` |
| `web_modern_macro_ui_rain_v1` | Macro comments, grapheme typewriter, parallax crowd silhouettes, banknote rain, product card, HUD | `lens_distortion`, `scanlines`, text selectors, one-drop sprite emitters + depth/bokeh, `glow` |
| `web_modern_kinetic_type_v1` | Four 3D type beats on yellow bars, stage reveal from the centre, shutter streaks | Transform Z, bar `scale_x` + word fade, `temporal_motion_blur` 180°/8 samples |
| `web_modern_vhs_carousel_v1` | CRT frame, spring-overshoot logo carousel, hot title halo, analog typewriter, scanline/chroma wear | `retro_tv`, `scanlines`, `chromatic_aberration`, `directional_blur`, `glow`/`bloom` |

All plans are 1920×1080, 30 fps, 150 frames by default (5 s). `validate()` is
fail-closed: only 30 fps and 60–300 frames are accepted.

## Build, emit and render

From the workspace root:

```bash
cmake --build ChrononTemplate/build/dev-fast --target \
  chronontemplate_emit_modern_documentary \
  chronontemplate_modern_documentary_pack_test

ChrononTemplate/build/dev-fast/chronontemplate_emit_modern_documentary \
  out/modern_documentary_styles

for plan in out/modern_documentary_styles/*.plan.json; do
  Chronon3d/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli \
    validate --plan "$plan" --assets-root "$PWD"
done
```

Render one preview (MP4 is encoded by Chronon3D; `--motion-blur` is not needed
when the plan itself declares `temporal_motion_blur`):

```bash
Chronon3d/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli \
  render --plan out/modern_documentary_styles/web_modern_kinetic_type_v1.plan.json \
  --assets-root "$PWD" --backend vulkan --profile draft \
  --start-frame 0 --end-frame 149 \
  -o out/modern_documentary_styles/kinetic.mp4
```

Generate the support sprite once:

```bash
python3 ChrononTemplate/tools/web_styles/generate_modern_documentary_assets.py \
  --out ChrononTemplate/assets/modern_documentary
```

## Design decisions

- The highlighter is a real `Multiply` quad placed before the text in layer
  order and animated with a coupled `scale_x` + `position_x` pair, so its left
  edge stays pinned instead of growing from the centre.
- Rack focus uses per-frame `camera_samples` and a thin-lens `dof` block. The
  camera owns `f_stop`; `aperture` is not authored.
- The typewriter uses the existing character selector with an empty reveal
  range that travels forward. It does not need a per-glyph keyframe for every
  character.
- The banknote rain is authored as 96 one-drop particle emitters. The canonical
  CPU particle lane renders one sampled element per layer, so a single
  high-count emitter collapses to one visible note; one-drop emitters keep the
  same sampler, sprite, `environment` fake depth, bokeh and spin while filling
  the frame. The drops are born just below the comment card with a slow fall,
  so they stay in the exposed lower band instead of crossing it in two frames.
  This is a 2.5D environment emitter, not a full three-axis rigid-body solver.
- Screen-space particle layers use the top-left box convention: `position` is
  the emitter box top-left, not its centre, and Y grows downward. The helper
  converts the authored centre and uses positive velocity/gravity for rain.
- No new Chronon3D primitive was required for this pass: every item in the
  analyses has an existing supported implementation, or is intentionally
  approximated (see limits).

## Known limits

- The glitch is a deterministic combination of `mesh_warp`, `chromatic_aberration`,
  offset text flashes and bands. It is not a true datamosh/bitstream corruption.
- Particles rotate in the image plane. Three-axis tumbling and per-particle
  collision are out of scope for the existing emitter.
- The software particle lane draws one sampled element per layer. The rain
  workaround is therefore structural (96 emitters); the strict native GPU lane
  would draw all instances of one emitter, but this build rejects the macro
  look's radial lens-distortion resample under `require_gpu_native`.
- The banknote sprite is 384×384 RGBA with a 2:1 note centred; the renderer
  instantiates square quads, so the surrounding square is transparent.
- The kinetic beat is an in-place stage reveal (bar scale + word fade), not a
  true camera whip-pan. The 180°/8 shutter blur is still declared and applies
  to the bar/word motion.
- The torn edge is a procedural polyline seam. It is not a per-pixel alpha
  erosion shader applied to the photo's own alpha.
- Sub-pixel text stability depends on the renderer's floating-point text path;
  this pack authors continuous camera coordinates but cannot by itself change
  glyph rasterization.
- The macro UI is vector-drawn. It is not a captured screen recording and makes
  no claim to real DOM layout.

## Verification (2026-11-14)

- `chronontemplate_modern_documentary_pack_test`: **123 checks, 0 failures**
  (build dir `ChrononTemplate/build/dev-fast`).
- All five emitted plans pass `validate --plan <plan> --assets-root "$PWD"`
  at 1920×1080 / 30 fps.
- Stills inspected under `out/modern_documentary_styles/` (`small/` and
  `keyframes5/`): `vox112_d`, `split74`, `macro100_drops96`, `kinetic100b`,
  `vhs78_c`.
- The 15-frame clip `out/modern_documentary_styles/clips/macro_drops96_96_110.mp4`
  was decoded to PNG: banknotes move across the exposed lower band and around
  the product card. The mixed software/native lane requires the one-drop
  emitters described in the limits.
- `--gpu-hot-path-mode require_gpu_native` was attempted for the macro plan and
  fails closed on the radial lens-distortion resample; the pack therefore keeps
  the mixed lane and does not claim strict native GPU support for that look.
