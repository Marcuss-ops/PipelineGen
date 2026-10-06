# Entity image overlays: composite composition and dynamic duration

## Summary

Two editorial contracts govern how PERSON / ORGANIZATION / GPE / CONCEPT
image cards are grouped and timed inside the semantic overlay plan
(`renderinggen.overlay-plan.v1`):

1. **Composite grouping** — entity-image cards whose certified spoken
   anchors are **five seconds or closer and in the same scene** are
   presented as **ONE composite overlay item** (one queued render, one
   Drive artifact), not as two or more sequential image videos.
2. **Dynamic duration** — an entity overlay's display window derives from
   the **certified spoken mention** of the entity plus a half-second
   readability hold, clamped into **[1 s, 5 s]**. There is no fixed
   five-second reset, and the duration is never estimated from text
   length or scene duration.

Both contracts are producer-side decisions (PipelineGen): RenderingGen and
Chronon only lower what the sealed plan already decided.

## Composite grouping contract

Implemented by `composeNearbyEntityImages` in
`internal/capabilities/scripts/entity_projection.go`.

- **Merge window**: mentions whose `start_ms` anchors are at most
  `entityImageMergeGapMS = 5_000` ms apart and share the same `scene_id`
  join the same cluster. Items without a scene never group; mentions in
  different scenes never group.
- **Cluster size 2..5** (`maxEntityImageGroup`): up to five portraits share
  one item. Remainders repartition deterministically so a group always has
  at least two children (6 → 4+2, 7 → 5+2, 11 → 5+4+2).
