-- 106: Add monitoring state columns to category_channels.
-- PR 2 (June 2026): category_channels becomes the single source of truth
-- for channel configuration. The JSON fallback is removed; the monitor
-- reads channels exclusively through channels.Service.
--
-- enabled: defaults to 1 so existing channels are active after migration.
-- next_check_at: RFC3339 timestamp for when to next check. NULL means "due now".
-- last_checked_at: RFC3339 timestamp of the last successful check.

ALTER TABLE category_channels ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE category_channels ADD COLUMN next_check_at TEXT;
ALTER TABLE category_channels ADD COLUMN last_checked_at TEXT;
