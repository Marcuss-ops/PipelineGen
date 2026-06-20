# `internal/domain/outbox` — Burned Down (this PR)

## Status

**done** — deleted in the PR that introduced the legacy-directory CI guard
(Check 13 in `scripts/ci-architectural-checks.sh`).

## What existed

A single file, `internal/domain/outbox/outbox.go`:

```go
package outbox

const EventAssetIndexRequested = "asset.index.requested"

type IndexRequestPayload struct {
    AssetID           string `json:"asset_id"`
    EmbeddingModel    string `json:"embedding_model"`
    EmbeddingVersion  string `json:"embedding_version"`
    CollectionVersion string `json:"collection_version"`
}
```

## Migration target

The canonical copy of these symbols lives at
`internal/infrastructure/database/sqlite/outboxevents/registry.go`:

```go
package outboxevents

const EventAssetIndexRequested = "asset.index.requested"
```

`IndexRequestPayload` is duplicated inline in
`internal/application/jobs/outbox/indexing.go` (decoder side); no consumer has
ever imported the struct from `domain/outbox`.

## Audit results

Audit method: `rg 'internal/domain/outbox' --type go` AND
`rg '"asset.index.requested"|EventAssetIndexRequested|IndexRequestPayload' --type go`.

| Probe | Result |
|---|---|
| Go files importing `internal/domain/outbox` | **0** (zero importers) |
| Imports of the duplicate package name `outbox` from this dir | 0 (consumers go through `outboxevents`) |
| String literal `"asset.index.requested"` outside this dir | 0 matches in code (only docstrings) |
| `EventAssetIndexRequested` referenced from outside the duplicate | only `outboxevents.EventAssetIndexRequested` — different package, canonical |
| `outboxevents` re-export for **typed** `IndexRequestPayload` | none — payloads are decoded into local structs in the handler, not the central type |

Net: deleting `internal/domain/outbox/outbox.go` does **not** break a single
Go type, function, or constant reference.

## Cut-over steps (this PR, executed)

1. Verified the audit results above (this doc).
2. Removed `internal/domain/outbox/outbox.go` (the only file).
3. Removed `internal/domain/outbox/` directory.
4. Updated `architecture/migration.yaml`, `architecture/ownership.yaml`,
   `scripts/archcheck/baseline.json` to remove the path.
5. Verified the legacy-directory CI guard (Check 13) does not flag the
   deletion — `git diff --diff-filter=A` only fires on additions.
6. Verified `go build ./...` and `go vet ./...` remain green.

## What to do if you find a stray reference in the future

If a future PR introduces a reference to `internal/domain/outbox` again,
the CI guard will fail with the message from Check 13. Move the symbol —
either inline a string, or use the canonical `outboxevents.EventAssetIndexRequested`.
