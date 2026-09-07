import { buildSearchQueryKey, sortObject } from './search-cache-util.js';

export const searchCacheQueryMethods = {
  searchCatalog(query, limit = 24) {
    return this.searchCatalogPage(query, limit, 1).clips;
  }

,
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

,
  catalogStats() {
    return this.db.prepare(`
      SELECT COUNT(*) AS unique_clips,
             SUM(CASE WHEN active_on_provider = 1 THEN 1 ELSE 0 END) AS active_clips,
             SUM(CASE WHEN active_on_provider = 0 THEN 1 ELSE 0 END) AS inactive_clips
      FROM artlist_clips
    `).get();
  }

,
  getInactiveClips(limit = 100) {
    return this.db.prepare(`
      SELECT clip_id, title, page_url, last_seen_at, last_catalog_sync_id
      FROM artlist_clips
      WHERE active_on_provider = 0
      ORDER BY last_seen_at DESC
      LIMIT ?
    `).all(Math.max(1, Math.min(Number(limit) || 100, 1_000)));
  }

,
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

,
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

,
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

,
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

,
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

,
  delete(cacheKey) {
    this.items.delete(cacheKey);
    try {
      this.db.prepare('DELETE FROM artlist_search_cache WHERE cache_key = ?').run(cacheKey);
    } catch {
      // Ignore delete failures; the next read will treat the stale row as a miss.
    }
  }

,
};

