import { test, describe } from 'node:test';
import assert from 'node:assert/strict';

import { ArtlistBrowserApiClient } from '../artlist/browser-api-client.js';

describe('ArtlistBrowserApiClient', () => {
  test('does not opt into the legacy cookie file by default', () => {
    const previous = process.env.ARTLIST_COOKIE_FILE;
    delete process.env.ARTLIST_COOKIE_FILE;

    try {
      const client = new ArtlistBrowserApiClient({
        browser: {},
        registry: {},
      });

      assert.equal(client.cookiePath, '');
    } finally {
      if (previous === undefined) delete process.env.ARTLIST_COOKIE_FILE;
      else process.env.ARTLIST_COOKIE_FILE = previous;
    }
  });

  test('classifies a rate-limited browser API response without fallback', async () => {
    const page = {
      setViewport: async () => {},
      setUserAgent: async () => {},
      goto: async () => ({ status: () => 429 }),
      close: async () => {},
    };
    const browser = {
      createBrowserContext: async () => ({
        newPage: async () => page,
        close: async () => {},
      }),
    };

    const client = new ArtlistBrowserApiClient({
      browser,
      cookiePath: '',
      registry: { footage_search: { enabled: true, method: 'POST', url: 'https://artlist.io/graphql' } },
    });

    await assert.rejects(
      () => client.searchFootage({ term: 'rate limited query' }),
      (err) => err.code === 'ARTLIST_RATE_LIMITED',
    );
  });

  test('posts the discovered endpoint from inside the browser context', async () => {
    const seen = {};
    const page = {
      setViewport: async () => {},
      setUserAgent: async () => {},
      goto: async () => {},
      evaluate: async (fn, arg) => {
        const originalFetch = global.fetch;
        global.fetch = async (url, init) => {
          seen.url = url;
          seen.init = init;
          return {
            status: 200,
            ok: true,
            headers: { get: () => 'application/json' },
            text: async () => JSON.stringify({
              data: {
                results: [
                  {
                    id: 'clip-1',
                    title: 'Business Team',
                    previewUrl: 'https://cdn/clip-1.mp4',
                  },
                ],
              },
            }),
          };
        };

        try {
          return await fn(arg);
        } finally {
          global.fetch = originalFetch;
        }
      },
      close: async () => {},
    };
    const browser = {
      createBrowserContext: async () => ({
        newPage: async () => page,
        close: async () => {},
      }),
    };

    const client = new ArtlistBrowserApiClient({
      browser,
      cookiePath: '',
      registry: {
        footage_search: {
          enabled: true,
          method: 'POST',
          url: 'https://artlist.io/graphql',
          kind: 'graphql',
          operationName: 'SearchFootage',
        },
      },
    });

    const result = await client.searchFootage({
      term: 'business team office',
      page: 2,
      limit: 10,
      filters: { resolution: '4k' },
    });

    assert.equal(seen.url, 'https://artlist.io/graphql');
    assert.equal(seen.init.method, 'POST');
    assert.equal(JSON.parse(seen.init.body).operationName, 'SearchFootage');
    assert.equal(JSON.parse(seen.init.body).variables.term, 'business team office');
    assert.equal(JSON.parse(seen.init.body).variables.page, 2);
    assert.equal(JSON.parse(seen.init.body).variables.limit, 10);
    assert.equal(result.status, 200);
    assert.equal(result.data.data.results[0].id, 'clip-1');
  });
});
