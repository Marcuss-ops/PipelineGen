# TICKET — Scripts dead chains: overlay, VidRush adapters, runner and final job (2026-11-19)

- **Status:** OPEN (2026-11-19) — 107 unreachable functions in `internal/capabilities/scripts` across three call chains
- **Severity:** P2 — the largest residual block; several are oracles that tests still use
- **Owner:** scripts pipeline maintainers
- **Source:** `deadcode -test=false ./...` on `refactored@17752ccfb` (445 unreachable functions); frozen list in [`deadcode-2026-11-19.txt`](deadcode-2026-11-19.txt)
- **Rule:** delete only after confirming no interface, reflection or build tag reaches the symbol; no test assertion weakened

## Problem

The scripts capability carries 107 of the 445 residual functions, in three
separately-owned chains:

- **Overlay chain (33):** `overlay_canvas.go` (11 ceiling/group helpers),
  `overlay_entity_cards_text.go` (7 date/text scoring helpers),
  `overlay_category_filter.go` (5), `overlay_entity_cards.go` (4),
  `overlay_plan.go` (3), `overlay_numeric_grounding.go` (2),
  `overlay_scene_images.go` (1).
- **VidRush adapters (40):** `adapters/scene_annotations.go` (8) and the numeric
  sibling (2), fanout/materialization types (8), processor adapters (4), then
  `usecase`, `ports`, `generation`, `geography`, `scene` and `runner` helpers.
- **Runner / final job / render queue (34):** `result_reducer.go` (4),
  `audio_timeline.go` (3), `final_job_*` (6), `render_queue_*` (5), `runner_*`
  (7), plus the translated-NLP and lineage helpers.

Some of these are oracles the scripts tests assert through (for example the
entity-card date scoring). Deleting them without moving the assertion removes
coverage; the rule is: an oracle moves to `_test.go` with a comment naming what
it mirrors, everything else is deleted with the feature.

## Scope (measured)

| File | # |
|---|---:|
| `internal/capabilities/scripts/overlay_canvas.go` | 11 |
| `internal/capabilities/scripts/overlay_entity_cards_text.go` | 7 |
| `internal/capabilities/scripts/overlay_category_filter.go` | 5 |
| `internal/capabilities/scripts/overlay_entity_cards.go` | 4 |
| `internal/capabilities/scripts/overlay_plan.go` | 3 |
| `internal/capabilities/scripts/overlay_numeric_grounding.go` | 2 |
| `internal/capabilities/scripts/overlay_scene_images.go` | 1 |
| `internal/capabilities/scripts/adapters/scene_annotations.go` | 8 |
| `internal/capabilities/scripts/adapters/vidrush_fanout_types.go` | 3 |
| `internal/capabilities/scripts/ports/video_source_discovery.go` | 3 |
| `internal/capabilities/scripts/adapters/media_resolver_entity_binding.go` | 2 |
| `internal/capabilities/scripts/adapters/processor/typed_fail_adapters.go` | 2 |
| `internal/capabilities/scripts/adapters/scene_annotations_numeric.go` | 2 |
| `internal/capabilities/scripts/adapters/vidrush_asset_surface.go` | 2 |
| `internal/capabilities/scripts/adapters/entity_image_filter.go` | 1 |
| `internal/capabilities/scripts/adapters/processor/gemmamemory.go` | 1 |
| `internal/capabilities/scripts/adapters/processor_names.go` | 1 |
| `internal/capabilities/scripts/adapters/processor_vidrush_materialization.go` | 1 |
| `internal/capabilities/scripts/adapters/vidrush_fanout_plan.go` | 1 |
| `internal/capabilities/scripts/adapters/vidrush_helpers.go` | 1 |
| `internal/capabilities/scripts/adapters/vidrush_image_validation.go` | 1 |
| `internal/capabilities/scripts/generation/plan_builder.go` | 1 |
| `internal/capabilities/scripts/geography/enrichment.go` | 1 |
| `internal/capabilities/scripts/ports/research.go` | 1 |
| `internal/capabilities/scripts/runner/backpressure.go` | 1 |
| `internal/capabilities/scripts/runner/dispatch.go` | 1 |
| `internal/capabilities/scripts/scene/synthesizer.go` | 1 |
| `internal/capabilities/scripts/usecase/clip_sampler_impl.go` | 1 |
| `internal/capabilities/scripts/usecase/clip_sampler_registry.go` | 1 |
| `internal/capabilities/scripts/usecase/gencore/persistence.go` | 1 |
| `internal/capabilities/scripts/usecase/research_subject_filter.go` | 1 |
| `internal/capabilities/scripts/usecase/source_research_validate.go` | 1 |
| `internal/capabilities/scripts/result_reducer.go` | 4 |
| `internal/capabilities/scripts/audio_timeline.go` | 3 |
| `internal/capabilities/scripts/final_job_payload.go` | 2 |
| `internal/capabilities/scripts/render_queue_map_cache.go` | 2 |
| `internal/capabilities/scripts/render_queue_submit.go` | 2 |
| `internal/capabilities/scripts/runner_dispatch.go` | 2 |
| `internal/capabilities/scripts/runner_execution_pipeline.go` | 2 |
| `internal/capabilities/scripts/runner_render_units.go` | 2 |
| `internal/capabilities/scripts/translated_nlp.go` | 2 |
| `internal/capabilities/scripts/entity_projection.go` | 1 |
| `internal/capabilities/scripts/execution_lineage.go` | 1 |
| `internal/capabilities/scripts/final_job_compiled_plan.go` | 1 |
| `internal/capabilities/scripts/final_job_image_preflight.go` | 1 |
| `internal/capabilities/scripts/final_job_segment_identity.go` | 1 |
| `internal/capabilities/scripts/final_job_source.go` | 1 |
| `internal/capabilities/scripts/incremental_coordinator.go` | 1 |
| `internal/capabilities/scripts/model_result.go` | 1 |
| `internal/capabilities/scripts/model_run.go` | 1 |
| `internal/capabilities/scripts/render_attempt_analytics.go` | 1 |
| `internal/capabilities/scripts/render_queue_completion.go` | 1 |
| `internal/capabilities/scripts/runner_deps.go` | 1 |
| `internal/capabilities/scripts/semantic_production_plan.go` | 1 |
| **Total** | **107** |

## Plan

1. Wave A, overlay chain: confirm no production caller for each helper, move
   the scoring/date oracles used by tests into `_test.go`, delete the rest.
2. Wave B, VidRush adapters: check whether the fanout/materialization processors
   are a retired sourcing path; if so delete the family with its tests, otherwise
   wire the entry point.
3. Wave C, runner/final job: delete helpers whose only callers were removed in
   the Rust cutover; keep `BuildFinalJobPayloads` unless a caller is proven
   absent on all entry points.
4. One commit per wave, each with `go build`, `go vet` and the package tests.

## Acceptance criteria

- [ ] 0 unreachable functions under `internal/capabilities/scripts`.
- [ ] Every deleted oracle has its assertion either migrated to `_test.go` or
      removed together with the behavior it covered.
- [ ] `go build ./...`, `go vet ./internal/capabilities/scripts/...` and
      `go test ./internal/capabilities/scripts/... -count=1` are green after
      each wave.
- [ ] The global deadcode guard records the new count per wave.

## Verification

```sh
deadcode -test=false ./... | grep '^internal/capabilities/scripts/'
go test ./internal/capabilities/scripts/... -count=1
```
