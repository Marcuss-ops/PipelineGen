# Qdrant removal map — 2026-11-16

**Scope.** This document maps the Qdrant surface in `refactored/` after the
2026-11-16 audit. It records the one boundary that must survive
(`mediamemory`), the packages that must go, the import edges that block each
deletion, the order in which they can be cut, and the gate for every step. It
does not claim that Qdrant is removed: the removal is staged work.

**Rule.** No package is deleted while a consumer still imports it, and no
consumer is cut without its replacement feature decision. The Qdrant adapter is
infrastructure behind a capability port; the composition root is the only site
allowed to instantiate it.

## 1. Snapshot

`internal/platform/qdrant` is **130 Go files**: 83 production files
(13,140 LOC) and 47 test files (11,219 LOC).

| Package | Prod files | Prod LOC | Test files | Test LOC | Verdict |
|---|---:|---:|---:|---:|---|
| `qdrant` (root runtime) | 1 | 256 | 0 | 0 | remove |
| `collections` | 11 | 1,610 | 9 | 1,931 | remove |
| `dr` | 6 | 698 | 2 | 568 | keep (type dependency of `schema`) |
| `health` | 1 | 136 | 0 | 0 | remove |
| `indexing` | 23 | 3,761 | 14 | 3,395 | keep (ProjectionWriter used by the frame indexer) |
| `indexing/clipindexer` | 6 | 563 | 1 | 127 | remove |
| `indexing/mediamemory` | 1 | 141 | 1 | 309 | **legitimate** |
| `schema` | 11 | 1,867 | 5 | 524 | keep (frame collection contract) |
| `search` | 4 | 1,062 | 6 | 1,717 | remove |
| `transport` | 12 | 2,047 | 4 | 1,497 | keep (HTTP client used by the frame indexer) |
| `verification` | 7 | 999 | 5 | 1,151 | remove |

## 2. The one legitimate surface: `mediamemory`

- **Port:** `internal/capabilities/mediamemory/ports.go:124`
  (`KeyframeVisualIndexer`). The capability package has **zero** imports of
  `internal/platform/qdrant/**`.
- **Adapter:** `internal/platform/qdrant/indexing/mediamemory` — one production
  file (141 LOC) implementing the port against the
  `pipelinegen_media_frames` collection.
- **Only wiring site:** `internal/app/wiring/build_bundles_domain_assets.go:179-181`.
- **Compiled closure of the adapter:** `indexing`, `schema`, `transport`, `dr`.
  Nothing else in `internal/platform/qdrant` is reachable from it.
- **Guard:** `internal/capabilities/mediamemory/qdrant_isolation_test.go` fails
  if the capability ever imports the infrastructure directly, or if the scan
  becomes vacuous.

**Known coupling left behind.** The adapter is constructed with
`params.process.QdrantClient` (see `internal/app/wiring/process/types.go:27`),
the same client shared with clip indexing and search. Step P1 below gives the
frame indexer its own client construction so the shared process client can
disappear with the rest.

## 3. What must be removed, and who blocks it

| Target | Production LOC | External importers (blockers) |
|---|---:|---|
| `indexing/clipindexer` | 563 | `internal/capabilities/youtube/adapters/constructors.go`, `.../youtube_adapters_exec.go`; `internal/app/wiring/assets_core.go`, `build_bundles_voiceover.go`, `build_process_qdrant.go`, `process/types.go`, `stock/types.go`, `wire_assets_clips.go` |
| `search` | 1,062 | `internal/capabilities/ai/autotag/autotag.go` (image embedder); `internal/app/wiring/adapters_infra_embedding.go`, `registry_internal_modules.go`, `search/adapters_qdrant_transport_health.go`, `wire_services_orchestration.go`, `process/types.go`; `internal/platform/qdrant/runtime.go` |
| `collections` | 1,610 | `cmd/admin/reconcile/dr_qdrant.go`, `dr_qdrant_mark_failed_cleaned.go`; `internal/capabilities/maintenance/dr_adapter.go`; `internal/app/wiring/process/types.go`, `search/adapters_qdrant_transport_health.go`, `wire_services_orchestration.go`; `internal/platform/qdrant/runtime.go` |
| `verification` | 999 | `internal/app/wiring/registry_operator_admin_api.go`; `internal/capabilities/maintenance/dr_adapter.go`; `internal/platform/qdrant/runtime.go`, `collections/**` |
| `health` | 136 | `internal/app/wiring/build_bundles_core.go`, `process/types.go`, `search/adapters_qdrant_transport_health.go`; `internal/platform/qdrant/runtime.go` |
| `qdrant` root runtime | 256 | `internal/app/wiring/build_process_qdrant.go`, `process/types.go` and its tests |
| `dr` | 698 | `cmd/admin/reconcile/dr_qdrant.go`; `internal/capabilities/maintenance/dr_adapter.go`; `internal/platform/qdrant/schema/**`, `collections/**` |

