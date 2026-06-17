-- =========================================================================
-- Migration: 036_canonical_asset_subtables.sql
-- Purpose: Extract denormalized arrays/objects from media_assets.metadata_json
--          into proper relational subtables. This is the foundational
--          schema for the "Canonical Asset Model" (vision §2), enabling:
--            * JOIN-efficient queries (no json_extract on hot paths)
--            * Audit trail for asset versions
--            * State machine for processing steps (one row per step)
--            * Many-to-many relations between assets
--
-- Design Rationale & Risks Mitigated:
--   1. asset_locations: ON DELETE CASCADE. If an asset is hard-deleted from
--      the DB, its location stubs are reaped automatically (locations are
--      meaningless without their parent asset).
--   2. asset_processing: Uses UNIQUE(asset_id, step) to fit the idempotent
--      upsert pattern. The table only tracks the latest state of each step,
--      not full history — granular history lives in the jobs table which
--      already provides append-only event sourcing.
--   3. asset_relations: Both FKs CASCADE. Adds a CHECK constraint to prevent
--      an asset referencing itself (parent != child) — a hard invariant
--      that would otherwise be a subtle application-layer bug.
--   4. asset_versions: Purposely omits the FOREIGN KEY to media_assets so
--      version timelines survive hard deletions as an immutable audit trail.
--      Uses a per-asset monotonic version integer (1, 2, 3, ...) so the
--      audit log reads "v2 → v3" rather than meaningless global IDs.
--
-- Backward Compatibility:
--   * No delete of existing columns. The app keeps reading from
--     media_assets.metadata_json while dual-writes / backfilling is
--     handled in separate application-side code (future commit).
--   * CHECK constraints are safe in modern SQLite (>=3.3.0). The bundled
--     driver (mattn/go-sqlite3) ships well past that.
--   * media_assets table guaranteed to exist by migration 033 (shadows
--     the schema in 001_velox_core.sql), so FK targets are always present
--     regardless of bootstrap order.
-- =========================================================================

-- 1. Asset Locations (1-to-Many: one asset, many storage locations)
CREATE TABLE IF NOT EXISTS asset_locations (
    id            TEXT PRIMARY KEY,
    asset_id      TEXT NOT NULL,
    location_kind TEXT NOT NULL
        CHECK (location_kind IN ('local','drive','s3','r2','gcs','minio','http')),
    uri           TEXT NOT NULL,
    path          TEXT NOT NULL DEFAULT '',
    external_id   TEXT NOT NULL DEFAULT '',
    is_primary    INTEGER NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','available','missing','corrupted')),
    checksum      TEXT NOT NULL DEFAULT '',
    size_bytes    INTEGER NOT NULL DEFAULT 0,
    mime_type     TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_asset_locations_asset_id
    ON asset_locations(asset_id);

-- Compound index for reverse lookup: "find the local file matching this URI"
-- (e.g. reconcile dedup sweeps, missing-file diagnostics)
CREATE INDEX IF NOT EXISTS idx_asset_locations_uri
    ON asset_locations(location_kind, uri);

-- Only one primary location per asset (storage router picks this on read)
CREATE UNIQUE INDEX IF NOT EXISTS idx_asset_locations_one_primary
    ON asset_locations(asset_id) WHERE is_primary = 1;


-- 2. Asset Processing (1-to-Many Steps per asset)
CREATE TABLE IF NOT EXISTS asset_processing (
    id              TEXT PRIMARY KEY,
    asset_id        TEXT NOT NULL,
    step            TEXT NOT NULL
        CHECK (step IN ('download','normalize','transcribe','translate',
                        'embed_text','embed_visual','embed_audio',
                        'index_qdrant','thumbnail','dedup')),
    status          TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','running','completed','failed','skipped')),
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 3,
    last_error      TEXT NOT NULL DEFAULT '',
    last_attempt_at TEXT,
    last_success_at TEXT,
    worker_id       TEXT,
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE,
    -- Idempotent upsert: re-running the same step overwrites the row.
    -- Granular attempt history lives in jobs + job_events.
    UNIQUE (asset_id, step)
);

-- Sweeper index: pick up stuck or pending steps on the next background tick
CREATE INDEX IF NOT EXISTS idx_asset_processing_sweeper
    ON asset_processing(status, updated_at);


-- 3. Asset Relations (Many-to-Many: assets can be related to other assets)
CREATE TABLE IF NOT EXISTS asset_relations (
    id              TEXT PRIMARY KEY,
    parent_asset_id TEXT NOT NULL,
    child_asset_id  TEXT NOT NULL,
    relation_kind   TEXT NOT NULL
        CHECK (relation_kind IN ('derived_from','part_of','used_by',
                                 'version_of','duplicate_of','transcript_of')),
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (parent_asset_id) REFERENCES media_assets(id) ON DELETE CASCADE,
    FOREIGN KEY (child_asset_id)  REFERENCES media_assets(id) ON DELETE CASCADE,

    UNIQUE (parent_asset_id, child_asset_id, relation_kind),
    -- Hard invariant: no self-reference. An asset cannot be its own parent.
    CHECK (parent_asset_id != child_asset_id)
);

-- UNIQUE covers forward lookup; this index enables reverse lookups
-- "what assets point to this one?" (e.g. dedup sweeps, tree traversal)
CREATE INDEX IF NOT EXISTS idx_asset_relations_child
    ON asset_relations(child_asset_id);


-- 4. Asset Versions (Immutable Audit Trail — survives asset deletion)
CREATE TABLE IF NOT EXISTS asset_versions (
    id            TEXT PRIMARY KEY,
    asset_id      TEXT NOT NULL,
    version       INTEGER NOT NULL CHECK (version > 0),
    snapshot_json TEXT NOT NULL,
    change_kind   TEXT NOT NULL
        CHECK (change_kind IN ('created','updated','replaced','deleted','restored')),
    changed_by    TEXT,
    change_reason TEXT,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),

    -- Per-asset monotonic sequence (1, 2, 3, ...). The UNIQUE constraint
    -- automatically provides an efficient ORDER BY version DESC lookup.
    UNIQUE (asset_id, version)

    -- No foreign key to media_assets: this table is the audit log and must
    -- survive the deletion of the audit subject. Orphaned versions are valid.
);
