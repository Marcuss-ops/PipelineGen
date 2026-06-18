-- 064_backfill_processing_status.sql
-- Backfills historical status/error values from media_assets into
-- asset_processing before the columns are dropped from media_assets.
--
-- Each asset with a non-empty status gets a "legacy_backfill" processing
-- record. The status column held values like "ready", "processing",
-- "failed", etc. — we map:
--   "ready"     → "completed" (asset was successfully processed)
--   "failed"    → "failed" (asset had an error)
--   "processing"→ "running" (asset was mid-processing, treat as failed)
--   anything else → "completed" (assume success)
--
-- This is an idempotent migration: rows that already exist in
-- asset_processing for (asset_id, 'legacy_backfill') are NOT overwritten.

INSERT OR IGNORE INTO asset_processing (asset_id, step, status, error_message, started_at, completed_at, attempt_count)
SELECT
    id,
    'legacy_backfill',
    CASE
        WHEN status = 'failed' THEN 'failed'
        WHEN status = 'processing' THEN 'failed'
        ELSE 'completed'
    END,
    CASE
        WHEN error IS NOT NULL AND error != '' THEN error
        WHEN status = 'processing' THEN 'asset was mid-processing at migration time'
        ELSE ''
    END,
    updated_at,
    CASE
        WHEN status IN ('failed', 'completed', 'ready') THEN updated_at
        ELSE NULL
    END,
    1
FROM media_assets
WHERE (status IS NOT NULL AND status != '' AND status != 'ready')
   OR (error IS NOT NULL AND error != '');
