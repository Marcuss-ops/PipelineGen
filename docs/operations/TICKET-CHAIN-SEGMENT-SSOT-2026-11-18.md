# TICKET — chain_segments single source of truth (2026-11-18)

- **Status:** PARTIAL — the wire contract is pinned on all three sides, the
  post-seal mutation surface and run ceiling are declared and gated, and the
  post-seal passes are proven deterministic and idempotent. What remains is the
  behaviour change: the identity decision still runs twice (Rust planner +
  PipelineGen media plane), which needs a `*.v2` window and a visual acceptance
  run, not a silent refactor.
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
- **Consumer identity is one declared shim.** RenderingGen's `segmentIdentity`
  (`internal/overlay/chain_compile.go`) replaced the inline inference: the
  mapping is pinned case by case (`TestSegmentIdentityDeclaredMapping`), a
  segment carrying a map *and* image bytes or carrying no content fails the
  compile (`TestSegmentIdentityFailsClosed`,
  `TestCompileChainRejectsContentlessSegment`), and a legacy segment without
  `kind` still lowers (`TestCompileChainInfersMissingKind`).
- **The Rust boundary is pinned, and the donor path is verified.** A portrait
  attached by the planner can no longer promote the card or pick its preset
  (`identity_ownership_tests`), and an asset without URL + 64-char SHA is no
  longer copied onto a card. The 30 golden plan fixtures — the byte-level
  contract for the whole planner — are unchanged.
- **Determinism and the ceiling are gated.** `TestPromotionIsIdempotent`,
  `TestPromotionFingerprintIsDeterministic` and
  `TestEntityImageCeilingDropsOnlyEntityImages` pin the ticket's remaining
  reachable acceptance criterion: the same request yields the same fingerprint,
  a replayed promotion is a no-op, and the ceiling drops entity images only.

## Open

1. **Where promotion lives — blocked on the preset sampler, not on the catalog.**
   The planner already receives the certified portraits
   (`media.verified_entity_assets`, built by `overlayPlanMedia`) and already
   attaches donors to cards (`media.rs::attach_resolved_entity_images`), so the
   missing piece is *not* the catalog. It is the **preset**: a promoted image
   gets `PresetID = SelectEntityImagePreset(planID, sceneID, itemID)`, which is
   `DefaultDeterministicPresetSampler.Sample` seeded on
   job+scene+item with family `entity_image` over `imagePresetCandidates`
   (`preset.image`, the **Chronon-owned** `VisualPresetRegistry` vocabulary).
   Moving the decision into the planner therefore means either
   - (a) shipping that vocabulary in the request and porting the seeded sampler
     into Rust — a second implementation of the selection math in another
     language, which `deterministic_preset_sampler.go` explicitly rules out
     ("the sampler never hardcodes a preset id … so the Go side can never drift
     from Chronon's catalog"); or
   - (b) accepting a different entrance rotation for every entity image.

   Both are product decisions, so the split is now pinned instead of migrated:
   `media.rs::identity_ownership_tests` fails if the planner ever writes `kind`
   or `preset_id` while attaching a portrait, and the same test asserts the
   donor path only accepts an asset with a renderable URL and a 64-char content
   hash (the rule `append_verified_intent_images` already enforced).
2. **Delete the second decision, not just gate it.** After (1), remove
   `promoteChainEntityCardSegments`' identity rewrite and keep asset attachment.
3. **Move the ceiling pre-Rust** (`MaxEntityImageOverlaysPerRun` as a request
   ceiling) so no plan loses items after the seal.
4. **Remove the consumer's inference entirely.** `segmentIdentity`
   (`RenderingGen/internal/overlay/chain_compile.go`) no longer guesses an
   ambiguous or empty segment, but it still infers `kind`/`template_id` for the
   unambiguous legacy shapes. Once the producer always emits both — including
   `template_id` for card and number segments — the shim becomes an error and
   the mapping test is deleted.
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
- ~~A plan compiled twice from the same request yields the same fingerprint.~~
  **MET** — `TestPromotionFingerprintIsDeterministic`, which also covers a
  replayed promotion (`TestPromotionIsIdempotent`).
- `make verify-main` green with the three key-set gates in it. **BLOCKED** by a
  local, untracked cache entry (`data/final-job-fragments/66/*.mp4` carrying a
  pre-canonical avcC); no code in this ticket is involved.

## Prossimi passi (2026-11-19)

Il contratto è pinnato; quello che manca è la decisione di prodotto sul
sampler dei preset (§Open 1) e, a valle, la rimozione della seconda decisione.
L'ordine eseguibile è questo:

1. **Scegliere tra (a) e (b) del §Open 1** e scriverlo qui con data. Senza
   questa scelta i punti 2–6 non si possono toccare: sono tutti conseguenze.
2. **Se (a):** spedire il vocabolario `preset.image` nella richiesta e portare
   il sampler seedato in Rust; gate: `identity_ownership_tests` continua a
   passare e i 30 golden restano byte-identici.
3. **Se (b):** accettare la rotazione diversa e dichiararla; gate: nessuna
   modifica ai golden, ma la scelta va scritta in `OVERLAY-EDITORIAL-OWNERSHIP.md`.
4. **Rimuovere la riscrittura** in `promoteChainEntityCardSegments` (punto 2 del
   §Open) e ridurre `TestChainSegmentPromotionTouchesOnlyTheDeclaredSurface` a
   «nessuna chiave cambia».
5. **Spostare il ceiling pre-Rust** (punto 3): `MaxEntityImageOverlaysPerRun`
   nella richiesta, così nessun item sparisce dopo il seal.
6. **Ritirare `segmentIdentity`** (punto 4) quando il producer emette sempre
   `kind` e `template_id`, e rigenerare la fixture invece di tenerla scritta a
   mano (punto 5).

I residui **funzionali** delle catene (handoff a 0, backdrop, fit cover,
verifica del movimento camera) non stanno qui: sono in
[`TICKET-OVERLAY-CHAIN-ENTITY-IMAGES-2026-11-19.md`](TICKET-OVERLAY-CHAIN-ENTITY-IMAGES-2026-11-19.md).
