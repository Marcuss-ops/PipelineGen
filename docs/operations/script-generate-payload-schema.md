# `POST /api/script/generate` — job payload schema

Operator reference for the request body of the async script-generation
endpoint. The **Go structs are the single source of truth**; this page
documents the JSON nesting that callers get wrong, so a rejected attempt
is fixed on the first try instead of costing a 400 round-trip.

## Source of truth (godlike/06 SSOT)

| Contract | Owner |
|---|---|
| Top-level envelope + item | `internal/kernel/script/generation_envelope.go` (`GenerationEnvelopeV2`, `GenerationItemV2`) |
| `source` | `internal/kernel/script/source_spec.go` (`SourceSpec`) |
| `script_params` | `internal/kernel/script/script_spec.go` (`ScriptSpec`) |
| `output` | `internal/kernel/script/output_spec.go` (`OutputSpec`) |
| Result envelope (`items`) | `internal/kernel/script/generation_result.go` (`GenerationEnvelopeResult`, `GenerationEnvelopeItem`) |
| Request binding + validation | `internal/capabilities/script/handler_generate_request.go` (`bindGenerateEnvelope`), `internal/kernel/script/generation_validation.go` (`GenerationEnvelopeV2.Validate`), `internal/capabilities/scripts/usecase/gencore/payload_validator.go` (`PayloadValidator`) |

Any field rename or move MUST update this page in lockstep.

## Contract versions

`version` selects the wire contract and is no longer decorative:

| version | meaning |
|---|---|
| `2` (legacy) | the historical wire, still accepted in full. The duplicate spellings listed below are reconciled with `payloadlint` warnings, so every recorded job file and replay keeps working. |
| `3` (canonical) | the same wire shape with **one owner per fact**: those duplicate keys are rejected with a `400` naming the JSON path and the surviving owner. |

Any other value is rejected (`version must be 2 (legacy) or 3 (canonical)`).
New payloads should declare `3`; the drift-free template is
`ops/jobs/canonical/script-generate-reference-v3-20261126.json`.

Version 3 retires three V2 spellings. Each one carries a fact that another
block already owns authoritatively:

| V2 key | version 3 owner | why |
|---|---|---|
| `items[].script_params.force_refresh` | envelope `force_refresh` | the item-level flag never bypassed the idempotency store; it was silently ignored. |
| `items[].script_params.voiceover_group` | `items[].output.voiceover_group` | the voiceover router resolves only the output value. |
| `items[].output.audio` | `items[].audio` | the same `AudioOutputConfig` shape in two places; the runner reads the item-level block. |
| `items[].map_type` | `items[].map_presentation.style` | one basemap fact; the legacy scalar also accepted a narrower alias vocabulary than the registry. |
| `items[].map_camera_animation` | `items[].map_presentation.camera` | the same camera move, resolved by the presentation contract. |
| `items[].map_label_animation` | — (omit the key) | retained compositions draw their own text; the legacy field was fixed to `none` and only existed to say nothing. |

Everything else is unchanged in version 3: same nesting, same fields, same
validators. `script_params.images_per_scene` and
`media_plan.planner.max_images_per_scene` both stay because they are not the
same fact (a cadence and a ceiling); `payloadlint` still reports them when they
disagree. `map_animation` also stays accepted in both versions: it is the
legacy composition vocabulary, not a duplicate of a single `map_presentation`
field.

## Nesting

The body is an **envelope** with an `items` array. Every generation option
lives **inside an item**; there are no top-level shortcut keys.

`preset` is the endpoint variant: `custom`, `with_images`, `full_media`,
`catalog`, `search`, `batch`, or omitted (pass-through). Any other value is
rejected at ingress instead of silently doing nothing.

```json
{
  "version": 2,
  "preset": "custom",
  "correlation_id": "optional-trace-id",
  "force_refresh": false,
  "items": [
    {
      "id": "optional-item-id",
      "title": "My Title",
      "language": "en",
      "source":       { "type": "text", "source_text": "..." },
      "script_params": { "target_words": 1200 },
      "overlay_style": {
        "font_family": "inter",
        "size": { "font_size": 64 },
        "glow_size": 18,
        "glow_color": "#000000",
        "stroke_size": 3
      },
      "map_type": "chronon_editorial",
      "map_camera_animation": "slow_approach",
      "map_label_animation": "none",
      "map_animation": "random",
      "animation_contract_version": 2,
      "animation_directives": [
        {"scope":"element","scene_ref":"scene-1","target":"important_phrase","mode":"exact","motion_id":"phrase_apple_clean_01_blur_soft_reveal"},
        {"scope":"element","scene_ref":"scene-1","target":"image","mode":"prefer","motion_id":"image_focus_reveal"}
      ],
      "random_light_leak_on_clip_start": true,
      "image_motions": ["image_fade_reveal", "image_focus_reveal"],
      "entity_image_motions": ["image_fade_reveal", "image_scale_reveal"],
      "output":       { "...": "opt-in post-generation artifacts" },
      "audio":        { "...": "audio.mode + editorial audio intent" },
      "media_plan":   { "...": "visual media selection" }
    }
  ]
}
```

`overlay_style` is the run-level visual authority for generated **text**.
`font_family` accepts `poppins`, `inter`, `inter_bold`, `dejavu_sans`,
`playfair_display_italic`, `bricolage_grotesque`, `oswald`, or `abril_fatface`; the queue stages the
selected content-addressed font once per family and RenderingGen rejects any
unbundled selection. `size.font_size` sets text size in pixels; `glow_size`
accepts `0..256`; `glow_color` accepts `#RRGGBB`; `stroke_size` accepts `0..64`. Omit a field to retain the
preset/role default; explicit zero disables the relevant effect. Run typography
applies to phrase, word, number, quote, entity captions, and captioned image
layers, without changing image geometry or image styles. Precedence for a
caption is run-level `overlay_style`, then explicit item/child runtime style,
then legacy `caption_font_family`, then the language-aware default; entity
composition styles only supply layout, motion, and fallback color.

