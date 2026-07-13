-- 153_create_asset_artifacts.sql
--
-- Step 1 of PR-CATALOG-MULTILINGUA (Italian plan, July 2026).
-- Creates the canonical per-clip file artifact registry.
--
-- godlike/06 SSOT (one canonical owner per fact): every .mp4, .png,
-- waveform, and preview file lives HERE, addressable by
-- (asset_id, role). `media_assets` stays the identity row;
-- physical file metadata lives in `asset_artifacts`.
--
-- Why a NEW table (not a column on media_assets): the canonical
-- artifact family already has two distinct surfaces that share the
-- word "artifact":
--   - artifact_stages (FASE 3, migration 147): worker-side per-
--     publication staging, atomic with the outbox event.
--   - artifacts (PR3, migration 051): content-addressed blob registry.
-- asset_artifacts is the per-clip file registry — the 3rd distinct
-- surface. The table name keeps the godlike/06 SSOT single-owner
-- invariant intact (no cross-table drift; each table owns a
-- different fact).
--
-- Role enum (app-layer validates on write — SQL is a defensive fence):
--   render_master      — canonical single-version master (mp4)
--   preview            — 640x360 lighter version
--   thumbnail          — extracted frame(s)
--   waveform           — audio waveform visualization
--   source_archive     — original upload (used for re-processing)
--
-- Status enum:
--   pending            — row created, file may be local-only
--   uploaded           — upload to Drive completed (size + sha256
--                        confirmed against local file)
--   verified           — embedding extraction + Qdrant index succeeded
--   deleted            — soft-delete; row preserved for audit
--
-- Constraints:
--   - (render_master, preview) has a 1-per-asset invariant enforced
--     by a UNIQUE partial index. A clip may have at most ONE
--     render_master and ONE preview.
--   - Other roles (thumbnail, waveform, source_archive) may have
--     multiple rows per asset (e.g. multiple waveform frames).
--   - FK CASCADE: deleting a media_assets row cascades to
--     asset_artifacts (mirrors the CASCADE pattern from
--     asset_visual_summaries, migration 151).
--
-- godlike/07 fail-closed: CHECK constraints on role + status reject
-- any value not in the canonical set at the SQL layer. The
-- application layer is the typed-enum owner (godlike/06 SSOT); SQL
-- is a second line of defense.

CREATE TABLE IF NOT EXISTS asset_artifacts (
    id            TEXT PRIMARY KEY,
    asset_id      TEXT NOT NULL,

    role          TEXT NOT NULL
                  CHECK (role IN ('render_master','preview','thumbnail','waveform','source_archive')),
    mime_type     TEXT NOT NULL DEFAULT '',

    local_path    TEXT NOT NULL DEFAULT '',
    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link    TEXT NOT NULL DEFAULT '',

    file_size     INTEGER NOT NULL DEFAULT 0,
    file_sha256   TEXT NOT NULL DEFAULT '',

    width         INTEGER NOT NULL DEFAULT 0,
    height        INTEGER NOT NULL DEFAULT 0,
    frame_rate    REAL NOT NULL DEFAULT 0.0,
    duration_ms   INTEGER NOT NULL DEFAULT 0,

    status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','uploaded','verified','deleted')),

    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);

-- General lookup by (asset_id, role) — the scene binder, the API
-- composition layer, and the CoverArt preview read.
CREATE INDEX IF NOT EXISTS idx_asset_artifacts_asset_role
    ON asset_artifacts (asset_id, role);

-- 1-per-asset invariant for the canonical master + preview pair.
-- Partial unique index: thumbnail/waveform/source_archive are NOT
-- constrained (multiple frames / re-encodes are valid for these).
CREATE UNIQUE INDEX IF NOT EXISTS idx_asset_artifacts_unique_singleton
    ON asset_artifacts (asset_id, role)
    WHERE role IN ('render_master', 'preview');

-- Publisher drain pattern: oldest pending first (mirrors
-- idx_artifact_stages_state_created from migration 147).
CREATE INDEX IF NOT EXISTS idx_asset_artifacts_status_updated
    ON asset_artifacts (status, updated_at DESC);
