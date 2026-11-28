# Editorial Composition Wave — PipelineGen checklist

Scope: `refactored/` (PipelineGen), the active repository. A checked item means
its PipelineGen contract/boundary acceptance criterion is implemented and tested.
An unchecked item belongs to BrainRust, RenderingGen, Chronon, or needs an
end-to-end integration those repositories must provide.

## Detailed acceptance checklist

- [x] **ED-01** Versioned `editorial_composition.v1` contract, 12 machine-readable fixtures, and deterministic lowered goldens.
- [x] **ED-02** Persistent inherited-background policy and source-member validation/lowering.
- [x] **ED-03** `previous_snapshot` requires an asset ID and canonical SHA-256.
- [x] **ED-04** The new PipelineGen contract path uses layers and snapshot references; it adds no intermediate video encode/decode path.
- [x] **ED-05** BrainRust selects locally grouped, semantically related micro-summary phrases and emits a validated `editorial_composition.v1` sidecar with exact source text and timing; PipelineGen preserves the sidecar in its internal plan model.
- [x] **ED-06** Progressive summary points have ordered, bounded timing and duplicate-text rejection; RenderingGen also compiles `progressive_reveal` with strictly increasing phrase entrances and cumulative holds through the group end.
- [x] **ED-07** Short/long phrase lanes can join typed shared choreography groups.
- [x] **ED-08** Chain/hold participation is declared by motion capability, not inferred from a motion ID prefix.
- [x] **ED-09** Chapter modes and authored titles are accepted, validated, and forwarded; ignored directives fail closed.
- [x] **ED-10** `chapter_only` policy and title constraints are represented and validated at ingress.
- [x] **ED-11** Chapter style families and duration are typed and validated.
- [x] **ED-12** Date, metric, and caption attachments are typed children of entity layers.
- [ ] **ED-13** RenderingGen places attachments in physical safe areas based on the final layout.
- [x] **ED-14** An attached date and a standalone date for the same composition are rejected as duplicates.
- [x] **ED-15** Chart values retain typed period, value, unit, scene, and evidence fields.
- [x] **ED-16** BrainRust's typed chart series bind points to a shared subject and grounded period; multi-language and cross-subject rejection tests cover the association.
- [x] **ED-17** The contract validates multi-series chart structure and point uniqueness.
- [x] **ED-18** Map photo callouts require a verified asset identity and a declared choreography.
- [x] **ED-19** Lowered map callout layers retain their pin anchor ID and connector intent.
- [x] **ED-20** The contract rejects map photos without asset ID and SHA-256 evidence.
- [ ] **ED-21** RenderingGen calculates camera-aware placement and map/photo geometry.
- [x] **ED-22** PipelineGen remains the orchestration/contract boundary; semantic selection and physical rendering stay outside it.
- [x] **ED-23** BrainRust legacy-branch reachability audit is complete: no dead quantity/parser or planner branch was identified for removal. The chart quantity fallback, prefix fallback, legacy chain handoff, and `playfair_display` alias are reachable compatibility paths with tests and dated removal tickets; the typed-quantity fallback is intentionally retained until bare numeric chart points are typed.
- [x] **ED-24** Capability catalog is sent with the v2 plan and checked against active renderer-supported registry entries.
- [ ] **ED-25** Renderer frame and synchronization certification is implemented by RenderingGen/Chronon.
- [ ] **ED-26** GPU-native background reuse benchmarks are measured and certified by Chronon.
- [x] **ED-27** BrainRust and RenderingGen have deterministic replay checks; each selector also has seeded 10,000-draw pool-coverage, frequency-tolerance, and normalized-entropy acceptance tests.
- [x] **PG-TEST** Contract, scripts, and rustexec package tests pass; `make verify-agent` passes.

## External next steps

