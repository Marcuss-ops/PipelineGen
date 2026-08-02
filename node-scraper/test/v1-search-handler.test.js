import { test, describe } from 'node:test';
import assert from 'node:assert/strict';

import { handleV1ClipSearch } from '../src/server/routes.js';

function createReq(body) {
  return {
    method: 'POST',
    [Symbol.asyncIterator]: async function* () {
      yield Buffer.from(body);
    },
  };
}

function createRes() {
  return {
    statusCode: null,
    headers: null,
    body: null,
    writeHead(status, headers) {
      this.statusCode = status;
      this.headers = headers;
    },
    end(data) {
      this.body = data;
    },
  };
}

describe('handleV1ClipSearch', () => {
  test('returns 400 for missing query', async () => {
    const req = createReq(JSON.stringify({ limit: 10 }));
    const res = createRes();
    await handleV1ClipSearch(req, res, {
      config: { DEFAULT_LIMIT: 8, PROFILE_DIR: '' },
      state: {
        incRequest: () => 1,
        setLastSearchAt: () => {},
      },
      deps: {
        getBrowser: async () => ({}),
        searchArtlistGateway: async () => {
          throw new Error('should not be called');
        },
      },
    });

    assert.equal(res.statusCode, 400);
    assert.equal(JSON.parse(res.body).error, 'query is required');
  });

  test('returns 503 and marks session expired', async () => {
    let lastLaunchError = null;
    const req = createReq(JSON.stringify({ query: 'business team' }));
    const res = createRes();
    await handleV1ClipSearch(req, res, {
      config: { DEFAULT_LIMIT: 8, PROFILE_DIR: '' },
      state: {
        incRequest: () => 1,
        setLastSearchAt: () => {},
        setLastLaunchError: (msg) => {
          lastLaunchError = msg;
        },
      },
      deps: {
        getBrowser: async () => ({}),
        searchArtlistGateway: async () => {
          const err = new Error('Artlist session unavailable: HTTP 401');
          err.code = 'SESSION_EXPIRED';
          throw err;
        },
      },
    });

    assert.equal(res.statusCode, 503);
    const payload = JSON.parse(res.body);
    assert.equal(payload.error, 'SESSION_EXPIRED');
    assert.ok(lastLaunchError.includes('401'));
  });

  test('returns stable search payload on success', async () => {
    let lastLaunchError = 'previous failure';
    const req = createReq(JSON.stringify({ query: 'business team' }));
    const res = createRes();
    await handleV1ClipSearch(req, res, {
      config: { DEFAULT_LIMIT: 8, PROFILE_DIR: '' },
      state: {
        incRequest: () => 1,
        setLastSearchAt: () => {},
        setLastLaunchError: (msg) => {
          lastLaunchError = msg;
        },
      },
      deps: {
        getBrowser: async () => ({}),
        searchArtlistGateway: async () => ({
          ok: true,
          provider: 'artlist',
          query: 'business team',
          term: 'business team',
          page: 1,
          limit: 8,
          search_url: 'https://artlist.io/stock-footage/search?terms=business%20team',
          cache_hit: false,
          source: 'browser_api',
          results: [{ clip_id: '1', title: 'Clip 1' }],
          clips: [{ clip_id: '1', title: 'Clip 1' }],
          saved: 0,
        }),
      },
    });

    assert.equal(res.statusCode, 200);
    const payload = JSON.parse(res.body);
    assert.equal(payload.results[0].clip_id, '1');
    assert.equal(payload.clips[0].title, 'Clip 1');
    assert.equal(payload.cache_hit, false);
    assert.equal(lastLaunchError, null);
  });

  test('fails closed when the provider search exceeds its budget', async () => {
    const req = createReq(JSON.stringify({ query: 'provider hangs' }));
    const res = createRes();
    await handleV1ClipSearch(req, res, {
      config: { DEFAULT_LIMIT: 8, PROFILE_DIR: '', SEARCH_TIMEOUT_MS: 5 },
      state: {
        incRequest: () => 1,
        setLastSearchAt: () => {},
      },
      deps: {
        getBrowser: async () => ({}),
        searchArtlistGateway: () => new Promise(() => {}),
      },
    });

    assert.equal(res.statusCode, 504);
    assert.equal(JSON.parse(res.body).error, 'ARTLIST_SEARCH_TIMEOUT');
  });
});
