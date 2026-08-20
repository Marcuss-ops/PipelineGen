import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import Database from 'better-sqlite3';
import { normalizeArtlistClip } from './normalize.js';

const DEFAULT_TTL_MS = 12 * 60 * 60 * 1000;
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
      CREATE TABLE IF NOT EXISTS artlist_clips (
        clip_id TEXT PRIMARY KEY, title TEXT NOT NULL, description TEXT NOT NULL,
        creator TEXT NOT NULL, page_url TEXT NOT NULL, preview_url TEXT NOT NULL,
        thumbnail_url TEXT NOT NULL, duration_ms INTEGER NOT NULL DEFAULT 0,
        width INTEGER NOT NULL DEFAULT 0, height INTEGER NOT NULL DEFAULT 0,
        fps REAL NOT NULL DEFAULT 0, tags_json TEXT NOT NULL,
        categories_json TEXT NOT NULL, metadata_json TEXT NOT NULL,
        download_urls_json TEXT NOT NULL DEFAULT '[]',
        download_urls_expires_at TEXT,
        first_seen_at TEXT NOT NULL, last_seen_at TEXT NOT NULL
      );
      CREATE TABLE IF NOT EXISTS artlist_query_clips (
        query_key TEXT NOT NULL, query TEXT NOT NULL, filters_json TEXT NOT NULL,
        clip_id TEXT NOT NULL, page INTEGER NOT NULL, rank INTEGER NOT NULL,
        discovered_at TEXT NOT NULL, expires_at TEXT NOT NULL,
        PRIMARY KEY (query_key, clip_id),
        FOREIGN KEY (clip_id) REFERENCES artlist_clips(clip_id) ON DELETE CASCADE
      );
      CREATE INDEX IF NOT EXISTS idx_artlist_query_clips_lookup
        ON artlist_query_clips(query_key, page, rank);
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
    ]) {
      try { this.db.exec(statement); } catch (error) {
        if (!String(error.message).includes('duplicate column name')) throw error;
      }
    }
    this.db.exec('DELETE FROM artlist_clips_fts');
    this.backfillCatalog();
  }

  upsertCatalog(clips, request = {}) {
    if (!Array.isArray(clips) || clips.length === 0) return;
    const now = new Date().toISOString();
    const expiresAt = new Date(Date.now() + this.ttlMs).toISOString();
    const upsert = this.db.prepare(`INSERT INTO artlist_clips
      (clip_id,title,description,creator,page_url,preview_url,thumbnail_url,duration_ms,width,height,fps,tags_json,categories_json,metadata_json,download_urls_json,download_urls_expires_at,first_seen_at,last_seen_at)
      VALUES (@clip_id,@title,@description,@creator,@page_url,@preview_url,@thumbnail_url,@duration_ms,@width,@height,@fps,@tags_json,@categories_json,@metadata_json,@download_urls_json,@download_urls_expires_at,@now,@now)
      ON CONFLICT(clip_id) DO UPDATE SET title=excluded.title,description=excluded.description,creator=excluded.creator,page_url=excluded.page_url,preview_url=excluded.preview_url,thumbnail_url=excluded.thumbnail_url,duration_ms=excluded.duration_ms,width=excluded.width,height=excluded.height,fps=excluded.fps,tags_json=excluded.tags_json,categories_json=excluded.categories_json,metadata_json=excluded.metadata_json,
      download_urls_json=CASE WHEN excluded.download_urls_json != '[]' THEN excluded.download_urls_json ELSE artlist_clips.download_urls_json END,
      download_urls_expires_at=CASE WHEN excluded.download_urls_json != '[]' THEN excluded.download_urls_expires_at ELSE artlist_clips.download_urls_expires_at END,
      last_seen_at=excluded.last_seen_at`);
    const linked = [];
    this.db.transaction((items) => {
      for (const [index, raw] of items.entries()) {
        const clip = normalizeArtlistClip(raw);
        if (!clip.clip_id) continue;
        const tags = clip.tags || [], categories = clip.categories || [];
        const downloadUrls = [...new Set([
          ...(Array.isArray(clip.stream_urls) ? clip.stream_urls : []),
          clip.primary_url, clip.preview_url,
        ].map((url) => String(url || '').trim()).filter((url) => /^https?:\/\//i.test(url)))];
        upsert.run({ clip_id: clip.clip_id, title: clip.title || clip.clip_id,
          description: clip.description || '', creator: clip.creator || '',
          page_url: clip.page_url || '', preview_url: clip.preview_url || clip.primary_url || '',
          thumbnail_url: clip.thumbnail_url || '', duration_ms: clip.duration_ms || 0,
          width: clip.width || 0, height: clip.height || 0, fps: clip.fps || 0,
          tags_json: JSON.stringify(tags), categories_json: JSON.stringify(categories),
          metadata_json: JSON.stringify(clip.raw_metadata || {}), download_urls_json: JSON.stringify(downloadUrls),
          download_urls_expires_at: downloadUrls.length ? expiresAt : null, now });
        linked.push({ clipId: clip.clip_id, rank: Number(request.rankOffset || 0) + index });
      }
    })(clips);
    if (request.query && linked.length) {
      const insert = this.db.prepare(`INSERT INTO artlist_query_clips
        (query_key,query,filters_json,clip_id,page,rank,discovered_at,expires_at)
        VALUES (@query_key,@query,@filters_json,@clip_id,@page,@rank,@discovered_at,@expires_at)
        ON CONFLICT(query_key,clip_id) DO UPDATE SET page=excluded.page,rank=excluded.rank,discovered_at=excluded.discovered_at,expires_at=excluded.expires_at`);
      this.db.transaction((rows) => rows.forEach((row) => insert.run({
        query_key: request.query_key || buildSearchQueryKey(request), query: String(request.query),
        filters_json: JSON.stringify(sortObject(request.filters || {})), clip_id: row.clipId,
        page: Number(request.page || 1), rank: row.rank, discovered_at: now, expires_at: expiresAt,
      })))(linked);
    }
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
        stream_urls: JSON.parse(row.download_urls_json || '[]'),
        download_urls: JSON.parse(row.download_urls_json || '[]'),
        duration_ms: row.duration_ms, width: row.width, height: row.height, fps: row.fps,
        tags: JSON.parse(row.tags_json || '[]'), categories: JSON.parse(row.categories_json || '[]'),
        raw_metadata: JSON.parse(row.metadata_json || '{}') })) };
    } catch {
      return { clips: [], total: 0 };
    }
  }

  catalogStats() {
    return this.db.prepare('SELECT COUNT(*) AS unique_clips FROM artlist_clips').get();
  }

  getQueryLinks(query, filters = {}, { maxAgeMs = this.ttlMs } = {}) {
    const queryKey = buildSearchQueryKey({ query, filters });
    const cutoff = new Date(Date.now() - maxAgeMs).toISOString();
    const rows = this.db.prepare(`SELECT qc.clip_id, qc.page, qc.rank, c.download_urls_json
      FROM artlist_query_clips qc JOIN artlist_clips c ON c.clip_id = qc.clip_id
      WHERE qc.query_key = ? AND qc.expires_at >= ? ORDER BY qc.page, qc.rank`).all(queryKey, cutoff);
    return rows.map((row) => ({
      clip_id: row.clip_id,
      page: row.page,
      rank: row.rank,
      download_urls: JSON.parse(row.download_urls_json || '[]'),
    }));
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

  put(cacheKey, request, response, ttlMs = this.ttlMs) {
    const now = Date.now();
    const expiresAt = new Date(now + ttlMs).toISOString();
    const createdAt = new Date(now).toISOString();
    const payload = JSON.stringify(response);

    try {
      this.upsertCatalog(response?.clips || response?.results || [], request);
    } catch (err) {
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
    } catch {
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
