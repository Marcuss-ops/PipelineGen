-- 142_asset_renditions_unique.sql
--
-- Enforce uniqueness of rendition kind per asset so that
-- AssetFinalizerTx can upsert rendition rows with
-- ON CONFLICT(asset_id, kind) without ambiguity.
CREATE UNIQUE INDEX IF NOT EXISTS ux_asset_renditions_asset_kind
    ON asset_renditions (asset_id, kind);
