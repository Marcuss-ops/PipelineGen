import { test, describe } from 'node:test';
import assert from 'node:assert/strict';

import { ArtlistHttpApiClient, extractHttpClips } from '../artlist/http-api-client.js';

describe('ArtlistHttpApiClient', () => {
  test('builds the captured Artlist GraphQL ClipList request and exposes total pagination', async () => {
    const previousFetch = global.fetch;
    let seen;
    global.fetch = async (url, init) => {
      seen = { url, init };
      return {
        ok: true,
        status: 200,
        text: async () => JSON.stringify({ data: { clipList: {
          totalExact: 99,
          exactResults: [{ id: 123, clipName: 'Electricity meter', clipPath: 'https://cdn/123.mp4' }],
          similarResults: [],
        } } }),
      };
    };
    try {
      const client = new ArtlistHttpApiClient({
        endpoint: { url: 'https://search-api.artlist.io/v1/graphql', method: 'POST', kind: 'graphql' },
      });
      const response = await client.searchFootage({ term: 'electricity meter', page: 1 });
      const payload = JSON.parse(seen.init.body);
      assert.equal(seen.url, 'https://search-api.artlist.io/v1/graphql');
      assert.equal(payload.operationName, 'ClipList');
      assert.match(payload.query, /clipList/);
      assert.deepEqual(payload.variables.searchTerms, ['electricity meter']);
      assert.equal(payload.variables.page, 1);
      assert.equal(seen.init.headers['x-user-status'], 'guest');
      assert.match(seen.init.headers['x-visitor-id'], /^[0-9a-f-]{36}$/);
      assert.equal(response.pagination.total, 99);
      assert.equal(response.pagination.has_next_page, true);
    } finally {
      global.fetch = previousFetch;
    }
  });

  test('queries the configured endpoint without creating a browser', async () => {
    const previousFetch = global.fetch;
    let seen;
    global.fetch = async (url, init) => {
      seen = { url, init };
      return {
        ok: true,
        status: 200,
        text: async () => JSON.stringify({ data: { results: [
          { id: '1', title: 'Electricity grid', previewUrl: 'https://cdn/1.mp4' },
          { id: '1', title: 'duplicate', previewUrl: 'https://cdn/1.mp4' },
          { id: '2', title: 'Power lines', previewUrl: 'https://cdn/2.mp4' },
        ] } }),
      };
    };
    try {
      const client = new ArtlistHttpApiClient({
        endpoint: { url: 'http://catalog.test/search', method: 'POST', kind: 'json' },
      });
      const response = await client.searchFootage({ term: 'electricity', page: 2, limit: 50 });
      assert.equal(seen.url, 'http://catalog.test/search');
      assert.equal(seen.init.method, 'POST');
      assert.equal(JSON.parse(seen.init.body).query, 'electricity');
      assert.equal(JSON.parse(seen.init.body).page, 2);
      assert.equal(response.status, 200);
      assert.equal(extractHttpClips(response.data).length, 2);
    } finally {
      global.fetch = previousFetch;
    }
  });

  test('returns typed session errors for unauthorized responses', async () => {
    const previousFetch = global.fetch;
    global.fetch = async () => ({ ok: false, status: 403, text: async () => 'blocked' });
    try {
      const client = new ArtlistHttpApiClient({ endpoint: { url: 'http://catalog.test/search' } });
      await assert.rejects(() => client.searchFootage({ term: 'electricity' }), (error) => {
        assert.equal(error.code, 'SESSION_EXPIRED');
        return true;
      });
    } finally {
      global.fetch = previousFetch;
    }
  });
});
