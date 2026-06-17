-- 032_category_channels_lookback.sql
-- Adds lookback_days, max_segments, and segment_prompt columns for
-- time-based retroactive scanning and per-channel segment customization.
-- lookback_days: if > 0, overrides playlist_end with --dateafter YYYYMMDD
-- max_segments:   how many segments to extract per video (0 = default 3)
-- segment_prompt: custom AI prompt for the Gemma segment finder

ALTER TABLE category_channels ADD COLUMN lookback_days INTEGER NOT NULL DEFAULT 0;
ALTER TABLE category_channels ADD COLUMN max_segments INTEGER NOT NULL DEFAULT 0;
ALTER TABLE category_channels ADD COLUMN segment_prompt TEXT NOT NULL DEFAULT '';
