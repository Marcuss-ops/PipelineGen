# PR-05: Generic Outbox Events

> **Status**: Draft. Pull from main before implementing.

## Goal

Consolidate `media_index_outbox` (currently hard-coded for Qdrant indexing) into a
**generic `outbox_events` table** that hosts **all** asynchronous side-effects
triggered by business-state writes — Qdrant indexing today, plus webhooks,
notifications, Drive sync, video render triggers, etc. tomorrow.

After this PR, adding a new side-effect = registering a handler; **no schema
migration required**.

## Why now

Today the outbox is a one-trick pony. The next external dependency we wire
(webhook delivery, in-app notifications, retryable Drive upload) faces a
choice: clone `media_index_outbox` for each, or generalize. Cloning costs
schema sprawl; generalizing is cheaper. This PR takes the latter path.

The transactional outbox pattern is well-understood; the migration is the
expensive part. We do it once now.

## Scope

### In scope
- New generic `outbox_events` schema (migration 037)
- One-shot data migration from `media_index_outbox` (migration 038)
- Read-shim layer so existing queries still find Qdrant-index rows during
  the dual-read window (migration 039)
- New canonical package `internal/outbox/` (split from
  `internal/repository/outbox/`)
- `Dispatcher.Dispatch(event)` — generic tx-aware enqueue
- `Worker` with `HandlerRegistry` — generic worker that routes by
  `event_type`
- One concrete handler (`qdrant_index`) that wraps the existing
  `clipindexer.IndexClip` call
- Tests: schema migration round-trip, dispatcher tx-atomicity, worker
  routing, handler idempotency

### Out of scope
- Postgres adapter for `outbox_events` (Phase 2 — but the schema will be
  portable)
- Webhook / notification handlers (separate PRs that consume the new API)
- Operator UI for replay / inspection
- Cross-aggregate transactional events (one outbox row per business
  write — not a saga orchestrator)

## Design

### New schema (migration 037)

```sql
CREATE TABLE outbox_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id        TEXT    NOT NULL,            -- idempotency key (caller-provided ULID/UUID)
    event_type      TEXT    NOT NULL,            -- e.g. 'asset.index_qdrant', 'webhook.dispatch'
    aggregate_type  TEXT    NOT NULL,            -- e.g. 'media_asset', 'script', 'video'
    aggregate_id    TEXT    NOT NULL,            -- the asset_id / script_id / video_id
    payload_json    TEXT    NOT NULL DEFAULT '{}',
    occurred_at     TEXT    NOT NULL DEFAULT (datetime('now')),
    status          TEXT    NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','in_flight','processed','failed','dead_letter')),
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 5,
    last_error      TEXT    NOT NULL DEFAULT '',
    next_attempt_at TEXT    NOT NULL DEFAULT (datetime('now')),
    locked_by       TEXT,                         -- worker_id that claimed
    locked_until    TEXT,                         -- lease expiry (5 min)
    created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT    NOT NULL DEFAULT (datetime('now')),

    UNIQUE (event_id)                            -- producer-side dedup
);

CREATE INDEX idx_outbox_pending
    ON outbox_events(status, next_attempt_at) WHERE status = 'pending';
CREATE INDEX idx_outbox_aggregate
    ON outbox_events(aggregate_type, aggregate_id);
CREATE INDEX idx_outbox_event_type_status
    ON outbox_events(event_type, status);
```

**Why `event_id TEXT UNIQUE`**: producers can supply a deterministic id
(content-hash of aggregate_id + payload) so a duplicate write from a
retry is no-op. The current `media_index_outbox` has
`UNIQUE (asset_id, content_hash, embedding_model, embedding_version,
collection_version)` — moving that to `event_id = hash(asset_id +
content_hash + embedding_model + embedding_version + collection_version)`
gives the same guarantee with one fewer index.

**Why `aggregate_type + aggregate_id`**: enables cross-side-effect queries
("show me every event for this asset") without a join, and lets the worker
keep `payload_json` compact (no need to repeat `aggregate_id` inside).

### Data migration (migration 038)