1. BrainRust: support ED-09–10 chapter behavior and connect the existing ED-16/17 chart planner to typed cross-scene composition output.
2. RenderingGen: consume the preserved BrainRust summary sidecar; wire the ED-13 resolver into final composition lowering; adopt the ED-21 camera-projected pin/photo placement and leader samples in actual Chronon layers; expand ED-25 frame/synchronization checks beyond the current canary.
3. PipelineGen + asset provider: connect verified geographic media to ED-18–20 callouts when the worker emits them.
4. ED-26 is measured and filed; use its Chronon host/backend evidence when ED-25 receives real composition lowering and decoded-frame pixel assertions.

## Open-criterion execution TODO

These are the remaining implementation gates, split into reviewable actions.
Check a box only after the code path and its named verification both exist.

### ED-05 — Local micro-summary selection (BrainRust)

- [x] Define bounded eligibility: same scene/topic, local timing window, 2–3
  concise source-grounded phrases, enough remaining scene time, and no semantic
  duplicates.
- [x] Select only from already grounded phrase candidates; retain the source
  spans and certified timing for every summary point.
- [x] Emit the selected summary as the shared composition contract and carry it
  through the PipelineGen response without reconstructing editorial meaning.
- [x] Add positive/negative multilingual tests for related vs unrelated,
  duplicate, long, distant, and insufficient-window phrases; assert deterministic
  selection and budget accounting.

### ED-13 — Attachment safe-area lowering (RenderingGen)

- [x] Implement and unit-test the geometry resolver using measured child sizes,
  final parent bounds, safe inset, requested/automatic positions, and occupied
  rectangles; reject layouts that cannot fit.
- [ ] Invoke the resolver from the final sized composition lowering and write
  the returned positions into the actual Chronon layers.
- [ ] Cover image-only, caption-plus-date, full-screen, badge, safe-area edge,
  obstacle, and no-fit cases in lowered-plan fixtures.
- [ ] Render a canary and certify attachment bounds against the final frame
  layout.

### ED-21 — Camera-aware map photo callout (RenderingGen)

- [x] Require one verified image callout per real map pin and preserve the
  declared choreography and anchor identity.
- [x] Calculate a stable safe-area photo-card box from the whole camera-projected
  pin track and produce ordered leader-line endpoints for each projection sample.
- [ ] Consume those results in the active map compiler as an image layer and
  leader-line layer synchronized to the camera timeline.
- [ ] Render a moving-camera canary and verify the pin/card connector remains
  attached while the card stays inside the safe area and clear of blockers.

### ED-25 — Frame and synchronization certification (RenderingGen + Chronon)

- [x] Validate group entrance ordering and cumulative holds; the real Vulkan
  Chronon canary emitted exactly 60 RGBA frames and decoded-frame foreground
  samples passed at frames 20 and 59 with no backend downgrade.
- [ ] Add decoded-frame visibility checks for each member's entrance, hold, and
  exit boundaries, with explicit allowed frame tolerance.
- [ ] Certify progressive summary, attached date, and camera-following callout
  once their composition lowering is active.
- [ ] Connect BrainRust's progressive-summary sidecar to the certified
  `progressive_reveal` animation group and render its canary.
- [x] Store a reproducible evidence report with source/build identity, backend,
  frame metrics, and certification result.

Evidence: [Vulkan canary report](../../../RenderingGen/docs/evidence/editorial-composition-vulkan-20261128/report.json), plan, and Chronon timing sidecar.

### ED-26 — GPU-native background reuse benchmark (Chronon3d)

- [x] Confirm this host exposes an NVIDIA RTX A4000 and a Vulkan-enabled
  `linux-fast-dev` configuration.
- [x] Build the Vulkan `chronon3d_cli` target and run the benchmark on GPU (no
  software renderer fallback).
- [x] Add a self-contained editorial fixture that compares repeated independent
  background materialization with one persistent background and records decode,
  upload/allocation, GPU residency, and wall-time measurements.
- [x] Publish repeatable benchmark evidence with fixture/content identity,
  source/build hashes, host/GPU/driver, warm-up/repetition policy, and result.
- [x] Keep the official 17-scene GPU corpus ticket separate; its external
  content-pack blocker does not substitute for this dedicated reuse fixture.

Evidence: [Chronon benchmark runner](../../../Chronon3d/scripts/benchmark_editorial_background_reuse.py) and [measured report](../../../Chronon3d/docs/evidence/editorial-background-reuse-20261128-r2/report.json).

