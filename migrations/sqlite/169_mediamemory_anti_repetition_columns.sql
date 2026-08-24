-- 169_mediamemory_anti_repetition_columns.sql — Fase 2.3
-- media_usage_events extends with channel_id + video_id so the
-- ranker can apply repetition_penalty deterministically without
-- a runtime join against media_assets.
--
-- godlike/06 SSOT (one canonical DDL home per column): this file
-- is the SOLE place the channel_id + video_id columns land.
-- The application-level canonical is in
-- internal/application/mediamemory/types.go::UsageEvent.
-- The SQL ↔ Go row-scan lives in
-- internal/platform/sqlite/mediamemory/usage_repository.go.
--
-- godlike/06 SSOT (Phase 2.3 wire-shape contract): channel_id and
-- video_id are TEXT NOT NULL DEFAULT '' (empty string sentinel —
-- callers who DON'T supply them get the legacy Phase 1.x audit
-- log). Empty values are valid (rendered as no penalty axis
-- input); the same-asset axis still drives the contract via
-- asset_id which has always been recorded.
--
-- godlike/06 SSOT (one index per cross-fase query): the new
-- channel_id index feeds the resolver's project-scoped
-- channel-saturation read; the video_id index feeds the
-- same-video reuse read. Both MUST exist before the resolver
-- hot-path can serve projects >1k usages / day per channel.

ALTER TABLE media_usage_events
    ADD COLUMN channel_id TEXT NOT NULL DEFAULT '';

ALTER TABLE media_usage_events
    ADD COLUMN video_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_media_usage_events_project_channel
    ON media_usage_events(project_id, channel_id);

CREATE INDEX IF NOT EXISTS idx_media_usage_events_project_video
    ON media_usage_events(project_id, video_id);