```sql
INSERT INTO outbox_events
    (event_id, event_type, aggregate_type, aggregate_id,
     payload_json, status, attempt_count, max_attempts,
     next_attempt_at, created_at, updated_at)
SELECT
    'sha256:' || lower(hex(substr(asset_id, 1)) || ':' ||
                       content_hash || ':' || embedding_model || ':' ||
                       embedding_version || ':' || collection_version),
    'asset.index_qdrant',
    'media_asset',
    asset_id,
    json_object(
        'content_hash',       content_hash,
        'embedding_model',    embedding_model,
        'embedding_version',  embedding_version,
        'collection_version', collection_version,
        'asset_id',           asset_id
    ),
    status,
    attempt_count,
    attempt_count + 3,            -- preserve remaining-retry semantics
    next_attempt_at,
    created_at,
    updated_at
FROM media_index_outbox
WHERE status NOT IN ('processed');  -- don't re-queue already-done work
```

In-process rows with `status='in_flight'` are reset to `pending` so the
new worker re-claims them with its own lock; this is correct because the
old outbox worker is stopped during the migration window.

### Read-shim (migration 039)

The dual-read window must be short. Migration 019 creates a SQL VIEW
`media_index_outbox_compat` that selects from `outbox_events` filtered to
`event_type='asset.index_qdrant'` with column aliases matching the old
shape. Old read paths are rewired to the view, not the table.

```sql
CREATE VIEW media_index_outbox_compat AS
SELECT
    id                  AS id,
    json_extract(payload_json, '$.asset_id')           AS asset_id,
    json_extract(payload_json, '$.content_hash')       AS content_hash,
    json_extract(payload_json, '$.embedding_model')    AS embedding_model,
    json_extract(payload_json, '$.embedding_version')  AS embedding_version,
    json_extract(payload_json, '$.collection_version') AS collection_version,
    status, attempt_count, last_error, next_attempt_at,
    created_at, updated_at
FROM outbox_events
WHERE event_type = 'asset.index_qdrant';
```

`grep`-and-replace in `internal/repository/outbox/repository.go`'s
`media_index_outbox` → `media_index_outbox_compat`. The legacy table is
left in place but unwritten.

### Migration 040 (separate PR)

After one production cycle with no writes to `media_index_outbox`:
```sql
DROP VIEW media_index_outbox_compat;
DROP TABLE media_index_outbox;
```

### Code restructure

```
internal/outbox/                              ← new canonical home
  events.go           OutboxEvent, EventType aggregates
  repository.go       generic CRUD
  dispatcher.go       tx-aware Enqueue (called inside caller's tx)
  worker.go           handler registry + claim/complete/fail
  handlers/
    qdrant_index.go   wraps clipindexer.IndexClip
    registry.go       HandlerFunc registry
  txmanager.go        re-export of internal/repository/outbox.TxManager (delete from old location)
```

Old `internal/repository/outbox/` keeps the `Dispatcher` and `Repository`
until migration 040 lands, behind a deprecation comment. After 040:
the package disappears.

### Dispatcher API

```go
type Event struct {
    ID            string          // caller MUST supply; ULID/UUID/sha256(prefix)
    EventType     EventType       // e.g. EventTypeQdrantIndex
    AggregateType AggregateType   // e.g. AggregateTypeMediaAsset
    AggregateID   string          // the asset_id / script_id / ...
    Payload       json.RawMessage // event-specific data
    OccurredAt    time.Time
    MaxAttempts   int             // defaults to 5
}

func (d *Dispatcher) Dispatch(ctx context.Context, tx *sql.Tx, e Event) error
```

The caller writes business state to its own repositories using the same
tx, then calls `d.Dispatch(ctx, tx, e)`. A single `COMMIT` flushes both.
This is the same atomicity guarantee as the current
`outbox.Dispatcher.EnqueueAndIndex(clip, contentHash)`, just with a
generic payload.

### Worker API

```go
type HandlerFunc func(ctx context.Context, e *Event) error

type Registry struct { handlers map[EventType]HandlerFunc }

func (r *Registry) Register(eventType EventType, h HandlerFunc)

type Worker struct {
    repo     *Repository
    registry *Registry
    log      *zap.Logger
}

func (w *Worker) Start(ctx context.Context)
```

`Worker.Start` polls every 500ms (same cadence as the outbox worker
today), atomically `UPDATE … SET status='in_flight', locked_by=?,
locked_until=?` and dispatches to the matched handler. On success
`status='processed'`. On failure: exponential backoff up to 1h,
`max_attempts`, then `status='dead_letter'`.

### QdrantIndex handler (the migration's load-bearing handler)

