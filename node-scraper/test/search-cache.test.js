import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import {
  buildSearchCacheKey,
  createSearchCache,
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
});
