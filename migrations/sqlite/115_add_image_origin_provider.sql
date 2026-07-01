-- Migration 115: Add origin and provider columns to media_assets (Step 2, July 2026).
--
-- Part of the image territory separation plan:
--   origin  = 'retrieved' | 'generated' | 'uploaded'
--   provider = 'wikipedia' | 'duckduckgo' | 'searxng' | 'drive' |
--              'google-slides' | 'flux' | 'nvidia' | 'upload' | 'unknown'
--
-- Backfill: all existing rows with source='image' get origin='retrieved'.
-- Later steps will set origin='generated' for AI images at ingest time.

-- 1. Add columns with safe defaults.
ALTER TABLE media_assets ADD COLUMN origin TEXT NOT NULL DEFAULT 'retrieved';
ALTER TABLE media_assets ADD COLUMN provider TEXT NOT NULL DEFAULT '';

-- 2. Backfill existing rows: set provider='unknown' for all image rows
--    without an explicit provider assignment at migration time.
UPDATE media_assets SET provider = 'unknown' WHERE source = 'image' AND provider = '';

-- 3. Create indexes for territory-filtered queries.
CREATE INDEX IF NOT EXISTS idx_media_assets_origin ON media_assets(origin);
CREATE INDEX IF NOT EXISTS idx_media_assets_provider ON media_assets(provider);
