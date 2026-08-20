-- database: primary
-- 223_artlist_catalog.sql
-- Canonical local Artlist catalog (August 2026).
--
-- Identity invariant:
--   artlist_clips.clip_id is the provider identity and the sole
--   deduplication key. A clip discovered by many queries still has one
--   artlist_clips row and many artlist_query_clips rows.
--
-- URL safety invariant:
--   only stable page/thumbnail URLs belong in this catalog. Signed HLS,
--   tokenized CDN, and download URLs are deliberately not represented;
--   they must be resolved at acquisition time.
--
-- The existing artlist_search_cache remains a response/TTL cache. These
-- tables are the durable catalog projection and must not be treated as
-- interchangeable with that cache.

CREATE TABLE IF NOT EXISTS artlist_clips (
    clip_id             TEXT PRIMARY KEY,
    title               TEXT NOT NULL DEFAULT '',
    author              TEXT NOT NULL DEFAULT '',
    duration_ms         INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    canonical_clip_url  TEXT NOT NULL DEFAULT '',
    thumbnail_url       TEXT NOT NULL DEFAULT '',
    tags_json           TEXT NOT NULL DEFAULT '[]',
    categories_json     TEXT NOT NULL DEFAULT '[]',
    description         TEXT NOT NULL DEFAULT '',
    metadata_json       TEXT NOT NULL DEFAULT '{}',
    first_seen_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    active              INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    downloaded          INTEGER NOT NULL DEFAULT 0 CHECK (downloaded IN (0, 1)),
    drive_file_id       TEXT NOT NULL DEFAULT '',
    drive_link          TEXT NOT NULL DEFAULT '',
    local_path          TEXT NOT NULL DEFAULT '',
    file_hash           TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_artlist_clips_active_seen
    ON artlist_clips(active, last_seen_at DESC);

CREATE INDEX IF NOT EXISTS idx_artlist_clips_downloaded
    ON artlist_clips(downloaded, active);

CREATE INDEX IF NOT EXISTS idx_artlist_clips_drive_file
    ON artlist_clips(drive_file_id)
    WHERE drive_file_id != '';

CREATE TABLE IF NOT EXISTS artlist_queries (
    query_id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    query                       TEXT NOT NULL DEFAULT '',
    normalized_query            TEXT NOT NULL DEFAULT '',
    query_key                   TEXT NOT NULL UNIQUE,
    filters_json                TEXT NOT NULL DEFAULT '{}',
    provider_sort_type          INTEGER NOT NULL DEFAULT 1,
    provider_total              INTEGER NOT NULL DEFAULT 0 CHECK (provider_total >= 0),
    provider_total_authoritative INTEGER NOT NULL DEFAULT 1 CHECK (provider_total_authoritative IN (0, 1)),
    result_count                INTEGER NOT NULL DEFAULT 0 CHECK (result_count >= 0),
    first_synced_at             TEXT,
    last_synced_at              TEXT,
    expires_at                  TEXT,
    sync_status                 TEXT NOT NULL DEFAULT 'never'
                                CHECK (sync_status IN ('never', 'running', 'succeeded', 'failed')),
    last_error                  TEXT NOT NULL DEFAULT '',
    created_at                  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at                  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_artlist_queries_normalized
    ON artlist_queries(normalized_query);

CREATE INDEX IF NOT EXISTS idx_artlist_queries_sync_due
    ON artlist_queries(expires_at, sync_status);

CREATE TABLE IF NOT EXISTS artlist_query_clips (
    query_id        INTEGER NOT NULL,
    clip_id         TEXT NOT NULL,
    provider_rank   INTEGER NOT NULL DEFAULT 0 CHECK (provider_rank >= 0),
    provider_page   INTEGER NOT NULL DEFAULT 1 CHECK (provider_page >= 1),
    first_seen_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (query_id, clip_id),
    FOREIGN KEY (query_id) REFERENCES artlist_queries(query_id) ON DELETE CASCADE,
    FOREIGN KEY (clip_id) REFERENCES artlist_clips(clip_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_artlist_query_clips_rank
    ON artlist_query_clips(query_id, provider_page, provider_rank, clip_id);

CREATE INDEX IF NOT EXISTS idx_artlist_query_clips_clip
    ON artlist_query_clips(clip_id, query_id);

-- The canonical schema intentionally stops at durable metadata. The
-- node-scraper's better-sqlite3 projection may build an FTS5 index over
-- these columns, while the Go SQLite driver used by the primary database
-- is not compiled with the FTS5 module.
