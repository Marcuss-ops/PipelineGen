# TICKET — chain_segments single source of truth (2026-11-18)

- **Status:** PARTIAL — the wire contract is pinned on all three sides and the
  post-seal mutation surface is gated; the identity decision itself still runs
  twice (Rust planner + PipelineGen media plane).
- **Severity:** P1 — a post-seal pass rewrites chain segment identity, which the
  ownership doc forbade, and nothing failed when it did.
- **Owner:** overlay/editorial pipeline + media plane
- **Source:** analysis 2026-11-18 on `refactored@8b41b5bf1`,
  `BrainRust/overlay-compiler`, `RenderingGen@1c799cfd6`
- **Related:** ADR-029 (authority boundary), `OVERLAY-EDITORIAL-OWNERSHIP.md`,
  `entity-overlay-composition.md`, BrainRust TICKET-14

## Problem

`params.chain_segments` is produced by BrainRust and consumed twice in Go, but
until 2026-11-18 the "single contract" existed only as prose plus two copies of
a fixture and two SHA constants.

1. **Entity promotion rewrites segment identity after the seal.**
   `promoteChainEntityCardSegments` (`scripts/overlay_plan_seal.go:120`) sets
   `seg["kind"]`/`seg["template_id"]` and **deletes** `text`, `entity_style_id`,
   `motion_id`, `motion_params`, `params` (`:154-158`), then flips the item to
   `image_chain` (`:176-177`). That is identity and motion re-selection after
   Rust ranked the plan, while the ownership doc said `Kind`/`TemplateID`/
   `Text`/`MotionID` are read-only post-seal.
2. **A run ceiling drops items after the seal.** `capEntityImageOverlays`
   (`overlay_entity_cards.go:377`) removes `entity_image` items past
   `MaxEntityImageOverlaysPerRun`, so the rendered plan is not the ranked plan.
3. **The invariant test could not see any of it.** `snapshotEditorial`
   snapshotted item-level fields only and the test ran map materialization
   alone, never promotion and never a segment.
4. **Three wire definitions, one untyped.** The Rust producer built segments as
   `serde_json::Map` with `insert_non_empty`, so a key rename could not be
   caught by the Rust compiler; the Go producer used `omitempty` throughout and
   the RenderingGen mirror used none.
5. **The alignment gate skipped itself.** `TestChainSegmentFieldsMatchRenderingGenMirror`
   read the sibling file, compared one direction only and `t.Skip`ped when the
   checkout was absent. The Rust producer was not covered at all.
6. **The fixture was not producer output.** It carried five `"text": ""` keys
   that `insert_non_empty` can never emit and was missing the always-emitted
   `chain_gap_us`, so a decode/encode round trip silently changed the document.
7. **The two sources of truth meet here.** Whether an entity has a certified
   portrait lives in PostgreSQL (`entity_image_catalog`); whether the card
   becomes an image is an editorial decision. Rust ranks blind, Go decides
   afterwards, and the decision was undocumented.

## What landed (2026-11-18)

- **Typed producer.** `ChainSegment` in
  `BrainRust/overlay-compiler/src/editorial_plan_v2/planner/coalesce.rs` replaces
  the untyped map. `is_blank` reproduces the old `insert_non_empty` semantics, so
  the wire bytes are unchanged (all 30 golden plan-contract tests stay green).
- **Three independent key-set pins** for the same 27 keys: Rust
  `segment_contract_tests::CANONICAL_KEYS`, `capmodel`'s
  `chain_segments_contract_test.go`, RenderingGen's
  `internal/overlay/chain_segments_contract_test.go`. None of them depends on a
  sibling checkout.
- **`omitempty` pinned** on the Go producer: a Go re-emit cannot keep an empty
  string the producer would have dropped, which would otherwise change the plan
  fingerprint of an identical chain.
- **Fixture corrected** to producer shape (blank `text` removed,
  `chain_gap_us` added, both repositories byte-identical at
  `7647670ee04550892cc9c8939bba6b71d354698ccdb2bdbdd8a4ba8d25dde9d0`) with a
  strict decode/encode round-trip assertion.
- **Post-seal segment gate.** `snapshotEditorial` now snapshots every segment's
  `Kind`/`TemplateID`/`Text`/`EntityID`/`MotionID`/`GroupID`/`StartUS`/
  `DurationUS`, and two tests pin the declared surface:
  `TestChainSegmentPromotionTouchesOnlyTheDeclaredSurface` (added keys, dropped
  keys, preserved timing/entity/order) and
  `TestChainSegmentPresetResolutionTouchesOnlyPresetID`.
- **Matrix corrected.** `OVERLAY-EDITORIAL-OWNERSHIP.md` now declares entity-image
  promotion as ADR-029 exception 2 and the entity-image ceiling as a declared
  drop, each with its pinning test.
- **Mirror comparison is bidirectional** and fails instead of passing when the
  `chainSegment` anchor disappears.

## Open

1. **Decide where promotion lives (the real fix).** Pass the certified-image set
   into the compile request — the Rust planner already attaches resolved donors
   (`media.rs:360 attach_resolved_entity_images`) and fuses captioned cards
   (`coalesce.rs fuse_captioned_entity_image_chains`); it only lacks the catalog
   half. Once it can promote, PipelineGen attaches bytes only and the exception
   disappears. This changes plan fingerprints, so it needs a `*.v2` schema window
   where both paths run.
2. **Delete the second decision, not just gate it.** After (1), remove
   `promoteChainEntityCardSegments`' identity rewrite and keep asset attachment.
3. **Move the ceiling pre-Rust** (`MaxEntityImageOverlaysPerRun` as a request
   ceiling) so no plan loses items after the seal.
4. **Remove the consumer's inference.** `segRIItem`
   (`RenderingGen/internal/overlay/chain_compile.go:86`) still derives
   `kind`/`template_id` from content when a segment omits them. Once the producer
   always emits both, this fallback must fail closed instead of guessing.
5. **Generate the fixture or drop the SHA pin.** The fixture is authored, so it
   proves lowering, never producer parity. Either a Rust test emits these bytes or
   the two SHA constants are replaced by a generated artifact.
6. **Retire the local decoders.** `chainSegmentMaps`, `chainSegments`,
   `overlayPlanChainSegments` and `chainSegmentStyleMaps` are already gone; the
   remaining `DecodeChainSegments` / `ChainSegmentMaps` pair must survive only
   until (1) makes the plan cross the boundary already validated.

## Acceptance criteria for closing

- `promoteChainEntityCardSegments` no longer mutates `kind`, `template_id`,
  `motion_id`, `text`; `TestChainSegmentPromotionTouchesOnlyTheDeclaredSurface`
  is reduced to "no key changes at all".
- No item or segment disappears between the Rust return and the renderer.
- A plan compiled twice from the same request yields the same fingerprint.
- `make verify-main` green with the three key-set gates in it.
