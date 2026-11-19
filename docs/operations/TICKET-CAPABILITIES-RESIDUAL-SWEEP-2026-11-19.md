# TICKET — Capabilities residual: stock pipeline, cliprender, images, animations and entities (2026-11-19)

- **Status:** OPEN (2026-11-19) — 65 unreachable functions across the capability packages not covered by the dedicated tickets
- **Severity:** P2 — dead constructors and validators in live packages
- **Owner:** capability owners by package
- **Source:** `deadcode -test=false ./...` on `refactored@17752ccfb` (445 unreachable functions); frozen list in [`deadcode-2026-11-19.txt`](deadcode-2026-11-19.txt)
- **Rule:** delete only after confirming no interface, reflection or build tag reaches the symbol; no test assertion weakened

## Problem

Largest groups: `assets/providers/stock/stockpipeline` (9, plus 3 in `publish`,
2 in `stockplan`, 1 in `enrichment`), `cliprender` (6), `images/entitycatalog`
(6, plus 4 in `images` and its contract alias), `animreg` (5), `entities` (5),
`mediamemory` (4), `youtube` (5), `translation` (3), `assets/artifacts` (2),
then single helpers in `audio`, `channelprofile`, `execution`, `jobs/worker`,
`linguistics`, `render` and `sourcing`, plus `internal/app/knobs` (2).

Two shapes recur: exported constructors that were replaced by a bundle builder
(`NewFinalizer`, `NewArgosTranslator`, `NewClipSamplerRegistry`) and validators
with no caller (`PartialDownloadPlan.Validate`, `ValidateVidRushSegmentAssetBindings`).
The constructors can go; a validator with no caller is either a missing check or
dead API, and each needs a one-line decision in this ticket.

## Scope (measured)

| File | # |
|---|---:|
| `internal/capabilities/assets/providers/stock/stockpipeline/naming.go` | 4 |
| `internal/capabilities/animreg/animation_registry.go` | 3 |
| `internal/capabilities/assets/providers/stock/stockpipeline/publish/publish.go` | 3 |
| `internal/capabilities/cliprender/render_cache.go` | 3 |
| `internal/capabilities/entities/type_aliases.go` | 3 |
| `internal/capabilities/images/entitycatalog/verified_asset_registry.go` | 3 |
| `internal/capabilities/mediamemory/types_enums.go` | 3 |
| `internal/app/knobs/registry.go` | 2 |
| `internal/capabilities/assets/artifacts/finalizer.go` | 2 |
| `internal/capabilities/assets/providers/stock/stockplan/youtube_highlights.go` | 2 |
| `internal/capabilities/images/entitycatalog/alias_graph.go` | 2 |
| `internal/capabilities/images/search_queries_fanout.go` | 2 |
| `internal/capabilities/translation/argos_translator.go` | 2 |
| `internal/capabilities/youtube/adapters/youtube_asset_mapper.go` | 2 |
| `internal/capabilities/youtube/metadata/quality_scoring.go` | 2 |
| `internal/capabilities/animreg/animation_registry_projection.go` | 1 |
| `internal/capabilities/animreg/catalog_digest.go` | 1 |
| `internal/capabilities/assets/providers/stock/enrichment/emitter.go` | 1 |
| `internal/capabilities/assets/providers/stock/stockpipeline/finalization_render.go` | 1 |
| `internal/capabilities/assets/providers/stock/stockpipeline/render_dto.go` | 1 |
| `internal/capabilities/assets/providers/stock/stockpipeline/service_constructors.go` | 1 |
| `internal/capabilities/assets/providers/stock/stockpipeline/step_publish.go` | 1 |
| `internal/capabilities/assets/providers/stock/stockpipeline/util.go` | 1 |
| `internal/capabilities/audio/strategy.go` | 1 |
| `internal/capabilities/channelprofile/registry.go` | 1 |
| `internal/capabilities/cliprender/async_worker.go` | 1 |
| `internal/capabilities/cliprender/continuation.go` | 1 |
| `internal/capabilities/cliprender/contract_ssot.go` | 1 |
| `internal/capabilities/entities/entity_media_resolver.go` | 1 |
| `internal/capabilities/entities/overlay_window.go` | 1 |
| `internal/capabilities/execution/steps/store.go` | 1 |
| `internal/capabilities/images/contract/resolver.go` | 1 |
| `internal/capabilities/images/entitycatalog/state_policy.go` | 1 |
| `internal/capabilities/images/image_contract_aliases.go` | 1 |
| `internal/capabilities/jobs/worker/runner.go` | 1 |
| `internal/capabilities/linguistics/lexicon_aliasing.go` | 1 |
| `internal/capabilities/linguistics/lexicon_registry.go` | 1 |
| `internal/capabilities/mediamemory/resolver_lookup.go` | 1 |
| `internal/capabilities/render/plan.go` | 1 |
| `internal/capabilities/sourcing/index_status.go` | 1 |
| `internal/capabilities/translation/ports.go` | 1 |
| `internal/capabilities/youtube/dto/tagutil_clean.go` | 1 |
| **Total** | **65** |

Key symbols: `stockpipeline` publish naming helpers, `NewFinalizer`, `PartialDownloadPlan.Validate`

## Plan

1. Sweep `assets/providers/stock` first (15 functions): the publish-naming
   helpers are the visible leftover of the stock-pipeline publish rewrite.
2. Then `cliprender` (6), `images`/`entitycatalog` (9) and `animreg` (5) with
   their package tests.
3. For each validator, record wire-or-delete; do not delete a validator that a
   production parse path should call without fixing that path.
4. One commit per package area; lower the guard per wave.

## Acceptance criteria

- [ ] 0 unreachable functions in the listed packages.
- [ ] Every removed validator has a recorded decision (wired elsewhere or dead).
- [ ] `go build ./...`, `go vet` on the touched packages and their tests are green.
- [ ] The global deadcode guard records the new count.

## Verification

```sh
deadcode -test=false ./... | grep -E 'capabilities/(assets/providers/stock|cliprender|images|animreg|entities|mediamemory|youtube|translation)'
go test ./internal/capabilities/... -count=1
```