```go
package handlers

import (
    "context"
    "encoding/json"
    "velox/go-master/internal/media/clipindexer"
    "velox/go-master/internal/outbox"
)

type qdrantIndexPayload struct {
    AssetID           string `json:"asset_id"`
    ContentHash       string `json:"content_hash"`
    EmbeddingModel    string `json:"embedding_model"`
    EmbeddingVersion  string `json:"embedding_version"`
    CollectionVersion string `json:"collection_version"`
}

// QdrantIndexHandler is the new home of the logic that today lives
// inside internal/repository/outbox/worker.go.ProcessFunc. Wraps
// clipindexer.IndexClip so the existing index hot-path is unchanged.
func QdrantIndexHandler(ctx context.Context, e *outbox.Event) error {
    var p qdrantIndexPayload
    if err := json.Unmarshal(e.Payload, &p); err != nil {
        return fmt.Errorf("qdrant_index: invalid payload: %w", err)
    }
    return clipindexer.IndexClip(ctx, p.AssetID)
}

func init() {
    outbox.RegisterHandler(outbox.EventTypeQdrantIndex, QdrantIndexHandler)
}
```

## Validation criteria

1. **Schema migration round-trip**: after applying 037+038 on a copy of
   production `media.db.sqlite`, every row that had `status='pending'`
   in `media_index_outbox` appears in `outbox_events` with
   `event_type='asset.index_qdrant'` and identical
   `payload_json` (after decode).
2. **Atomic dispatcher**: in a test, write a media_asset + outbox event,
   then `panic()` after the call. Re-check both: present (panic before
   COMMIT aborts the tx). Repeat with panic *between* the two writes —
   still both or neither.
3. **Worker routing**: register two handlers, fire two events of
   different `event_type`, assert each was routed to the right handler
   and no cross-routing happened.
4. **Idempotency**: fire the same `event_id` twice via dispatcher with
   two separate txns. Assert only one row exists; the second insert
   raised a UNIQUE violation that the dispatcher converts to nil
   (semantically "already enqueued").
5. **Backward-compat reads**: pre-existing Qdrant stale-link-cleaner
   sweeps continue to work using the new view. Run sweep for 1h after
   migration — no drift in `dead_letter` count.
6. **Worker lease**: a worker that crashes mid-handler has its row
   reclaimed after `locked_until` expires. Verified by manually setting
   `locked_until` to past and seeing the row go back to `pending`.
7. **Existing 13 tests** in `internal/repository/outbox/` still pass
   against the read-shim view (proves production reads are unaffected).

## Rollout

1. **PR-05a** (this PR): schema + data migration + read-shim + new API
   behind feature flag `VELOX_OUTBOX_V2`. Old path stays live.
2. **PR-05b** (follow-up): flip `VELOX_OUTBOX_V2=true` in staging,
   monitor for 24h. If `dead_letter` count delta is zero vs. last
   week, merge to production with the flag flipped.
3. **PR-05c** (cleanup): drop the old `media_index_outbox` table and
   view (migration 040), delete the legacy package. Closed loop.

## Risks & mitigations

| Risk | Mitigation |
|------|------------|
| Migration 038 touches every non-processed row | benchmark on snapshot of production data; backup `media.db.sqlite` before apply |
| `event_id` collision if hash function is bad | use ULID via `hashutil.RandomString(16)` when caller doesn't supply one; document the obligation |
| Worker thrashing on the same row (rare with lease) | `locked_until` (5 min) + `ReclaimStale` on each poll |
| View re-introduces old SQL quirks (`json_extract`) | keep one round of integration tests against view before production flip |
| Rename `internal/repository/outbox` breaks callers | keep package compileable until PR-05c lands; introduce `internal/outbox` as overlay package, not replacement |
| Existing alerts on `media_index_outbox` rows | update alerting rules in `config/prometheus.yml` to read from `outbox_events WHERE event_type='asset.index_qdrant'` |

## Files touched (estimate)

- New: 1 SQL migration (~80 lines), 1 SQL view (~20 lines), 1 new
  package (~400 lines), 1 handler (~30 lines)
- Modified: `outbox.Dispatcher` callers (~5 sites), `Worker` callers
  (1 site), Prometheus alerting (1 file, 6 lines), PR-01 migration
  runners, AGENTS.md outbox section
- Deleted later (PR-05c): legacy `internal/repository/outbox/` files
  (~600 lines, including the now-redundant txmanager.go and
  tx.go)

## Out of scope decisions elsewhere

The schema choice of `event_id TEXT UNIQUE` over `event_id INTEGER
PRIMARY KEY AUTOINCREMENT` is intentional: producer-supplied keys are
necessary for at-least-once context propagation across the
business-write tx boundary. If we ever switch the source of truth from
SQLite to Postgres, we'll keep this — UUIDs work just as well in
Postgres.
