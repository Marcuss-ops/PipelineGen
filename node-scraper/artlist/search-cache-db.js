import fs from 'node:fs';
import path from 'node:path';
import Database from 'better-sqlite3';
import { PERMANENT_RELATION_EXPIRES_AT } from './search-cache-util.js';

export const searchCacheDbMethods = {
  ensureDatabase() {
    if (this.db) {
      return;
    }

    fs.mkdirSync(path.dirname(this.dbPath), { recursive: true });
    this.db = new Database(this.dbPath);
    this.db.pragma('journal_mode = WAL');
    this.db.pragma('synchronous = NORMAL');
    this.db.pragma('busy_timeout = 5000');
    this.db.exec(`
      CREATE TABLE IF NOT EXISTS artlist_search_cache (
        cache_key TEXT PRIMARY KEY,
        query TEXT NOT NULL,
        filters_json TEXT NOT NULL,
        page INTEGER NOT NULL,
        limit_value INTEGER NOT NULL,
        response_json TEXT NOT NULL,
        created_at TEXT NOT NULL,
        expires_at TEXT NOT NULL
      );
      CREATE INDEX IF NOT EXISTS idx_artlist_search_cache_expires_at
        ON artlist_search_cache (expires_at);
      CREATE TABLE IF NOT EXISTS artlist_queries (
        query_id INTEGER PRIMARY KEY AUTOINCREMENT,
        query TEXT NOT NULL,
        normalized_query TEXT NOT NULL,
        query_key TEXT NOT NULL UNIQUE,
        filters_json TEXT NOT NULL,
        provider_sort_type INTEGER NOT NULL DEFAULT 1,
        provider_total INTEGER NOT NULL DEFAULT 0,
        provider_total_authoritative INTEGER NOT NULL DEFAULT 1,
        result_count INTEGER NOT NULL DEFAULT 0,
        raw_results INTEGER NOT NULL DEFAULT 0,
        unique_clip_ids INTEGER NOT NULL DEFAULT 0,
        page_count INTEGER NOT NULL DEFAULT 0,
        snapshot_complete INTEGER NOT NULL DEFAULT 0,
        first_synced_at TEXT,
        last_synced_at TEXT,
        expires_at TEXT,
        next_refresh_at TEXT,
        last_complete_at TEXT,
        last_complete_sync_id TEXT,
        sync_status TEXT NOT NULL DEFAULT 'never',
        last_error TEXT NOT NULL DEFAULT '',
        last_catalog_sync_id TEXT,
        sync_scope TEXT NOT NULL DEFAULT 'query',
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL
      );
      CREATE INDEX IF NOT EXISTS idx_artlist_queries_sync_due
        ON artlist_queries (expires_at, sync_status);
      CREATE TABLE IF NOT EXISTS artlist_clips (
        clip_id TEXT PRIMARY KEY, title TEXT NOT NULL, description TEXT NOT NULL,
        creator TEXT NOT NULL, page_url TEXT NOT NULL, preview_url TEXT NOT NULL,
        thumbnail_url TEXT NOT NULL, duration_ms INTEGER NOT NULL DEFAULT 0,
        width INTEGER NOT NULL DEFAULT 0, height INTEGER NOT NULL DEFAULT 0,
        fps REAL NOT NULL DEFAULT 0, tags_json TEXT NOT NULL,
        categories_json TEXT NOT NULL, metadata_json TEXT NOT NULL,
        download_urls_json TEXT NOT NULL DEFAULT '[]',
        download_urls_expires_at TEXT,
        first_seen_at TEXT NOT NULL, last_seen_at TEXT NOT NULL,
        active_on_provider INTEGER NOT NULL DEFAULT 1,
        last_catalog_sync_id TEXT
      );
      CREATE TABLE IF NOT EXISTS artlist_query_clips (
        query_key TEXT NOT NULL, query TEXT NOT NULL, filters_json TEXT NOT NULL,
        clip_id TEXT NOT NULL, page INTEGER NOT NULL, rank INTEGER NOT NULL,
        discovered_at TEXT NOT NULL, expires_at TEXT NOT NULL,
        last_seen_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
        active INTEGER NOT NULL DEFAULT 1,
        PRIMARY KEY (query_key, clip_id),
        FOREIGN KEY (clip_id) REFERENCES artlist_clips(clip_id) ON DELETE CASCADE
      );
      CREATE INDEX IF NOT EXISTS idx_artlist_query_clips_lookup
        ON artlist_query_clips(query_key, page, rank);
      CREATE TABLE IF NOT EXISTS artlist_catalog_sync_runs (
        sync_id TEXT PRIMARY KEY,
        query_key TEXT NOT NULL,
        query TEXT NOT NULL,
        filters_json TEXT NOT NULL,
        sync_scope TEXT NOT NULL DEFAULT 'query',
        status TEXT NOT NULL DEFAULT 'running',
        provider_total INTEGER NOT NULL DEFAULT 0,
        pages_expected INTEGER NOT NULL DEFAULT 0,
        pages_completed INTEGER NOT NULL DEFAULT 0,
        raw_results INTEGER NOT NULL DEFAULT 0,
        unique_clip_ids INTEGER NOT NULL DEFAULT 0,
        duplicates INTEGER NOT NULL DEFAULT 0,
        missing INTEGER NOT NULL DEFAULT 0,
        started_at TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        completed_at TEXT,
        last_page INTEGER NOT NULL DEFAULT 0,
        last_error TEXT NOT NULL DEFAULT '',
        new_clip_ids INTEGER NOT NULL DEFAULT 0,
        known_clip_ids INTEGER NOT NULL DEFAULT 0,
        known_streak INTEGER NOT NULL DEFAULT 0,
        stopped_on_known INTEGER NOT NULL DEFAULT 0,
        stop_reason TEXT NOT NULL DEFAULT ''
      );
      CREATE TABLE IF NOT EXISTS artlist_catalog_sync_schedule (
        schedule_key TEXT PRIMARY KEY,
        incremental_interval_ms INTEGER NOT NULL,
        reconciliation_interval_ms INTEGER NOT NULL,
        next_incremental_at TEXT NOT NULL,
        next_reconciliation_at TEXT NOT NULL,
        last_incremental_sync_id TEXT,
        last_reconciliation_sync_id TEXT,
        last_incremental_at TEXT,
        last_reconciliation_at TEXT,
        last_error TEXT NOT NULL DEFAULT '',
        updated_at TEXT NOT NULL
      );
      CREATE INDEX IF NOT EXISTS idx_artlist_catalog_sync_runs_query
        ON artlist_catalog_sync_runs(query_key, started_at DESC);
      CREATE TABLE IF NOT EXISTS artlist_catalog_sync_pages (
        sync_id TEXT NOT NULL,
        page INTEGER NOT NULL,
        raw_results INTEGER NOT NULL DEFAULT 0,
        completed_at TEXT NOT NULL,
        PRIMARY KEY (sync_id, page),
        FOREIGN KEY (sync_id) REFERENCES artlist_catalog_sync_runs(sync_id) ON DELETE CASCADE
      );
      CREATE INDEX IF NOT EXISTS idx_artlist_catalog_sync_pages_sync
        ON artlist_catalog_sync_pages(sync_id, page);
      CREATE TABLE IF NOT EXISTS artlist_query_snapshot_clips (
        sync_id TEXT NOT NULL,
        query_key TEXT NOT NULL,
        clip_id TEXT NOT NULL,
        page INTEGER NOT NULL,
        rank INTEGER NOT NULL,
        discovered_at TEXT NOT NULL,
        PRIMARY KEY (sync_id, clip_id),
        FOREIGN KEY (sync_id) REFERENCES artlist_catalog_sync_runs(sync_id) ON DELETE CASCADE,
        FOREIGN KEY (clip_id) REFERENCES artlist_clips(clip_id) ON DELETE CASCADE
      );
      CREATE INDEX IF NOT EXISTS idx_artlist_query_snapshot_clips_rank
        ON artlist_query_snapshot_clips(sync_id, page, rank);
      CREATE VIRTUAL TABLE IF NOT EXISTS artlist_clips_fts USING fts5(
        clip_id UNINDEXED, title, description, creator, tags, categories
      );
      CREATE INDEX IF NOT EXISTS idx_artlist_clips_duration ON artlist_clips(duration_ms);
      CREATE INDEX IF NOT EXISTS idx_artlist_clips_resolution ON artlist_clips(width, height);
      CREATE INDEX IF NOT EXISTS idx_artlist_clips_fps ON artlist_clips(fps);
      CREATE INDEX IF NOT EXISTS idx_artlist_clips_creator ON artlist_clips(creator);
      CREATE INDEX IF NOT EXISTS idx_artlist_clips_last_seen ON artlist_clips(last_seen_at);
      CREATE TRIGGER IF NOT EXISTS artlist_clips_ai AFTER INSERT ON artlist_clips BEGIN
        INSERT INTO artlist_clips_fts(clip_id,title,description,creator,tags,categories)
        VALUES (new.clip_id,new.title,new.description,new.creator,new.tags_json,new.categories_json);
      END;
      CREATE TRIGGER IF NOT EXISTS artlist_clips_au AFTER UPDATE ON artlist_clips BEGIN
        DELETE FROM artlist_clips_fts WHERE clip_id = old.clip_id;
        INSERT INTO artlist_clips_fts(clip_id,title,description,creator,tags,categories)
        VALUES (new.clip_id,new.title,new.description,new.creator,new.tags_json,new.categories_json);
      END;
      CREATE TRIGGER IF NOT EXISTS artlist_clips_ad AFTER DELETE ON artlist_clips BEGIN
        DELETE FROM artlist_clips_fts WHERE clip_id = old.clip_id;
      END;
    `);
    for (const statement of [
      "ALTER TABLE artlist_clips ADD COLUMN download_urls_json TEXT NOT NULL DEFAULT '[]'",
      'ALTER TABLE artlist_clips ADD COLUMN download_urls_expires_at TEXT',
      'ALTER TABLE artlist_clips ADD COLUMN active_on_provider INTEGER NOT NULL DEFAULT 1',
      'ALTER TABLE artlist_clips ADD COLUMN last_catalog_sync_id TEXT',
      "ALTER TABLE artlist_queries ADD COLUMN raw_results INTEGER NOT NULL DEFAULT 0",
      "ALTER TABLE artlist_queries ADD COLUMN unique_clip_ids INTEGER NOT NULL DEFAULT 0",
      "ALTER TABLE artlist_queries ADD COLUMN page_count INTEGER NOT NULL DEFAULT 0",
      "ALTER TABLE artlist_queries ADD COLUMN snapshot_complete INTEGER NOT NULL DEFAULT 0",
      "ALTER TABLE artlist_queries ADD COLUMN next_refresh_at TEXT",
      "ALTER TABLE artlist_queries ADD COLUMN last_complete_at TEXT",
      "ALTER TABLE artlist_queries ADD COLUMN last_complete_sync_id TEXT",
      "ALTER TABLE artlist_queries ADD COLUMN last_catalog_sync_id TEXT",
      "ALTER TABLE artlist_queries ADD COLUMN sync_scope TEXT NOT NULL DEFAULT 'query'",
      'ALTER TABLE artlist_query_clips ADD COLUMN last_seen_at TEXT',
      'ALTER TABLE artlist_query_clips ADD COLUMN active INTEGER NOT NULL DEFAULT 1',
      'ALTER TABLE artlist_catalog_sync_runs ADD COLUMN new_clip_ids INTEGER NOT NULL DEFAULT 0',
      'ALTER TABLE artlist_catalog_sync_runs ADD COLUMN known_clip_ids INTEGER NOT NULL DEFAULT 0',
      'ALTER TABLE artlist_catalog_sync_runs ADD COLUMN known_streak INTEGER NOT NULL DEFAULT 0',
      'ALTER TABLE artlist_catalog_sync_runs ADD COLUMN stopped_on_known INTEGER NOT NULL DEFAULT 0',
      "ALTER TABLE artlist_catalog_sync_runs ADD COLUMN stop_reason TEXT NOT NULL DEFAULT ''",
    ]) {
      try { this.db.exec(statement); } catch (error) {
        if (!String(error.message).includes('duplicate column name')) throw error;
      }
    }
    this.db.prepare(`
      UPDATE artlist_query_clips
      SET expires_at = ?, last_seen_at = COALESCE(last_seen_at, discovered_at), active = 1
    `).run(PERMANENT_RELATION_EXPIRES_AT);
    this.db.exec(`
      CREATE INDEX IF NOT EXISTS idx_artlist_clips_provider_state
        ON artlist_clips(active_on_provider, last_seen_at DESC);
      CREATE INDEX IF NOT EXISTS idx_artlist_query_clips_active
        ON artlist_query_clips(query_key, active, rank)
    `);
    this.sanitizeDurableCatalogRows();
    this.db.exec('DELETE FROM artlist_clips_fts');
    this.backfillCatalog();
  }


};
