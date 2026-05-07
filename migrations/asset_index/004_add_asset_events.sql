-- 004_add_asset_events.sql
-- Asset events table for tracking asset processing history

CREATE TABLE IF NOT EXISTS asset_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    message TEXT,
    metadata TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (asset_id) REFERENCES asset_index(asset_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_asset_events_asset_id
ON asset_events(asset_id);

CREATE INDEX IF NOT EXISTS idx_asset_events_type
ON asset_events(event_type);

CREATE INDEX IF NOT EXISTS idx_asset_events_created_at
ON asset_events(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_asset_events_composite
ON asset_events(asset_id, event_type, created_at DESC);
