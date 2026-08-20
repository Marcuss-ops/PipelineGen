// Test: Artlist detail endpoint.
//
// Covers the Node server handler `handleDetail` in src/server/routes.js.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { handleDetail } from '../src/server/routes.js';
import { buildSearchCacheKey, createSearchCache } from '../artlist/search-cache.js';

function createMockReq({ method = 'POST', body = '{}' } = {}) {
  return {
    method,
    [Symbol.asyncIterator]: async function* () {
      yield Buffer.from(body);
    },
  };
}

function createMockRes() {
  const res = {
    statusCode: null,
    headers: null,
    body: null,
    writeHead(status, headers) {
      this.statusCode = status;
      this.headers = headers;
      return this;
    },
    end(data) {
      this.body = data;
      return this;
    },
  };
  return res;
}

function createCtx({ clip = null, error = null } = {}) {
  let reqId = 0;
  return {
    state: {
      incRequest() {
        reqId += 1;
        return reqId;
      },
    },
    deps: {
      async getBrowser() {
        return {};
      },
      async fetchClipDetails(_browser, _url) {
        if (error) {
          throw error;
        }
        return clip;
      },
    },
  };
}

describe('handleDetail', () => {
  test('returns 400 when clip_page_url is missing', async () => {
    const req = createMockReq({ body: JSON.stringify({}) });
    const res = createMockRes();
    await handleDetail(req, res, createCtx());

    assert.equal(res.statusCode, 400);
    const payload = JSON.parse(res.body);
    assert.equal(payload.ok, false);
    assert.ok(payload.error.includes('Missing clip_page_url'));
  });

  test('returns 404 when detail fetcher returns null', async () => {
    const req = createMockReq({
      body: JSON.stringify({ clip_page_url: 'https://artlist.io/clip/123' }),
    });
    const res = createMockRes();
    await handleDetail(req, res, createCtx({ clip: null }));

    assert.equal(res.statusCode, 404);
    const payload = JSON.parse(res.body);
    assert.equal(payload.ok, false);
    assert.ok(payload.error.includes('not found'));
  });

  test('returns rich clip metadata on success', async () => {
    const clip = {
      clip_id: '555555',
      title: 'Skyline at Sundown',
      description: 'City skyline during sunset',
      creator: 'John Richter',
      country: 'Spain',
      location: 'Barcelona',
      tags: ['Skyline', 'Evening'],
      categories: ['Cities'],
      clip_page_url: 'https://artlist.io/stock-footage/clip/skyline/555555',
      thumbnail_url: 'https://cdn.artlist.io/555555/thumb.jpg',
      preview_url: 'https://cdn.artlist.io/555555/preview.mp4',
      primary_url: 'https://cdn.artlist.io/555555/master.mp4',
      stream_urls: ['https://cdn.artlist.io/555555/stream.m3u8'],
      raw_metadata: { extra: 'value' },
    };

    const req = createMockReq({
      body: JSON.stringify({ clip_page_url: clip.clip_page_url }),
    });
    const res = createMockRes();
    await handleDetail(req, res, createCtx({ clip }));

    assert.equal(res.statusCode, 200);
    const payload = JSON.parse(res.body);
    assert.equal(payload.ok, true);
    assert.equal(payload.clip.clip_id, '555555');
    assert.equal(payload.clip.title, 'Skyline at Sundown');
    assert.equal(payload.clip.country, 'Spain');
    assert.ok(payload._meta.elapsed_ms >= 0);
  });

  test('resolves a fresh stream after the cached URL expires without losing clip identity', async () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'artlist-expired-stream-'));
    const cache = createSearchCache(path.join(dir, 'cache.sqlite'), 60_000);
    const clipPageUrl = 'https://artlist.io/stock-footage/clip/electricity-meter/424242';
    const request = { query: 'electricity meter', filters: {}, page: 1, limit: 1 };
    const staleClip = {
      id: '424242',
      clip_id: '424242',
      title: 'Electricity meter reading',
      description: 'Power meter in an industrial room',
      creator: 'Catalog Creator',
      tags: ['electricity', 'meter', 'power'],
      categories: ['technology'],
      clipPageUrl,
      thumbnailUrl: 'https://cdn.artlist.io/thumbs/424242.jpg',
      previewUrl: 'https://cdn.artlist.io/424242/expired.mp4?token=old',
      primary_url: 'https://cdn.artlist.io/424242/expired.m3u8?expires=old',
      raw_metadata: { license: 'standard', catalog_marker: 'preserve-me' },
    };
    const cacheKey = buildSearchCacheKey(request);
    cache.put(cacheKey, request, { clips: [staleClip] }, -1);

    assert.equal(cache.get(cacheKey), null, 'the expired HTTP response must not be reused');
    const durable = cache.searchCatalog('electricity meter', 10).find((clip) => clip.clip_id === '424242');
    assert.ok(durable, 'the durable catalog identity must remain available');
    assert.equal(durable.page_url, clipPageUrl);
    assert.equal(durable.title, staleClip.title);
    assert.equal(durable.description, staleClip.description);
    assert.deepEqual(durable.tags, staleClip.tags);
    assert.deepEqual(durable.categories, staleClip.categories);
    assert.equal(durable.raw_metadata.catalog_marker, 'preserve-me');
    assert.equal(durable.preview_url, '');
    assert.deepEqual(durable.stream_urls, []);

    let detailCalls = 0;
    const freshClip = {
      ...staleClip,
      preview_url: 'https://cdn.artlist.io/424242/fresh.mp4?token=new',
      primary_url: 'https://cdn.artlist.io/424242/fresh.m3u8?expires=new',
      stream_urls: ['https://cdn.artlist.io/424242/fresh.m3u8?expires=new'],
    };
    const ctx = createCtx({ clip: freshClip });
    const originalFetchClipDetails = ctx.deps.fetchClipDetails;
    ctx.deps.fetchClipDetails = async (...args) => {
      detailCalls += 1;
      return originalFetchClipDetails(...args);
    };

    const req = createMockReq({ body: JSON.stringify({ clip_page_url: durable.page_url }) });
    const res = createMockRes();
    await handleDetail(req, res, ctx);

    assert.equal(res.statusCode, 200);
    assert.equal(detailCalls, 1, 'the stable page must be resolved again');
    const payload = JSON.parse(res.body);
    assert.equal(payload.ok, true);
    assert.equal(payload.clip.clip_id, durable.clip_id);
    assert.equal(payload.clip.title, durable.title);
    assert.equal(payload.clip.description, durable.description);
    assert.deepEqual(payload.clip.tags, durable.tags);
    assert.deepEqual(payload.clip.categories, durable.categories);
    assert.equal(payload.clip.stream_urls[0], freshClip.stream_urls[0]);
    assert.notEqual(payload.clip.stream_urls[0], staleClip.primary_url);
  });

  test('returns 500 when fetchClipDetails throws', async () => {
    const req = createMockReq({
      body: JSON.stringify({ clip_page_url: 'https://artlist.io/clip/123' }),
    });
    const res = createMockRes();
    await handleDetail(req, res, createCtx({ error: new Error('browser crashed') }));

    assert.equal(res.statusCode, 500);
    const payload = JSON.parse(res.body);
    assert.equal(payload.ok, false);
    assert.ok(payload.error.includes('browser crashed'));
  });
});
