-- database: primary
-- 233_drop_metadata_json_canonical_key_mirrors.sql
--
-- PR-METAJSON-STOP-MIRROR (August 2026): removes the 6 canonical keys
-- from metadata_json that are already owned by first-class columns on
-- media_assets. After this migration, these facts live ONLY in their
-- dedicated columns — metadata_json carries provider-specific extras
-- and semantic enrichment facts that lack their own columns.
--
-- The 6 keys removed:
--   $.title             → media_assets.title
--   $.source_provider   → media_assets.source_provider
--   $.source_video_id   → media_assets.source_video_id
--   $.source_version    → media_assets.source_version
--   $.tags              → media_assets.tags
--   $.category          → media_assets.category
--
-- Keys intentionally NOT removed (no dedicated column exists):
--   $.description, $.source_title, $.source_channel, $.drive_path,
--   $.indexing_status, $.summary, $.hook, $.topics, $.speakers,
--   $.mentioned_people, $.quality_score, $.sponsor_segment,
--   $.size_bytes, $.round, $.start_sec, $.end_sec, $.publish_action,
--   $.event, $.subject, $.slug, $.origin
--
-- These stay in metadata_json until they get their own columns or
-- a dedicated typed table.
--
-- IDEMPOTENCY: json_remove on a missing key is a no-op; re-running is
-- safe (it just removes what's already been removed).

UPDATE media_assets
SET metadata_json = json_remove(
    json_remove(
        json_remove(
            json_remove(
                json_remove(
                    json_remove(COALESCE(metadata_json, '{}'),
                        '$.title'),
                    '$.source_provider'),
                '$.source_video_id'),
            '$.source_version'),
        '$.tags'),
    '$.category')
WHERE metadata_json IS NOT NULL AND metadata_json != '';
