# QDRANT-003 — canonical reindex pipeline + schema-versioned collections

> Ticket: QDRANT-003 (June 2026) — replaces the legacy `reindex.go`
> (raw SQL + VectorAsset) with a canonical pipeline:
> `AssetStore → PayloadMapper → IndexWriter → CollectionManager →
> Client.SwitchAlias`. Implements `IndexSchema`, `IndexWriter`,
> `CollectionManager` and atomic alias swap so a reindex never wipes
> production.
> Owner: `internal/infrastructure/qdrant/` + `cmd/admin/reindex_qdrant.go`.

## STATO REALE (June 2026 closure)

- `IndexSchema`, `EmbeddingSpec`, `SparseSpec`, `PayloadIndexSpec` —
  canonical struct definitions in `internal/infrastructure/qdrant/types.go`.
- `DefaultV3Schema()` — single canonical V3 manifest referenced by
  `BuildAssetBundle` + `cmd/admin/reindex_qdrant.go`.
- `PayloadMapper` (`payload_mapper.go`) — converts `AssetData` →
  Qdrant `Point` with dimension + NaN validation; the only place
  vector names are configured.
- `IndexWriter` (`index_writer.go`) — implements `IndexWriterPort` +
  `QdrantDeleter`; `UpsertFromClips`, `ReindexAll`, `DeletePoints`,
  `ValidatePoint`.
- `CollectionManager` (`collection_manager.go`) — `EnsureSchema`
  (creates/updates the physical collection), `CompareActiveCollection`
  (schema diff), `SwitchAlias` (atomic), `RollbackAlias`, `Inspect`,
  `GetActiveCollection`.
- `cmd/admin/reindex_qdrant.go` — wires the pipeline, surfaces
  `ReindexResult` (counts + failed IDs) and the new
  `SwitchReport.Ready` gate (see QDRANT-004 doc).
- `ReindexResult` + `SwitchReport` — typed outputs for operator
  dashboards.
- `ErrSchemaIncompatible`, `ErrCollectionNotFound`,
  `ErrAliasNotFound`, `ErrAliasSwitchNotReady`,
  `ErrVectorDimensionMismatch`, `ErrNaNOrInf`, `ErrEmptyVector`,
  `ErrChannelUnavailable` — typed error surface so callers can
  branch on retry policy.

## LEGACY DA ELIMINARE

| Item | Where | Status |
|---|---|---|
| `cmd/admin/reindex.go` (legacy raw-SQL reindex) | pre-PR | **deleted** in QDRANT-003 |
| `VectorAsset` direct-upsert path (bypassing the mapper) | pre-PR `internal/infrastructure/...` | **deleted** |
| Operators directly mutating the alias via `POST /collections/aliases` without `CollectionManager` | runbooks | **prohibited** by doc — must come through the manager |
| Hard-coded channel names ("text", "visual", "audio", "bm25_text") outside the schema | QDRANT-003 follow-up | closed (channel names flow from `IndexSchema.DenseVectors` / `SparseVectors`, see `payload_mapper.go::getVectorForChannel`) |

## GATE ANTI-REGRESSIONE

```bash
# 1. The legacy reindex.go binary should not reappear.
ls cmd/admin/reindex.go  # → ENOENT

# 2. Direct alias POST outside CollectionManager should not reappear
#    (collections/aliases is reserved). The ONLY permitted callers
#    of `client.SwitchAlias` are inside CollectionManager methods.
grep -RIn "client.SwitchAlias\|cm.client.SwitchAlias\|client → SwitchAlias" \
  internal/infrastructure/qdrant/  # → only CollectionManager.SwitchAlias / RollbackAlias

# 3. The schema index pipeline compiles + the reindex command
#    recognises the gate.
go build ./cmd/admin/... ./internal/infrastructure/qdrant/...

# 4. The schema comparison test pins behaviour when expectations
#    diverge from the actual Qdrant collection.
go test ./internal/infrastructure/qdrant/... -run 'TestCompareSchema|TestIndexSchema_HasChannel|TestIndexSchema_GetDense|TestIndexSchema_PhysicalName|TestSchemaValidate'

# 5. Unit-level smoke (golden input file → expected ReindexResult
#    shape) is preserved by feeding a fixed DB and asserting
#    `IndexedAssets` matches a known-good count.

# 6. Dry-run path is honoured: --apply is the only mutation path.
go run ./cmd/admin reindex-qdrant --dry-run --limit=10 --json  # → JSON dump, no Qdrant writes
```

Any gate failure means the reindex pipeline is no longer the **single
canonical entry point** — operators must not bypass `CollectionManager`.
