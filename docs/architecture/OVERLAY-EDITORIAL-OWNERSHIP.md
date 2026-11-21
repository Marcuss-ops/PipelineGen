# Overlay editorial ownership after the Rust seal

**Status:** enforced 2026-11-18 by
`internal/capabilities/scripts/overlay_editorial_ownership_test.go`.
**Scope:** PipelineGen request builder and post-seal materialization; BrainRust
is the owner of the sealed editorial decisions.

## Contract

BrainRust's `editorial_plan_v2::compile_plan` validates the complete request,
runs ranking/admission/timing and returns a sealed `FinalPlanV2`. PipelineGen
then:

1. serializes the request;
2. calls `OverlayPlanCompiler.CompileOverlayPlanV2` once;
3. reattaches producer-only `LocalPath` values;
4. materializes chain presets and map plates;
5. promotes an entity card to an entity image when the media plane holds a
   verified portrait for it (item and chain segment);
6. seals and audits the plan.

Steps 3-5 are the only post-seal mutations. They are **materialization** and one
declared identity exception, not re-selection. The invariant is: after the
compiler returns, Go may change `PresetID`, `Params` metadata, map/asset
references and — through entity-image promotion only — a segment's
`Kind`/`TemplateID`/`Text`/`MotionID`, but may not change item order,
`StartUS`/`DurationUS`, `GroupMotionID`, any `EntityID`, or the number of items
and segments.

**Promotion is owned by the planner (2026-11-21, landed).** Whether an identity
has a certified portrait is known only to the PipelineGen media plane
(`entity_image_catalog` in PostgreSQL), so PipelineGen still *resolves* it. It no
longer *decides*: `overlayPlanMedia` ships the resolved portraits on
`media.promotable_entity_assets` and the v2 planner promotes the card or the
chain segment before it seals (`planner/promotion.rs`). The Go seal boundary now
adopts the document unchanged. **Decision 2026-11-21 (see `TICKET-CHAIN-SEGMENT-SSOT-2026-11-18.md`).**

- The entrance preset is resolved by decision **(a)**: `canvas.image_presets`
  (the Chronon-owned `preset.image` vocabulary) travels in the request and the
  seeded sampler is ported to Rust with byte-exact parity vectors
  (`job_fingerprint`, `scene_id`, `semantic_id`, `preset_family`; SHA-256;
  big-endian `u64` of the first eight **ASCII hex** characters, the Go quirk).
  (b) — a different rotation per entity image — was rejected as a visual
  regression.
- The catalog half is **not** `verified_entity_assets`. That surface ships only
  annotation images and already makes a card an `entity_image`; the Go resolver
  also indexes `SemanticRenderBundle`, and the old pass skipped a segment that
  already carried assets, so "has assets" and "is promotable" are disjoint sets.
  `media.promotable_entity_assets` is the separate surface: one entry per plan
  occurrence that resolves to renderable, content-addressed bytes, keyed by the
  occurrence identity the planner stamps on the item.
- Promotion is a single pass, post-coalescing, seeded with the fused chain item
  id (so the preset matches what the Go pass chose). A card that already carries
  assets or is already `entity_image` is left untouched; the run ceiling is
  applied only when the pass promoted something, as before.

## Decision matrix

| Decision | Owner | Where | Allowed after seal? | Pinned by |
| --- | --- | --- | --- | --- |
| Entity ranking | BrainRust | `editorial_plan_v2` | No | Rust parity tests |
| Image/phrase/bullet admission | BrainRust | `editorial_plan_v2` | No | Rust parity tests |
| Item ordering | BrainRust | sealed `FinalPlanV2` | No | `TestPostRustPlanPreservesEditorialDecisions` |
| Timing / frame windows | BrainRust | planner | No | same test |
| Motion / group motion | BrainRust | planner | No | same test |
| Chain segment preset | PipelineGen (ADR-029) until the v2 window; then BrainRust via `canvas.image_presets` (decision 2026-11-21 (a)) | `applyChainSegmentPresets` | Yes, `PresetID` only | `TestApplyChainSegmentPresetsResolvesEveryChainSegment`, `TestChainSegmentPresetResolutionTouchesOnlyPresetID` |
| Entity-card → entity image (item and chain segment) | BrainRust planner, fed by the PipelineGen media plane | `editorial_plan_v2/planner/promotion.rs` | No | Rust `promotion::tests::*`, `overlay_plan_contract::promotable_media_is_promoted_by_the_planner_not_by_the_consumer`; Go `TestPromotableEntityAssetsCarryThePlanIdentityAndTheVerifiedBytes` |
| Entity-image run ceiling | BrainRust planner (post-promotion only) + PipelineGen media resolver (pre-Rust) | `promotion::cap_entity_images`, `media_resolver_entity_binding.go` | No | Rust `the_ceiling_drops_entity_images_only_and_never_on_a_noop_plan` |
| Map plate / local asset | PipelineGen + RenderingGen | `materializeEditorialMapItems` | Yes, `Map`/`AssetRefs` resolution only | `TestMaterializeEditorialMapItemsResolvesPresentationCardPresets` |
| Fingerprint / render key | PipelineGen seal | `overlay_plan_seal.go` | Yes, derived state | seal tests |
| Intake/audit/lineage | PipelineGen | `overlay_plan_freeze.go` | Yes, read-only telemetry | freeze tests |

