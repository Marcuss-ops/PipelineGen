import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import Database from 'better-sqlite3';
import { normalizeArtlistClip } from './normalize.js';

const DEFAULT_TTL_MS = 12 * 60 * 60 * 1000;
const DEFAULT_INCREMENTAL_INTERVAL_MS = 24 * 60 * 60 * 1000;
const DEFAULT_RECONCILIATION_INTERVAL_MS = 7 * 24 * 60 * 60 * 1000;
// Query-to-clip knowledge is durable. The legacy expires_at column remains
// for schema compatibility but no longer controls relationship visibility.
const PERMANENT_RELATION_EXPIRES_AT = '9999-12-31T23:59:59.999Z';
const DEFAULT_DB_PATH = process.env.ARTLIST_SEARCH_CACHE_DB || path.join(process.cwd(), 'data', 'artlist-search-cache.sqlite');

let sharedCache = null;

function sortObject(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return value;
  }

  return Object.fromEntries(
    Object.entries(value)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, nested]) => [key, sortObject(nested)]),
  );
}

const TRANSIENT_METADATA_KEY = /(?:download|stream|primary|preview|video|hls|manifest|playlist|signed|token)/i;
const TRANSIENT_MEDIA_URL = /(?:\.(?:m3u8|mp4|mov|webm|mkv)(?:[?#]|$)|\/(?:hls|manifest|playlist|stream|download)(?:[/?#]|$)|(?:[?&](?:token|signature|expires|x-amz-signature)=))/i;

export function isTransientArtlistUrl(value) {
  const url = String(value || '').trim();
  return /^https?:\/\//i.test(url) && TRANSIENT_MEDIA_URL.test(url);
}

function stableCatalogUrl(value) {
  const url = String(value || '').trim();
  return url && !isTransientArtlistUrl(url) ? url : '';
}

function sanitizeDurableMetadata(value, key = '') {
  if (value == null) return value;
  if (typeof value === 'string') return isTransientArtlistUrl(value) ? undefined : value;
  if (Array.isArray(value)) return value.map((item) => sanitizeDurableMetadata(item, key)).filter((item) => item !== undefined);
  if (typeof value !== 'object') return value;
  if (key && TRANSIENT_METADATA_KEY.test(key)) return undefined;

  const result = {};
  for (const [childKey, childValue] of Object.entries(value)) {
    const sanitized = sanitizeDurableMetadata(childValue, childKey);
    if (sanitized !== undefined) result[childKey] = sanitized;
  }
  return result;
}

export function buildSearchCacheKey({ query, filters = {}, page = 1, limit = 24 }) {
  const canonical = JSON.stringify({
    query: String(query || '').trim().toLowerCase(),
    filters: sortObject(filters || {}),
    page: Number.isFinite(Number(page)) ? Number(page) : 1,
    limit: Number.isFinite(Number(limit)) ? Number(limit) : 24,
  });
  return crypto.createHash('sha256').update(canonical).digest('hex');
}

export function buildSearchQueryKey({ query, filters = {} }) {
  return crypto.createHash('sha256').update(JSON.stringify({
    query: String(query || '').trim().toLowerCase(),
    filters: sortObject(filters || {}),
  })).digest('hex');
}

class ArtlistSearchCache {
  constructor(dbPath = DEFAULT_DB_PATH, ttlMs = DEFAULT_TTL_MS) {
    this.dbPath = dbPath;
    this.ttlMs = ttlMs;
    this.items = new Map();
    this.db = null;
    this.ensureDatabase();
  }

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

  upsertCatalog(clips, request = {}) {
    if (!Array.isArray(clips) || clips.length === 0) return;
    const now = new Date().toISOString();
    const upsert = this.db.prepare(`INSERT INTO artlist_clips
      (clip_id,title,description,creator,page_url,preview_url,thumbnail_url,duration_ms,width,height,fps,tags_json,categories_json,metadata_json,download_urls_json,download_urls_expires_at,first_seen_at,last_seen_at,active_on_provider,last_catalog_sync_id)
      VALUES (@clip_id,@title,@description,@creator,@page_url,@preview_url,@thumbnail_url,@duration_ms,@width,@height,@fps,@tags_json,@categories_json,@metadata_json,@download_urls_json,@download_urls_expires_at,@now,@now,@active_on_provider,@last_catalog_sync_id)
      ON CONFLICT(clip_id) DO UPDATE SET
        title=excluded.title,
        description=excluded.description,
        creator=excluded.creator,
        page_url=excluded.page_url,
        preview_url=excluded.preview_url,
        thumbnail_url=excluded.thumbnail_url,
        duration_ms=excluded.duration_ms,
        width=excluded.width,
        height=excluded.height,
        fps=excluded.fps,
        tags_json=excluded.tags_json,
        categories_json=excluded.categories_json,
        metadata_json=excluded.metadata_json,
        download_urls_json='[]',
        download_urls_expires_at=NULL,
        last_seen_at=excluded.last_seen_at,
        active_on_provider=CASE WHEN @mark_provider_active = 1 THEN 1 ELSE artlist_clips.active_on_provider END,
        last_catalog_sync_id=CASE WHEN @last_catalog_sync_id != '' THEN @last_catalog_sync_id ELSE artlist_clips.last_catalog_sync_id END`);
    const linked = [];
    this.db.transaction((items) => {
      for (const [index, raw] of items.entries()) {
        const clip = normalizeArtlistClip(raw);
        if (!clip.clip_id) continue;
        const tags = clip.tags || [], categories = clip.categories || [];
        const stablePageUrl = stableCatalogUrl(clip.page_url);
        const stableThumbnailUrl = stableCatalogUrl(clip.thumbnail_url);
        const durableMetadata = sanitizeDurableMetadata(clip.raw_metadata || {}) || {};
        // Media URLs from search responses may be signed or short-lived. Keep
        // them in the response-TTL cache only; the durable catalog stores
        // stable page metadata and resolves a fresh stream at acquisition.
        upsert.run({ clip_id: clip.clip_id, title: clip.title || clip.clip_id,
          description: clip.description || '', creator: clip.creator || '',
          page_url: stablePageUrl, preview_url: '',
          thumbnail_url: stableThumbnailUrl, duration_ms: clip.duration_ms || 0,
          width: clip.width || 0, height: clip.height || 0, fps: clip.fps || 0,
          tags_json: JSON.stringify(tags), categories_json: JSON.stringify(categories),
          metadata_json: JSON.stringify(durableMetadata), download_urls_json: '[]',
          download_urls_expires_at: null, active_on_provider: 1,
          last_catalog_sync_id: String(request.catalog_sync_id || ''),
          mark_provider_active: request.mark_provider_active ? 1 : 0, now });
        linked.push({ clipId: clip.clip_id, rank: Number(request.rankOffset || 0) + index });
      }
    })(clips);
    if (request.query !== undefined && linked.length) {
      const insert = this.db.prepare(`INSERT INTO artlist_query_clips
        (query_key,query,filters_json,clip_id,page,rank,discovered_at,expires_at,last_seen_at,active)
        VALUES (@query_key,@query,@filters_json,@clip_id,@page,@rank,@discovered_at,@expires_at,@last_seen_at,1)
        ON CONFLICT(query_key,clip_id) DO UPDATE SET
          page=MIN(artlist_query_clips.page, excluded.page),
          rank=MIN(artlist_query_clips.rank, excluded.rank),
          last_seen_at=excluded.last_seen_at,
          active=1`);
      this.db.transaction((rows) => rows.forEach((row) => insert.run({
        query_key: request.query_key || buildSearchQueryKey(request), query: String(request.query),
        filters_json: JSON.stringify(sortObject(request.filters || {})), clip_id: row.clipId,
        page: Number(request.page || 1), rank: row.rank, discovered_at: now,
        expires_at: PERMANENT_RELATION_EXPIRES_AT, last_seen_at: now,
      })))(linked);
    }
    if (request.catalog_sync_id && linked.length) {
      const snapshotInsert = this.db.prepare(`INSERT INTO artlist_query_snapshot_clips
        (sync_id,query_key,clip_id,page,rank,discovered_at)
        VALUES (@sync_id,@query_key,@clip_id,@page,@rank,@discovered_at)
        ON CONFLICT(sync_id,clip_id) DO UPDATE SET
          page=MIN(artlist_query_snapshot_clips.page, excluded.page),
          rank=MIN(artlist_query_snapshot_clips.rank, excluded.rank),
          discovered_at=excluded.discovered_at`);
      this.db.transaction((rows) => rows.forEach((row) => snapshotInsert.run({
        sync_id: String(request.catalog_sync_id),
        query_key: request.query_key || buildSearchQueryKey(request),
        clip_id: row.clipId,
        page: Number(request.page || 1),
        rank: row.rank,
        discovered_at: now,
      })))(linked);
    }
  }

  startCatalogSync({
    query = '',
    filters = {},
    providerSortType = 1,
    providerTotalAuthoritative = true,
    resumeSyncId = '',
    syncScope = 'auto',
  } = {}) {
    const now = new Date().toISOString();
    const normalizedQuery = String(query || '').trim().toLowerCase();
    const queryKey = buildSearchQueryKey({ query: normalizedQuery, filters });
    const filtersJSON = JSON.stringify(sortObject(filters || {}));
    const syncId = crypto.randomUUID();
    const inferredSyncScope = normalizedQuery === ''
      && (!Array.isArray(filters.filterCategories) || filters.filterCategories.length === 0)
      ? 'full_catalog'
      : 'query';
    const resolvedSyncScope = syncScope === 'auto' ? inferredSyncScope : String(syncScope);

    if (String(resumeSyncId || '').trim()) {
      const requestedSyncId = String(resumeSyncId).trim();
      const existing = this.db.prepare(`
        SELECT r.sync_id, r.query_key, r.status, r.sync_scope,
               q.last_catalog_sync_id AS active_sync_id
        FROM artlist_catalog_sync_runs r
        LEFT JOIN artlist_queries q ON q.query_key = r.query_key
        WHERE r.sync_id = ?
      `).get(requestedSyncId);
      if (!existing || existing.query_key !== queryKey || existing.status !== 'running') {
        const error = new Error(`Cannot resume catalog sync ${requestedSyncId}: running checkpoint not found`);
        error.code = 'ARTLIST_RESUME_NOT_FOUND';
        error.syncId = requestedSyncId;
        throw error;
      }
      if (existing.active_sync_id !== requestedSyncId) {
        const error = new Error(`Cannot resume catalog sync ${requestedSyncId}: it is not the active checkpoint`);
        error.code = 'ARTLIST_RESUME_STALE';
        error.syncId = requestedSyncId;
        throw error;
      }
      if (existing.sync_scope !== resolvedSyncScope) {
        const error = new Error(`Cannot resume catalog sync ${requestedSyncId}: sync scope changed`);
        error.code = 'ARTLIST_RESUME_SCOPE_MISMATCH';
        error.syncId = requestedSyncId;
        throw error;
      }
      return queryKey;
    }

    this.db.prepare(`
      INSERT INTO artlist_queries (
        query, normalized_query, query_key, filters_json, provider_sort_type,
        provider_total_authoritative, sync_status, last_error, last_catalog_sync_id,
        sync_scope, first_synced_at, updated_at, created_at
      ) VALUES (
        @query, @normalized_query, @query_key, @filters_json, @provider_sort_type,
        @provider_total_authoritative, 'running', '', @sync_id, @sync_scope,
        @now, @now, @now
      )
      ON CONFLICT(query_key) DO UPDATE SET
        query = excluded.query,
        normalized_query = excluded.normalized_query,
        filters_json = excluded.filters_json,
        provider_sort_type = excluded.provider_sort_type,
        provider_total_authoritative = excluded.provider_total_authoritative,
        sync_status = 'running',
        last_error = '',
        provider_total = CASE WHEN excluded.sync_scope = 'incremental' THEN provider_total ELSE 0 END,
        raw_results = CASE WHEN excluded.sync_scope = 'incremental' THEN raw_results ELSE 0 END,
        unique_clip_ids = CASE WHEN excluded.sync_scope = 'incremental' THEN unique_clip_ids ELSE 0 END,
        page_count = CASE WHEN excluded.sync_scope = 'incremental' THEN page_count ELSE 0 END,
        snapshot_complete = CASE WHEN excluded.sync_scope = 'incremental' THEN snapshot_complete ELSE 0 END,
        last_catalog_sync_id = excluded.last_catalog_sync_id,
        sync_scope = excluded.sync_scope,
        updated_at = excluded.updated_at
    `).run({
      query: String(query || ''),
      normalized_query: normalizedQuery,
      query_key: queryKey,
      filters_json: filtersJSON,
      provider_sort_type: Number(providerSortType) || 1,
      provider_total_authoritative: providerTotalAuthoritative ? 1 : 0,
      sync_id: syncId,
      sync_scope: resolvedSyncScope,
      now,
    });
    this.db.prepare(`
      INSERT INTO artlist_catalog_sync_runs (
        sync_id, query_key, query, filters_json, sync_scope, status, started_at, updated_at
      ) VALUES (@sync_id, @query_key, @query, @filters_json, @sync_scope, 'running', @now, @now)
    `).run({
      sync_id: syncId,
      query_key: queryKey,
      query: String(query || ''),
      filters_json: filtersJSON,
      sync_scope: resolvedSyncScope,
      now,
    });
    // Query-to-clip relations are durable knowledge, not response-cache rows.
    // Keep historical associations; the staged snapshot tables determine the
    // exact result set of the latest complete sync.
    return queryKey;
  }

  recordCatalogSyncPage(queryKey, {
    page = 0,
    pageCount = 0,
    rawResults = 0,
  } = {}) {
    const sync = this.db.prepare(
      'SELECT last_catalog_sync_id AS sync_id FROM artlist_queries WHERE query_key = ?',
    ).get(queryKey);
    if (!sync?.sync_id) return;
    const normalizedPage = Math.max(1, Number(page) || 1);
    const normalizedRawResults = Math.max(0, Number(rawResults) || 0);
    const now = new Date().toISOString();
    this.db.prepare(`
      INSERT INTO artlist_catalog_sync_pages (sync_id, page, raw_results, completed_at)
      VALUES (@sync_id, @page, @raw_results, @now)
      ON CONFLICT(sync_id, page) DO UPDATE SET
        raw_results = excluded.raw_results,
        completed_at = excluded.completed_at
    `).run({
      sync_id: sync.sync_id,
      page: normalizedPage,
      raw_results: normalizedRawResults,
      now,
    });
    this.db.prepare(`
      UPDATE artlist_catalog_sync_runs
      SET pages_expected = CASE WHEN @page_count > 0 THEN @page_count ELSE pages_expected END,
          pages_completed = (SELECT COUNT(*) FROM artlist_catalog_sync_pages WHERE sync_id = @sync_id),
          raw_results = (SELECT COALESCE(SUM(raw_results), 0) FROM artlist_catalog_sync_pages WHERE sync_id = @sync_id),
          last_page = MAX(last_page, @page),
          updated_at = @now
      WHERE sync_id = @sync_id AND status = 'running'
    `).run({
      sync_id: sync.sync_id,
      page: normalizedPage,
      page_count: Math.max(0, Number(pageCount) || 0),
      now,
    });
  }

  getCatalogSyncResumePage(syncId) {
    const pages = this.db.prepare(`
      SELECT page FROM artlist_catalog_sync_pages
      WHERE sync_id = ? ORDER BY page
    `).all(String(syncId || '')).map((row) => Number(row.page));
    const completed = new Set(pages);
    let nextPage = 1;
    while (completed.has(nextPage)) nextPage += 1;
    return nextPage;
  }

  getCatalogSyncSnapshotStats(syncId) {
    return this.db.prepare(`
      SELECT COUNT(*) AS unique_clip_ids
      FROM artlist_query_snapshot_clips
      WHERE sync_id = ?
    `).get(String(syncId || ''));
  }

  completeCatalogSync(queryKey, {
    providerTotal = 0,
    resultCount = 0,
    rawResults = 0,
    uniqueClipIds = 0,
    duplicates = 0,
    missing = 0,
    pageCount = 0,
    expiresAt = null,
    newClipIds = 0,
    knownClipIds = 0,
    knownStreak = 0,
    stoppedOnKnown = false,
    stopReason = '',
  } = {}) {
    const now = new Date().toISOString();
    const effectiveExpiresAt = expiresAt || new Date(Date.now() + this.ttlMs).toISOString();
    const sync = this.db.prepare(`
      SELECT last_catalog_sync_id AS sync_id, sync_scope
      FROM artlist_queries WHERE query_key = ?
    `).get(queryKey);
    if (!sync?.sync_id) return;
    const snapshotComplete = sync.sync_scope !== 'incremental'
      && Number(providerTotal) === Number(uniqueClipIds)
      && Number(missing) === 0;
    const preserveSnapshot = sync.sync_scope === 'incremental';

    this.db.transaction(() => {
      this.db.prepare(`
        UPDATE artlist_queries
        SET provider_total = CASE WHEN @preserve_snapshot = 1 THEN provider_total ELSE @provider_total END,
            result_count = CASE WHEN @preserve_snapshot = 1 THEN result_count ELSE @result_count END,
            raw_results = CASE WHEN @preserve_snapshot = 1 THEN raw_results ELSE @raw_results END,
            unique_clip_ids = CASE WHEN @preserve_snapshot = 1 THEN unique_clip_ids ELSE @unique_clip_ids END,
            page_count = CASE WHEN @preserve_snapshot = 1 THEN page_count ELSE @page_count END,
            snapshot_complete = CASE WHEN @preserve_snapshot = 1 THEN snapshot_complete ELSE @snapshot_complete END,
            last_synced_at = @now,
            expires_at = CASE WHEN @preserve_snapshot = 1 THEN expires_at ELSE @expires_at END,
            next_refresh_at = CASE WHEN @preserve_snapshot = 1 THEN next_refresh_at WHEN @snapshot_complete = 1 THEN @expires_at ELSE next_refresh_at END,
            last_complete_at = CASE WHEN @preserve_snapshot = 1 THEN last_complete_at WHEN @snapshot_complete = 1 THEN @now ELSE last_complete_at END,
            last_complete_sync_id = CASE WHEN @preserve_snapshot = 1 THEN last_complete_sync_id WHEN @snapshot_complete = 1 THEN @sync_id ELSE last_complete_sync_id END,
            sync_status = 'succeeded',
            last_error = '',
            updated_at = @now
        WHERE query_key = @query_key
      `).run({
        query_key: queryKey,
        sync_id: sync.sync_id,
        provider_total: Math.max(0, Number(providerTotal) || 0),
        result_count: Math.max(0, Number(resultCount) || 0),
        raw_results: Math.max(0, Number(rawResults) || 0),
        unique_clip_ids: Math.max(0, Number(uniqueClipIds) || 0),
        page_count: Math.max(0, Number(pageCount) || 0),
        snapshot_complete: snapshotComplete ? 1 : 0,
        preserve_snapshot: preserveSnapshot ? 1 : 0,
        expires_at: effectiveExpiresAt,
        now,
      });
      this.db.prepare(`
        UPDATE artlist_catalog_sync_runs
        SET status = 'succeeded',
            provider_total = @provider_total,
            pages_expected = CASE WHEN @page_count > 0 THEN @page_count ELSE pages_expected END,
            raw_results = @raw_results,
            unique_clip_ids = @unique_clip_ids,
            duplicates = @duplicates,
            missing = @missing,
            new_clip_ids = @new_clip_ids,
            known_clip_ids = @known_clip_ids,
            known_streak = @known_streak,
            stopped_on_known = @stopped_on_known,
            stop_reason = @stop_reason,
            updated_at = @now,
            completed_at = @now,
            last_error = ''
        WHERE sync_id = @sync_id
      `).run({
        sync_id: sync.sync_id,
        provider_total: Math.max(0, Number(providerTotal) || 0),
        page_count: Math.max(0, Number(pageCount) || 0),
        raw_results: Math.max(0, Number(rawResults) || 0),
        unique_clip_ids: Math.max(0, Number(uniqueClipIds) || 0),
        duplicates: Math.max(0, Number(duplicates) || 0),
        missing: Math.max(0, Number(missing) || 0),
        new_clip_ids: Math.max(0, Number(newClipIds) || 0),
        known_clip_ids: Math.max(0, Number(knownClipIds) || 0),
        known_streak: Math.max(0, Number(knownStreak) || 0),
        stopped_on_known: stoppedOnKnown ? 1 : 0,
        stop_reason: String(stopReason || ''),
        now,
      });
      // Only a successful, complete browse-all run can prove that a clip is
      // no longer present at the provider. Query-scoped syncs never deactivate
      // clips merely because they did not match that query.
      if (sync.sync_scope === 'full_catalog'
        && Number(providerTotal) === Number(uniqueClipIds)
        && Number(missing) === 0) {
        this.db.prepare(`
          UPDATE artlist_clips
          SET active_on_provider = 0
          WHERE active_on_provider = 1
            AND (last_catalog_sync_id IS NULL OR last_catalog_sync_id != @sync_id)
        `).run({ sync_id: sync.sync_id });
      }
    })();
  }

  failCatalogSync(queryKey, error) {
    const now = new Date().toISOString();
    const message = String(error?.message || error || 'catalog sync failed').slice(0, 2_000);
    const sync = this.db.prepare(
      'SELECT last_catalog_sync_id AS sync_id FROM artlist_queries WHERE query_key = ?',
    ).get(queryKey);
    this.db.prepare(`
      UPDATE artlist_queries
      SET sync_status = 'failed',
          snapshot_complete = CASE WHEN sync_scope = 'incremental' THEN snapshot_complete ELSE 0 END,
          last_error = @last_error, updated_at = @now
      WHERE query_key = @query_key
    `).run({ query_key: queryKey, last_error: message, now });
    if (sync?.sync_id) {
      this.db.prepare(`
        UPDATE artlist_catalog_sync_runs
        SET status = 'failed', last_error = @last_error, updated_at = @now, completed_at = @now
        WHERE sync_id = @sync_id
      `).run({ sync_id: sync.sync_id, last_error: message, now });
    }
  }

  getCatalogSync(queryKey) {
    return this.db.prepare(`
      SELECT q.query_id, q.query, q.normalized_query, q.query_key, q.filters_json,
             q.provider_sort_type, q.provider_total, q.provider_total_authoritative,
             q.result_count, q.raw_results, q.unique_clip_ids, q.page_count,
             q.snapshot_complete, q.first_synced_at, q.last_synced_at,
             q.expires_at, q.next_refresh_at, q.last_complete_at,
             q.last_complete_sync_id, q.sync_status, q.last_error,
             q.last_catalog_sync_id AS sync_id, q.sync_scope, q.created_at, q.updated_at,
             r.status AS run_status, r.pages_expected, r.pages_completed,
             r.raw_results AS run_raw_results, r.unique_clip_ids AS run_unique_clip_ids,
             r.duplicates, r.missing, r.new_clip_ids, r.known_clip_ids,
             r.known_streak, r.stopped_on_known, r.stop_reason,
             r.started_at AS run_started_at, r.updated_at AS run_updated_at,
             r.completed_at AS run_completed_at, r.last_page, r.last_error AS run_last_error
      FROM artlist_queries q
      LEFT JOIN artlist_catalog_sync_runs r ON r.sync_id = q.last_catalog_sync_id
      WHERE q.query_key = ?
    `).get(queryKey) || null;
  }

  sanitizeDurableCatalogRows() {
    const rows = this.db.prepare(`
      SELECT clip_id, page_url, thumbnail_url, metadata_json
      FROM artlist_clips
    `).all();
    const update = this.db.prepare(`
      UPDATE artlist_clips
      SET page_url = @page_url,
          thumbnail_url = @thumbnail_url,
          preview_url = '',
          download_urls_json = '[]',
          download_urls_expires_at = NULL,
          metadata_json = @metadata_json
      WHERE clip_id = @clip_id
    `);
    this.db.transaction((items) => {
      for (const row of items) {
        let metadata = {};
        try { metadata = JSON.parse(row.metadata_json || '{}'); } catch { /* scrub malformed metadata */ }
        update.run({
          clip_id: row.clip_id,
          page_url: stableCatalogUrl(row.page_url),
          thumbnail_url: stableCatalogUrl(row.thumbnail_url),
          metadata_json: JSON.stringify(sanitizeDurableMetadata(metadata) || {}),
        });
      }
    })(rows);
  }

  getCatalogSyncBySyncId(syncId) {
    return this.db.prepare(`
      SELECT r.sync_id, r.query_key, r.query, r.filters_json,
             r.sync_scope, r.status, r.provider_total,
             r.pages_expected, r.pages_completed, r.raw_results,
             r.unique_clip_ids, r.duplicates, r.missing,
             r.new_clip_ids, r.known_clip_ids, r.known_streak,
             r.stopped_on_known, r.stop_reason,
             r.started_at, r.updated_at, r.completed_at,
             r.last_page, r.last_error,
             q.normalized_query, q.snapshot_complete,
             q.last_complete_at, q.last_complete_sync_id,
             q.sync_status, q.next_refresh_at
      FROM artlist_catalog_sync_runs r
      LEFT JOIN artlist_queries q ON q.query_key = r.query_key
      WHERE r.sync_id = ?
    `).get(String(syncId || '')) || null;
  }

  hasCompleteFullCatalog({ filters = { sortType: 1 } } = {}) {
    const queryKey = buildSearchQueryKey({ query: '', filters });
    const row = this.db.prepare(`
      SELECT q.snapshot_complete, r.status, r.sync_scope
      FROM artlist_queries q
      JOIN artlist_catalog_sync_runs r ON r.sync_id = q.last_complete_sync_id
      WHERE q.query_key = ? AND q.last_complete_sync_id IS NOT NULL
    `).get(queryKey);
    return Boolean(row && Number(row.snapshot_complete) === 1
      && row.status === 'succeeded' && row.sync_scope === 'full_catalog');
  }

  findKnownCatalogClipIds(clipsOrIds = []) {
    const values = (Array.isArray(clipsOrIds) ? clipsOrIds : [clipsOrIds])
      .map((clip) => typeof clip === 'object' ? clip?.clip_id || clip?.id : clip)
      .map((id) => String(id || '').trim())
      .filter(Boolean);
    const ids = [...new Set(values)];
    if (!ids.length) return [];
    const placeholders = ids.map(() => '?').join(',');
    return this.db.prepare(`SELECT clip_id FROM artlist_clips WHERE clip_id IN (${placeholders})`)
      .all(...ids).map((row) => row.clip_id);
  }

  configureCatalogSyncSchedule({
    scheduleKey = 'default',
    incrementalIntervalMs = DEFAULT_INCREMENTAL_INTERVAL_MS,
    reconciliationIntervalMs = DEFAULT_RECONCILIATION_INTERVAL_MS,
    now = Date.now(),
  } = {}) {
    const key = String(scheduleKey || 'default');
    const current = this.db.prepare(
      'SELECT * FROM artlist_catalog_sync_schedule WHERE schedule_key = ?',
    ).get(key);
    const nowMs = Number(now) || Date.now();
    const incrementalMs = Math.max(60_000, Number(incrementalIntervalMs) || DEFAULT_INCREMENTAL_INTERVAL_MS);
    const reconciliationMs = Math.max(60_000, Number(reconciliationIntervalMs) || DEFAULT_RECONCILIATION_INTERVAL_MS);
    const nowISO = new Date(nowMs).toISOString();
    const nextIncremental = current?.next_incremental_at || new Date(nowMs + incrementalMs).toISOString();
    const nextReconciliation = current?.next_reconciliation_at || new Date(nowMs + reconciliationMs).toISOString();
    this.db.prepare(`
      INSERT INTO artlist_catalog_sync_schedule (
        schedule_key, incremental_interval_ms, reconciliation_interval_ms,
        next_incremental_at, next_reconciliation_at, updated_at
      ) VALUES (@schedule_key, @incremental_interval_ms, @reconciliation_interval_ms,
        @next_incremental_at, @next_reconciliation_at, @updated_at)
      ON CONFLICT(schedule_key) DO UPDATE SET
        incremental_interval_ms = excluded.incremental_interval_ms,
        reconciliation_interval_ms = excluded.reconciliation_interval_ms,
        updated_at = excluded.updated_at
    `).run({
      schedule_key: key,
      incremental_interval_ms: incrementalMs,
      reconciliation_interval_ms: reconciliationMs,
      next_incremental_at: nextIncremental,
      next_reconciliation_at: nextReconciliation,
      updated_at: nowISO,
    });
    return this.getCatalogSyncSchedule(key);
  }

  getCatalogSyncSchedule(scheduleKey = 'default') {
    return this.db.prepare(
      'SELECT * FROM artlist_catalog_sync_schedule WHERE schedule_key = ?',
    ).get(String(scheduleKey || 'default')) || null;
  }

  claimDueCatalogSyncSchedule({ scheduleKey = 'default', now = Date.now() } = {}) {
    const schedule = this.getCatalogSyncSchedule(scheduleKey) || this.configureCatalogSyncSchedule({ scheduleKey, now });
    const nowMs = Number(now) || Date.now();
    const dueIncremental = Date.parse(schedule.next_incremental_at) <= nowMs;
    const dueReconciliation = Date.parse(schedule.next_reconciliation_at) <= nowMs;
    if (!dueIncremental && !dueReconciliation) return { incremental: false, reconciliation: false, schedule };

    const nowISO = new Date(nowMs).toISOString();
    const nextIncremental = dueReconciliation || dueIncremental
      ? new Date(nowMs + schedule.incremental_interval_ms).toISOString()
      : schedule.next_incremental_at;
    const nextReconciliation = dueReconciliation
      ? new Date(nowMs + schedule.reconciliation_interval_ms).toISOString()
      : schedule.next_reconciliation_at;
    this.db.prepare(`
      UPDATE artlist_catalog_sync_schedule
      SET next_incremental_at = @next_incremental_at,
          next_reconciliation_at = @next_reconciliation_at,
          updated_at = @updated_at,
          last_error = ''
      WHERE schedule_key = @schedule_key
    `).run({
      schedule_key: String(scheduleKey || 'default'),
      next_incremental_at: nextIncremental,
      next_reconciliation_at: nextReconciliation,
      updated_at: nowISO,
    });
    return {
      incremental: dueIncremental && !dueReconciliation,
      reconciliation: dueReconciliation,
      schedule: this.getCatalogSyncSchedule(scheduleKey),
    };
  }

  recordCatalogSyncScheduleRun({ scheduleKey = 'default', kind, syncId, error = null, now = Date.now() } = {}) {
    if (kind !== 'incremental' && kind !== 'reconciliation') return;
    const nowISO = new Date(Number(now) || Date.now()).toISOString();
    const column = kind === 'incremental' ? 'incremental' : 'reconciliation';
    this.db.prepare(`
      UPDATE artlist_catalog_sync_schedule
      SET last_${column}_sync_id = @sync_id,
          last_${column}_at = @now,
          last_error = @last_error,
          updated_at = @now
      WHERE schedule_key = @schedule_key
    `).run({
      schedule_key: String(scheduleKey || 'default'),
      sync_id: String(syncId || ''),
      now: nowISO,
      last_error: error ? String(error.message || error).slice(0, 2_000) : '',
    });
  }

  backfillCatalog() {
    try {
      const rows = this.db.prepare('SELECT response_json FROM artlist_search_cache').all();
      const clips = [];
      for (const row of rows) {
        try {
          const response = JSON.parse(row.response_json);
          clips.push(...(Array.isArray(response?.clips) ? response.clips : response?.results || []));
        } catch { /* ignore malformed snapshot */ }
      }
      this.upsertCatalog(clips);
    } catch (err) {
      console.warn(`[artlist-cache] catalog backfill skipped: ${err.message}`);
    }
  }

  searchCatalog(query, limit = 24) {
    return this.searchCatalogPage(query, limit, 1).clips;
  }

  searchCatalogPage(query, limit = 24, page = 1) {
    const tokens = String(query || '').toLowerCase().match(/[a-z0-9]{2,}/g) || [];
    if (!tokens.length) return { clips: [], total: 0 };
    const match = tokens.map((token) => `${token.replaceAll('"', '')}*`).join(' OR ');
    const pageLimit = Math.max(1, Math.min(Number(limit) || 24, 50));
    const offset = Math.max(0, (Number(page) - 1) * pageLimit);
    try {
      const total = this.db.prepare(`SELECT COUNT(*) AS total FROM artlist_clips c
        JOIN artlist_clips_fts f ON f.clip_id = c.clip_id
        WHERE artlist_clips_fts MATCH ?`).get(match).total;
      const rows = this.db.prepare(`SELECT c.* FROM artlist_clips c
        JOIN artlist_clips_fts f ON f.clip_id = c.clip_id
        WHERE artlist_clips_fts MATCH ?
        ORDER BY bm25(artlist_clips_fts, 0, 10, 3, 2, 8, 2), c.last_seen_at DESC LIMIT ? OFFSET ?`)
        .all(match, pageLimit, offset);
      return { total, clips: rows.map((row) => ({ provider: 'artlist', clip_id: row.clip_id, id: row.clip_id,
        title: row.title, name: row.title, description: row.description, creator: row.creator,
        page_url: row.page_url, clip_page_url: row.page_url, preview_url: row.preview_url,
        primary_url: row.preview_url, thumbnail_url: row.thumbnail_url,
        // A fresh media URL must be resolved when the clip is acquired.
        stream_urls: [],
        download_urls: [],
        duration_ms: row.duration_ms, width: row.width, height: row.height, fps: row.fps,
        tags: JSON.parse(row.tags_json || '[]'), categories: JSON.parse(row.categories_json || '[]'),
        raw_metadata: JSON.parse(row.metadata_json || '{}') })) };
    } catch {
      return { clips: [], total: 0 };
    }
  }

  catalogStats() {
    return this.db.prepare(`
      SELECT COUNT(*) AS unique_clips,
             SUM(CASE WHEN active_on_provider = 1 THEN 1 ELSE 0 END) AS active_clips,
             SUM(CASE WHEN active_on_provider = 0 THEN 1 ELSE 0 END) AS inactive_clips
      FROM artlist_clips
    `).get();
  }

  getInactiveClips(limit = 100) {
    return this.db.prepare(`
      SELECT clip_id, title, page_url, last_seen_at, last_catalog_sync_id
      FROM artlist_clips
      WHERE active_on_provider = 0
      ORDER BY last_seen_at DESC
      LIMIT ?
    `).all(Math.max(1, Math.min(Number(limit) || 100, 1_000)));
  }

  getQueryLinks(query, filters = {}, { maxAgeMs = this.ttlMs } = {}) {
    const queryKey = buildSearchQueryKey({ query, filters });
    void maxAgeMs;
    const completeSnapshot = this.db.prepare(`
      SELECT last_complete_sync_id AS sync_id
      FROM artlist_queries
      WHERE query_key = ? AND last_complete_sync_id IS NOT NULL
    `).get(queryKey);
    if (completeSnapshot?.sync_id) {
      const rows = this.db.prepare(`
        SELECT sc.clip_id, sc.page, sc.rank
        FROM artlist_query_snapshot_clips sc
        JOIN artlist_clips c ON c.clip_id = sc.clip_id
        WHERE sc.sync_id = ?
        ORDER BY sc.page, sc.rank
      `).all(completeSnapshot.sync_id);
      return rows.map((row) => ({
        clip_id: row.clip_id,
        page: row.page,
        provider_page: row.page,
        rank: row.rank,
        provider_rank: row.rank,
        download_urls: [],
      }));
    }

    // maxAgeMs is retained for API compatibility, but deliberately ignored:
    // query-to-clip knowledge does not expire with HTTP responses.
    const rows = this.db.prepare(`SELECT qc.clip_id, qc.page, qc.rank
      FROM artlist_query_clips qc JOIN artlist_clips c ON c.clip_id = qc.clip_id
      WHERE qc.query_key = ? AND qc.active = 1 ORDER BY qc.page, qc.rank`).all(queryKey);
    return rows.map((row) => ({
      clip_id: row.clip_id,
      page: row.page,
      provider_page: row.page,
      rank: row.rank,
      provider_rank: row.rank,
      download_urls: [],
    }));
  }

  getQuerySnapshot(query, filters = {}) {
    const queryKey = buildSearchQueryKey({ query, filters });
    const state = this.db.prepare(`
      SELECT q.query, q.normalized_query, q.query_key, q.filters_json,
             q.last_complete_sync_id AS sync_id, q.last_complete_at,
             q.snapshot_complete, r.provider_total, r.raw_results,
             r.unique_clip_ids, r.duplicates, r.missing,
             r.pages_expected AS page_count, r.status
      FROM artlist_queries q
      JOIN artlist_catalog_sync_runs r ON r.sync_id = q.last_complete_sync_id
      WHERE q.query_key = ? AND q.last_complete_sync_id IS NOT NULL
    `).get(queryKey);
    if (!state || state.status !== 'succeeded') return null;

    const clips = this.db.prepare(`
      SELECT sc.clip_id, sc.page, sc.rank, c.title, c.description, c.creator,
             c.page_url, c.thumbnail_url, c.duration_ms, c.width, c.height, c.fps,
             c.tags_json, c.categories_json, c.metadata_json
      FROM artlist_query_snapshot_clips sc
      JOIN artlist_clips c ON c.clip_id = sc.clip_id
      WHERE sc.sync_id = ?
      ORDER BY sc.page, sc.rank
    `).all(state.sync_id).map((row) => ({
      provider: 'artlist',
      clip_id: row.clip_id,
      id: row.clip_id,
      title: row.title,
      name: row.title,
      description: row.description,
      creator: row.creator,
      page_url: row.page_url,
      clip_page_url: row.page_url,
      preview_url: '',
      primary_url: '',
      thumbnail_url: row.thumbnail_url,
      duration_ms: row.duration_ms,
      width: row.width,
      height: row.height,
      fps: row.fps,
      tags: JSON.parse(row.tags_json || '[]'),
      categories: JSON.parse(row.categories_json || '[]'),
      raw_metadata: JSON.parse(row.metadata_json || '{}'),
      page: row.page,
      provider_page: row.page,
      rank: row.rank,
      provider_rank: row.rank,
      download_urls: [],
    }));
    return {
      ...state,
      complete: state.provider_total === state.unique_clip_ids
        && state.missing === 0
        && clips.length === state.unique_clip_ids,
      clips,
    };
  }

  get(cacheKey) {
    const now = Date.now();

    const cached = this.items.get(cacheKey);
    if (cached && cached.expiresAt > now) {
      return cached.response;
    }
    if (cached) {
      this.items.delete(cacheKey);
    }

    let row;
    try {
      row = this.db.prepare(
        `SELECT response_json, expires_at FROM artlist_search_cache WHERE cache_key = ?`,
      ).get(cacheKey);
    } catch {
      return null;
    }
    if (!row) {
      return null;
    }

    const expiresAt = Date.parse(row.expires_at);
    if (!Number.isFinite(expiresAt) || expiresAt <= now) {
      this.delete(cacheKey);
      return null;
    }

    let response;
    try {
      response = JSON.parse(row.response_json);
    } catch {
      this.delete(cacheKey);
      return null;
    }

    this.items.set(cacheKey, {
      response,
      expiresAt,
    });

    return response;
  }

  getRelated(query, { maxAgeMs = 14 * 24 * 60 * 60 * 1000 } = {}) {
    const wanted = new Set(String(query || '').toLowerCase().match(/[a-z0-9]{4,}/g) || []);
    if (wanted.size === 0) return null;
    const cutoff = new Date(Date.now() - maxAgeMs).toISOString();
    let rows;
    try {
      rows = this.db.prepare(
        `SELECT query, response_json, expires_at FROM artlist_search_cache
         WHERE expires_at < @now AND expires_at >= @cutoff
         ORDER BY expires_at DESC`,
      ).all({ now: new Date().toISOString(), cutoff });
    } catch {
      return null;
    }

    let best = null;
    for (const row of rows) {
      let response;
      try {
        response = JSON.parse(row.response_json);
      } catch {
        continue;
      }
      const clips = Array.isArray(response?.clips) ? response.clips : response?.results;
      if (!Array.isArray(clips) || clips.length === 0) continue;
      const cachedTokens = new Set(String(row.query || '').toLowerCase().match(/[a-z0-9]{4,}/g) || []);
      const overlap = [...wanted].filter((token) => cachedTokens.has(token)).length;
      const clipText = JSON.stringify(clips).toLowerCase();
      const contentOverlap = [...wanted].filter((token) => clipText.includes(token)).length;
      const score = overlap * 10 + contentOverlap * 3;
      if (overlap === 0 || (best && score <= best.score)) continue;
      best = { overlap, score, query: row.query, response };
    }
    return best;
  }

  put(cacheKey, request, response, ttlMs = this.ttlMs, { strict = false } = {}) {
    const now = Date.now();
    const expiresAt = new Date(now + ttlMs).toISOString();
    const createdAt = new Date(now).toISOString();
    const payload = JSON.stringify(response);

    try {
      this.upsertCatalog(response?.clips || response?.results || [], {
        ...request,
        mark_provider_active: request.mark_provider_active !== false,
      });
    } catch (err) {
      if (strict) throw err;
      console.warn(`[artlist-cache] catalog upsert skipped: ${err.message}`);
    }

    this.items.set(cacheKey, {
      response,
      expiresAt: Date.parse(expiresAt),
    });

    try {
      this.db.prepare(`
        INSERT INTO artlist_search_cache (
          cache_key, query, filters_json, page, limit_value, response_json, created_at, expires_at
        ) VALUES (
          @cache_key, @query, @filters_json, @page, @limit_value, @response_json, @created_at, @expires_at
        )
        ON CONFLICT(cache_key) DO UPDATE SET
          query = excluded.query,
          filters_json = excluded.filters_json,
          page = excluded.page,
          limit_value = excluded.limit_value,
          response_json = excluded.response_json,
          created_at = excluded.created_at,
          expires_at = excluded.expires_at
      `).run({
        cache_key: cacheKey,
        query: String(request.query || ''),
        filters_json: JSON.stringify(sortObject(request.filters || {})),
        page: Number(request.page || 1),
        limit_value: Number(request.limit || 24),
        response_json: payload,
        created_at: createdAt,
        expires_at: expiresAt,
      });
    } catch (error) {
      if (strict) throw error;
      // Cache writes are best-effort. Search results remain valid if
      // SQLite is temporarily unavailable or locked.
    }
  }

  delete(cacheKey) {
    this.items.delete(cacheKey);
    try {
      this.db.prepare('DELETE FROM artlist_search_cache WHERE cache_key = ?').run(cacheKey);
    } catch {
      // Ignore delete failures; the next read will treat the stale row as a miss.
    }
  }
}

export function getSearchCache() {
  if (!sharedCache) {
    sharedCache = new ArtlistSearchCache();
  }
  return sharedCache;
}

export function createSearchCache(dbPath, ttlMs) {
  return new ArtlistSearchCache(dbPath, ttlMs);
}