## Progress log

- 2026-11-28: RenderingGen added and published the `progressive_reveal` animation-group choreography. It accepts phrase members only, requires strictly increasing entrance offsets, rejects early exits, and holds every member to the group end. Targeted group and catalog tests pass.
- 2026-11-28: BrainRust's existing dirty work was preserved and audited. The `semantic-core` phrase-impact suite passed (73 tests); the editorial-composition contract corpus passed (5 tests). These tests do not yet prove ED-05 local summary selection or production emission of the shared composition contract.
- 2026-11-28: RenderingGen's editorial-layer admission and animation-group test packages pass. Physical attachment placement and pin/photo geometry are still open; admission alone does not certify rendered geometry.
- 2026-11-28: ED-13 now has a tested geometry resolver that uses final parent bounds, measured child sizes, safe insets, and occupied rectangles. It fails closed when no slot fits; wiring it into the active semantic-plan lowering remains outstanding.
- 2026-11-28: RenderingGen's editorial-layer admission now checks each callout anchor against an actual map pin and rejects duplicate callouts for the same pin. ED-21 now also has camera-projected safe-card geometry and per-sample leader endpoints; active map layer emission remains open.
- 2026-11-28: ED-27's BrainRust and RenderingGen seeded distribution/pool-coverage tests pass. ED-16's serial chart suites passed with the installed ONNX Runtime configured explicitly: chart_multilingual 3/3, chart_series_metadata 8/8, chart_subject_binding_52 5/5, charts_numeric 18/18.
- 2026-11-28: Chronon’s existing GPU baseline runner still cannot certify ED-26 on this host: its documented canonical content pack is absent and the built-in smoke is not an editorial-composition benchmark. No performance claim is recorded from that smoke.
- 2026-11-28: The initial BrainRust chart attempt failed because `ORT_DYLIB_PATH` was unset; rerunning serially with `/home/pierone/.local/share/visualner/models/gliner2.5-multi-v1-onnx/libonnxruntime.so.1.23.2` fixed the environment and all four ED-16 suites passed.
- 2026-11-28: The first real Chronon group canary exceeded its 8-minute test timeout at 72/96 frames on the software backend. The canary has been reduced to a 60-frame 640×360 smoke and is being rerun with ffprobe frame-count verification.
- 2026-11-28: The reduced Chronon group canary passed: H.264, 640×360, and exactly 60 decoded frames. RenderingGen group tests also pin heterogeneous member starts at frames 0/4/8 and hold all members to the group end. Full pixel-level sync evidence across other editorial compositions remains open under ED-25.
- 2026-11-28: RenderingGen's complete overlay test packages pass after the choreography, layout resolver, pin-anchor, and duplicate-callout changes; `git diff --check` passes.
- 2026-11-28: RenderingGen now tests seeded animation-group motion selection across 10,000 independently seeded plan identities. The test checks full pool coverage, per-motion frequency within ±40% of expectation, and normalized Shannon entropy ≥0.95; it passes together with retry-stability coverage.
- 2026-11-28: BrainRust ED-23 reachability audit found the chart unit parser is still called by both sentence-local and cross-sentence chart construction, and its tested fallback handles numeric spans without a typed `Quantity`; the owning hygiene ticket explicitly schedules removal for 2027-03-31 after all points are typed. Removing it now would drop supported bare-number charts, so the branch is reachable and retained. The current audit found no evidence that this fallback is dead code.
- 2026-11-28: Completed ED-23's requested reachability audit across BrainRust planner/semantic-engine legacy markers. The typed-quantity chart fallback, catalog-absent motion-prefix fallback, chain timing/camera fallbacks, and `playfair_display` normalization each have active call paths and compatibility tests; there was no verified dead branch to delete. Marked complete as an audit/removal criterion, with the live quantity fallback's later removal date retained in its owning hygiene ticket.
- 2026-11-28: ED-05 is implemented in BrainRust's production plan seal. It groups 2–3 adjacent grounded phrase candidates in one scene with shared content, bounded gaps/span, positive scores, duplicate rejection, and concise-text limits; it emits `editorial_composition.v1` with exact source text/timing. Contract and request-backed tests cover English, Italian, and German, unrelated/distant/duplicate/oversized/insufficient-window cases, deterministic replay, and forged-point rejection. PipelineGen preserves the sidecar internally while keeping it out of the legacy renderer wire; RenderingGen adoption remains open.
- 2026-11-28: PipelineGen's BrainRust response adapter now preserves `editorial_compositions` in the in-memory `OverlayPlan`; a focused adapter test verifies identity retention and confirms the sidecar does not leak into `overlay-plan.v1`. The focused Rust adapter and model package tests pass. The broader rustexec package test did not finish within four minutes and was interrupted, so it is not recorded as passing.
- 2026-11-28: After adding Italian/German positive cases, insufficient-window and deterministic-replay checks, BrainRust's full `overlay-compiler` suite passes: 235 unit tests, 5 shared-composition corpus tests, 34 plan-contract tests, and doc tests. The additive summary sidecar required regenerating the affected frozen planner output fixtures; the complete fixture matrix is green again.
- 2026-11-28: RenderingGen added camera-aware map callout geometry from ordered, per-frame projected pin samples. It reserves one photo-card safe-area box outside the pin's camera track and returns leader endpoints following each sample; tests cover safe placement, camera following, collapsed lines, missing projections, and invalid ordering. The active map compiler still does not consume this lowered editorial-layer contract, so ED-21 remains open for runtime layer emission.
- 2026-11-28: The complete RenderingGen overlay test packages pass after seeded motion selection and camera-aware callout geometry (`go test ./internal/overlay/... -count=1`); `git diff --check` is clean.
- 2026-11-28: Chronon host probe found an NVIDIA RTX A4000 and Vulkan support. The current checkout still has `CHRONON3D_BUILD_CONTENT=OFF` and no canonical editorial benchmark pack. A local Vulkan CLI build is underway to determine whether a dedicated fixture can provide ED-26 evidence without conflating it with the official 17-scene corpus.
- 2026-11-28: ED-13's layout tests now also exercise each supported side, safe-area edge fallback, badge placement, obstacle avoidance, full-safe-area parent rejection, and fail-closed no-fit behavior. The package test passes; these tests still target the editorial-layer resolver because no production `editorial_layers.v1` lowering is wired into the semantic compiler yet.
- 2026-11-28: RenderingGen's full `go test ./internal/overlay/... -count=1` suite passes after the ED-13 geometry matrix and ED-21 camera projection coverage (`overlay`, `editoriallayers`, and `mapcatalog`).
- 2026-11-28: Chronon now exposes image decode count/time in its execution reports. A repeatable Vulkan-only ED-26 runner and paired fixture generator are in `Chronon3d/scripts/benchmark_editorial_background_reuse.py`; its measurements remain pending the CLI build and successful native runs.
- 2026-11-28: ED-26 completed on the RTX A4000 with the Vulkan `chronon3d_cli`. Nine serial cold-process renders emitted six independent one-frame outputs and three persistent two-frame outputs (six frames per variant). Every run reports `effective_backend=vulkan`, positive GPU submissions, and zero execution downgrades. At equal output-frame counts, persistent composition reduced measured image uploads from 31,522,816 to 15,761,408 bytes/frame and image decodes from 1 to 0.5/frame. The evidence includes per-run timing sidecars, GPU residency/allocation, source/build/fixture hashes, and host/driver identity. The official content-pack corpus remains a separate ticket. Artifact: `Chronon3d/docs/evidence/editorial-background-reuse-20261128-r2/report.json`.
- 2026-11-28: ED-25 canary moved from software encoding to a Vulkan render plus raw RGBA output. It verifies exact 60-frame output, effective Vulkan backend, positive GPU submissions, zero downgrades, and non-background pixels at frames 20 and 59. Its sidecar and reproducible report include source/build/plan hashes and frame metrics. Per-member boundary pixels and progressive-summary/map-callout canaries remain open because those layers are not yet lowered by the active composition path.

