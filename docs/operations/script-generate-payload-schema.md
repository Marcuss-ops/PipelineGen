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
      "animation_policies": [
        {"target":"important_phrase","group_id":"scene-1","motion_id":"phrase_apple_clean_07_slide_up_soft"},
        {"target":"image","group_id":"scene-1","subgroup_id":"hero","motion_id":"image_focus_reveal"}
      ],
      "random_light_leak_on_clip_start": true,
      "phrase_motions": ["phrase_apple_clean_07_slide_up_soft"],
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

- `version` is always `2`. `items` must contain **at least one** entry.
- A single-item envelope maps to the unified `/generate` flow; multiple
  items map to batch generation.
- Every per-item block (`source`, `script_params`, `output`, `docs`,
  `audio`, `media_plan`, `overlay_background`, `overlay_style`,
  `map_type`, `map_camera_animation`, `map_label_animation`,
  `map_animation`,
  `phrase_motions`, `image_motions`, `entity_image_motions`,
  `phrase_motion_family`, `animation_policies`,
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
and spatial 3D motions. Their
family is filtered before the requested animation-count window is selected, so
the window does not collapse to the most numerous family. Phrases shorter than
six words always use the Short Phrases lane, including phrases marked high
priority. Retries with the same job identity keep the same selection. Image
motions are sampled independently per image item.
`modern_apple` remains available when a payload explicitly wants only that
style family.

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

`phrase_motions`, `image_motions`, and `entity_image_motions` accept arrays of
certified animation IDs and rotate through the requested pool. `phrase_motions`
also accepts the 15 `doc_01`…`doc_15` Documentary Highlighting showcase IDs;
PipelineGen resolves those aliases to phrase-relative Brush, Apple, or 3D
motions before validating and compiling the pool, so they adapt to the supplied
copy instead of replaying sample-specific geometry. Omit an array to use the
existing seeded per-overlay rotation.

Nearby overlay items in the same scene are fused into one chain overlay when
they overlap or have no more than five seconds of empty timeline between them.
Chains retain each segment's own text/image/map content and selected motion;
`animation_counts.chain_gap_ms` can lower the maximum gap and
`animation_counts.chain_disabled: 1` opts out. RenderingGen lowers the chain as
one overlay render instead of one MP4 per member.

Use `animation_policies` when one motion should be chosen explicitly for each
animation group (or subgroup), instead of the planner's normal per-overlay
rotation. Each entry has `target` (`important_phrase`, `image`, `caption`,
`date`, or `metric`),
`group_id`, optional `subgroup_id`, optional `motion_id`, and optional
`motion_params`. Omit `motion_id` (or provide it as an empty string) to keep the
group declaration but have the runtime choose one random certified motion for
all matching items; this choice is seeded and stable on retry. `motion_params`
requires an explicit `motion_id`. The group selector matches a producer-supplied
group ID, or a `scene_id` when no explicit group ID exists; an item's ID is the
default subgroup, and image composite children may specify their own subgroup
IDs. A subgroup policy takes precedence over a group policy; a matching group
policy applies to every compatible item in that group. Explicit item/child
motion IDs remain higher priority. Groups not mentioned by this payload retain
their existing automatic per-overlay rotation. Motion IDs must be certified for
their target; `date` applies to `TIMELINE_DATE_CARD` and `metric` to
`METRIC_STAT_CARD`. Duplicate selectors and invalid timing params fail closed.

`random_light_leak_on_clip_start: true` adds a short certified light-leak
overlay at the beginning of each real video clip (synthetic freeze tails are
skipped). Light leaks on image overlays can be selected with an `image` motion
policy and a `lightleak_*` motion ID. Pair clip-start accents with
`audio.random_sfx_on_clip_start` for a deterministic whoosh, or use explicit
`audio.sound_effects` to place a chosen SFX; image overlay SFX remain controlled
by `audio.random_sfx_on_image_overlay`.

```json
{
  "animation_policies": [
    {"target":"important_phrase", "group_id":"scene-1", "motion_id":"phrase_apple_clean_07_slide_up_soft"},
    {"target":"image", "group_id":"people", "subgroup_id":"hero", "motion_id":"image_focus_reveal"},
    {"target":"caption", "group_id":"people"}
  ]
}
```

`max_phrase_overlays` requests the **run-level ceiling** for grounded phrase
overlays. Zero or omission keeps the certified default of five. Positive values
may lower or raise that default, but the runtime clamps the effective ceiling to
**15 phrases per run**. Candidates are deduplicated and ranked before admission,
so the highest-ranked 15 survive an oversized request; the effective limit is
reported in `phrase_overlay_budget.requested_phrase_overlays`. The image ceiling
is unaffected. `video.create` exposes the same key at the top level and forwards
it to its script-generate child.

`max_overlays_per_scene` caps selected overlay items in each scene before
composition. For example, `2` retains at most two items per scene. Nearby
survivors can be fused by the animation-chain runtime; set
`animation_counts.chain_max_segments` to `2` to limit each chain to two
animations. The chain hands off with `fly_under_fade`, which dips the framing
down before revealing the next segment. Omission or zero keeps the existing
scene policy.

Entity image cards are grouped and timed automatically; there is no payload
switch to opt out. Cards whose certified spoken anchors are five seconds or
closer (same scene) render as ONE composite overlay with independent per-portrait
motions, and every entity card's duration follows the spoken mention dynamically
(mention + 0.5 s hold, clamped to 1–5 s). See
`docs/operations/entity-overlay-composition.md` for the full contract.

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