`overlay_style.bullet` is the optional **second font** for bullet-point copy
only. It accepts `font_family` from the same closed family list, `font_size_px`
(`24..240`), `glow_size` (`0..256`), `glow_color` (`#RRGGBB`) and `stroke_size`
(`0..64`). Bullet-point items (`scene_bullet` and `chapter_bullet`, including a
bullet fused into an animation chain) use this block; every other text lane
keeps the run-level typography. An omitted field falls back to the run-level
value, and an omitted block — or an omitted run-level value — keeps the planner
default, so a payload that does not ask for two fonts renders exactly as before.
A per-scene `editorial_bullet_styles` entry stays the most specific choice and
wins on the bullet it names.

```json
"overlay_style": {
  "font_family": "inter",
  "size": {"font_size": 72},
  "bullet": {
    "font_family": "oswald", "font_size_px": 96,
    "glow_size": 8, "glow_color": "#FFD400", "stroke_size": 2
  }
}
```

`overlay_style.card_background` puts an optional coloured rectangle behind any
run-level text, including the context label under an entity name. `color` is
required and accepts `#RRGGBB`; `opacity` is `0..1`, `radius` is `0..512` px,
and `padding` accepts at most `[x,y]` with values `0..512` px. The plate is
opt-in: omitting the block leaves every text lane plate-free, exactly as
before. The plate uses the text's `font_family`, so the label and the rectangle
behind it are selected together. The colour reaches entity captions and
captioned image layers as well as phrases, words, dates and metrics.

`stroke` adds a border (`color` `#RRGGBB`, `width` `0..64` px). `shadow` adds a
native drop shadow (`color` `#RRGGBB`, `opacity` `0..1`, `blur` `0..64` px, and
an `offset` of exactly two values in `-128..128`). `motions` is an optional
closed pool of plate entrances — `fade`, `slide_left`, `slide_right`,
`slide_up`, `scale_in`, `wipe_right` — rotated in plan order so one job with
several captions shows different rectangle animations.The plate entrance starts on
the caption's own start frame. The label starts when the plate settles and
fades in over four frames, so the word (and its shadow/glow) never hangs outside
a half-drawn, half-slid or half-scaled rectangle. A selected motion owns the
card entrance: it replaces the caption's layer-level
opacity/position/scale tracks, while per-glyph text animators keep playing. Omitting `motions` keeps the historical
behaviour: the plate copies the caption entrance. A pool may carry `1..8` ids;
an unknown id fails validation.

```json
"overlay_style": {
  "font_family": "oswald",
  "card_background": {
    "color": "#0B1F33", "opacity": 0.88, "radius": 14,
    "padding": [24, 12],
    "stroke": {"color": "#57D9FF", "width": 2.5},
    "shadow": {"color": "#000000", "opacity": 0.55,
      "blur": 18, "offset": [0, 8]},
    "motions": ["fade", "slide_left", "wipe_right"]
  }
}
```

`overlay_style.image` tunes generated image cards: `width`, `height`,
`position_x`, `position_y`, `radius`, `fit` (`cover`/`contain`/`fill`),
`bounded`, `glow` (`radius` 0..256, `intensity` 0..4, `color` #RRGGBB;
radius 0 disables it), plus `border`, `stroke` and `shadow` (color, opacity,
blur, offset). `shadow.blur` is `0..64` px — the ceiling Chronon's native
drop shadow certifies (`radius > 64` is rejected there); anything above it
fails validation instead of being silently clamped. The block is projected to
every standalone image item at plan seal, even when `overlay_images` declares
no pins; a pin `style` overrides it field by field. The `glow` block is
image-only: it lowers to Chronon's native media glow effect and never changes
caption text. `effects` attaches the same
bounded native effect stack `output.render.effects` accepts (same type
vocabulary, same parameter bounds, max 12 entries; an empty array clears it).
Each entry must supply the parameter that makes it visible —
`amount` for `glitch`, `chromatic_aberration`, `vignette`, `sharpen`;
both `amount` and `wavelength` for `scanlines`; both `amount` and `size` for
`noise`; `radius` for `gaussian_blur`; `block_size` for `mosaic`; both `size`
and `amount` for `halftone`; `intensity` for `banded_grain`; one of
`radius`/`intensity` for `glow` and `bloom`; one of `curvature`/`scale` for
`lens_distortion`. A type without it would render nothing, so it is rejected
rather than shipped as decoration. Effects decorate the photographic surface
only — never captions or plates — and are honored by standalone images,
composites and entity cards.
`grid` is the image counterpart of `output.render.grid` and needs **no**
backdrop: it draws for the image's lifetime and accepts `scope`
(`full_canvas`/`foreground`). It is one layer per item: on a composite the
grid is emitted once for the whole card, never once per member. The backdrop
grid below accepts the same `scope` (default `full_canvas`, `foreground`
confines it to the card). For a **standalone** image only, `backdrop`
reuses the same asset as a softly blurred full-canvas background and may add a
light canvas grid:

```json
"overlay_style": {"image": {
  "bounded": true, "fit": "contain",
  "effects": [
    {"type": "vignette", "amount": 0.22, "radius": 0.8, "softness": 0.55},
    {"type": "noise", "amount": 0.05, "size": 1.2, "seed": 11, "animated": true}
  ],
  "grid": {"scope": "foreground", "style": "lines", "color": "#7FA8C8",
    "spacing_px": 96, "line_width_px": 1, "opacity": 0.12},
  "backdrop": {"zoom": 1.15, "blur_px": 18,
    "grid": {"style": "lines", "color": "#7FA8C8", "spacing_px": 120,
      "line_width_px": 1, "opacity": 0.1}},
  "glow": {"radius": 24, "intensity": 0.25, "color": "#FFFFFF"},
  "shadow": {"enabled": true, "color": "#000000", "opacity": 0.6,
    "blur": 10, "offset": [0, 6]}
}}
```

