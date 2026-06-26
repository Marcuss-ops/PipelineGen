# QDRANT-001 — tenant isolation & canonical search-result shape

> Ticket: QDRANT-001 (June 2026) — workspace_id SQL hydration + LocalPath /
> DriveLink removal from the canonical Qdrant search-result struct.
> Owner: `internal/infrastructure/qdrant/` + `internal/app/registry_adapters.go`.

## STATO REALE (June 2026 closure)

| Layer | State | Code site |
|---|---|---|
| workspace_id SQL filter | **live** in `ClipsRepository.List` | `internal/infrastructure/database/sqlite/assets/clips_repository.go::List` |
| Filter contract gain | `WorkspaceID string` + `IsAdmin bool` added to `asset.Filter` | `internal/domain/asset/types_aux.go` |
| Adapter wiring | `mediasearchReadAdapter.GetMany` passes `WorkspaceID` + `IsAdmin` from `WorkspaceContext` | `internal/app/registry_adapters.go` |
| Canonical `SearchResult` shape | `LocalPath` + `DriveLink` JSON fields **removed**; payload map keeps `asset_id`, `source`, `media_type`, `category`, `tags`, `search_text`, etc. | `internal/infrastructure/qdrant/types.go` |
| Payload emission | `BuildPayload` no longer writes `drive_link` (server-internal locator) | `internal/infrastructure/qdrant/payload_mapper.go::BuildPayload` |
| Decoder | `searchResultToVectorSearchResult` no longer reads `payload["local_path"|"drive_link"]` | `internal/infrastructure/qdrant/search_adapter.go` |
| `TODO QDRANT-001` comments | **all removed** | n/a (rg-clean below) |

The `appsearch.VectorSearchResult` (application-layer port DTO) keeps
`LocalPath` + `DriveLink` as `omitempty` legacy fields. After this closure
they are no longer populated by the search adapter; downstream code reading
`appsearch.VectorSearchResult.LocalPath` will receive an empty string. A
future PR may deprecate those fields at the application boundary.

## LEGACY DA ELIMINARE

| Item | Where | Followup |
|---|---|---|
| Stale `drive_link` keys in legacy Qdrant points (pre-closure upserts) | Qdrant cluster | Background reconcile via `payload_key_drop` in a QDRANT-005 follow-up. The leakage path is already closed because the decoder stops reading the key. |
| Application-layer `appsearch.VectorSearchResult.LocalPath` / `.DriveLink` | `internal/application/assets/search/ports.go` | Out of scope for QDRANT-001; the user-facing DTO change belongs to a follow-up deprecation PR. |
| `delivery.Signer.BuildAuthorizedURL` callers that historically read `LocalPath` for raw-id URL building | `internal/infrastructure/delivery/signer.go` | Already migrated to `assetID` + `workspaceID` only. |
| Hard-coded `payload["local_path"]` reads anywhere in the codebase | `internal/infrastructure/qdrant/` files | Closed in this PR (search_adapter.go). Any future reader must use `asset_id` + signed delivery URLs. |

## GATE ANTI-REGRESSIONE

A reviewer or CI gate runs the following checks; **any failure blocks** the
back-port of the closure.

```bash
# 1. The TODO comment marker must be absent (QDRANT-001's "filter when the
#    column lands" promise is now satisfied).
grep -RIn "TODO QDRANT-001" internal/ cmd/ docs/  # → 0 hits

# 2. LocalPath / DriveLink must not reappear in the canonical qdrant
#    SearchResult struct, the BuildPayload output, or the search adapter.
grep -RIn "LocalPath\|DriveLink" \
  internal/infrastructure/qdrant/types.go \
  internal/infrastructure/qdrant/payload_mapper.go \
  internal/infrastructure/qdrant/search_adapter.go
# → 0 hits in those three files

# 3. BuildPayload must not emit a "local_path" or "drive_link" key.
grep -n "payload\[\"local_path\"\]" internal/infrastructure/qdrant/payload_mapper.go  # → 0 hits
grep -n "payload\[\"drive_link\"\]" internal/infrastructure/qdrant/payload_mapper.go  # → 0 hits

# 4. Compiles clean.
go build ./internal/infrastructure/qdrant/... ./internal/app/... ./internal/infrastructure/database/sqlite/...  # → exit 0

# 5. Service-layer stays auth-only: SQL filter is in the repo, but the
#    application layer still rejects empty / "default" workspaces.
go test ./internal/application/mediasearch/...  # → ErrMissingWorkspace coverage intact
```

If any gate fails, the regression must be either re-fixed or explicitly
documented in a follow-up ticket with a deadline; status quo is that
**QDRANT-001 is closed** and any regression is a leak.
