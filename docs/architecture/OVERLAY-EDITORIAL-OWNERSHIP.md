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

**Why promotion is an exception.** Whether an entity has a certified portrait is
own only to the PipelineGen media plane (`entity_image_catalog` in PostgreSQL).
BrainRust ranks the card and writes the chain without that knowledge, and the
Go pass attaches the verified asset after the seal. `attach_resolved_entity_images`
in the Rust planner covers the donors that already travel in the request; the
post-seal pass is the catalog-backed half, and it is the only place a segment's
`kind` may change.

## Decision matrix

| Decision | Owner | Where | Allowed after seal? | Pinned by |
| --- | --- | --- | --- | --- |
| Entity ranking | BrainRust | `editorial_plan_v2` | No | Rust parity tests |
| Image/phrase/bullet admission | BrainRust | `editorial_plan_v2` | No | Rust parity tests |
| Item ordering | BrainRust | sealed `FinalPlanV2` | No | `TestPostRustPlanPreservesEditorialDecisions` |
| Timing / frame windows | BrainRust | planner | No | same test |
| Motion / group motion | BrainRust | planner | No | same test |
| Chain segment preset | PipelineGen (ADR-029) | `applyChainSegmentPresets` | Yes, `PresetID` only | `TestApplyChainSegmentPresetsResolvesEveryChainSegment`, `TestChainSegmentPresetResolutionTouchesOnlyPresetID` |
| Entity-card → entity image (item and chain segment) | PipelineGen media plane (ADR-029 exception 2) | `promoteVerifiedEntityCards` / `promoteChainEntityCardSegments` | Yes: segment `Kind`, `TemplateID`, `PresetID`, `ImagePresetID`, `EntityCaption`, `AssetID`, `Assets`; drops segment `Text`, `MotionID`, `MotionParams`, `EntityStyleID`, `Params`; item `Kind`→`image_chain`, `TemplateID`→`IMAGE_CHAIN`, legacy `Text` cleared | `TestChainSegmentPromotionTouchesOnlyTheDeclaredSurface` |
| Entity-image run ceiling | PipelineGen | `capEntityImageOverlays` | Yes: drops `entity_image` items past `MaxEntityImageOverlaysPerRun` | `overlay_plan_entity_image_promotion_test.go` |
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
| `MaxEntityImageOverlaysPerRun` | `overlay_plan_seal.go:78`, `media_resolver_entity_binding.go:79,122` | pre-Rust media ceiling | keep |
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