## Budget census

The audit cited `internal/capabilities/overlays/planner.go`; that file does not
exist at `main` on 2026-11-18. The dead-code sweep removed
`planner_candidates.go`; the surviving budget surface is:

| Symbol | Production callers | Classification | Action |
| --- | --- | --- | --- |
| `MaxImageOverlaysPerRun` | `overlay_plan_compile.go:127`, `final_job_image_preflight.go:302`, `overlay_canvas.go:121`, `planner_config.go:55` | pre-Rust run ceiling | keep |
| `MaxEntityImageOverlaysPerRun` | `media_resolver_entity_binding.go:79,122` | pre-Rust media ceiling | keep |
| `MaxPhraseOverlaysPerRun`, `MaxPhraseOverlaysHardLimit`, `EffectivePhraseOverlayLimit` | `overlay_canvas.go:113,230,555,566`, `generation_envelope.go:220` | pre-Rust request ceiling | keep |
| `MaxMapOverlaysPerScene`, `MaxMapOverlaysPerRun` | `planner_config.go:51`, `overlay_canvas.go:581,591` | pre-Rust map ceiling | keep |
| `MaxNumberOverlaysPerRun`, `MaxBrandTextOverlaysPerRun` | no in-repo production caller found | exported contract constant | keep until the producer/API owner confirms retirement |
| `MeasurePhraseOverlayBudgetWithLimit` | `overlay_plan_freeze.go:70` | post-seal telemetry | keep, read-only |
| `OverlayItemWindowUS` | `render_queue_submit.go:390` | timing projection | keep, no decision |
| `CountContent` | `render_attempt_analytics.go:186` | telemetry | keep, read-only |
| `AdmitMapComposition` | `map_composition.go:295` | pre-Rust map admission | keep |

No function in `visual_budget*.go` is reported by `deadcode` as unreachable on
2026-11-18. The two exported constants without an in-repo caller are not
functions and are kept as public contract until their owner retires them.

## What changed in the audit

- `planner.go` is gone. Use `planner_map*.go`, `visual_budget*.go`,
  `priority.go` and `overlay_plan_v2_request.go` as the current surface.
- `overlay_plan_v2_request.go` is 760 lines at this revision, not 819.
- `compileOverlayPlanV2` already documents "no Go selection before or after";
  the test now makes that statement executable.

## Chain segment wire contract

`params.chain_segments` is a 27-key wire shape shared by three code bases: the
Rust producer (`ChainSegment` in
`editorial_plan_v2/planner/coalesce.rs`), `capmodel.ChainSegment` in this
repository and `chainSegment` in RenderingGen's
`internal/overlay/chain_compile.go`.

Each side pins the same key set in its own test, so a key added on one side
fails in that side's repository without depending on a sibling checkout:

| Side | Test |
| --- | --- |
| Rust producer | `segment_contract_tests::chain_segment_wire_keys_are_the_documented_contract` |
| PipelineGen | `overlays/model/chain_segments_contract_test.go` |
| RenderingGen | `internal/overlay/chain_segments_contract_test.go` |

The cross-checkout mirror comparison
(`TestChainSegmentFieldsMatchRenderingGenMirror`) is bidirectional but
best-effort: it skips when the sibling repository is absent, which is why it is
not the contract gate.

Segment identity is resolved in one declared place. The producer always writes
`kind` and `template_id`; RenderingGen's `segmentIdentity`
(`internal/overlay/chain_compile.go`) is the compatibility shim for hand-authored
plans and only infers what the content names unambiguously — a segment carrying
both a map and image bytes, or carrying no content at all, fails the compile
instead of being guessed.

The post-seal passes are deterministic by test, not by convention: promotion is
idempotent, a replayed promotion keeps the fingerprint, and the same request
compiled twice yields the same fingerprint (`TestPromotionIsIdempotent`,
`TestPromotionFingerprintIsDeterministic`).

The fixture (`testdata/contracts/chain-segments-v1.json`, byte-identical in both
repositories) is **authored, not generated**: it proves the consumer lowers five
independent image segments. The 18/11 revision removed the five `"text": ""`
keys the producer can never emit and added the always-emitted `chain_gap_us`, so
a decode/encode round trip is now the identity.

## Verification

```sh
cd refactored
go test -count=1 ./internal/capabilities/scripts -run 'PostRust|ChainSegment|Materialize'
go test -count=1 ./internal/capabilities/overlays
cd ../BrainRust/overlay-compiler && cargo test --offline
cd ../../RenderingGen/renderinggen && go test ./internal/overlay -run 'Chain|Conformance'
```

If a future change needs to reorder or re-filter items after Rust, it must
change this document and the invariant test in the same commit, with an owner
and a reason. A silent post-seal selection is a bug.
