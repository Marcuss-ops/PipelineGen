-- 094_add_media_assets_index_state_column.sql
--
-- QDRANT-002 PR6 (inline with PR5 prerequisite): promote the
-- index_state from a JSON-set value inside metadata_json
-- ($.index_state) to a first-class column on media_assets. Today
-- the values are spread across metadata_json and use a lowercase,
-- ad-hoc alphabet (`embedding`, `upserting`, `indexed`, `failed`,
-- `retrying`); the canonical state machine is
--   DISCOVERED → INDEX_PENDING → INDEXING → INDEXED → INDEX_FAILED
--                                       → DELETE_PENDING → DELETED
-- (lifecycle companion: LifecycleState ∈ {STAGING,PROCESSING,ACTIVE,
-- DELETED,ready,pending} in asset_types.go).
--
-- Companion code (lands together in one direct-to-main commit):
--   internal/domain/asset/index_state.go
--     IndexState type + 7 const constants + Valid() helper.
--   internal/infrastructure/indexing/clipindexer/indexing_state.go
--     setIndexState rewrites the column instead of json_set on
--     metadata_json; setIndexedAt folds the sidecar metadata
--     ($.indexed_at, $.indexed_content_hash, $.embedding_model,
--     $.embedding_model_version) and the column flip into a single
--     atomic UPDATE.
--   internal/infrastructure/indexing/clipindexer/indexing.go
--     tryFastPath reads media_assets.index_state directly instead
--     of COALESCE(json_extract(metadata_json, '$.index_state'), '').
--   internal/application/jobs/outbox/index_delete.go
--     IndexDeleteHandler writes DELETE_PENDING pre-Qdrant, DELETED
--     post-SoftDelete. idempotency pre-flight #2 now consults both
--     lifecycle_state AND index_state for early-success skip.
--   internal/platform/sqlite/assets/clips_repository.go
--     SetIndexState method on *assets.ClipsRepository (satisfies the
--     extended AssetDeleter port).
--
-- Why a column instead of metadata_json:
--   1. Indexable: dashboards / housekeeping queries stop paying the
--      json_extract cost on every media_assets scan.
--   2. IndexHealth metrics can group by media_assets.index_state
--      natively; currently they have to json_extract, which the
--      current Prometheus query language handles poorly.
--   3. Schema migrations can ALTER it; you cannot enforce a CHECK
--      constraint on a JSON-set value.
--
-- Backfill safety: the ALTER TABLE applies the DEFAULT immediately,
-- so by the time the UPDATE runs every row holds
-- `index_state = 'DISCOVERED'`. The WHERE clause
-- `WHERE index_state = 'DISCOVERED'` therefore reaches every row
-- until a writer runs (outbox worker picks up an event the next
-- animation tick at the earliest). Once a worker writes an
-- authoritative value, subsequent applications of this migration
-- are no-ops for that row. Reruns of the migration on prior
-- versions of the code (pre-`setIndexState` rewrite) are still
-- idempotent because the operator runs the migration AGAINST a
-- historical snapshot only when restoring from a backup — in that
-- case the WHERE clause still applies: the snapshot has
-- `index_state = 'DISCOVERED'` everywhere, so backfills correctly.
--
-- `index_state_updated_at` is a sibling column. Pairing it with
-- `index_state` keeps a single source of truth for state-machine
-- rotation (the worker writes both in one UPDATE) and avoids the
-- pre-PR6 risk that drift between `metadata_json.$.index_state`
-- and `metadata_json.$.index_state_updated_at` confuses an
-- operator running `WHERE index_state_updated_at < now() - 1day`.
--
-- Sidecar fields (NOT promoted in this PR — they stay in
-- metadata_json because they belong to other schemas / concerns):
--   - $.indexed_at            → indexed-completion timestamp
--   - $.indexed_content_hash  → freshness fingerprint for fast-path
--   - $.embedding_model       → QDRANT-003 model identity
--   - $.embedding_model_version
--   - $.last_index_error      → operator audit string
--   - $.collection_version    → QDRANT-003 collection alias binding
-- Promoting collection_version is a planned QDRANT-005 followup; it
-- is touched here by the backfill so the column-shaped equivalent
-- does not diverge from the sidecar.

ALTER TABLE media_assets ADD COLUMN index_state TEXT NOT NULL DEFAULT 'DISCOVERED';
ALTER TABLE media_assets ADD COLUMN index_state_updated_at TEXT NOT NULL DEFAULT '';

-- Backfill: map legacy JSON-set values to canonical IndexState.
-- Order is irrelevant for the CASE expression; the WHERE clause
-- restricts the write to rows still at the default sentinel.
UPDATE media_assets
SET index_state = CASE COALESCE(json_extract(metadata_json, '$.index_state'), '')
        WHEN ''          THEN 'DISCOVERED'
        WHEN 'embedding' THEN 'INDEXING'
        WHEN 'upserting' THEN 'INDEXING'
        WHEN 'retrying'  THEN 'INDEX_PENDING'
        WHEN 'indexed'   THEN 'INDEXED'
        WHEN 'failed'    THEN 'INDEX_FAILED'
        ELSE                   'DISCOVERED'
    END,
    index_state_updated_at = COALESCE(json_extract(metadata_json, '$.index_state_updated_at'), '')
WHERE index_state = 'DISCOVERED';

-- Optional index for dashboards / housekeeping queries (QDRANT-005
-- followup will lean on this for IndexHealth.peekCounters). Keeping
-- the index conditional so reapplying the migration is a no-op.
CREATE INDEX IF NOT EXISTS idx_media_assets_index_state
    ON media_assets(index_state);
