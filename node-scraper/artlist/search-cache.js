import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import Database from 'better-sqlite3';

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
    `);
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
