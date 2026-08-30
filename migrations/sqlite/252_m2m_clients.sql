-- 252_m2m_clients.sql: scoped machine-to-machine credentials.
-- Only secret_hash is persisted; plaintext is returned once by provisioning.
CREATE TABLE IF NOT EXISTS m2m_clients (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    client_id TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    secret_hash TEXT NOT NULL UNIQUE,
    scopes_json TEXT NOT NULL DEFAULT '[]',
    enabled INTEGER NOT NULL DEFAULT 1,
    rate_limit_rps REAL NOT NULL DEFAULT 2,
    rate_limit_burst INTEGER NOT NULL DEFAULT 10,
    quota_max_scenes INTEGER NOT NULL DEFAULT 1000,
    quota_max_total_secs INTEGER NOT NULL DEFAULT 14400,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_used_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_m2m_clients_secret_hash ON m2m_clients(secret_hash);
