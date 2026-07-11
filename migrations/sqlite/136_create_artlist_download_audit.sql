-- 136_create_artlist_download_audit.sql
-- Audit table for Artlist downloads. One row per automatic download,
-- used both for compliance audit trail and for daily per-account
-- rate-limit enforcement.
CREATE TABLE IF NOT EXISTS artlist_download_audit (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL DEFAULT 'artlist',
    account_id TEXT NOT NULL DEFAULT 'default',
    asset_id TEXT NOT NULL,
    external_url TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_artlist_download_audit_provider_account_day
ON artlist_download_audit (provider, account_id, date(created_at));

CREATE INDEX IF NOT EXISTS idx_artlist_download_audit_status
ON artlist_download_audit (status);
