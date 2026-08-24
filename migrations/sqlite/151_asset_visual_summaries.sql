-- 151_asset_visual_summaries.sql
--
-- Localized, versioned VLM-generated visual summary per media asset.
-- Each row stores the aggregated caption/visible_actions/visible_entities
-- produced by a single VLM pass over frames sampled from the clip's
-- local_path. There is AT MOST ONE row per media asset (PRIMARY KEY =
-- asset_id) — the visual summary is a single aggregate, not a family
-- of (language, kind) tracks like asset_text_tracks.
--
-- Design decisions (godlike/06 SSOT — one canonical owner per fact):
--   - PRIMARY KEY = asset_id (1:1 with media_assets.id). CANNOT be
--     replaced by an auto-increment surrogate because the row's identity
--     is the asset itself — re-running the VLM pass upserts in place.
--   - frame_count + interval_seconds are stored per row to make the
--     supersede gate auditable: a re-index at a different interval
--     produces a distinct SourceHash and forces a Qdrant re-index.
--   - preprocessing_version + model_name + model_version are the
--     (content_version, schema_version, preprocessing_version,
--     model_version) tuple required by the "version the Qdrant
--     projection with content, schema, preprocessing, and model
--     versions" engineering rule (AGENTS.md / migration rules). The
--     projection is rebuildable from SQLite by re-running the
--     VLM pass with the SAME (preproc, model) tuple.
--   - source_hash is SHA-256 over
--       sorted(visible_actions) || sorted(visible_entities)
--       || model_name || model_version || preprocessing_version
--       || frame_count
--     Used by Qdrant re-index to detect "VLM pass produced an
--     identical aggregate" → skip the upsert.
--   - sampled_at is the wall-clock time the VLM pass completed. Empty
--     when the row was NOT produced by a real pass (the canonical
--     EmptyAssetDB constructor surface — pre-VLM-era legacy).
--   - updated_at uses datetime('now') on every upsert so the supersede
--     gate can answer "which clip's row is most recent?" without
--     parsing sampled_at across timezones.
--
-- Consumed by:
--   - internal/domain/asset/clip_visual_summary.go (canonical struct)
--   - internal/platform/sqlite/assets/visual_summary_repository.go
--   - internal/infrastructure/qdrant/indexing/payload_builder.go
--       (reads row, emits visual_summary/visible_actions/visible_entities
--        + visual_preprocessing_version + visual_model_name +
--        visual_model_version payload keys)
--   - cmd/admin/reindex_visual_summary.go (reindex gate command)
--   - internal/infrastructure/qdrant/verification/verifier.go
--       (ReindexVerifier cross-check: SQLite row vs Qdrant payload)

CREATE TABLE IF NOT EXISTS asset_visual_summaries (
    asset_id              TEXT PRIMARY KEY NOT NULL,

    visual_summary_text   TEXT NOT NULL DEFAULT '',
    visible_actions_json  TEXT NOT NULL DEFAULT '[]',  -- JSON array string
    visible_entities_json TEXT NOT NULL DEFAULT '[]',  -- JSON array string

    frame_count           INTEGER NOT NULL DEFAULT 0
                          CHECK (frame_count >= 0),
    interval_seconds      REAL NOT NULL DEFAULT 0.0
                          CHECK (interval_seconds >= 0.0),

    preprocessing_version TEXT NOT NULL DEFAULT '',  -- canonical "vlm-sampler/<semver>"
    model_name            TEXT NOT NULL DEFAULT '',  -- e.g. "llava-1.6-7b"
    model_version         TEXT NOT NULL DEFAULT '',  -- e.g. "2026-07-13"

    source_hash           TEXT NOT NULL DEFAULT '',
    sampled_at            TEXT NOT NULL DEFAULT '',  -- RFC3339; empty when row NOT produced by real pass
    sampled_at_unix       INTEGER NOT NULL DEFAULT 0,

    created_at            TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at            TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);

-- Lookup by asset_id is the primary key; no extra index needed.

-- Reindex-by-model-version query: enumerate all clips whose VLM pass
-- used a specific (model_name, model_version) so the admin reindex
-- command can rebuild the Qdrant projection when the VLM checkpoint
-- is bumped.
CREATE INDEX IF NOT EXISTS idx_asset_visual_summaries_model
    ON asset_visual_summaries (model_name, model_version);

-- Dedup / change-detection: the supersede gate reads source_hash to
-- decide whether to upsert into Qdrant. Indexing by source_hash
-- enables the "find clips with identical SourceHash" cross-check.
CREATE INDEX IF NOT EXISTS idx_asset_visual_summaries_source_hash
    ON asset_visual_summaries (source_hash);

-- Recent-passes query: the admin runbook's "which 100 most recent
-- VLM passes?" introspection query (used for migration drift
-- monitoring — FASE-9 backlog).
CREATE INDEX IF NOT EXISTS idx_asset_visual_summaries_sampled_at
    ON asset_visual_summaries (sampled_at_unix DESC);
