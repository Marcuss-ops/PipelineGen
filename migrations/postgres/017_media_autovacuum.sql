-- 017_media_autovacuum.sql
-- DB-06 (2026-11-27): these tables are small in live rows but churn heavily
-- (upserts, deletes, lease updates). The default autovacuum scale factor
-- (20%) never triggers on them, so dead tuples accumulated until a manual
-- VACUUM: asset_text_tracks had 148 dead rows against 1 live row and
-- asset_text_track_segments 365 dead against 34. Lower the thresholds per
-- table so autovacuum keeps up; this is a storage-parameter change only.

ALTER TABLE media_assets SET (
    autovacuum_vacuum_scale_factor = 0.02,
    autovacuum_vacuum_insert_scale_factor = 0.02,
    autovacuum_analyze_scale_factor = 0.01
);

ALTER TABLE outbox_events SET (
    autovacuum_vacuum_scale_factor = 0.02,
    autovacuum_vacuum_insert_scale_factor = 0.02,
    autovacuum_analyze_scale_factor = 0.01
);

ALTER TABLE asset_text_tracks SET (
    autovacuum_vacuum_scale_factor = 0.02,
    autovacuum_vacuum_insert_scale_factor = 0.02,
    autovacuum_analyze_scale_factor = 0.01
);

ALTER TABLE asset_text_track_segments SET (
    autovacuum_vacuum_scale_factor = 0.02,
    autovacuum_vacuum_insert_scale_factor = 0.02,
    autovacuum_analyze_scale_factor = 0.01
);

-- worker_heartbeats is a legacy media table: present on long-lived databases,
-- absent on a freshly migrated one. Tune it only when it exists.
DO $$
BEGIN
    IF to_regclass('public.worker_heartbeats') IS NOT NULL THEN
        EXECUTE 'ALTER TABLE worker_heartbeats SET (
            autovacuum_vacuum_scale_factor = 0.02,
            autovacuum_vacuum_insert_scale_factor = 0.02,
            autovacuum_analyze_scale_factor = 0.01
        )';
    END IF;
END $$;
