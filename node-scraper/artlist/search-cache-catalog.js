import crypto from 'node:crypto';
import { normalizeArtlistClip } from './normalize.js';
import {
  buildSearchQueryKey,
  PERMANENT_RELATION_EXPIRES_AT,
  sanitizeDurableMetadata,
  sortObject,
  stableCatalogUrl,
} from './search-cache-util.js';

export const searchCacheCatalogMethods = {
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

,
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

,
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

,
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

,
  getCatalogSyncSnapshotStats(syncId) {
    return this.db.prepare(`
      SELECT COUNT(*) AS unique_clip_ids
      FROM artlist_query_snapshot_clips
      WHERE sync_id = ?
    `).get(String(syncId || ''));
  }

,
};
