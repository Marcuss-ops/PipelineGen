-- Migration 182: clip_storage_index
-- Date: July 2026
-- Stock Pipeline Cutover P0-CLIP-IDEMP — clip-level storage-index table.
--
-- A clip is uniquely identified by:
--   sha256hex(subject_id|source_video_id|start_ms|end_ms)
-- (see internal/kernel/asset/clip_key.go::ClipKey).
--
-- The clip_storage_index table records per-layer presence bits so
-- the domain port (internal/domain/clips/idempotency.go::Idempotency)
-- can detect the 4 storage cases (SQLite alive / Drive alive /
-- Qdrant alive / all/none) on every clip-create invocation. The
-- "each run naturally idempotent" guarantee from the user spec
-- depends on this table: a re-run hits Inspect() before any
-- side-effect and only fills the missing layer.
--
-- # Schema invariants
--
--   - PRIMARY KEY on clip_key (= UNIQUE by transitivity). Clip
--     identity is the SOLE canonical row identity; we do NOT
--     introduce a synthetic INTEGER PRIMARY KEY because the
--     hex digest IS the canonical identity and we want the row
--     to be portable across SQLite → Postgres → MySQL at
--     promotion time without a re-key migration.
--
--   - has_db is REQUIRED (godlike/07: established persistence is
--     the precondition for the other 2 layers). The presence
--     bits are bit-typed integers (0/1) rather than SQLite
--     boolean to keep the column portable across engines;
--     LayerPresence (domain/clips) is the typed runtime bool
--     projection.
--
--   - asset_id is the media_assets.id UUID. Set when
--     RecordPersistence flips has_db 0→1; carried along so
--     downstream phases (INDEX, UPLOAD repair) can target
--     the media_assets row by primary key instead of re-
--     resolving from clip_key.
--
--   - drive_file_id, drive_link, qdrant_point_id are NULLABLE.
--     The presence bit is the canonical "what layer do I trust"
--     signal; the ID columns are the canonical "what data do I
--     have" signal. Their lifecycles are decoupled so a layer
--     can be present (bit=1) without the materialized ID
--     (typical during ASYNC replay under the outbox).
--
--   - Timestamps follow the timeutil.FormatRFC3339 convention
--     used throughout the codebase. We do NOT use SQLite
--     DEFAULT datetime('now') because the application side
--     owns the clock — tests inject deterministic timestamps
--     via the same formatter.
--
-- # godlike/06 SSOT
--
-- This table is the SOLE canonical owner of per-clip per-layer
-- presence. Other candidates (`media_assets.drive_file_id`,
-- `media_assets.index_state`, the outbox `event_key`) are
-- downstream CONSUMERS of the per-layer bits — they are NOT
-- the SSOT. The presence ledger lives HERE so a SINGLE
-- inspection query (`SELECT has_db, has_drive, has_qdrant FROM
-- clip_storage_index WHERE clip_key=?`) tells the orchestrator
-- exactly which subset of the 8 storage cases applies.
--
-- # godlike/07 NO-FAKE-AVAILABILITY
--
-- This table MUST NOT be silently populated by an outbox event
-- replay that writes (clip_key, has_x=1) WITHOUT a corresponding
-- materialised ID (file_id / point_id) UNLESS that absence is
-- intentional and surfaced in the operator's "missing layer"
-- dial. Domain port ErrEmptyClipIdentity / ErrEmptyAssetID /
-- ErrEmptyDriveFileID / ErrEmptyQdrantPointID enforce that
-- contract — the production watchers see the same.
--
-- # Migration is forward-only
--
-- No ALTER on existing media_assets columns, no DROP/CREATE
-- on any pre-existing table. The new table sits alongside.
-- Backfill: NONE — a new run starts the table at empty;
-- subsequent clip creates populate rows. Legacy data is
-- reconciled by the P0-3 first phase body (PERSIST) which,
-- on the first clip-create for a legacy clip_key, INSERTs
-- the SQLite row + stamps clip_storage_index atomically.

CREATE TABLE IF NOT EXISTS clip_storage_index (
    clip_key        TEXT PRIMARY KEY,        -- sha256hex of subject|video|start_ms|end_ms (64 chars)
    asset_id        TEXT,                    -- media_assets.id UUID (NULL until RecordPersistence)
    has_db          INTEGER NOT NULL,        -- 0 or 1
    has_drive       INTEGER NOT NULL,        -- 0 or 1
    has_qdrant      INTEGER NOT NULL,        -- 0 or 1
    drive_file_id   TEXT,                    -- NULL when has_drive = 0
    drive_link      TEXT,                    -- NULL when has_drive = 0
    qdrant_point_id TEXT,                    -- NULL when has_qdrant = 0
    persisted_at    TEXT,                    -- has_db flip 0→1 timestamp (RFC3339); NULL while bit=0
    uploaded_at     TEXT,                    -- has_drive flip 0→1 timestamp (RFC3339); NULL while bit=0
    indexed_at      TEXT,                    -- has_qdrant flip 0→1 timestamp (RFC3339); NULL while bit=0
    created_at      TEXT NOT NULL,           -- row creation (RFC3339)
    updated_at      TEXT NOT NULL            -- last mutation (RFC3339)
);

-- Operator monitoring indexes — partial by presence bit so the
-- "N clips missing Drive coverage" / "N clips missing Qdrant
-- coverage" dashboards don't require full-table scans. The
-- partial WHERE clauses only include rows where the bit flip
-- would be desired (has_db=1 AND has_X=0), which matches the
-- canonical repair-only semantic from the user spec.
CREATE INDEX IF NOT EXISTS ix_clip_storage_index_drive_missing
    ON clip_storage_index(has_drive, has_db)
    WHERE has_drive = 0 AND has_db = 1;

CREATE INDEX IF NOT EXISTS ix_clip_storage_index_qdrant_missing
    ON clip_storage_index(has_qdrant, has_db)
    WHERE has_qdrant = 0 AND has_db = 1;

-- Lookup on media_assets.id for the cross-table repair path
-- (Phase 07 INDEX reading asset_id → media_assets row → emit
-- outbox.asset.index.requested). Composite so a single index
-- serves both "give me the clip_key by asset_id" and the
-- "all has_db=1 rows ordered by asset_id" repairs.
CREATE INDEX IF NOT EXISTS ix_clip_storage_index_asset_id
    ON clip_storage_index(asset_id)
    WHERE asset_id IS NOT NULL;
