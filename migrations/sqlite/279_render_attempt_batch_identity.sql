-- database: primary
-- Migration 279: persist semantic membership and timing for fused overlay
-- render attempts so batch throughput can be audited from analytics rows.
ALTER TABLE render_attempt_analytics ADD COLUMN batch_id TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN batch_item_ids_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE render_attempt_analytics ADD COLUMN batch_start_us INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN batch_end_us INTEGER NOT NULL DEFAULT 0;
