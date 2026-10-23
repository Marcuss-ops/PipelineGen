-- database: cache
-- Content-addressed reuse index for certified per-item overlay renders.
-- The bytes live in the object store under their SHA-256; this table stores
-- only the certified locator plus the key that names the render. Every row is
-- rebuildable: dropping this table costs re-renders, never correctness.
CREATE TABLE IF NOT EXISTS overlay_render_cache_entries (
    cache_key TEXT PRIMARY KEY,
    render_key TEXT NOT NULL,
    plan_context TEXT NOT NULL,
    epoch TEXT NOT NULL,
    artifact_json TEXT NOT NULL,
    artifact_sha256 TEXT NOT NULL,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    job_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'READY' CHECK (status IN ('READY', 'INVALID')),
    invalidated_reason TEXT NOT NULL DEFAULT '',
    hit_count INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_used_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_cache_overlay_render_status
    ON overlay_render_cache_entries(status);
CREATE INDEX IF NOT EXISTS idx_cache_overlay_render_epoch
    ON overlay_render_cache_entries(epoch);
CREATE INDEX IF NOT EXISTS idx_cache_overlay_render_render_key
    ON overlay_render_cache_entries(render_key);
