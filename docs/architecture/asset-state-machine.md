# Asset State Machine

The `AssetState` enum in `internal/domain/asset` is the canonical, explicit
14-state machine for the asset journey from discovery to multilingual ready.
It is the single source of truth for the alphabet, the transition matrix,
and the helper predicates. No other package may declare an `AssetState`
enum or shadowed constant.

## The 14 canonical states

| Constant | Value | Meaning |
|----------|-------|---------|
| `StateAssetDiscovered` | `DISCOVERED` | Initial sentinel. |
| `StateAssetDownloaded` | `DOWNLOADED` | Bytes are local. |
| `StateAssetNormalized` | `NORMALIZED` | Canonical codec/container shape. |
| `StateAssetHashed` | `HASHED` | Content-hash computed. |
| `StateAssetUploaded` | `UPLOADED` | Asset is on remote storage. |
| `StateAssetTranscribed` | `TRANSCRIBED` | Original-language transcript present. |
| `StateAssetEnriched` | `ENRICHED` | Description + visual summary present. |
| `StateAssetTranslated` | `TRANSLATED` | All enabled languages have a track. |
| `StateAssetIndexPending` | `INDEX_PENDING` | Qdrant upsert enqueued. |
| `StateAssetIndexed` | `INDEXED` | Qdrant upsert confirmed. |
| `StateAssetReady` | `READY` | Single-language readiness gate passes. |
| `StateAssetReadyMultilingual` | `READY_MULTILINGUAL` | Multilingual readiness gate passes. |
| `StateAssetFailedRetryable` | `FAILED_RETRYABLE` | Semi-terminal; operator re-entry allowed. |
| `StateAssetFailedPermanent` | `FAILED_PERMANENT` | True terminal failure. |

## Transition matrix

- **Happy path** (11 forward edges):
  `DISCOVERED → DOWNLOADED → NORMALIZED → HASHED → UPLOADED → TRANSCRIBED →
   ENRICHED → TRANSLATED → INDEX_PENDING → INDEXED → READY → READY_MULTILINGUAL`.
- **Degradation**: `READY_MULTILINGUAL → READY`.
- **Failure exits**: any pre-terminal state → `FAILED_RETRYABLE` or `FAILED_PERMANENT`.
- **Retry re-entry**: `FAILED_RETRYABLE → any pre-terminal state`.
- **FAILED_PERMANENT** has zero out-edges.
- Self-loops are idempotent.
- Unknown source/target values are rejected.

## Relationship to other state machines

`AssetState` is orthogonal to the existing layered state machines:

- `LifecycleState` (`lifecycle_state.go`) tracks deletion/online semantics.
- `PipelineState` (`pipeline_state.go`) is an append-only per-item event log.
- `IndexState` (`index_state.go`) is the indexer's narrow progress view.

## Fail-closed contract

An uninitialised `AssetState` (`""`) must NOT pass any `IsValidTransition`
check, including the zero-value self-loop.

## Historical notes

- Introduced in **PR-CATALOG-MULTILINGUA step 7** (July 2026) as the
  canonical 14-state asset journey.
- **Migration 157** (`migrations/sqlite/157_asset_state.sql`) added the
  `media_assets.asset_state` column with a `DEFAULT 'DISCOVERED'` literal.
  The SQL default must stay in lockstep with
  `string(asset.StateAssetDiscovered)`.
- The state machine originally lived in a single `asset_state.go` file. It
  was later split into focused files (`asset_state.go`,
  `asset_state_values.go`, `asset_state_predicates.go`,
  `asset_state_transitions.go`) while keeping the canonical 14-value owner
  in `asset_state_values.go`.

## Future work

- Wire `SetAssetStateTx` in the asset repository.
- Replace some `PipelineState` readers in the operator dashboard with
  `AssetState` readers.