Grid geometry is closed: `style` is `lines` or `dots`; use `line_width_px`
(`0.25..32`) for lines or `dot_radius_px` (`0.5..64`) for dots. `spacing_px`
is `4..512`, `opacity` is `0..1`, and `enabled:false` disables the grid while
keeping the blurred backdrop. `backdrop` is ignored for composites and groups,
which keep independent per-layer frames.

The standalone `overlay_style.image.grid` block uses the same closed geometry
but its `enabled:false` is an explicit off switch, and omitted keys keep the
renderer defaults (lines, `#7FA8C8`, 96 px spacing). Use it when you want the
grid without the blurred backdrop treatment.

`overlay_images` pins explicit stills to a scene. Each pin may carry one
chosen cue; it replaces the deterministic random cue for that image and is
resolved through the same audio asset registry as `audio.sound_effects`:

```json
"overlay_images": [{
  "scene_id": "scene-1", "asset_id": "context-portrait-1",
  "local_path": "/cache/context-portrait-1.png", "sha256": "<64 hex>",
  "mime_type": "image/png",
  "sound_effect": {"asset_id": "whoosh", "gain_db": -18, "duration_ms": 250}
}]
```

`asset_id` accepts any effect id from the indexed library (see
`ops/jobs/sfx_index.json`; refresh it with `bin/admin index-sound-effects-drive
-apply`). `gain_db` is `-96..24`; `duration_ms` is optional, capped at 60 s and
clamped to the image's visible window. The cue fires **once at the start of the
clip**: an image chain is one clip, so its internal segment boundaries never
repeat the effect. Within a scene, cues attach to that scene's image items in
declaration order and start-time order; a scene that declares more cues than
image items fails the run rather than dropping a cue.

A pin may also carry `style`, the same image style block as
`overlay_style.image` (border, stroke, shadow, glow, radius, fit, `effects`,
`grid`, `backdrop`). It overrides the run-level style for the image that pin
produces only — every field the pin leaves unset keeps the run-level value,
and a declared `effects` stack replaces the run-level one instead of adding to
it:

```json
"overlay_images": [{
  "scene_id": "scene-1", "asset_id": "context-portrait-1",
  "local_path": "/cache/context-portrait-1.png", "sha256": "<64 hex>",
  "sound_effect": {"asset_id": "whoosh", "gain_db": -18},
  "style": {
    "border": {"width": 4, "color": "#22D3EE", "radius": 18},
    "grid": {"enabled": false},
    "effects": [{"type": "vignette", "amount": 0.3}]
  }
}]
```

The pin style follows the same positional rule as the cue: within a scene the
pins attach to that scene's image items in declaration order and start-time
order, and a scene that declares more styled pins than image items fails the
run. `style` requires `scene_id` (the style is bound to the image that scene
produces).

For source video clips rendered by Chronon, `output.render` accepts the clip
card controls plus native effects and a procedural grid:

```json
"output": {"render": {
  "enabled": true,
  "background": {"mode": "blur_source", "blur_px": 28, "zoom": 1.1},
  "foreground_scale_percent": 75,
  "watermark": {"enabled": true, "text": "VeloxEditing",
    "position": "top_right", "opacity": 0.72, "margin_px": 36,
    "style": {"font": "inter_bold", "font_size_px": 36,
      "color": "#FFFFFF", "stroke": {"color": "#101820", "width": 2},
      "shadow": {"color": "#000000", "opacity": 0.65, "blur_px": 8, "offset_y": 3}}},
  "source_frame": {
    "border": {"width_px": 8, "color": "#EDEDED", "radius_px": 28},
    "shadow": {"color": "#101820", "opacity": 0.8, "blur_px": 24, "offset_y_px": 10},
    "stroke": {"width_px": 2, "color": "#FFFFFF"},
    "clip_radius_px": 20
  },
  "effects": [{"type": "glow", "radius": 18, "intensity": 0.2}],
  "grid": {"scope": "foreground", "style": "lines", "spacing_px": 96, "line_width_px": 1,
    "color": "#7FA8C8", "opacity": 0.08},
  "subtitles": {"enabled": true, "mode": "burn", "style": {
    "font": "inter", "font_size_px": 56, "color": "#FFD400",
    "stroke": {"color": "#000000", "width": 4},
    "shadow": {"color": "#000000", "opacity": 0.8, "blur_px": 10, "offset_y": 4}
  }}
}}
```

`foreground_scale_percent` bounds the foreground clip inside the canvas;
`source_frame` controls its border, shadow, stroke and rounded corners.
`watermark` can be text, an image `asset_id`, or both. `position` accepts
`top_left`, `top_right`, `center`, `bottom_left`, or `bottom_right`; `margin_px`
sets its inset, `opacity` accepts 0..1, and `style` sets font, size, color,
stroke, shadow and transition. Style width/height/scale can set a precise
watermark box. The watermark travels in the same sealed Chronon render plan.
`background.mode` is `none`, `blur_source`, or `asset` (with `asset_id` for an
asset). For `blur_source`, `background.blur_px` sets the gaussian radius of the
derived plate (6–48 px, default 24) and `background.zoom` scales it (1–10×,
default 1 = cover). An omitted (zero) value keeps the renderer default. Both are
rejected on `none`/`asset`, which carry no derived plate, and both participate
in the render fingerprint. They travel all the way into the sealed clip.render
plan (`CompileInput.BackgroundBlurPX/BackgroundZoom`); the mode gate and the
numeric bounds are enforced when the localized render is planned, by
`cliprender.ValidateBackgroundTreatment` — one validator for script.generate and
clip.render alike. `effects` uses the
closed Chronon effect allowlist from
`POST /api/clips/render`; `grid.scope` may be `full_canvas` (the default) or
`foreground`, which confines it to the scaled source clip. Subtitle `font`, `color`, `stroke` and `shadow` use
the `VideoVisualStyleSpec` contract. Invalid effects or grid geometry fail
closed before rendering. These values participate in the render fingerprint,
so changed styling cannot reuse stale clip bytes.
Clip-only card, background, effect, grid and watermark settings are omitted
from stock-backed narration renders; stock assets are never updated or
replaced by these clip treatments.

