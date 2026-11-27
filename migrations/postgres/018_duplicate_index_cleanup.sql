-- 018_duplicate_index_cleanup.sql
-- DB-07 (2026-11-27): drop indexes that duplicate a stricter or composite
-- sibling. Each drop is guarded by the survivor's presence so a database that
-- never created the duplicate (or is missing the survivor) is left untouched.

-- asset_text_track_segments: the unique (track_id, sequence_no) index already
-- serves every lookup the separate track_id index served.
DO $$
BEGIN
    IF to_regclass('public.asset_text_track_segments_track_id_sequence_no_key') IS NOT NULL
       AND to_regclass('public.idx_asset_text_track_segments_track') IS NOT NULL THEN
        DROP INDEX idx_asset_text_track_segments_track;
    END IF;
END $$;

-- media_embeddings: the primary key already starts with asset_id, so the
-- standalone asset_id index is a redundant second btree.
DO $$
BEGIN
    IF to_regclass('public.idx_media_embeddings_asset') IS NOT NULL THEN
        DROP INDEX idx_media_embeddings_asset;
    END IF;
END $$;

-- render_jobs: the partial unique index is stricter (same key, narrower
-- predicate); keep it and drop the older unconditional duplicate.
DO $$
BEGIN
    IF to_regclass('public.render_jobs_idempotency_key_unique') IS NOT NULL
       AND to_regclass('public.idx_render_jobs_idempotency') IS NOT NULL THEN
        DROP INDEX idx_render_jobs_idempotency;
    END IF;
END $$;
