-- 019_media_autovacuum_hot_tables.sql
-- DB-06 (2026-11-27): 017 tuned the tables the audit measured first. Live
-- observation after the cutover showed the same failure mode on the remaining
-- churn-heavy surfaces: asset_locations accumulated 1040 dead tuples against
-- 18055 live rows and media_asset_sources 330, with last_autovacuum still
-- never — the default 20% scale factor never fires on them.
--
-- This migration applies the SAME thresholds as 017 to the rest of the
-- high-churn set. It is storage-parameter tuning only: no row is read or
-- written, and a table that does not exist on this deployment is skipped
-- (render_jobs and rendering_workers are created by the queue/worker
-- deployment, not by the media self-bootstrap).
--
-- Order-independent: it can run before or after 018.

DO $$
DECLARE
    target text;
BEGIN
    FOREACH target IN ARRAY ARRAY[
        'asset_locations',
        'media_asset_sources',
        'media_embeddings',
        'processing_metrics',
        'registry_events',
        'render_jobs',
        'render_attempts',
        'rendering_workers',
        'localized_render_reuse'
    ] LOOP
        IF to_regclass('public.' || target) IS NOT NULL THEN
            EXECUTE format(
                'ALTER TABLE %I SET ('
                'autovacuum_vacuum_scale_factor = 0.02, '
                'autovacuum_vacuum_insert_scale_factor = 0.02, '
                'autovacuum_analyze_scale_factor = 0.01)',
                target);
        END IF;
    END LOOP;
END $$;
