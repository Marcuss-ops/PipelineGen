# Data and Configuration Ownership

> **Status**: **canonical** (promoted June 2026 from godlike/ design-state).
> This document is the single source of truth for PipelineGen's
> data and configuration ownership axis (database, Qdrant projection,
> filesystem/Drive, configuration boot pipeline, future storage
> changes). It supersedes overlapping data/configuration rules previously
> restated in `AGENTS.md` (`Instructions`, `Qdrant Entity Associations`,
> `Pattern 2`) and `ARCHITECTURE.md` (§6 Persistence, §9 Configuration).
>
> **Authority carve-out**:
> - **this doc wins** for *data and configuration ownership* (DB driver
>   lock, FTS5 ban, schema boundaries, table capability ownership,
>   Qdrant projection sequence, Drive authority, configuration boot
>   pipeline, EXPAND/BACKFILL/CUTOVER/CONTRACT).
> - **`AGENTS.md` wins** for agent-facing rules (AI generation policy,
>   admin token, agent instructions).
> - **`ARCHITECTURE.md` wins** for the structural diagram axis
>   (module ownership table, data flow journeys, target-tree phases).
>
> If sources disagree, fix the code; the loader will tell you which
> rule was violated.

## Durable authority

The primary SQLite database is the authority for durable PipelineGen metadata. Qdrant, Drive metadata, local files, caches, and generated documents are derived views or delivery targets.

## One owner per fact

- Asset identity belongs to the asset domain.
- Asset locations belong to the location model.
- Processing status belongs to the processing model.
- Jobs and leases belong to the job domain.
- Workflow progress belongs to the workflow domain.
- Artifacts belong to the artifact model.
- Delivery state belongs to the delivery domain.
- Outbox records belong to the outbox domain.

The same fact must not have multiple independent writers.

*(Per-package enforcement of these facts lives in [`architecture/ownership.yaml`](../../architecture/ownership.yaml).)*

## Database rules

- New SQLite migrations live under `migrations/sqlite/`.
- Released migrations are not edited.
- There is one migration ledger and one runner.
- Every table has one owning capability.
- HTTP handlers do not contain SQL.
- Application ports do not expose raw database handles.
- Cross-capability access uses typed ports, explicit read models, or events.
- The driver must remain **`mattn/go-sqlite3`**. Pure-Go alternatives are
  forbidden — see `internal/infrastructure/database/storage.OpenSQLiteDB()`
  and the `go.mod` module path `github.com/mattn/go-sqlite3`.
- **FTS5 is strictly banned** (its compilation flag depends on the
  driver build; do not depend on it). For full-text use
  `pkg/sqlutil.BuildFallbackLikeConditions` / `BuildFallbackLikeConditionsOR`.
- Connections opened through `internal/infrastructure/database/storage`
  MUST enforce the canonical PRAGMAs `journal_mode=WAL`,
  `busy_timeout=5000`, `synchronous=NORMAL` — the runner owns the
  connection string and applies the PRAGMAs before any capability
  reaches for a handle.

## Qdrant projection

SQLite owns identity, lifecycle, source facts, versions, metadata, and durable locations. Qdrant owns the searchable vector projection.

The canonical sequence is:

1. commit metadata in SQLite;
2. persist an outbox record in the same transaction;
3. update Qdrant asynchronously and idempotently;
4. track projection version and outcome;
5. allow a complete rebuild from SQLite.

## Drive and filesystem

Drive and local files represent locations, not identity. Upload, move, rename, and removal operations pass through one location or delivery service.

## Configuration

Configuration follows one boot pipeline:

```text
input -> load -> defaults -> validate -> immutable configuration
```

Defaults and validation are defined once. Business services receive narrow capability configuration. Runtime mutation and duplicated fallback values are forbidden.

The current loader lives at `internal/platform/config/config.go::Get()`
(target-tree Phase 2, June 2026). `(*Config).Validate()` is **not**
invoked from inside `Get()`; the composition root must call it
explicitly before any capability boots. After `Validate()` returns
`nil`, the configuration is treated as read-only — runtime mutation
trips the rule above.

## Future storage changes

Any later storage-engine migration uses the existing repository boundaries and the sequence EXPAND, BACKFILL, CUTOVER, CONTRACT. It must not introduce permanent dual-write or dual-read paths.