Additional surfaces to retire with the code:

- `config.yaml`, `config.example.yaml`, `config.production.example.yaml`
  (`ClipIndexer`, Qdrant settings).
- `docker-compose.test-qdrant.yml`.
- `cmd/archcheck/scan/boundaries/percheck_qdrant_index_import_ban.go` — the ban
  stays in force until the last consumer is gone, then it is retired with the
  package it guards.
- `tests/fixtures/synthetic_assets_test.go` imports `qdrant/schema`; it follows
  `schema`, not the removal target.

## 4. Removal order

| Step | Work | Exit gate |
|---|---|---|
| **P0 — freeze (done 2026-11-16)** | Guard test pinning `capabilities/mediamemory` to the port; map committed with this document | `go test ./internal/capabilities/mediamemory` + `go build ./...` |
| **P1 — own the client (DONE 2026-11-17)** | Frame indexer builds its own client via `NewFrameQdrantIndexerFromConfig` in the adapter package; composition no longer passes `params.process.QdrantClient` | frame indexer + wiring tests, `go build ./...` |
| **P2 — cut clip indexing (DONE 2026-11-17)** | `indexing/clipindexer` deleted; its imperative surface is now `capabilities/indexing.ReindexRequesterHolder` delegating to `pgmedia.ReindexRequester` | `go build ./...`, YouTube + wiring tests, archcheck (no hard hits) |
| **P3 — cut image search** | Decide the product fate of the CLIP image embedder in `autotag`; remove `search` imports and `mediasearch_readiness` Qdrant checks | `go build ./...`, autotag tests, `/health` readiness contract |
| **P4 — cut operator DR** | Remove Qdrant reconcile commands (`cmd/admin/reconcile/dr_qdrant*`) and `maintenance` locator-cleaner Qdrant paths | `go build ./...`, admin tests |
| **P5 — delete the shell** | Delete `collections`, `verification`, `health`, `search`, root `qdrant`; shrink `transport`/`schema`/`dr` to the frame-indexer subset | `go build ./...`, full unit suite, archcheck |
| **P6 — retire the surface** | Drop `docker-compose.test-qdrant.yml`, Qdrant config blocks, unused deps; retire the archcheck ban | `make verify-main` equivalent on a quiescent tree |

## 5. Risks and decisions that block the cut

1. **`autotag` visual search is a product feature.** Removing `search` removes
   the CLIP-server image embedder; it needs a replacement or an explicit
   retirement.
2. **`mediasearch_readiness` health.** `/health` currently reports the Qdrant
   transport; P3 must keep a truthful readiness signal without Qdrant.
3. **Operator tooling.** `cmd/admin/reconcile` and `maintenance` are the only
   remaining repair paths; deleting them without a Postgres replacement leaves
   operators without a locator repair tool.
4. **`dr` is a type dependency of `schema`.** P5 can shrink it, but `schema`
   cannot be deleted while the frame indexer exists.
5. **Concurrent edits.** On 2026-11-16 another agent was editing
   `internal/capabilities/scripts`; the Qdrant packages were untouched, but the
   full-suite gate must be run on a quiescent tree.

## 6. Non-goals of this change

- No package under `internal/platform/qdrant` is deleted here.
- No consumer is cut here.
- The guard and the map only make the boundary explicit and the removal
  mechanical.
