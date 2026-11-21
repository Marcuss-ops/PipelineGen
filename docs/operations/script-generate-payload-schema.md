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

## Nesting

The body is an **envelope** with an `items` array. Every generation option
lives **inside an item**; there are no top-level shortcut keys.

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
      "docs":         { "enabled": true, "languages": ["en", "it"] },
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
vocabulary, same parameter bounds, max 12 entries; `[{ "type": "none" }]`
clears it). Each entry must supply the parameter that makes it visible —
`amount` for `glitch`, `scanlines`, `chromatic_aberration`, `vignette`,
`noise`, `sharpen`; `radius` for `gaussian_blur`; `block_size` for `mosaic`;
`size` for `halftone`; `intensity` for `banded_grain`; one of
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

`gain_db` is `-96..24`; `duration_ms` is optional and clamped to the image's
visible window. Within a scene, cues attach to that scene's image items in
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
  "background": {"mode": "blur_source"},
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
asset). `effects` uses the closed Chronon effect allowlist from
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

- `version` is always `2`. `items` must contain **at least one** entry.
- A single-item envelope maps to the unified `/generate` flow; multiple
  items map to batch generation.
- Every per-item block (`source`, `script_params`, `output`, `docs`,
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
| Maps | `map_presentation.animation` / `.animation_pool` / `.style` / `.style_pool` | registered composition id or `random`; optional arrays restrict the draw; style is `chronon_editorial`, `esri_topo`, `esri_sat`, or `random` | seeded draw from the renderer-certified pools |

A one-element `image_motions`, `entity_image_motions`, or
`entity_caption_motions` array pins that lane to the single id; a longer array
rotates through it. `short_phrase_motion` is a scalar pin and takes precedence
over the seeded short-phrase rotation.
`map_presentation` cannot be mixed with the legacy `map_type`,
`map_camera_animation`, `map_label_animation` or `map_animation` fields: the
mixture fails closed instead of guessing a winner.

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
    "timeline_label": "AUGUST 2007",
    "mountain_overlay_asset": "showcases/assets/taurus_mountain_range.png"
  }
}
```

`animation` chooses the map composition and `style` chooses the underlying
basemap independently. Both pools accept only registered choices certified by
the active native renderer; unknown ids fail before rendering. The current
The registry contains all 26 ChrononPlugin composition ids. Eighteen can be
drawn automatically. `historic_location_timeline` and `mountain_ridge_callout`
are exact-only because they need extra data. The six authored editorial
scenes are also exact-only and enforce their Indiana/US geography. There are
three renderer-backed basemaps.
`country_feature` and `timeline_label` let an exact historical timeline carry
its required data; `mountain_overlay_asset` must identify a readable RGBA
overlay. `continental_atlas` can infer Italy from a grounded Italian pin;
other countries must be named explicitly. Editorial scenes require the
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

`map_type` selects one basemap for the run. The single map renderer is the
native renderer of ChrononPlugin (the Python renderer is retired), which
draws the **editorial vector basemap only**:

- `chronon_editorial` (aliases `chronon`, `editorial`) — **default** when the
  field is omitted; the certified editorial look.
- `random` — a seeded draw over the servable styles (today the editorial
  basemap; never a decorative canvas).
- `satellite`, `roadmap`, `terrain` — the Esri raster tile families. They are
  **demolition debt** (TICKET-MAPS-NATIVE-PORT): rejected with a typed error
  until their bit-exact parity gates land — never silently swapped for a
  different look.

`map_camera_animation` currently allows only `slow_approach`; omission and
`random` resolve to that deterministic camera move. `map_label_animation` is
fixed to `none` because retained maps draw their own text. The catalog has
exactly ten animations: six Saudi/Middle East scenes (`ed_saudi_base`,
`ed_iran_glow`, `ed_yemen_fill`, `ed_yemen_markers`, `ed_hormuz_routes`,
`ed_mocha_tactical`), three ordinary glow maps (`region_zoom`, `pulse_marker`,
`spotlight`), and `slow_approach`.
`map_animation` accepts those nine compositions as its vocabulary and fails
closed on other IDs — but the native renderer serves the certified **base
flyover only** until each composed family lands its parity gate. An explicitly
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
`audio.random_sfx_on_clip_start` for a deterministic whoosh, or use explicit
`audio.sound_effects` to place a chosen SFX; image overlay SFX are chosen per
image with `overlay_images[].sound_effect`, while
`audio.random_sfx_on_image_overlay` remains the deterministic random fallback
for images the payload leaves unclaimed. `random_image_effects: true` is the
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
**15 phrases per run**. Candidates are deduplicated and ranked before admission,
so the highest-ranked 15 survive an oversized request; the effective limit is
reported in `phrase_overlay_budget.requested_phrase_overlays`. The image ceiling
is unaffected.

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

## The two mistakes that produce the expensive 400s

1. **`script_params` at the wrong level or in the wrong case.** It is a
   key **inside each object of `items[]`** — not at the envelope root,
   and not `scriptParams`. The Go field is `ScriptParams` with json tag
   `script_params`.
2. **`docs` vs `output` at the wrong level.** Post-generation artifact
   selection is **per item**: `docs` is its own item key (document
   publication) and `output` is the item's opt-in artifact block. The
   **result** envelope is where `items` reappears
   (`GenerationEnvelopeResult.items`); there is no top-level `output`
   key in the request or the result.

## Related

- `docs/operations/network-exposure.md` §3 — the `curl` invocation for
  `POST /api/script/generate` and the `202 Accepted` + polling contract.
- `docs/operations/job-debug-runbook.md` — diagnosing the resulting job
  row when generation fails after acceptance, including §10 for the
  `final_job=true` remote handoff (`REMOTE_RENDER_PENDING`, the attach-aware
  retry, and the `VELOX_FINAL_JOB_ATTACH_SECONDS` wait budget).
- `docs/api/ACTIVE_API_GENERATED.md` — the generated route manifest.
