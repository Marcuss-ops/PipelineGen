import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import { searchArtlistGateway, searchArtlistIncremental } from '../artlist/gateway-search.js';
import { buildSearchCacheKey, buildSearchQueryKey, createSearchCache } from '../artlist/search-cache.js';
import { isRelevantClip, scoreClipRelevance } from '../src/scrape/scoring.js';

function createTempCache(prefix) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), prefix));
  return createSearchCache(path.join(dir, 'cache.sqlite'), 60_000);
}

function seedCompleteSnapshot(cache, { query, clipId, title }) {
  const filters = { sortType: 1 };
  const queryKey = cache.startCatalogSync({ query, filters });
  const state = cache.getCatalogSync(queryKey);
  cache.put(buildSearchCacheKey({ query, filters, page: 1, limit: 50 }), {
    query, filters, page: 1, limit: 50, query_key: queryKey,
    catalog_sync_id: state.sync_id,
  }, { clips: [{ id: clipId, title }] }, 60_000, { strict: true });
  cache.recordCatalogSyncPage(queryKey, { page: 1, pageCount: 1, rawResults: 1 });
  cache.completeCatalogSync(queryKey, {
    providerTotal: 1,
    resultCount: 1,
    rawResults: 1,
    uniqueClipIds: 1,
    pageCount: 1,
  });
}

