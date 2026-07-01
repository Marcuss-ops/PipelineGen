-- 116_create_generated_image_details.sql
--
-- Image-territories action plan (July 2026), FASE 4A EXPAND.
-- Per-asset detail table for AI-generated image provenance, 1:1 with
-- media_assets, FK CASCADE. Migration-numbering deviation: user spec
-- asked for 115_*, but two existing 115_* migrations already collide
-- on disk; this lands at 116 to avoid a 3rd collision.

CREATE TABLE IF NOT EXISTS generated_image_details (
    asset_id          TEXT PRIMARY KEY,
    prompt_original   TEXT NOT NULL DEFAULT '',
    prompt_resolved   TEXT NOT NULL DEFAULT '',
    style_id          TEXT NOT NULL DEFAULT '',
    style_version     TEXT NOT NULL DEFAULT '',
    model             TEXT NOT NULL DEFAULT '',
    seed              INTEGER NOT NULL DEFAULT 0,
    generation_job_id TEXT NOT NULL DEFAULT '',
    source_hash       TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);
