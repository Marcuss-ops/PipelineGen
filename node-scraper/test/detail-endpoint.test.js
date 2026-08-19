// Test: Artlist detail endpoint.
//
// Covers the Node server handler `handleDetail` in src/server/routes.js.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { handleDetail } from '../src/server/routes.js';

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