describe('searchArtlistGateway source precedence', () => {
  test('uses the exact query snapshot before generic local catalog search', async () => {
    const cache = createTempCache('artlist-gateway-snapshot-');
    seedCompleteSnapshot(cache, {
      query: 'electricity meter',
      clipId: 'snapshot-1',
      title: 'Snapshot provider result',
    });
    cache.put('generic-local', {
      query: 'generic seed', filters: {}, page: 1, limit: 50,
    }, { clips: [{ id: 'generic-1', title: 'Generic local result' }] });

    const result = await searchArtlistGateway({
      query: 'electricity meter',
      filters: {},
      limit: 10,
      mode: 'catalog_first',
      searchCache: cache,
      registryPath: path.join(os.tmpdir(), 'missing-artlist-registry.json'),
    });

    assert.equal(result.source, 'query_snapshot');
    assert.equal(result.snapshot_complete, true);
    assert.equal(result.provider_contacted, false);
    assert.deepEqual(result.clips.map((clip) => clip.clip_id), ['snapshot-1']);
  });

  test('falls back to the generic local catalog before the provider', async () => {
    const cache = createTempCache('artlist-gateway-local-');
    cache.put('generic-local', {
      query: 'seed', filters: {}, page: 1, limit: 50,
    }, { clips: [{ id: 'local-1', title: 'Electricity meter local result' }] });

    const result = await searchArtlistGateway({
      query: 'electricity meter',
      filters: {},
      limit: 10,
      mode: 'catalog_first',
      searchCache: cache,
      registryPath: path.join(os.tmpdir(), 'missing-artlist-registry.json'),
    });

    assert.equal(result.source, 'catalog');
    assert.equal(result.provider_contacted, false);
    assert.equal(result.clips[0].clip_id, 'local-1');
  });

  test('incremental sync persists newest delta and stops on known clips', async () => {
    const cache = createTempCache('artlist-gateway-incremental-');
    const filters = { sortType: 1 };
    cache.put('known-seed', {
      query: '', filters, page: 1, limit: 50,
    }, { clips: [1, 2, 3, 4].map((id) => ({ id: `known-${id}`, title: `Known clip ${id}` })) });
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-gateway-incremental-registry-'));
    const registryPath = path.join(dir, 'endpoints.json');
    fs.writeFileSync(registryPath, JSON.stringify({
      footage_search: {
        enabled: true,
        transport: 'http',
        kind: 'graphql',
        method: 'POST',
        url: 'http://catalog.test/incremental',
      },
    }));

    const previousFetch = global.fetch;
    const requests = [];
    global.fetch = async (_url, init) => {
      const page = JSON.parse(init.body).variables.page;
      requests.push(page);
      const ids = page === 1
        ? ['new-1', 'new-2', 'known-1']
        : ['known-2', 'known-3', 'known-4'];
      return {
        ok: true,
        status: 200,
        text: async () => JSON.stringify({ data: { clipList: {
          totalExact: 100,
          exactResults: ids.map((id) => ({ id, clipName: id, clipPath: `https://cdn/${id}.mp4` })),
          similarResults: [],
        } } }),
      };
    };
    try {
      const result = await searchArtlistIncremental({
        filters,
        knownStreak: 3,
        maxPages: 10,
        searchCache: cache,
        registryPath,
      });
      const state = cache.getCatalogSync(buildSearchQueryKey({ query: '', filters }));
      assert.deepEqual(requests, [1, 2]);
      assert.equal(result.sync_scope, 'incremental');
      assert.equal(result.new_clip_ids, 2);
      assert.equal(result.stopped_on_known, true);
      assert.equal(state.sync_scope, 'incremental');
      assert.equal(state.new_clip_ids, 2);
      assert.equal(cache.catalogStats().unique_clips, 6);
    } finally {
      global.fetch = previousFetch;
    }
  });

  test('catalog_only replays the exact snapshot ranking without provider access', async () => {
    const cache = createTempCache('artlist-gateway-replay-');
    const query = 'electricity meter';
    const filters = { sortType: 1 };
    const queryKey = cache.startCatalogSync({ query, filters });
    const state = cache.getCatalogSync(queryKey);
    cache.put('replay-page-1', {
      query, filters, page: 1, limit: 50, query_key: queryKey,
      catalog_sync_id: state.sync_id,
    }, {
      clips: [
        { id: 'replay-a', title: 'Provider rank A' },
        { id: 'replay-b', title: 'Provider rank B' },
        { id: 'replay-c', title: 'Provider rank C' },
      ],
    }, 60_000, { strict: true });
    cache.recordCatalogSyncPage(queryKey, { page: 1, pageCount: 1, rawResults: 3 });
    cache.completeCatalogSync(queryKey, {
      providerTotal: 3, resultCount: 3, rawResults: 3, uniqueClipIds: 3,
      pageCount: 1,
    });

    const result = await searchArtlistGateway({
      query,
      filters,
      limit: 10,
      mode: 'catalog_only',
      forceRefresh: true,
      searchCache: cache,
      registryPath: path.join(os.tmpdir(), 'missing-artlist-registry.json'),
    });

    assert.deepEqual(result.clips.map((clip) => clip.clip_id), ['replay-a', 'replay-b', 'replay-c']);
    assert.equal(result.source, 'query_snapshot');
    assert.equal(result.provider_contacted, false);
    assert.equal(result.browser_launched, false);
  });

  test('certifies 20 offline segments with zero provider calls and relevant local clips', async () => {
    const cache = createTempCache('artlist-gateway-20-segments-');
    const filters = { sortType: 1 };
    const segments = [
      'woman boxer training gym',
      'boxing arena crowd',
      'electric car charging station',
      'business meeting office',
      'server data center',
      'city skyline night',
      'power station',
      'airport runway',
      'doctor hospital',
      'construction workers',
      'ocean waves beach',
      'forest wildfire smoke',
      'train station commuter',
      'space rocket launch',
      'farmer harvesting field',
      'underwater coral reef',
      'mountain climber',
      'classroom students',
      'chef restaurant kitchen',
      'solar panels roof',
    ];

    for (const [index, query] of segments.entries()) {
      const queryKey = cache.startCatalogSync({ query, filters });
      const state = cache.getCatalogSync(queryKey);
      const clipId = `offline-segment-${index + 1}`;
      const clip = {
        id: clipId,
        title: `${query} stock footage`,
        description: `Documentary footage showing ${query}`,
        tags: query.split(' '),
        categories: ['editorial'],
      };
      cache.put(buildSearchCacheKey({ query, filters, page: 1, limit: 50 }), {
        query, filters, page: 1, limit: 50, query_key: queryKey,
        catalog_sync_id: state.sync_id,
      }, { clips: [clip] }, 60_000, { strict: true });
      cache.recordCatalogSyncPage(queryKey, { page: 1, pageCount: 1, rawResults: 1 });
      cache.completeCatalogSync(queryKey, {
        providerTotal: 1,
        resultCount: 1,
        rawResults: 1,
        uniqueClipIds: 1,
        pageCount: 1,
      });
    }

    const previousFetch = global.fetch;
    let providerCalls = 0;
    global.fetch = async () => {
      providerCalls += 1;
      throw new Error('GraphQL must remain disabled during offline certification');
    };
    const failures = [];
    try {
      for (const [index, query] of segments.entries()) {
        const result = await searchArtlistGateway({
          query,
          filters,
          limit: 10,
          mode: 'catalog_only',
          searchCache: cache,
          registryPath: path.join(os.tmpdir(), 'graphql-disabled.json'),
        });
        const selected = result.clips[0];
        const score = scoreClipRelevance(query, selected);
        if (result.source !== 'query_snapshot'
          || result.provider_contacted !== false
          || result.browser_launched !== false
          || result.clips.length === 0
          || !isRelevantClip(query, selected)
          || score < 60) {
          failures.push({ index: index + 1, query, source: result.source, score });
        }
      }
    } finally {
      global.fetch = previousFetch;
    }

    assert.deepEqual(failures, []);
    assert.equal(providerCalls, 0);
    assert.equal(segments.length, 20);
  });

  test('live_required bypasses snapshot and local catalog and contacts the provider', async () => {
    const cache = createTempCache('artlist-gateway-live-');
    seedCompleteSnapshot(cache, {
      query: 'electricity meter',
      clipId: 'snapshot-1',
      title: 'Stale local result',
    });
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-gateway-registry-'));
    const registryPath = path.join(dir, 'endpoints.json');
    fs.writeFileSync(registryPath, JSON.stringify({
      footage_search: {
        enabled: true,
        transport: 'http',
        kind: 'graphql',
        method: 'POST',
        url: 'http://catalog.test/graphql',
      },
    }));

    const previousFetch = global.fetch;
    let providerCalls = 0;
    global.fetch = async (_url, init) => {
      providerCalls += 1;
      const payload = JSON.parse(init.body);
      assert.deepEqual(payload.variables.searchTerms, ['electricity meter']);
      return {
        ok: true,
        status: 200,
        text: async () => JSON.stringify({ data: { clipList: {
          totalExact: 1,
          exactResults: [{ id: 'live-1', clipName: 'Fresh provider result', clipPath: 'https://cdn/live-1.mp4' }],
          similarResults: [],
        } } }),
      };
    };
    try {
      const result = await searchArtlistGateway({
        query: 'electricity meter',
        filters: {},
        limit: 10,
        mode: 'live_required',
        searchCache: cache,
        registryPath,
      });
      assert.equal(providerCalls, 1);
      assert.equal(result.provider_contacted, true);
      assert.equal(result.source, 'http_api');
      assert.equal(result.clips[0].clip_id, 'live-1');
    } finally {
      global.fetch = previousFetch;
    }
  });
});
