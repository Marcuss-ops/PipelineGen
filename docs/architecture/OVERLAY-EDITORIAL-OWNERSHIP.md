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
5. seals and audits the plan.

Steps 3-4 are the only post-seal mutations. They are **materialization**, not
re-selection. The invariant is: after the compiler returns, Go may change
`PresetID`, `Params` metadata and map/asset references, but may not change item
order, `Kind`, `TemplateID`, `Text`, `EntityCaption`, `MotionID`,
`StartUS`/`DurationUS`, `GroupMotionID`, any `EntityID`, or the number of items
and segments.

**Card vs image is owned by the planner (2026-11-23, OVL-01).** Whether an
identity has a certified portrait is known to the PipelineGen media plane
(`entity_image_catalog` in PostgreSQL), so PipelineGen *resolves* it. It does
not *decide*: `overlayPlanMedia` ships **one** surface,
`media.verified_entity_assets`, keyed by the plan occurrence identity.
Annotation bindings and bundle/timeline resolutions share that list; there is no
separate `promotable` surface and no post-plan promotion pass. The v2 planner
decides in `media.rs::append_entity_cards`, before admission, coalescing and
clipping, and seals the result. The Go seal boundary adopts the document
unchanged.

- A disabled `images` category keeps the card textual: `append_entity_cards`
  does not attach the portrait, so admission does not drop the card.
- The run ceiling is the single `image_overlays` budget in `admission.rs`;
  there is no second, post-plan entity-image ceiling.
- A fused image chain gets no special promotion preset: an entity image takes
  the same presentation path as every other image.

## Decision matrix

| Decision | Owner | Where | Allowed after seal? | Pinned by |
| --- | --- | --- | --- | --- |
| Entity ranking | BrainRust | `editorial_plan_v2` | No | Rust parity tests |
| Image/phrase/bullet admission | BrainRust | `editorial_plan_v2` | No | Rust parity tests |
| Item ordering | BrainRust | sealed `FinalPlanV2` | No | `TestPostRustPlanPreservesEditorialDecisions` |
| Timing / frame windows | BrainRust | planner | No | same test |
| Motion / group motion | BrainRust | planner | No | same test |
| Chain segment preset | PipelineGen (ADR-029) | `applyChainSegmentPresets` | Yes, `PresetID` only | `TestApplyChainSegmentPresetsResolvesEveryChainSegment`, `TestChainSegmentPresetResolutionTouchesOnlyPresetID` |
| Entity-card → entity image (item and chain segment) | BrainRust planner, fed by the PipelineGen media plane | `editorial_plan_v2/planner/media.rs::append_entity_cards` | No | Rust `overlay_plan_contract::bundle_media_binds_the_card_without_a_post_plan_promotion`; Go `TestResolvedEntityAssetsCarryThePlanIdentityAndTheVerifiedBytes` |
| Entity-image run ceiling | BrainRust admission (`budgets.image_overlays`) + PipelineGen media resolver (pre-Rust) | `admission.rs`, `media_resolver_entity_binding.go` | No | Rust `entity_image_priority.rs`, `explicit_image_budget_is_a_run_ceiling` |
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
