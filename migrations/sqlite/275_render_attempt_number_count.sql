-- database: primary
-- Migration 275: persist numeric-entity counts in render attempt analytics.
ALTER TABLE render_attempt_analytics
    ADD COLUMN number_count INTEGER NOT NULL DEFAULT 0;
