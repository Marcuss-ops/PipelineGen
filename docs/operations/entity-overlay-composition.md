# Entity image overlays: admission, chains and explicit composites

## Summary

The producer admits entity imagery one identity at a time. Each admitted
identity yields at most one standalone `entity_image` item carrying one
verified asset. Nearby overlays may then be fused into an animation chain
(`image_chain`) with one segment per image. A multi-layer composite
(`image_layers`) is an **explicitly authored** shape, not something the
producer infers from proximity.

Three contracts govern this area:

1. **One identity, one item** — an entity card is promoted to an image item only
   when its canonical identity resolves to a verified asset.
2. **A chain is not a composite** — five nearby images become five chain
   segments with five independent motions, not one five-layer collage.
3. **Dynamic duration** — an entity overlay's display window derives from the
   certified spoken mention plus a half-second readability hold, clamped into
   `[1 s, 5 s]`.

All three are producer-side decisions. RenderingGen lowers what the sealed plan
already decided and does not re-group, re-time or re-select the imagery.

## Automatic grouping was removed

The former helpers `composeNearbyEntityImages` (in
`internal/capabilities/scripts/entity_projection.go`) and `AutoImageGroups`
(batch overlay planner) no longer exist in the tree. They have no production
caller and no compatibility shim; a nearby mention gap is no longer a reason to
merge two identities into one item.

The only path that still creates a multi-layer composite is an explicitly
authored `SceneInput.MultiEntityGroups` entry. Fields without that authoring
step are not grouped by the planner.

## Explicit composite groups

`WireMultiEntityOverlays` turns an authored `MultiEntityGroup` into one
`entity_image` item carrying N `asset_refs` and N `image_layers`. Each layer
keeps:

- the shared group window (`start_ms`/`end_ms`, `start_us`/`duration_us`);
- its own `preset_id` and certified `motion_id`;
- its own caption and caption motion;
- per-slot geometry in `params` (`width`, `height`, `position_x`,
  `position_y`, `fit: contain`).

The planner assigns one seeded choreography for the whole group
(`GroupMotionID`) from the certified `image.group.count_2` through
`image.group.count_5` pools. All image layers share the same start and end, so
their entrance and exit are visible while the group is on screen. The composite
parent clears `EntityCaption`; the visible names stay on the layers.

Schema bounds: `image_layers` requires 2..5 children. There is no six-layer
layout, and an authored group larger than five is a validation error rather
than a silent split.

## Image chains (five images stay five images)

`CoalesceChainedOverlays` fuses nearby overlays into one chain item when the
configured gap and scene boundary allow it. For images this produces:

```json
{
  "kind": "image_chain",
  "chain_segments": [
    {
      "kind": "image",
      "asset_id": "img-0",
      "start_us": 0,
      "duration_us": 2800000,
      "motion_id": "image_focus_reveal",
      "preset_id": "image_scale_in",
      "template_id": "IMAGE_OVERLAY"
    }
  ]
}
```

This excerpt is illustrative; the authoritative bytes are the shared fixture in
`testdata/contracts/chain-segments-v1.json`. A segment has no separate
`segment_id` on the wire: the chain item ID plus the segment index is its
identity. Its `StartUS`/`DurationUS` are absolute plan microseconds. The producer owns them;
RenderingGen only offsets each segment against the chain window and lowers it
through the same per-kind compiler a standalone item uses. A five-image chain
therefore keeps five distinct asset identities and five distinct motions. It
does not become one composite item, and no downstream stage re-selects a motion
after sealing.

The renderer handoff is `fly_under_fade`: every non-first, non-map segment
receives the camera dip and fade. Maps are excluded because their own
`camera_move` is already an entrance animation.

## Dynamic duration

Implemented by `EntitySpokenWindowDuration` in
`internal/capabilities/entities/overlay_resolver.go`:

```go
durationUS = clamp(audioEndUS - audioStartUS + 500ms,
                   MinEntityOverlayDurationUS /* 1s */,
                   MaxEntityOverlayDurationUS /* 5s */)
```

- **Floor (1 s)**: a very short spoken mention still reads for a full second.
- **Hold (+0.5 s)**: the card stays up half a second past the last spoken word.
- **Ceiling (5 s)**: a long narration never makes the image run indefinitely.

The dynamic window applies to entity cards and entity images. Generic per-scene
contextual images are a separate lane: they are anchored at their certified
scene start and use the image ceiling rather than the entity spoken window.

## Renderer contract

`RenderingGen/renderinggen/internal/overlay/chain_compile.go` enforces:

- a non-map chain (`phrase_chain`, `image_chain`, `mixed_chain`) carrying a map
  segment fails closed;
- a `map_chain` segment without a map fails closed;
- each chain segment has a positive duration, a non-negative start and a window
  inside the chain window;
- each segment is rebuilt as its own semantic item and lowered through the same
  dispatcher as a standalone item.

`validateSemanticImageLayers` independently enforces the 2..5 composite bound
and per-layer asset references.

## Test matrix

| Surface | Test |
|---|---|
| Five nearby images keep five identities and five motions | `internal/capabilities/overlays/model/chain_coalesce_test.go` (`TestCoalesceFiveImagesPreserveFiveIdentitiesAndMotions`) |
| Five images are not split by an implicit ceiling | `TestCoalesceFiveImagesIsNotSplitByImplicitCeiling` |
| Producer/consumer fixture identity and five-segment decoding | `internal/capabilities/overlays/model/chain_segments_conformance_test.go` (`TestChainSegmentsSharedConformanceFixture`) |
| Certified entity image motion selection | `internal/capabilities/scripts/entity_image_motion_rotation_test.go` (`TestAssignEntityImageMotionsUsesCertifiedCatalog`) |
| Explicit composite group choreography | `internal/capabilities/overlays/selection_group_motion_test.go` (`TestSelectImageGroupMotionIsCertifiedDeterministicAndNoRepeat`) |
| Renderer rejects a map inside a mixed chain | `RenderingGen/renderinggen/internal/overlay/chain_map_contract_test.go` (`TestCompileRejectsMapInMixedChain`) |
| Renderer rejects a map chain without a map | `TestCompileRejectsMapChainSegmentWithoutMap` |
| Renderer compiles five images as five visual segments | `RenderingGen/renderinggen/internal/overlay/chain_compile_test.go` (`TestCompileFiveImageChainKeepsFiveDistinctVisualSegments`) |

The cross-repository fixture is
`testdata/contracts/chain-segments-v1.json` in PipelineGen and
`contracts/chain-segments-v1.json` in RenderingGen, byte-identical with SHA-256
`c8b8195de94e39b3ff36ffba938f4941a3e929ce0bdb1fdd0c8b37e445088faa`.

## Historical note

Before 2026-11-11 this document described automatic 5-second/same-scene
grouping of entity portraits into 2..5 layer composites. That behaviour and its
helpers were removed. The dynamic-duration contract survived unchanged; the
composite wire shape survives only for explicitly authored
`MultiEntityGroups`.
