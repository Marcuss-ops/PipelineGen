-- 107: Add scheduling state columns to category_channels.
-- PR 3 (June 2026): persistent scheduler replaces per-channel goroutines
-- with a single global ticker that claims due channels via ClaimDue.
--
-- consecutive_failures: incremented on each failed check, reset on success.
-- last_error: error message from the last failed check.
-- last_success_at: RFC3339 timestamp of the last successful check.
-- lease_owner: ID of the worker that has claimed this channel.
-- lease_until: RFC3339 timestamp when the lease expires.

ALTER TABLE category_channels ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0;
ALTER TABLE category_channels ADD COLUMN last_error TEXT;
ALTER TABLE category_channels ADD COLUMN last_success_at TEXT;
ALTER TABLE category_channels ADD COLUMN lease_owner TEXT;
ALTER TABLE category_channels ADD COLUMN lease_until TEXT;
