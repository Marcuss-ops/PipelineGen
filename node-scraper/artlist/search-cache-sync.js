import {
  buildSearchQueryKey,
  sanitizeDurableMetadata,
  stableCatalogUrl,
} from './search-cache-util.js';

export const searchCacheSyncMethods = {
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

,
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

,
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

,
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

,
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

,
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

,
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

,
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

,
};
