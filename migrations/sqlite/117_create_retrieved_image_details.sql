-- 117_create_retrieved_image_details.sql
--
-- Image-territories action plan (July 2026), FASE 4A EXPAND.
-- Per-asset detail table for web-retrieved image provenance, 1:1 with
-- media_assets, FK CASCADE.

CREATE TABLE IF NOT EXISTS retrieved_image_details (
    asset_id         TEXT PRIMARY KEY,
    source_image_url TEXT NOT NULL DEFAULT '',
    source_page_url  TEXT NOT NULL DEFAULT '',
    license          TEXT NOT NULL DEFAULT '',
    author           TEXT NOT NULL DEFAULT '',
    search_query     TEXT NOT NULL DEFAULT '',
    retrieved_at     TEXT NOT NULL DEFAULT '',
    provider         TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);
