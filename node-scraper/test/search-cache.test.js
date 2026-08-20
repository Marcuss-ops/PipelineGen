import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import {
  buildSearchCacheKey,
  createSearchCache,
  isTransientArtlistUrl,
} from '../artlist/search-cache.js';

describe('search cache', () => {
  test('buildSearchCacheKey is stable across filter order', () => {
    const a = buildSearchCacheKey({
      query: 'Business Team',
      filters: { resolution: '4k', orientation: 'horizontal' },
      page: 1,
      limit: 20,
    });
    const b = buildSearchCacheKey({
      query: 'business team',
      filters: { orientation: 'horizontal', resolution: '4k' },
      page: 1,
      limit: 20,
    });
    assert.equal(a, b);
  });

  test('cache round-trips through SQLite', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-cache-'));
    const dbPath = path.join(dir, 'cache.sqlite');
    const cache = createSearchCache(dbPath, 60_000);

    const request = {
      query: 'business team office',
      filters: { resolution: '4k' },
      page: 1,
      limit: 20,
    };
    const response = {
      ok: true,
      provider: 'artlist',
      query: request.query,
      cache_hit: false,
      page: request.page,
      limit: request.limit,
      source: 'browser_api',
      results: [{ clip_id: '1', title: 'Clip 1' }],
      clips: [{ clip_id: '1', title: 'Clip 1' }],
    };

    const key = buildSearchCacheKey(request);
    assert.equal(cache.get(key), null);
    cache.put(key, request, response);

    const cached = cache.get(key);
    assert.ok(cached);
    assert.equal(cached.query, 'business team office');
    assert.equal(cached.results[0].clip_id, '1');
  });

  test('related lookup returns only recent non-empty Artlist results', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-related-cache-'));
    const cache = createSearchCache(path.join(dir, 'cache.sqlite'), 60_000);
    const request = { query: 'maya ruins', filters: {}, page: 1, limit: 3 };
    const response = {
      ok: true, provider: 'artlist', query: request.query,
      clips: [{ clip_id: '346928', clip_page_url: 'https://artlist.io/clip/346928' }],
    };
    cache.put(buildSearchCacheKey(request), request, response, -1);

    const related = cache.getRelated('ancient Maya temples jungle aerial cinematic', {
      maxAgeMs: 24 * 60 * 60 * 1000,
    });
    assert.equal(related?.query, 'maya ruins');
    assert.equal(related?.response.clips[0].clip_id, '346928');
  });

  test('catalog indexes clip metadata and searches without provider access', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-catalog-'));
    const cache = createSearchCache(path.join(dir, 'cache.sqlite'), 60_000);
    cache.put('catalog-seed', { query: 'seed', page: 1, limit: 3 }, {
      clips: [
        { id: '1', title: 'Electricity pylons', description: 'High voltage grid', tags: ['power'] },
        { id: '2', title: 'Ocean waves', tags: ['nature'] },
      ],
    });
    const results = cache.searchCatalog('electricity', 50);
    assert.equal(results.length, 1);
    assert.equal(results[0].clip_id, '1');
    assert.equal(cache.catalogStats().unique_clips, 2);
  });

  test('tracks an empty-query catalog sync and associates deduplicated clips', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-catalog-sync-'));
    const cache = createSearchCache(path.join(dir, 'cache.sqlite'), 60_000);
    const filters = { sortType: 1 };
    const queryKey = cache.startCatalogSync({
      query: '',
      filters,
      providerSortType: 1,
      providerTotalAuthoritative: true,
    });
    const clips = [
      { id: '847392', title: 'Boxing clip', tags: ['boxing'] },
      { id: '847392', title: 'Updated boxing clip', tags: ['boxing', 'training'] },
    ];
    cache.put('empty-page-1', {
      query: '', filters, page: 1, limit: 50, query_key: queryKey,
    }, { clips }, 60_000, { strict: true });
    cache.completeCatalogSync(queryKey, { providerTotal: 2, resultCount: 1 });

    const state = cache.getCatalogSync(queryKey);
    assert.equal(state.sync_status, 'succeeded');
    assert.equal(state.provider_total, 2);
    assert.equal(state.result_count, 1);
    assert.equal(cache.catalogStats().unique_clips, 1);
    assert.equal(cache.getQueryLinks('', filters).length, 1);
    assert.ok(cache.getCatalogSync(queryKey).expires_at);
  });

  test('stores a complete query snapshot with provider ranking and metrics', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-query-snapshot-'));
    const cache = createSearchCache(path.join(dir, 'cache.sqlite'), 1);
    const filters = { sortType: 1 };
    const queryKey = cache.startCatalogSync({
      query: 'electricity meter',
      filters,
      providerSortType: 1,
      providerTotalAuthoritative: true,
    });
    const state = cache.getCatalogSync(queryKey);
    const clips = [
      { id: 'clip-a', title: 'Electricity meter first' },
      { id: 'clip-b', title: 'Electricity meter second' },
    ];
    cache.put('snapshot-page-1', {
      query: 'electricity meter', filters, page: 1, limit: 50, query_key: queryKey,
      catalog_sync_id: state.sync_id,
    }, { clips }, 1, { strict: true });
    cache.recordCatalogSyncPage(queryKey, { page: 1, pageCount: 1, rawResults: 2 });
    cache.completeCatalogSync(queryKey, {
      providerTotal: 2,
      resultCount: 2,
      rawResults: 2,
      uniqueClipIds: 2,
      duplicates: 0,
      missing: 0,
      pageCount: 1,
    });

    const snapshot = cache.getQuerySnapshot('electricity meter', filters);
    assert.ok(snapshot);
    assert.equal(snapshot.complete, true);
    assert.equal(snapshot.provider_total, 2);
    assert.equal(snapshot.raw_results, 2);
    assert.equal(snapshot.unique_clip_ids, 2);
    assert.equal(snapshot.page_count, 1);
    assert.deepEqual(snapshot.clips.map((clip) => clip.clip_id), ['clip-a', 'clip-b']);
    assert.deepEqual(cache.getQueryLinks('electricity meter', filters).map((clip) => clip.provider_rank), [0, 1]);
    assert.ok(snapshot.last_complete_at);
    assert.ok(cache.getCatalogSync(queryKey).next_refresh_at);
  });

  test('is idempotent across identical complete syncs', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-idempotent-sync-'));
    const cache = createSearchCache(path.join(dir, 'cache.sqlite'), 60_000);
    const query = 'electricity meter';
    const filters = { sortType: 1 };
    const clips = [
      { id: 'idempotent-a', title: 'Electricity meter A' },
      { id: 'idempotent-b', title: 'Electricity meter B' },
    ];

    for (let run = 0; run < 2; run += 1) {
      const queryKey = cache.startCatalogSync({ query, filters });
      const state = cache.getCatalogSync(queryKey);
      cache.put(`idempotent-page-${run}`, {
        query, filters, page: 1, limit: 50, query_key: queryKey,
        catalog_sync_id: state.sync_id,
      }, { clips }, 60_000, { strict: true });
      cache.recordCatalogSyncPage(queryKey, { page: 1, pageCount: 1, rawResults: 2 });
      cache.completeCatalogSync(queryKey, {
        providerTotal: 2,
        resultCount: 2,
        rawResults: 2,
        uniqueClipIds: 2,
        duplicates: 0,
        missing: 0,
        pageCount: 1,
      });
    }

    assert.equal(cache.catalogStats().unique_clips, 2);
    assert.equal(cache.getQueryLinks(query, filters).length, 2);
    assert.deepEqual(cache.getQuerySnapshot(query, filters).clips.map((clip) => clip.clip_id), [
      'idempotent-a', 'idempotent-b',
    ]);
  });

  test('preserves the last complete query snapshot after a later failed sync', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-query-snapshot-failure-'));
    const cache = createSearchCache(path.join(dir, 'cache.sqlite'), 60_000);
    const filters = { sortType: 1 };
    const queryKey = cache.startCatalogSync({ query: 'electricity meter', filters });
    const firstState = cache.getCatalogSync(queryKey);
    cache.put('snapshot-good', {
      query: 'electricity meter', filters, page: 1, limit: 50, query_key: queryKey,
      catalog_sync_id: firstState.sync_id,
    }, { clips: [{ id: 'stable-1', title: 'Stable electricity clip' }] }, 60_000, { strict: true });
    cache.completeCatalogSync(queryKey, {
      providerTotal: 1, resultCount: 1, rawResults: 1, uniqueClipIds: 1,
    });

    const retryKey = cache.startCatalogSync({ query: 'electricity meter', filters });
    cache.failCatalogSync(retryKey, new Error('provider unavailable'));

    const snapshot = cache.getQuerySnapshot('electricity meter', filters);
    assert.equal(snapshot.complete, true);
    assert.deepEqual(snapshot.clips.map((clip) => clip.clip_id), ['stable-1']);
    assert.equal(cache.getCatalogSync(retryKey).sync_status, 'failed');
  });

  test('resumes a running sync from the first incomplete page without replacing the last snapshot', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-sync-resume-'));
    const cache = createSearchCache(path.join(dir, 'cache.sqlite'), 60_000);
    const filters = { sortType: 1 };
    const query = 'electricity meter';

    const firstKey = cache.startCatalogSync({ query, filters });
    const firstState = cache.getCatalogSync(firstKey);
    cache.put('resume-first-complete', {
      query, filters, page: 1, limit: 50, query_key: firstKey,
      catalog_sync_id: firstState.sync_id,
    }, { clips: [{ id: 'old-1', title: 'Old complete clip' }] }, 60_000, { strict: true });
    cache.recordCatalogSyncPage(firstKey, { page: 1, pageCount: 1, rawResults: 1 });
    cache.completeCatalogSync(firstKey, {
      providerTotal: 1, resultCount: 1, rawResults: 1, uniqueClipIds: 1,
    });
    const previousCompleteSyncId = cache.getCatalogSync(firstKey).last_complete_sync_id;

    const interruptedKey = cache.startCatalogSync({ query, filters });
    const interruptedState = cache.getCatalogSync(interruptedKey);
    cache.put('resume-page-1', {
      query, filters, page: 1, limit: 50, query_key: interruptedKey,
      catalog_sync_id: interruptedState.sync_id,
    }, { clips: [{ id: 'new-1', title: 'New page one' }] }, 60_000, { strict: true });
    cache.recordCatalogSyncPage(interruptedKey, { page: 1, pageCount: 2, rawResults: 1 });

    assert.equal(cache.getCatalogSyncResumePage(interruptedState.sync_id), 2);
    assert.equal(cache.getCatalogSync(interruptedKey).last_complete_sync_id, previousCompleteSyncId);
    assert.deepEqual(cache.getQuerySnapshot(query, filters).clips.map((clip) => clip.clip_id), ['old-1']);

    const resumedKey = cache.startCatalogSync({
      query,
      filters,
      resumeSyncId: interruptedState.sync_id,
    });
    assert.equal(resumedKey, interruptedKey);
    assert.equal(cache.getCatalogSyncResumePage(interruptedState.sync_id), 2);

    cache.put('resume-page-2', {
      query, filters, page: 2, limit: 50, query_key: resumedKey,
      catalog_sync_id: interruptedState.sync_id,
    }, { clips: [{ id: 'new-2', title: 'New page two' }] }, 60_000, { strict: true });
    cache.recordCatalogSyncPage(resumedKey, { page: 2, pageCount: 2, rawResults: 1 });
    cache.completeCatalogSync(resumedKey, {
      providerTotal: 2, resultCount: 2, rawResults: 2, uniqueClipIds: 2,
      pageCount: 2,
    });

    const finalSnapshot = cache.getQuerySnapshot(query, filters);
    assert.equal(finalSnapshot.complete, true);
    assert.deepEqual(finalSnapshot.clips.map((clip) => clip.clip_id), ['new-1', 'new-2']);
    assert.equal(cache.getCatalogSync(resumedKey).last_complete_sync_id, interruptedState.sync_id);
  });

  test('records a failed catalog sync with a bounded error', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-catalog-failure-'));
    const cache = createSearchCache(path.join(dir, 'cache.sqlite'), 60_000);
    const queryKey = cache.startCatalogSync({ query: '', filters: { sortType: 1 } });
    cache.failCatalogSync(queryKey, new Error('page 42 failed: provider unavailable'));

    const state = cache.getCatalogSync(queryKey);
    assert.equal(state.sync_status, 'failed');
    assert.match(state.last_error, /page 42 failed/);
  });

  test('tracks provider activity across complete full-catalog syncs', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-provider-state-'));
    const cache = createSearchCache(path.join(dir, 'cache.sqlite'), 60_000);
    const filters = { sortType: 1 };
    const firstQueryKey = cache.startCatalogSync({ query: '', filters });
    const firstState = cache.getCatalogSync(firstQueryKey);
    assert.equal(firstState.sync_scope, 'full_catalog');
    assert.match(firstState.sync_id, /^[0-9a-f-]{36}$/);

    cache.put('full-page-1', {
      query: '', filters, page: 1, limit: 50, query_key: firstQueryKey,
      catalog_sync_id: firstState.sync_id,
    }, { clips: [{ id: 'active-1', title: 'Current clip' }] }, 60_000, { strict: true });
    cache.recordCatalogSyncPage(firstQueryKey, { page: 1, pageCount: 1, rawResults: 1 });
    cache.completeCatalogSync(firstQueryKey, {
      providerTotal: 1, resultCount: 1, rawResults: 1, uniqueClipIds: 1,
      pageCount: 1,
    });

    const secondQueryKey = cache.startCatalogSync({ query: '', filters });
    const secondState = cache.getCatalogSync(secondQueryKey);
    cache.put('full-page-2', {
      query: '', filters, page: 1, limit: 50, query_key: secondQueryKey,
      catalog_sync_id: secondState.sync_id,
    }, { clips: [{ id: 'active-2', title: 'New current clip' }] }, 60_000, { strict: true });
    cache.recordCatalogSyncPage(secondQueryKey, { page: 1, pageCount: 1, rawResults: 1 });
    cache.completeCatalogSync(secondQueryKey, {
      providerTotal: 1, resultCount: 1, rawResults: 1, uniqueClipIds: 1,
      pageCount: 1,
    });

    const stats = cache.catalogStats();
    assert.equal(stats.unique_clips, 2);
    assert.equal(stats.active_clips, 1);
    assert.equal(stats.inactive_clips, 1);
    assert.deepEqual(cache.getInactiveClips().map((clip) => clip.clip_id), ['active-1']);
    const finalState = cache.getCatalogSync(secondQueryKey);
    assert.equal(finalState.run_status, 'succeeded');
    assert.equal(finalState.pages_expected, 1);
    assert.equal(finalState.pages_completed, 1);
    assert.equal(finalState.raw_results, 1);
    assert.equal(finalState.unique_clip_ids, 1);
    assert.equal(finalState.missing, 0);
  });

  test('does not deactivate provider clips when a full sync fails', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-provider-failure-'));
    const cache = createSearchCache(path.join(dir, 'cache.sqlite'), 60_000);
    const filters = { sortType: 1 };
    const queryKey = cache.startCatalogSync({ query: '', filters });
    const state = cache.getCatalogSync(queryKey);
    cache.put('known-page', {
      query: '', filters, page: 1, limit: 50, query_key: queryKey,
      catalog_sync_id: state.sync_id,
    }, { clips: [{ id: 'known-1', title: 'Known clip' }] }, 60_000, { strict: true });
    cache.completeCatalogSync(queryKey, {
      providerTotal: 1, resultCount: 1, rawResults: 1, uniqueClipIds: 1,
    });

    const failedQueryKey = cache.startCatalogSync({ query: '', filters });
    cache.failCatalogSync(failedQueryKey, new Error('page 2 failed'));

    assert.equal(cache.catalogStats().active_clips, 1);
    assert.equal(cache.catalogStats().inactive_clips, 0);
    assert.equal(cache.getCatalogSync(failedQueryKey).run_status, 'failed');
    assert.match(cache.getCatalogSync(failedQueryKey).run_last_error, /page 2 failed/);
  });

  test('query-scoped sync does not deactivate clips absent from that query', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-query-provider-state-'));
    const cache = createSearchCache(path.join(dir, 'cache.sqlite'), 60_000);
    const fullFilters = { sortType: 1 };
    const fullKey = cache.startCatalogSync({ query: '', filters: fullFilters });
    const fullState = cache.getCatalogSync(fullKey);
    cache.put('full-seed', {
      query: '', filters: fullFilters, page: 1, limit: 50, query_key: fullKey,
      catalog_sync_id: fullState.sync_id,
    }, { clips: [{ id: 'catalog-1', title: 'Catalog clip' }] }, 60_000, { strict: true });
    cache.completeCatalogSync(fullKey, {
      providerTotal: 1, resultCount: 1, uniqueClipIds: 1,
    });

    const queryFilters = { sortType: 1 };
    const queryKey = cache.startCatalogSync({ query: 'boxing', filters: queryFilters });
    const queryState = cache.getCatalogSync(queryKey);
    cache.put('query-page', {
      query: 'boxing', filters: queryFilters, page: 1, limit: 50, query_key: queryKey,
      catalog_sync_id: queryState.sync_id,
    }, { clips: [{ id: 'boxing-1', title: 'Boxing clip' }] }, 60_000, { strict: true });
    cache.completeCatalogSync(queryKey, {
      providerTotal: 1, resultCount: 1, uniqueClipIds: 1,
    });

    assert.equal(cache.catalogStats().active_clips, 2);
    assert.equal(cache.catalogStats().inactive_clips, 0);
  });

  test('keeps query-clip knowledge after HTTP cache expiry and later sync start', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-permanent-query-links-'));
    const cache = createSearchCache(path.join(dir, 'cache.sqlite'), 60_000);
    const request = { query: 'electricity meter', filters: {}, page: 1, limit: 2 };
    const key = buildSearchCacheKey(request);
    cache.put(key, request, {
      clips: [{ id: 'permanent-1', title: 'Electricity meter' }],
    }, -1);

    assert.equal(cache.get(key), null);
    assert.deepEqual(
      cache.getQueryLinks(request.query, request.filters, { maxAgeMs: 0 }).map((clip) => clip.clip_id),
      ['permanent-1'],
    );

    cache.startCatalogSync({ query: request.query, filters: request.filters });
    assert.deepEqual(
      cache.getQueryLinks(request.query, request.filters).map((clip) => clip.clip_id),
      ['permanent-1'],
    );
  });

  test('incremental sync records only delta metrics and preserves the last complete snapshot', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-incremental-sync-'));
    const cache = createSearchCache(path.join(dir, 'cache.sqlite'), 60_000);
    const filters = { sortType: 1 };
    const query = '';
    const fullKey = cache.startCatalogSync({ query, filters });
    const fullState = cache.getCatalogSync(fullKey);
    cache.put('incremental-full-seed', {
      query, filters, page: 1, limit: 50, query_key: fullKey,
      catalog_sync_id: fullState.sync_id,
    }, { clips: [{ id: 'stable-full-1', title: 'Stable full catalog clip' }] }, 60_000, { strict: true });
    cache.completeCatalogSync(fullKey, {
      providerTotal: 1, resultCount: 1, rawResults: 1, uniqueClipIds: 1, pageCount: 1,
    });
    const previousSnapshotId = cache.getCatalogSync(fullKey).last_complete_sync_id;

    const incrementalKey = cache.startCatalogSync({
      query, filters, syncScope: 'incremental', providerTotalAuthoritative: false,
    });
    const incrementalState = cache.getCatalogSync(incrementalKey);
    cache.put('incremental-delta', {
      query, filters, page: 1, limit: 50, query_key: incrementalKey,
      catalog_sync_id: incrementalState.sync_id,
    }, { clips: [{ id: 'newest-1', title: 'Newest clip' }] }, 60_000, { strict: true });
    cache.completeCatalogSync(incrementalKey, {
      providerTotal: 100, resultCount: 1, rawResults: 1, uniqueClipIds: 1,
      newClipIds: 1, knownClipIds: 0, knownStreak: 0, stoppedOnKnown: false,
      stopReason: 'max_pages', pageCount: 1,
    });

    const finalState = cache.getCatalogSync(incrementalKey);
    assert.equal(finalState.sync_scope, 'incremental');
    assert.equal(finalState.new_clip_ids, 1);
    assert.equal(finalState.last_complete_sync_id, previousSnapshotId);
    assert.equal(finalState.snapshot_complete, 1);
    assert.deepEqual(cache.getQuerySnapshot(query, filters).clips.map((clip) => clip.clip_id), ['stable-full-1']);
  });

  test('persists incremental and reconciliation schedule deadlines', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-schedule-'));
    const cache = createSearchCache(path.join(dir, 'cache.sqlite'), 60_000);
    const now = Date.parse('2026-08-20T00:00:00.000Z');
    const schedule = cache.configureCatalogSyncSchedule({
      now,
      incrementalIntervalMs: 60_000,
      reconciliationIntervalMs: 120_000,
    });
    assert.equal(schedule.incremental_interval_ms, 60_000);
    assert.equal(schedule.reconciliation_interval_ms, 120_000);
    assert.equal(Date.parse(schedule.next_incremental_at), now + 60_000);
    assert.equal(Date.parse(schedule.next_reconciliation_at), now + 120_000);

    const incrementalDue = cache.claimDueCatalogSyncSchedule({ now: now + 60_001 });
    assert.equal(incrementalDue.incremental, true);
    assert.equal(incrementalDue.reconciliation, false);
    assert.equal(Date.parse(incrementalDue.schedule.next_incremental_at), now + 120_001);

    const reconciliationDue = cache.claimDueCatalogSyncSchedule({ now: now + 120_001 });
    assert.equal(reconciliationDue.reconciliation, true);
    assert.equal(reconciliationDue.incremental, false);
    assert.ok(Date.parse(reconciliationDue.schedule.next_reconciliation_at) > now + 120_001);
  });

  test('separates stable clip URLs from transient media URLs', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-url-separation-'));
    const dbPath = path.join(dir, 'cache.sqlite');
    const cache = createSearchCache(dbPath, 60_000);
    const request = { query: 'electricity meter', filters: {}, page: 1, limit: 2 };
    cache.put(buildSearchCacheKey(request), request, {
      clips: [{
        id: 'url-42',
        title: 'Electricity meter',
        clipPageUrl: 'https://artlist.io/stock-footage/clip/electricity-meter/42',
        thumbnailUrl: 'https://cdn.artlist.io/thumbs/42.jpg',
        previewUrl: 'https://cdn.artlist.io/42/preview.mp4',
        primary_url: 'https://cdn.artlist.io/42/master.m3u8?token=secret',
        downloadUrl: 'https://cdn.artlist.io/42/download.mp4?signature=secret',
        raw_metadata: {
          stable: 'keep',
          page_url: 'https://artlist.io/stock-footage/clip/electricity-meter/42',
          hls_url: 'https://cdn.artlist.io/42/master.m3u8',
          nested: { download_url: 'https://cdn.artlist.io/42/download.mp4?token=secret' },
        },
      }],
    });

    assert.equal(isTransientArtlistUrl('https://cdn.artlist.io/42/master.m3u8'), true);
    assert.equal(isTransientArtlistUrl('https://cdn.artlist.io/thumbs/42.jpg'), false);
    const row = cache.db.prepare(`
      SELECT page_url, thumbnail_url, preview_url, download_urls_json,
             download_urls_expires_at, metadata_json
      FROM artlist_clips WHERE clip_id = 'url-42'
    `).get();
    assert.equal(row.page_url, 'https://artlist.io/stock-footage/clip/electricity-meter/42');
    assert.equal(row.thumbnail_url, 'https://cdn.artlist.io/thumbs/42.jpg');
    assert.equal(row.preview_url, '');
    assert.equal(row.download_urls_json, '[]');
    assert.equal(row.download_urls_expires_at, null);
    const metadata = JSON.parse(row.metadata_json);
    assert.equal(metadata.stable, 'keep');
    assert.equal(metadata.hls_url, undefined);
    assert.deepEqual(metadata.nested, {});

    cache.db.prepare(`
      UPDATE artlist_clips
      SET preview_url = 'https://old.example/old.mp4',
          download_urls_json = '["https://old.example/old.m3u8"]',
          metadata_json = '{"stream_url":"https://old.example/old.m3u8"}'
      WHERE clip_id = 'url-42'
    `).run();
    const reopened = createSearchCache(dbPath, 60_000);
    const scrubbed = reopened.db.prepare(`
      SELECT preview_url, download_urls_json, metadata_json
      FROM artlist_clips WHERE clip_id = 'url-42'
    `).get();
    assert.equal(scrubbed.preview_url, '');
    assert.equal(scrubbed.download_urls_json, '[]');
    const scrubbedMetadata = JSON.parse(scrubbed.metadata_json);
    assert.equal(scrubbedMetadata.stream_url, undefined);
    assert.equal(scrubbedMetadata.hls_url, undefined);
    assert.equal(scrubbedMetadata.stable, 'keep');
  });

  test('keeps transient media links out of the durable catalog', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-query-links-'));
    const cache = createSearchCache(path.join(dir, 'cache.sqlite'), 60_000);
    const request = { query: 'electricity meter', filters: {}, page: 1, limit: 2 };
    const clips = [{
      id: '42', title: 'Electricity meter', clipPath: 'https://cdn.example/42/master.m3u8',
      stream_urls: ['https://cdn.example/42/master.m3u8', 'https://cdn.example/42/preview.mp4'],
      preview_url: 'https://cdn.example/42/preview.mp4',
    }];
    cache.put(buildSearchCacheKey(request), request, { clips, results: clips });

    const links = cache.getQueryLinks(request.query, request.filters);
    assert.equal(links.length, 1);
    assert.equal(links[0].clip_id, '42');
    // Search-response media URLs are transient and must not be persisted in
    // the durable catalog or query relationship.
    assert.deepEqual(links[0].download_urls, []);
    const catalog = cache.searchCatalog('electricity', 10).find((clip) => clip.clip_id === '42');
    assert.deepEqual(catalog.download_urls, []);
    assert.equal(catalog.preview_url, '');
  });
});
