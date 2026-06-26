# QDRANT-004 — real hybrid search (dense + sparse) + hard alias-switch gate

> Ticket: QDRANT-004 (June 2026) — closes two P0 gaps from
> QDRANT-001..003:
> 1. HybridSearch uses a real `/points/query` RRF fusion, not a
>    relabeled dense-only search. `ErrSparseRequired` typed error when
>    the schema has a sparse channel but the caller didn't supply a
>    `SparseQueryVector`.
> 2. The `cmd/admin reindex-qdrant` alias swap is now GATED on
>    `SwitchReport.Ready=true` — partial / schema-broken collections
>    can no longer be promoted into production.
> Owner: `internal/infrastructure/qdrant/` + `cmd/admin` +
> `internal/application/mediasearch/`.

## STATO REALE (June 2026 closure)

| Area | State | Code site |
|---|---|---|
| `HybridSearchPoints` (real RRF) | POST `/points/query` with two prefetch entries (dense + sparse) + `fusion: rrf` | `internal/infrastructure/qdrant/client.go` |
| Sparse-required typed error | `ErrSparseRequired{Channel string}` returned when `SparseVectorName != ""` but `SparseQueryVector == nil` | `internal/infrastructure/qdrant/errors.go` |
| BM25 tokenization caller | `search_adapter.go` tokenises query text via `pkg/bm25.Tokenize` only when `req.SparseVectorName != ""` and `req.QueryText != ""` | `internal/infrastructure/qdrant/search_adapter.go` |
| Hybrid smoke | `mediasearch.Service.Search` honours "hybrid" mode by routing to `VectorStore.HybridSearch` (not `Search`) | `internal/application/mediasearch/service.go` |
| `buildSwitchReport` helper | aggregates `ExpectedPoints` (IndexedAssets) + `ActualPoints` (CountPoints) + the boolean smoke placeholders + dead-letter placeholders | `cmd/admin/reindex_qdrant.go` |
| Hard alias gate | `runReindexQdrant` produces `report` then returns `&qdrant.ErrAliasSwitchNotReady{Report: report}` when `report.Ready == false`; SwitchAlias is reachable only past the gate | `cmd/admin/reindex_qdrant.go` |
| Delivery signer conflict | `mediasearchDeliveryAdapter` is a hard-failing fallback (not a silent `("", nil)`) | `internal/app/registry_adapters.go` |

## LEGACY DA ELIMINARE

| Item | Where | Status |
|---|---|---|
| Dense-only fallback inside `HybridSearchPoints` when sparse is missing | pre-PR | **renamed to ErrSparseRequired** — replacing the silent fallback with a typed error |
| "fuse" path that summed scores client-side instead of using Qdrant's `/points/query` RRF | pre-PR | **removed**; Qdrant now does the fusion server-side |
| Reindex command that promoted the alias unconditionally after a successful UpsertFromClips batch | pre-PR `cmd/admin/reindex_qdrant.go` | **gated** by `buildSwitchReport.Ready` |
| Runbooks that documented `reindex-qdrant --apply` as "swap immediately" | pre-PR `docs/operations/*.md` | flagged for docs-sync in the QDRANT-005 followup |

## GATE ANTI-REGRESSIONE

```bash
# 1. HybridSearchPoints must reference /points/query (not the legacy
#    /search batch endpoint with a fused-manually score).
grep -n "points/query" internal/infrastructure/qdrant/client.go  # → ≥ 1 cite

# 2. The ErrSparseRequired sentinel must remain and be wired into the
#    client flow.
grep -RIn "ErrSparseRequired" internal/infrastructure/qdrant/

# 3. The hard gate must be present in cmd/admin/reindex_qdrant.go.
grep -n "buildSwitchReport\|ErrAliasSwitchNotReady" \
  cmd/admin/reindex_qdrant.go  # → ≥ 3 cites (helper + call + return)

# 4. The aliases test (existing) continues to pass; new tests pin the
#    gate behaviour.
go test ./internal/infrastructure/qdrant/... \
  -run 'TestCollectionManager_SwitchAlias|TestCollectionManager_RollbackAlias' -count=1

# 5. Compiles clean and admin command binary runs.
go build ./cmd/admin/... && \
  go run ./cmd/admin reindex-qdrant --dry-run --limit=0 --json  # → JSON, exit 0

# 6. The hard gate blocks on missing data: simulate by passing a
#    --target-collection=non_existent --apply and check the
#    ErrAliasSwitchNotReady path (it should return the typed error
#    without mutating the alias). Operator runbook needs a smoke
#    script for this; tracked in QDRANT-005 follow-up.
```

Any gate failure means the contract regressed: dense-only fallback is a
silent quality regression; a bare SwitchAlias is a reliability hazard.