- **Wire shape**: one `entity_image` item carrying N `asset_refs` and N
  `image_layers`. Each layer keeps:
  - its own relative `start_ms`/`end_ms` inside the parent window
    (the staggered reveal follows each portrait's own spoken anchor);
  - its own `preset_id` and its own certified `motion_id`
    (`assignEntityImageMotions` consumes one motion-pool ordinal per
    layer, so animations stay independent and non-repeating);
  - its own `caption` (the entity's display name);
  - per-slot geometry in `params` (`width`, `height`, `position_x`,
    `position_y`, `fit: contain`).
- **Slot geometry** (1920×1080 reference; scales with the canvas):
  duo = left/right cards at ±24 % width; trio = one row at 31 % pitch;
  quad = 2×2; penta = hero row of three + pair below.
- **Identity**: the composite item ID concatenates the child IDs
  (`id-a+id-b+…`); each layer ID is the original item ID and carries the
  child's `entity_id` (producer-only) so intent joins survive. The parent
  carries no `entity_id`/`entity_ref` — it represents the group.
- **Renderer support**: RenderingGen lowers every layer to an
  independently animated Chronon image layer (`compileImageLayers`); the
  overlay-plan.v1 schema sets no upper bound on `image_layers`.

## Dynamic duration contract

Implemented by `EntitySpokenWindowDuration` in
`internal/capabilities/entities/overlay_resolver.go`:

```go
durationUS = clamp(audioEndUS - audioStartUS + 500ms,
                   MinEntityOverlayDurationUS /* 1s */,
                   MaxEntityOverlayDurationUS /* 5s */)
```

- **Floor (1 s)**: a very short spoken mention still reads for a full
  second — the card never collapses into a sub-second flash.
- **Hold (+0.5 s)**: the card stays up half a second past the last spoken
  word of the mention so the narration can move on first.
- **Ceiling (5 s)**: a long narration never makes the image layer run
  indefinitely (`MaxEntityOverlayDurationUS`).

The dynamic entity duration is applied on these entity-image paths:

1. the entity overlay resolver — PERSON/ORG/GPE/CONCEPT cards
   (`ResolveRankedEntityOverlayPlan`);
2. product/logo image candidates (`imageCandidate` in
   `overlay_entity_cards.go`);
3. the semantic render bundle records the certified entity timeline and asset
   bindings for audit; the renderer plan is compiled from the canonical entity
   resolver, which preserves the spoken window and applies the hard image ceiling
   (`MaxImageOverlayDurationMS`).

**Separate scope:** generic per-scene contextual images are not entity
portraits. `sceneImageCandidate` in `overlay_scene_images.go` is anchored at
its certified scene start, begins after the entity-card opening window, and
currently uses `MaxImageOverlayDurationMS`; it does not use
`EntitySpokenWindowDuration`.

For entity cards/images, certified word/audio timing of the mention is the
only timing authority end to end. Generic scene images use the certified
scene start and their separate scene-image policy.

## Where it lands in the plan

A composite appears in `overlay_plan.items[]` as:

```json
{
  "id": "overlay-scene-0-ada-lovelace+overlay-scene-0-grace-hopper",
  "kind": "entity_image",
  "template_id": "image_popup",
  "start_ms": 0, "end_ms": 2000,
  "start_us": 0, "duration_us": 2000000,
  "asset_refs": [ { "asset_id": "…", "sha256": "…" }, { "asset_id": "…", "sha256": "…" } ],
  "image_layers": [
    { "id": "overlay-scene-0-ada-lovelace", "asset_id": "…", "start_ms": 0, "end_ms": 1000,
      "preset_id": "…", "motion_id": "…", "caption": "Ada Lovelace", "params": { "…": "…" } },
    { "id": "overlay-scene-0-grace-hopper", "asset_id": "…", "start_ms": 1000, "end_ms": 2000,
      "preset_id": "…", "motion_id": "…", "caption": "Grace Hopper", "params": { "…": "…" } }
  ]
}
```

The final-job payload gate (`final_job_payload.go`) admits one overlay per
rendered Drive asset and one overlay per frame window. Composites reduce
the number of rendered artifacts, and their child layers always live
inside the parent window, so the frame-intersection gate stays satisfied.

## Test matrix

| Surface | Test |
|---|---|
| Grouping 2..11 images, no stranded singles | `internal/capabilities/scripts/overlay_entity_cards_test.go` (`TestComposeNearbyEntityImagesGroupsTwoThroughFiveWithDeterministicRemainders`, `…PreservesMoreThanFiveInSameScene`) |
| 5 s boundary (5000 composes, 5001 separates), cross-scene isolation | `TestComposeNearbyEntityImagesHonorsFiveSecondMentionGap` |
| Composite timing/identity/captions + plan validation + wire + independent certified motions | `TestComposeNearbyEntityImagesCreatesOneStaggeredComposite` |
| Dynamic duration clamps + millisecond quantization | `internal/capabilities/entities/overlay_resolver_test.go` (`TestEntitySpokenWindowDurationDynamicClamps`, `TestResolveEntityOverlayPlan_DurationFollowsSpokenWindow`) |
| Bundle preserves repeated scene-scoped entity occurrences | `internal/capabilities/scripts/semantic_render_bundle_builder_test.go` (`TestBuildSemanticRenderBundleKeepsRepeatedEntityImageOccurrencesPerScene`) |
| Async Drive receipt is projected without mutating the render certificate | `internal/platform/sqlite/jobs/result_store_overlay_links_test.go` (`TestRecordOverlayDriveLinkKeepsAsyncReceiptBesideRenderArtifact`) |
| Real-engine side-by-side render (opt-in, `CHRONON_BIN=…`) | `RenderingGen/renderinggen/internal/overlay/composite_image_runtime_test.go` |

The live end-to-end certification is
`tests/operational/person_overlay_drive_e2e.sh`. The production path renders
one short transparent artifact per semantic overlay item (a composite remains
one item/artifact), checks the renderer/GPU certificate and bounded clip
windows, then waits for the asynchronous Drive outbox to record one valid
`result.overlay_links` receipt per rendered item. All receipts must resolve to
the same non-empty Drive folder; set `EXPECTED_OVERLAY_DRIVE_FOLDER` only when
a run must target a specific known folder. `overlay_render.artifact` is the
first render certificate and is not rewritten by that publication projection.
