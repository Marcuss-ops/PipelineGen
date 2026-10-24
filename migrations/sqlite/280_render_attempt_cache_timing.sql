-- database: primary
-- Migration 280: persist per-attempt overlay cache timings and outcome.
-- Times use microseconds so fast local lookups remain measurable.
ALTER TABLE render_attempt_analytics ADD COLUMN cache_lookup_us INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN cache_probe_us INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN cache_store_us INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN cache_outcome TEXT NOT NULL DEFAULT '';
