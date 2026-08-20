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

  test('fetches an empty-query catalog across pages and deduplicates clip ids', async () => {
    const previousFetch = global.fetch;
    const requests = [];
    global.fetch = async (_url, init) => {
      const payload = JSON.parse(init.body);
      requests.push(payload);
      const page = payload.variables.page;
      const ids = page === 1
        ? Array.from({ length: 50 }, (_, index) => index + 1)
        : page === 2
          ? Array.from({ length: 50 }, (_, index) => index + 50)
          : Array.from({ length: 5 }, (_, index) => index + 100);
      return {
        ok: true,
        status: 200,
        text: async () => JSON.stringify({ data: { clipList: {
          totalExact: 104,
          exactResults: ids.map((id) => ({ id, clipName: `Clip ${id}`, clipPath: `https://cdn/${id}.mp4` })),
          similarResults: [],
        } } }),
      };
    };
    try {
      const client = new ArtlistHttpApiClient({
        endpoint: { url: 'http://catalog.test/empty-query', method: 'POST', kind: 'graphql' },
        ratePerSecond: 100,
      });
      const result = await client.searchAllPages({ term: '', concurrency: 2, maxPages: 10 });
      assert.equal(result.query, '');
      assert.equal(result.total, 104);
      assert.equal(result.page_count, 3);
      assert.equal(result.raw_results, 105);
      assert.equal(result.unique_clip_ids, 104);
      assert.equal(result.duplicates, 1);
      assert.equal(result.missing, 0);
      assert.equal(result.complete, true);
      assert.deepEqual(requests.map((request) => request.variables.page).sort((a, b) => a - b), [1, 2, 3]);
      assert.deepEqual(requests[0].variables.searchTerms, []);
    } finally {
      global.fetch = previousFetch;
    }
  });

  test('serializes a blank GraphQL term as an empty searchTerms array', async () => {
    const previousFetch = global.fetch;
    let payload;
    global.fetch = async (_url, init) => {
      payload = JSON.parse(init.body);
      return {
        ok: true,
        status: 200,
        text: async () => JSON.stringify({ data: { clipList: {
          totalExact: 0,
          exactResults: [],
          similarResults: [],
        } } }),
      };
    };
    try {
      const client = new ArtlistHttpApiClient({
        endpoint: { url: 'http://catalog.test/blank-term', method: 'POST', kind: 'graphql' },
        ratePerSecond: 100,
      });
      await client.searchFootage({ term: '   ', page: 1 });
      assert.deepEqual(payload.variables.searchTerms, []);
    } finally {
      global.fetch = previousFetch;
    }
  });

  test('resumes pagination after the checkpoint page while probing page one', async () => {
    const previousFetch = global.fetch;
    const requests = [];
    global.fetch = async (_url, init) => {
      const payload = JSON.parse(init.body);
      const page = payload.variables.page;
      requests.push(page);
      const ids = page === 1
        ? [1]
        : page === 3
          ? [3]
          : page === 4
            ? [4]
            : [];
      return {
        ok: true,
        status: 200,
        text: async () => JSON.stringify({ data: { clipList: {
          totalExact: 151,
          exactResults: ids.map((id) => ({ id, clipName: `Clip ${id}`, clipPath: `https://cdn/${id}.mp4` })),
          similarResults: [],
        } } }),
      };
    };
    try {
      const client = new ArtlistHttpApiClient({
        endpoint: { url: 'http://catalog.test/resume', method: 'POST', kind: 'graphql' },
        ratePerSecond: 100,
      });
      const pages = [];
      const result = await client.searchAllPages({
        term: '',
        concurrency: 1,
        maxPages: 10,
        resumeFromPage: 3,
        onPage: (page) => pages.push(page.page),
        collectPages: false,
      });
      assert.deepEqual(requests, [1, 3, 4]);
      assert.deepEqual(pages, [3, 4]);
      assert.equal(result.resume_from_page, 3);
      assert.equal(result.page_count, 4);
      assert.equal(result.raw_results, 2);
      assert.equal(result.unique_clip_ids, 2);
      assert.equal(result.complete, false);
    } finally {
      global.fetch = previousFetch;
    }
  });

  test('fetches newest pages until the known-clip streak and reports the delta', async () => {
    const previousFetch = global.fetch;
    const requests = [];
    global.fetch = async (_url, init) => {
      const payload = JSON.parse(init.body);
      const page = payload.variables.page;
      requests.push(page);
      const ids = page === 1 ? [1, 2, 3] : [4, 5, 6];
      return {
        ok: true,
        status: 200,
        text: async () => JSON.stringify({ data: { clipList: {
          totalExact: 100,
          exactResults: ids.map((id) => ({ id, clipName: `Newest ${id}`, clipPath: `https://cdn/${id}.mp4` })),
          similarResults: [],
        } } }),
      };
    };
    try {
      const client = new ArtlistHttpApiClient({
        endpoint: { url: 'http://catalog.test/newest', method: 'POST', kind: 'graphql' },
        ratePerSecond: 100,
      });
      const result = await client.searchNewestUntilKnown({
        filters: { sortType: 1 },
        knownStreak: 3,
        maxPages: 10,
        isKnown: (clips) => clips.filter((clip) => Number(clip.clip_id) >= 4).map((clip) => clip.clip_id),
      });
      assert.deepEqual(requests, [1, 2]);
      assert.equal(result.total, 100);
      assert.equal(result.page_count, 2);
      assert.equal(result.raw_results, 6);
      assert.equal(result.unique_clip_ids, 6);
      assert.equal(result.new_clip_ids, 3);
      assert.equal(result.known_clip_ids, 3);
      assert.equal(result.stopped_on_known, true);
      assert.equal(result.stop_reason, 'known_streak');
    } finally {
      global.fetch = previousFetch;
    }
  });

  test('fails with the page number when a non-final catalog page is empty', async () => {
    const previousFetch = global.fetch;
    global.fetch = async (_url, init) => {
      const page = JSON.parse(init.body).variables.page;
      return {
        ok: true,
        status: 200,
        text: async () => JSON.stringify({ data: { clipList: {
          totalExact: 101,
          exactResults: page === 1
            ? [{ id: 1, clipName: 'Only first page clip', clipPath: 'https://cdn/1.mp4' }]
            : [],
          similarResults: [],
        } } }),
      };
    };
    try {
      const client = new ArtlistHttpApiClient({
        endpoint: { url: 'http://catalog.test/empty-page', method: 'POST', kind: 'graphql' },
        ratePerSecond: 100,
      });
      await assert.rejects(
        () => client.searchAllPages({ term: '', concurrency: 1, maxPages: 3 }),
        (error) => {
          assert.equal(error.code, 'ARTLIST_EMPTY_PAGE');
          assert.equal(error.page, 2);
          return true;
        },
      );
    } finally {
      global.fetch = previousFetch;
    }
  });

  test('returns typed errors for GraphQL application errors', async () => {
    const previousFetch = global.fetch;
    global.fetch = async () => ({
      ok: true,
      status: 200,
      text: async () => JSON.stringify({ errors: [{ message: 'validation failed' }] }),
    });
    try {
      const client = new ArtlistHttpApiClient({ endpoint: { url: 'http://catalog.test/graphql-errors' } });
      await assert.rejects(() => client.searchFootage({ term: '' }), (error) => {
        assert.equal(error.code, 'ARTLIST_GRAPHQL_ERROR');
        assert.equal(error.graphqlErrors[0].message, 'validation failed');
        return true;
      });
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