For `script_params.segments[]` entries with `no_voiceover:true`,
`duration_ms` is optional. When omitted, the runner uses the selected clip's
canonical media duration; provide `duration_ms` only when an editorial trim is
intended. The fixed-media intro/outro playback policy still uses an explicit
source window.

- `version` is `2` (legacy) or `3` (canonical) — see
  [Contract versions](#contract-versions). Any other value is rejected.
  `items` must contain **at least one** entry.
- `preset` is one of `custom`, `with_images`, `full_media`, `catalog`,
  `search`, `batch`. An omitted/empty preset behaves like `custom`. Anything
  else is rejected at ingress: the earlier `"news"` value that 61 recorded job
  files carried never existed in the runtime and silently applied nothing.
- `docs` is **retired**: `docs.enabled=true` fails closed, and the block is
  omitted from every example. Document publication no longer exists.
- A single-item envelope maps to the unified `/generate` flow; multiple
  items map to batch generation.
- Every per-item block (`source`, `script_params`, `output`,
  `audio`, `media_plan`, `overlay_background`, `overlay_style`,
  `map_type`, `map_camera_animation`, `map_label_animation`,
  `map_animation`,
  `short_phrase_motion`,
  `image_motions`, `image_with_text_motions`, `entity_image_motions`,
  `phrase_motion_family`, `date_motion_family`, `animation_contract_version`,
  `animation_directives`, `animation_catalog_digest`,
  `random_light_leak_on_clip_start`, `max_phrase_overlays`,
  `video_metadata`, `intro`, `outro`) is optional unless its own
  validator says otherwise (`source` is required).

For the full current phrase palette, set `phrase_motion_family` to
`modern_product_v1`. It includes the callable Apple, spatial 3D, and
documentary phrase catalogs. The 15 `doc_01`…`doc_15` IDs each keep their
own runtime treatment, including selective marker sweeps, red keyword impact,
mixed-font emphasis, underline, contrast focus, line reveal, scanner and dual
highlight. `documentary_highlighting_v1` selects only that documentary family.
Retired typewriter motions and the whole Brush line-treatment family are not
callable.
Long phrases (six words or more) rotate across Apple, documentary highlights,
and spatial 3D motions. Their family is filtered before the certified phrase
pool is walked, so the rotation does not collapse to the most numerous family.
Phrases shorter than six words always use the Short Phrases lane, including
phrases marked high priority. `animation_counts.short_important_phrase` and
`animation_counts.important_phrase` are UI preview hints, not runtime caps:
the runtime always walks the full compatible phrase pool. Dates, images, maps,
entities and metrics still honor their counts as window sizes. Retries with
the same job identity keep the same selection. Image motions are sampled
independently per image item.

`short_phrase_motion` pins one catalog animation for every 1-5-word phrase in
the run instead of rotating:

```json
"short_phrase_motion": "phrase_apple_clean_01_blur_soft_reveal"
```

A pin bypasses the registry's per-word buckets, `phrase_motion_family`
compatibility is still enforced, and neither the seeded rotation nor the
final duplicate-rotation pass may replace the pinned id. An unknown id, a
long-only id used as a short pin, or a word-count-specific short recipe is
rejected for a lane-wide pin unless every short phrase in the run has that
recipe's exact word count. Otherwise use a general short-phrase entrance.
Leave the field empty to keep the per-word rotation.
`modern_apple` remains available when a payload explicitly wants only that
style family.

### Choosing the animation per category

Every animated lane accepts an optional caller choice. **Omitting the field is
the default**: the runtime walks that lane's certified pool with the seeded
rotation (`motion_seed`, or the job identity when it is absent), so retries of
the same job stay byte-identical. An explicit choice replaces the rotation for
that lane; an unknown or lane-incompatible id fails closed at plan compilation.

| Lane | Payload field | Shape | Omitted means |
|---|---|---|---|
| Short phrases (1–5 words) | `short_phrase_motion` | one id | seeded per-word rotation; `phrase_motion_family` still applies |
| Dates (timeline date cards) | `date_motion_family` | `auto`, `date_v1`, `didone`, `typewriter` | full certified date pool (39 presets), seeded per card |
| Single images without caption | `image_motions` | array of ids | certified automatic image pool, seeded per image |
| Images with caption | `image_with_text_motions` | array of ids | certified image-with-text pool, seeded per image |
| Entity images | `entity_image_motions` | array of ids | full certified entity-image catalog, seeded per card |
| Entity captions | `entity_caption_motions` | array of ids | certified automatic caption pool, seeded per card |
| Entity card composition | `entity_style_id` | `random`, a tag selector, or `01`..`25` | `random` (full 25-card registry) |
| Maps | `map_presentation.preset` / `.animation` / `.animation_pool` / `.style` / `.style_pool` | approved preset id, or a registered composition id or `random`; optional arrays restrict the draw; style is `chronon_editorial`, `esri_topo`, `esri_sat`, or `random` | seeded draw from the renderer-certified pools |

A one-element `image_motions`, `entity_image_motions`, or
`entity_caption_motions` array pins that lane to the single id; a longer array
rotates through it. `short_phrase_motion` is a scalar pin and takes precedence
over the seeded short-phrase rotation.
`map_presentation` cannot be mixed with the legacy `map_type`,
`map_camera_animation`, `map_label_animation` or `map_animation` fields: the
mixture fails closed instead of guessing a winner. Version 3 goes further and
rejects `map_type`, `map_camera_animation` and `map_label_animation` outright
(see [Contract versions](#contract-versions)); `map_animation` stays accepted
as the legacy composition lane.

### Map presets

`map_presentation.preset` selects one of the twelve approved Italy map
presets by its stable id. A preset is an alias for a composition + basemap
pair, not a second vocabulary: it is expanded before normalization, so the
resolved plan carries the composition in `animation` and the basemap in
`style`, and the render item carries the same id as `map_preset`.

| preset id | composition | basemap |
|---|---|---|
| `italia_atlante_continentale_turchia` | `continental_atlas` | `esri_sat` |
| `italia_mappa_storica_duisburg` | `historic_location_timeline` | `esri_sat` |
| `italia_scansione_satellitare_pavia` | `satellite_scan_reveal` | `esri_sat` |
| `italia_riempimento_tattico_gerusalemme` | `territory_tactical_fill` | `chronon_editorial` |
| `italia_editoriale_voxel` | `editorial_vox` | `chronon_editorial` |
| `italia_editoriale_paper_cutout` | `editorial_paper_cutout` | `chronon_editorial` |
| `italia_editoriale_radar_scuro` | `editorial_dark_radar` | `chronon_editorial` |
| `italia_editoriale_connessioni_ad_arco` | `editorial_connection_arc` | `chronon_editorial` |
| `italia_editoriale_isola_fluttuante` | `editorial_floating_island` | `chronon_editorial` |
| `italia_basemap_vettoriale_regionale` | `territory_flood_fill` | `chronon_editorial` |
| `italia_basemap_chiara` | `territory_glow_reveal` | `esri_topo` |
| `italia_basemap_regionale_toscana` | `territory_extrusion` | `chronon_editorial` |

```json
{
  "map_presentation": {
    "preset": "italia_basemap_vettoriale_regionale",
    "level": "municipality",
    "camera": "slow_approach"
  }
}
```

A preset composes with `camera`, `level`, `selection_mode`, pin grouping and
the other map knobs. It fails closed when the id is unknown, when `animation`
or `style` name a different treatment, and when an `animation_pool` or
`style_pool` excludes half of the preset pair. Preset ids in `animation`,
`selection_mode: "exact"` and `mode: "manual"` behave like any explicit
composition id. The five editorial presets keep their authored US bounds and
reject Italian pins; the remaining seven cover Italy.

`date_motion_family: "typewriter"` restricts date cards to the ten certified
`date_typewriter_*` presets. With `animation_counts.dates` omitted or set to
`10`, a run with at least ten date cards rotates all ten; a lower count keeps
the lane window that size. `didone` and `date_v1` select the other two date
families. Unknown families fail closed.

Entity cards take `entity_image_motions` and `entity_caption_motions` through
the compile-path assignment.

```json
{
  "short_phrase_motion": "phrase_apple_clean_01_blur_soft_reveal",
  "date_motion_family": "typewriter",
  "image_motions": ["image_fade_reveal"],
  "image_with_text_motions": ["image_fade_reveal"],
  "entity_image_motions": ["image_scale_reveal"],
  "entity_style_id": "camera",
  "map_presentation": {
    "animation": "random",
    "animation_pool": ["continental_atlas", "territory_tactical_fill", "radar_location_reveal"],
    "style": "random",
    "style_pool": ["chronon_editorial", "esri_topo", "esri_sat"],
    "camera": "slow_approach",
    "country_feature": "Germany",
    "timeline_label": "AUGUST 2007"
  }
}
```

`animation` chooses the map composition and `style` chooses the underlying
basemap independently. Both pools accept only registered choices certified by
the active native renderer; unknown ids fail before rendering. The current
registry contains the 22 servable ChrononPlugin composition ids. Sixteen can
be drawn automatically. `historic_location_timeline` is exact-only because it
needs extra data. The five authored editorial scenes are also exact-only and
enforce their Indiana/US geography. There are three renderer-backed basemaps.
`country_feature` and `timeline_label` let an exact historical timeline carry
its required data. `continental_atlas` can infer Italy from a grounded Italian
pin; other countries must be named explicitly. Editorial scenes require the
`chronon_editorial` basemap and reject pins outside authored bounds. The
connection-arc scene depicts a fixed Indianapolis–Washington route, so it
cannot be used for unrelated routes.

The `random` literal is accepted only where a field documents it
(`map_presentation`, `entity_style_id`); for the phrase and image fields leave
the field out instead of sending `random`.

Ops payloads can be filled from a uniform CLI:

```bash
python3 ops/tools/apply_animation_choices.py payload.json \
  --short-phrase-motion phrase_apple_clean_01_blur_soft_reveal \
  --long-phrase-motion phrase_apple_clean_26_opacity_depth_push \
  --date-motion-family typewriter \
  --image-motions image_fade_reveal \
  --image-with-text-motions image_fade_reveal \
  --entity-style-id camera \
  --out payload-animations.json
```

Only the flags passed are written; unset categories keep the seeded random
pool.

`script_params.segments[].extract_entities` optionally overrides the
item-wide `output.extract_entities` tri-state for that scene. Omitted or
`"default"` inherits the run value; `"enabled"` explicitly extracts entities
for that segment (including when the run value is disabled), while
`"disabled"` skips its source/translated NLP, provider search, materialization,
and entity overlays. Boolean inputs are accepted for compatibility and are
normalized to the canonical `"enabled"` / `"disabled"` values. Unknown strings
fail validation.

```json
{
  "version": 2,
  "items": [{
    "source": { "type": "text", "topic": "..." },
    "script_params": { "segments": [
      {"id":"scene-1", "topic":"person scene", "extract_entities":"enabled"},
      {"id":"scene-2", "topic":"non-person scene", "extract_entities":"disabled"}
    ]},
    "output": {"extract_entities":"disabled"}
  }]
}
```

The segment toggle is an override rather than a second NLP category selector;
all extractions that run still obey `media_plan.extraction.include` and its
limits. Segment order/IDs remain part of the content fingerprint, including
the override values.

Visual overlay categories can also be selected with the item-level
`overlay_categories` map and overridden per script segment. Supported keys are
`phrases`, `images`, `numbers`, `quotes`, `brand_text`, `maps`, `charts`,
`recaps`, `titles`, and `bullets`; values are `default`, `enabled`, or
`disabled`. Per-scene values inherit the item setting when omitted or
`default`; with no item setting the existing runtime defaults apply. Segment
matching uses an explicit scene ID first, then its position when the segment
has no ID (or the same ID as the generated scene).

### Source and intro compatibility

These intro fields have different jobs and are not interchangeable:

| Payload field | Purpose | Compatible source shape |
|---|---|---|
| `items[].intro` | Fixed, non-narrated clip section with `clip_ids` and original-clip or silence playback. | Clip-bearing source (`clips`, `catalog`, `search`, `curate`) or `stock_only`. |
| `items[].media_plan.intro` | Visual timeline slot (`mode`, optional ordered `clips`, duration/candidate limits). Manual clips can precede generated narrative scenes. | Also works with `source.type=text`; it does not turn the source into a clip-only script. |
| `items[].overlay_categories` | Defaults for scene overlays such as images, maps and phrases; segments may override them. | Any source, subject to the scene and final-job constraints below. |

For `source.type=clips`, bind every `kind=narration` segment to its own
`clip_ids`; root `source.clip_ids` does not give a generated narration segment
scene ownership. A clip-only `final_job` cannot fulfill
`overlay_categories.images=enabled`, because final-job image overlays are
compiled only for narrative scenes. Use `source.type=text` with narrative
segments and `media_plan.intro` when the final job needs both intro clips and
image/map overlays.

`map_type` selects one basemap for the run. It is a version-2 spelling:
version 3 rejects it in favour of `map_presentation.style`. The single map
renderer is the
native renderer of ChrononPlugin (the Python renderer is retired), which
draws three renderer-backed basemaps:

- `chronon_editorial` (aliases `chronon`, `editorial`, `classic`) — the
  certified editorial look; `applyMapPresentationToItems` writes it as the
  concrete default when the field is omitted.
- `esri_topo` (aliases `modern`, `modern_map_1`, `modernmap1`, `topo`,
  `terrain`) — the Esri World Topo tile pyramid.
- `esri_sat` (alias `satellite`) — the Esri World Imagery tile pyramid.
- `random` — a seeded draw over the three servable styles; never a decorative
  canvas.

`roadmap` still maps to the retired `esri_light` style and is rejected by name;
unknown styles fail closed.

`map_camera_animation` currently allows only `slow_approach`; omission and
`random` resolve to that deterministic camera move. `map_label_animation` is
fixed to `none` because retained maps draw their own text. The legacy
`map_animation` compatibility vocabulary has ten ids: six Saudi/Middle East
scenes (`ed_saudi_base`, `ed_iran_glow`, `ed_yemen_fill`, `ed_yemen_markers`,
`ed_hormuz_routes`, `ed_mocha_tactical`), three ordinary glow maps
(`region_zoom`, `pulse_marker`, `spotlight`), and `slow_approach`.
`map_animation` accepts those nine compositions as its vocabulary and fails
closed on other IDs — but the native renderer serves the certified **base
flyover only** for that legacy path until each composed family lands its parity
gate. New payloads should use `map_presentation.animation`, whose registry
contains the 22 servable composition ids above. An explicitly
requested composition therefore fails closed with a typed error naming the
unported family (never approximated); omission and `random` resolve to the
base flyover: one anchor, the `slow_approach` dive, every grounded place
presented.

`image_motions` and `entity_image_motions` accept arrays of certified animation
IDs and rotate through the requested pool. Phrase motion is selected by the
planner from the registry's per-word buckets (or a `short_phrase_motion` pin, or
an explicit `motion_id`); the removed `phrase_motions` array is no longer part
of the payload contract. Omit an array to use the existing seeded per-overlay
rotation.

Nearby overlay items in the same scene are fused into one chain overlay when
they overlap or have no more than five seconds of empty timeline between them.
Chains retain each segment's own text/image/map content and selected motion;
`animation_counts.chain_gap_ms` can lower the maximum gap and
`animation_counts.chain_disabled: 1` opts out. The handoff between fused
segments is `animation_counts.chain_handoff_ms` (default `400`, clamped to
`0..2000`); `0` makes the segments abut. An automatic chain carries at
most **ten segments**: `animation_counts.chain_max_segments` lowers that bound
(values below two are floored to two) and can never raise it. RenderingGen
lowers the chain as one overlay render instead of one MP4 per member. A run
that would spill past the scene end is trimmed to the segments that start
inside the scene instead of being dropped whole.

`animation_counts.chain_camera` selects the run-level choreography for
**image** chains: `1` right, `2` left, `3` down, `4` up. It writes the
certified `image_group_camera_pan_*` motion onto the chain's `chain_camera`
parameter, where the images sit on one strip and the camera travels so each
image reaches the centre at its segment start; the per-segment entrance and
the `fly_under_fade` handoff are not applied. `0` or omission keeps the legacy
per-segment `fly_under_fade` handoff. Out-of-range values fall back to the
legacy handoff; the knob never affects text or mixed chains.

Use `animation_directives` with `animation_contract_version: 2` for the
canonical typed selection contract. Each entry requires `scope`, `scene_ref`
and `target` (`important_phrase`, `entity_image`, `caption`, `image`,
`map_camera`, `map_composition`, `map_label`, `date`, or `metric`); `mode` is
`auto`, `prefer`, or `exact`, and `exact` requires a non-empty `motion_id`.
`family` and `params` are optional. The compile boundary validates the
directives fail-closed and stamps the resolution provenance; an exact choice is
never silently replaced. The legacy `animation_policies` field is retired: a
non-empty value is rejected with an explicit error, so callers must migrate.
Historical plans that already carry the legacy wire remain readable by
RenderingGen.

`random_light_leak_on_clip_start: true` adds a short certified light-leak
overlay at the beginning of each real video clip (synthetic freeze tails are
skipped). Light leaks on image overlays can be selected with an `image` motion
directive and a `lightleak_*` motion ID. Pair clip-start accents with
`audio.random_sfx_on_clip_start` for a deterministic random cue at every real
clip start, or pin a chosen effect per clip with
`audio.clip_start_sound_effects`. The cue is keyed by `clip_id`, fires once at
that clip's start, and may target body, intro, or outro clips; explicit cues
win over the random lane, which then covers the remaining clips. Omitted
`gain_db` and `duration_ms` use the editorial `-6 dB` and `250 ms` defaults.
A `clip_id` not declared in source, segments, intro, or outro fails at ingress.

```json
"audio": {
  "clip_start_sound_effects": [
    {"clip_id": "clip-1", "asset_id": "whoosh1", "gain_db": -6, "duration_ms": 250}
  ]
}
```

Image overlay SFX are chosen per image with `overlay_images[].sound_effect`,
while `audio.random_sfx_on_image_overlay` remains the deterministic random
fallback for image and entity-image cards the payload leaves unclaimed. The
image lane does not apply to phrase, map, chart, number, or other overlay
kinds. Random lanes select from the
indexed effect families (`whoosh`, `transition`, `impact`, `riser`, `foley`,
`photo`, `ui`) in `ops/jobs/sfx_index.json`, so a run no longer repeats the
same three whooshes;
the selection is content-addressed, therefore a retry picks the same cue. An
unindexed deployment keeps the built-in whoosh aliases as the fallback.
`audio.random_sfx_on_phrase_overlay` adds one deterministic cue at each phrase
overlay start, including each text segment in a fused phrase chain. It remains
opt-in and uses the same indexed pool and built-in fallback. `random_image_effects: true` is the
visual counterpart: each image overlay gets one deterministic yes/no draw and,
when it says yes, one light EffectsPack-derived treatment (native effect type,
light parameters, per-image grain seed) attached as that image's `effects`
stack. An image that already declares `effects` — through
`overlay_style.image.effects` or a pin `style.effects` — is left untouched, and
re-deriving the same plan replays the same decorations.

```json
{
  "animation_contract_version": 2,
  "animation_directives": [
    {"scope":"element", "scene_ref":"scene-1", "target":"important_phrase", "mode":"exact", "motion_id":"phrase_apple_clean_01_blur_soft_reveal"},
    {"scope":"element", "scene_ref":"scene-1", "target":"image", "mode":"prefer", "motion_id":"image_focus_reveal"},
    {"scope":"element", "scene_ref":"scene-1", "target":"caption", "mode":"auto"}
  ]
}
```

`max_phrase_overlays` requests the **run-level ceiling** for grounded phrase
overlays. Zero or omission keeps the certified default of five. Positive values
may lower or raise that default, but the runtime clamps the effective ceiling to
**50 phrases per run** (`overlays.MaxPhraseOverlaysHardLimit`; the old "15" in
this page was stale). Candidates are deduplicated and ranked before admission,
so the highest-ranked 50 survive an oversized request; the effective limit is
reported in `phrase_overlay_budget.requested_phrase_overlays`. A request above
the hard limit is reported by `payloadlint` because the clamp is silent. The
image ceiling is unaffected.

`max_overlays_per_scene` caps selected overlay items in each scene before
composition. For example, `2` retains at most two items per scene. Nearby
survivors can be fused by the animation-chain runtime; set
`animation_counts.chain_max_segments` (2..10) to bound each chain. The chain
hands off with `fly_under_fade`, which dips the framing down before revealing
the next segment; `chain_handoff_ms` sets the gap (default `400`, `0` for
adjacent segments). Omission or zero keeps the existing scene policy.

Entity image cards are admitted one identity at a time: each admitted entity
yields at most one standalone `entity_image` item with one verified asset.
Nearby **captioned** entity cards may be fused into an `image_chain` with one
segment per image; a chain is not a composite and keeps each image's own motion
and asset identity. Plain contextual photos stay standalone (the renderer would
lower chain image segments as a simultaneous deck), and a chain carries at most
ten segments. Multi-layer composites are produced only from explicitly authored
`MultiEntityGroups`, never inferred from nearby mentions. Every entity card's
duration follows the spoken mention dynamically (mention + 0.5 s hold, clamped
to 1–5 s). See `docs/operations/entity-overlay-composition.md` for the full
contract.

```json
{
  "version": 2,
  "items": [{ "source": { "type": "text", "topic": "..." }, "max_phrase_overlays": 12 }]
}
```

```json
{
  "version": 2,
  "items": [{
    "source": { "type": "clips", "clip_ids": ["clip-1"] },
    "phrase_motion_family": "modern_product_v1"
  }]
}
```

## Fail-closed contract (why a wrong body returns 400)

### Media modes and render placement

`media_mode` is explicit. `clip_only` accepts clip evidence only, `stock_only`
accepts caller-selected stock folders/assets only, and `mixed` requires
`source.type=clips` plus `output.stock_enabled=enabled` and
`output.stock_bindings`. In mixed mode the clip binding and the stock binding
are preserved on the same scene, so one generation job can combine fixed
intro clips with stock-backed body scenes.

Video rendering belongs under `items[].output.render`. A sibling
`items[].render` field is rejected with an error pointing to that canonical
path.

Fixed `intro`/`outro` sections may omit `playback.source_in_ms` and
`playback.source_out_ms`; the runtime uses a deterministic five-second window.
Any other window must specify both endpoints with `source_out_ms >
source_in_ms`.

### Per-clip stock marking (`stock_clip_ids`)

`items[].script_params.segments[].stock_clip_ids` marks individual clips of
that segment as **used-as-stock** (audio-only). A marked clip keeps its
binding and its original audio joins the generated voiceover mix at **full
original volume** — it survives the global `VOICEOVER_ONLY` removal and is
never ducked — while its video is never processed with a localized clip
render and never shown. The scene's visual comes from its stock binding, so
a scene whose clips are ALL stock-marked must also carry
`segments[].stock_folder_id` (expanded into the stock binding); without one
the final-job build fails closed instead of handing the remote worker a
scene it cannot composite.

```json
{
  "version": 2,
  "items": [{
    "source": { "type": "clips" },
    "script_params": { "segments": [{
      "topic": "Body scene",
      "clip_ids": ["clip-a", "clip-b"],
      "stock_clip_ids": ["clip-b"],
      "stock_folder_id": "<drive-folder-id>",
      "stock_folder_link": "https://drive.google.com/drive/folders/<drive-folder-id>"
    }]}
  }]
}
```

Validation is fail-closed (400): every entry must name one of the **same**
segment's `clip_ids` (stock marking never invents clip ownership), entries
must be non-empty and unique. SSOT:
`internal/kernel/script/script_spec.go` (`ScriptSegment.StockClipIDs`),
`internal/kernel/script/generation_source_validation.go`
(`validateSegmentStockClipIDs`). The per-scene entity extraction override is
owned by `ScriptSegment.ExtractEntities` in that same contract and validated
by `GenerationEnvelopeV2.Validate()` in `generation_validation.go`.

`bindGenerateEnvelope` decodes with **`DisallowUnknownFields`** and
additionally rejects removed contract keys against the raw body before
decoding. Therefore a misplaced or retired key is a hard `400`, never a
silently ignored field:

- malformed JSON / wrong top-level shape → `400 {"ok":false,"error":"invalid payload: <gin error>"}`;
- unknown field (e.g. `script_params` at the envelope root, or a removed
  key such as `assemble_final`) → `400 {"ok":false,"error":"invalid payload: json: unknown field ..."}`;
- typed validation failure (limits, empty segments, oversized
  `source_text`, …) → `400 {"ok":false,"error":{code,message,stage,retryable,extra}}`.
- `GenerationEnvelopeV2.Validate()` runs the structural checks; the
  config-aware `PayloadValidator` runs second (structural → semantic →
  config-aware order is intentional).

## Checking a job file before submitting it

`cmd/payloadlint` decodes and validates job files with the same gates as the
endpoint: the strict decoder (`GenerationEnvelopeV2` through
`DecodeGenerationEnvelopeStrict`, unknown keys inside
`output`/`audio`/`media_plan` included) and the same `PayloadValidator`
(`gencore`, so the retired `docs`, segment caps and source_text limits apply),
then adds the contradictions the struct shape cannot express. The rules live in
`internal/capabilities/scripts/payloadcheck` and are shared with the `velox`
preflight:

- `IMAGE_SEARCH_DISABLED_WITH_ENTITY_IMAGES` — `image_search_mode=disabled`
  forbids every image search, but `entity_images.enabled=true` requests
  portraits. (`explicit_only` is **not** a contradiction: it restricts inferred
  contextual queries, not the direct identity lane.)
- `AUDIO_MODE_CONFLICT` — `items[].audio.mode` and
  `items[].output.audio.mode` are both set; the runtime takes `item.audio` and
  discards the other value without a trace. (Version 2 only: version 3 rejects
  `output.audio` outright.)
- `PHRASE_BUDGET_CLAMPED` — `max_phrase_overlays` above the hard limit is
  clamped in silence.
- `IMAGES_PER_SCENE_CONFLICT` — `script_params.images_per_scene` disagrees with
  `media_plan.planner.max_images_per_scene` while both are explicit.
- `TARGET_WORDS_MISMATCH` — `script_params.target_words` disagrees with the sum
  of the explicit segment targets.
- `VOICEOVER_GROUP_CONFLICT` — `script_params.voiceover_group` and
  `output.voiceover_group` disagree; the builder prefers one and drops the
  other. (Version 2 only.)
- `FORCE_REFRESH_DUPLICATE` (warning) — `script_params.force_refresh=true` does
  not bypass the idempotency store; only the envelope-level `force_refresh`
  does. (Version 2 only: version 3 rejects the key.)
- `TARGET_WORDS_WIDE` (warning) — `target_words` is more than twice the words in
  the supplied segments.

```bash
go run ./cmd/payloadlint ../ops/jobs            # every recorded job envelope
go run ./cmd/payloadlint -strict ../ops/jobs   # warnings fail too
go run ./cmd/payloadlint ../ops/jobs/canonical # the reference payload
```

A `version: 3` body also runs the canonical-owner gate, the same one the
endpoint applies: a V2-only duplicate key fails with its JSON path (for
example `items[0].output.audio`) instead of the semantic warning version 2
would print.

Exit code `1` means at least one error. The canonical, drift-free examples are
`ops/jobs/canonical/script-generate-reference-20261126.json` (legacy) and
`ops/jobs/canonical/script-generate-reference-v3-20261126.json` (canonical);
both are asserted by `cmd/payloadlint`'s test suite when the outer workspace is
present.

## The mistakes that produce the expensive 400s

1. **`script_params` at the wrong level or in the wrong case.** It is a
   key **inside each object of `items[]`** — not at the envelope root,
   and not `scriptParams`. The Go field is `ScriptParams` with json tag
   `script_params`.
2. **A render key on `output` instead of `output.render`.** Render
   delivery is `output.render.drive_subfolder_name`; `output` itself only
   carries the postprocessor toggles, `save_to_db`, `generate_timeline`,
   `languages` and `drive_folder_id`. A key placed one level too high is a
   `400` (unknown field) — never a silent drop. The **result** envelope is
   where `items` reappears (`GenerationEnvelopeResult.items`); there is no
   top-level `output` key in the request or the result.
3. **The retired `docs` block.** `docs` is no longer an artifact selector:
   an item that still sends `docs.enabled=true` is rejected.
4. **`final_job_remote`.** Retired: `final_job=true` already hands the
   complete video to the Master, which owns the final render. Declaring only
   the removed duplicate is a `400`.
5. **A V2 duplicate in a version-3 body.** `output.audio`,
   `script_params.force_refresh`, `script_params.voiceover_group`,
   `map_type`, `map_camera_animation` and `map_label_animation` are rejected
   by path in version 3 (see [Contract versions](#contract-versions)); the
   same body still decodes under `version: 2`.

## Related

- `docs/operations/network-exposure.md` §3 — the `curl` invocation for
  `POST /api/script/generate` and the `202 Accepted` + polling contract.
- `docs/operations/job-debug-runbook.md` — diagnosing the resulting job
  row when generation fails after acceptance, including §10 for the
  `final_job=true` remote handoff (`REMOTE_RENDER_PENDING`, the attach-aware
  retry, and the `VELOX_FINAL_JOB_ATTACH_SECONDS` wait budget).
- `docs/api/ACTIVE_API_GENERATED.md` — the generated route manifest.
