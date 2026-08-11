// Test: Artlist detail-page hydrator.
//
// Covers the user-requested "metadata strutturati" test area:
// extraction from API/GraphQL, __NEXT_DATA__, JSON-LD, DOM selectors,
// and controlled textual fallback.  Tests run without Puppeteer
// (pure functions) plus a lightweight browser/page mock for the
// main entry point.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  extractFromNextData,
  extractFromJsonLd,
  extractFromDom,
  mergeMetadata,
  fetchClipDetails,
  looksLikeStreamUrl,
} from '../src/scrape/detail-page.js';

const CLIP_PAGE_URL = 'https://artlist.io/stock-footage/clip/skyline-at-sundown/123456';

// ── extractFromNextData ─────────────────────────────────────────────────

describe('extractFromNextData', () => {
  test('extracts clip fields from pageProps.clip', () => {
    const nextData = {
      props: {
        pageProps: {
          clip: {
            id: '123456',
            title: 'Skyline at Sundown',
            description: 'City skyline during an evening sunset',
            creator: 'John Richter',
            country: 'Spain',
            location: 'Barcelona, Spain',
            tags: ['Skyline', 'Evening', 'Clouds', 'Sundown'],
            categories: ['Cities', 'Travel'],
            thumbnailUrl: 'https://cdn.artlist.io/thumb/123456.jpg',
            previewUrl: 'https://cdn.artlist.io/preview/123456.mp4',
          },
        },
      },
    };
    const result = extractFromNextData(nextData, CLIP_PAGE_URL);
    assert.equal(result.title, 'Skyline at Sundown');
    assert.equal(result.creator, 'John Richter');
    assert.equal(result.country, 'Spain');
    assert.deepEqual(result.tags, ['Skyline', 'Evening', 'Clouds', 'Sundown']);
    assert.deepEqual(result.categories, ['Cities', 'Travel']);
    assert.equal(result.thumbnail_url, 'https://cdn.artlist.io/thumb/123456.jpg');
    assert.equal(result.preview_url, 'https://cdn.artlist.io/preview/123456.mp4');
  });

  test('extracts from initialProps.asset when present', () => {
    const nextData = {
      props: {
        initialProps: {
          asset: {
            id: '999',
            name: 'Mountain Lake',
            author: 'Jane Doe',
            tags: ['nature', 'lake'],
          },
        },
      },
    };
    const result = extractFromNextData(nextData, CLIP_PAGE_URL);
    assert.equal(result.title, 'Mountain Lake');
    assert.equal(result.creator, 'Jane Doe');
    assert.deepEqual(result.tags, ['nature', 'lake']);
  });

  test('recursively finds nested clip object', () => {
    const nextData = {
      props: {
        pageProps: {
          data: {
            result: {
              clip: {
                id: '42',
                title: 'Deeply Nested',
                creator: 'Nested Author',
                tags: ['deep'],
              },
            },
          },
        },
      },
    };
    const result = extractFromNextData(nextData, CLIP_PAGE_URL);
    assert.equal(result.title, 'Deeply Nested');
    assert.equal(result.creator, 'Nested Author');
  });

  test('returns empty object when no clip data is present', () => {
    const result = extractFromNextData({ props: { pageProps: {} } }, CLIP_PAGE_URL);
    assert.deepEqual(result, {});
  });
});

// ── extractFromJsonLd ─────────────────────────────────────────────────

describe('extractFromJsonLd', () => {
  test('extracts VideoObject metadata', () => {
    const scripts = [
      {
        innerHTML: JSON.stringify({
          '@context': 'https://schema.org',
          '@type': 'VideoObject',
          name: 'Skyline at Sundown',
          description: 'City skyline during an evening sunset',
          thumbnailUrl: 'https://cdn.artlist.io/thumb.jpg',
          contentUrl: 'https://cdn.artlist.io/preview.mp4',
          url: 'https://artlist.io/stock-footage/clip/skyline-at-sundown/123456',
        }),
      },
    ];
    const result = extractFromJsonLd(scripts, CLIP_PAGE_URL);
    assert.equal(result.title, 'Skyline at Sundown');
    assert.equal(result.description, 'City skyline during an evening sunset');
    assert.equal(result.thumbnail_url, 'https://cdn.artlist.io/thumb.jpg');
    assert.equal(result.preview_url, 'https://cdn.artlist.io/preview.mp4');
    assert.equal(result.clip_page_url, 'https://artlist.io/stock-footage/clip/skyline-at-sundown/123456');
  });

  test('extracts from @graph array', () => {
    const scripts = [
      {
        innerHTML: JSON.stringify({
          '@graph': [
            {
              '@type': 'VideoObject',
              name: 'Graph Clip',
              description: 'From graph',
            },
          ],
        }),
      },
    ];
    const result = extractFromJsonLd(scripts, CLIP_PAGE_URL);
    assert.equal(result.title, 'Graph Clip');
    assert.equal(result.description, 'From graph');
  });

  test('ignores malformed JSON-LD', () => {
    const scripts = [{ innerHTML: '{not json' }];
    const result = extractFromJsonLd(scripts, CLIP_PAGE_URL);
    assert.deepEqual(result, {});
  });
});

// ── extractFromDom ────────────────────────────────────────────────────

describe('extractFromDom', () => {
  // Build a lightweight document mock that satisfies the selectors
  // used by extractFromDom.  The helper only needs querySelector,
  // querySelectorAll, and document.title.
  function makeDocument({ title = '', elements = [], meta = {} } = {}) {
    const bySelector = new Map();
    const node = (text, attrs = {}) => ({
      textContent: text,
      getAttribute: (name) => attrs[name] || null,
    });
    for (const el of elements) {
      const list = bySelector.get(el.selector) || [];
      list.push(node(el.text, el.attrs));
      bySelector.set(el.selector, list);
    }

    const firstBySelectors = (selectors) => {
      for (const s of selectors) {
        const list = bySelector.get(s);
        if (list && list.length > 0) return list[0];
      }
      return null;
    };

    const allBySelectors = (selectors) => {
      const out = [];
      for (const s of selectors) {
        const list = bySelector.get(s) || [];
        out.push(...list);
      }
      return out;
    };

    return {
      title: title || '',
      querySelector(selector) {
        // Meta selectors may be comma-separated: meta[property="x"], meta[name="x"].
        const metaNames = new Set();
        const metaRegex = /meta\[(?:property|name)="([^"]+)"\]/g;
        let m;
        while ((m = metaRegex.exec(selector)) !== null) {
          metaNames.add(m[1]);
        }
        if (metaNames.size > 0) {
          for (const name of metaNames) {
            if (meta[name]) return node('', { content: meta[name] });
          }
          return null;
        }
        return firstBySelectors([selector]);
      },
      querySelectorAll(selector) {
        // Support both single selectors and comma-separated selector lists.
        const selectors = selector.split(',').map((s) => s.trim());
        return allBySelectors(selectors);
      },
    };
  }

  test('extracts title, creator, country, and tags', () => {
    const document = makeDocument({
      title: 'Skyline at Sundown',
      elements: [
        { selector: '[data-testid="creator-name"]', text: 'John Richter' },
        { selector: '[data-testid="country"]', text: 'Spain' },
        { selector: '[data-testid="clip-tag"]', text: 'Skyline' },
        { selector: '[data-testid="clip-tag"]', text: 'Evening' },
        { selector: '[data-testid="clip-category"]', text: 'Cities' },
      ],
      meta: {
        'og:description': 'City skyline during an evening sunset',
        'og:image': 'https://cdn.artlist.io/thumb.jpg',
      },
    });
    const result = extractFromDom(document, CLIP_PAGE_URL);
    assert.equal(result.title, 'Skyline at Sundown');
    assert.equal(result.creator, 'John Richter');
    assert.equal(result.country, 'Spain');
    assert.equal(result.location, 'Spain');
    assert.deepEqual(result.tags, ['Skyline', 'Evening']);
    assert.deepEqual(result.categories, ['Cities']);
    assert.equal(result.thumbnail_url, 'https://cdn.artlist.io/thumb.jpg');
  });

  test('falls back to h1 when document.title is empty', () => {
    const document = makeDocument({
      title: '',
      elements: [{ selector: 'h1', text: 'Fallback Title' }],
    });
    const result = extractFromDom(document, CLIP_PAGE_URL);
    assert.equal(result.title, 'Fallback Title');
  });
});


// ── mergeMetadata ─────────────────────────────────────────────────────

describe('mergeMetadata', () => {
  test('merges sources and lets later sources override only with non-empty values', () => {
    const result = mergeMetadata([
      { title: 'API Title', creator: '', tags: ['api'] },
      { title: '', creator: 'DOM Creator', tags: [] },
      { description: 'Merged description' },
    ]);
    assert.equal(result.title, 'API Title');
    assert.equal(result.creator, 'DOM Creator');
    assert.deepEqual(result.tags, ['api']);
    assert.equal(result.description, 'Merged description');
  });

  test('returns empty object for empty input', () => {
    assert.deepEqual(mergeMetadata([]), {});
  });
});

// ── fetchClipDetails (mocked) ───────────────────────────────────────────

// ── looksLikeStreamUrl (close reviewer's coverage gap on commit 6358d0c40) ─
describe('looksLikeStreamUrl', () => {
  test('matches .m3u8 URLs (with and without query string)', () => {
    assert.equal(
      looksLikeStreamUrl('https://cdn.artlist.io/hls/123456/master.m3u8'),
      true
    );
    assert.equal(
      looksLikeStreamUrl('https://cdn.artlist.io/hls/123456/master.m3u8?token=xyz'),
      true
    );
  });

  test('matches .mp4 URLs (with and without query string)', () => {
    assert.equal(
      looksLikeStreamUrl('https://cdn.artlist.io/preview/123456.mp4'),
      true
    );
    assert.equal(
      looksLikeStreamUrl('https://cdn.artlist.io/preview/123456.mp4?session=abc'),
      true
    );
  });

  test('matches /manifest and /playlist HLS-style paths', () => {
    assert.equal(
      looksLikeStreamUrl('https://cdn.artlist.io/hls/123456/manifest.mpd'),
      true
    );
    assert.equal(
      looksLikeStreamUrl('https://cdn.artlist.io/hls/123456/playlist'),
      true
    );
  });

  test('does NOT match .webm URLs', () => {
    assert.equal(
      looksLikeStreamUrl('https://cdn.artlist.io/preview/123456.webm'),
      false
    );
  });

  test('does NOT match .avi URLs', () => {
    assert.equal(
      looksLikeStreamUrl('https://cdn.artlist.io/preview/123456.avi'),
      false
    );
  });

  test('does NOT match .mov /.mkv URLs (defensive)', () => {
    assert.equal(
      looksLikeStreamUrl('https://cdn.artlist.io/preview/123456.mov'),
      false
    );
    assert.equal(
      looksLikeStreamUrl('https://cdn.artlist.io/preview/123456.mkv'),
      false
    );
  });

  test('does NOT treat site.webmanifest as a media stream', () => {
    assert.equal(looksLikeStreamUrl('https://artlist.io/site.webmanifest?v=1'), false);
  });

  test('returns false for empty / null / undefined / non-string', () => {
    assert.equal(looksLikeStreamUrl(''), false);
    assert.equal(looksLikeStreamUrl(null), false);
    assert.equal(looksLikeStreamUrl(undefined), false);
    assert.equal(looksLikeStreamUrl(42), false);
  });
});

describe('fetchClipDetails', () => {
  function createMockBrowser(resultOverrides = {}, { streamEmissions = ['https://cdn.artlist.io/123456.m3u8'] } = {}) {
    const listeners = { request: [], response: [] };
    const emittedRequests = [];

    const page = {
      listeners,
      emittedRequests,
      async setViewport() {},
      async setUserAgent() {},
      on(event, handler) {
        listeners[event].push(handler);
      },
      removeListener(event, handler) {
        listeners[event] = listeners[event].filter((h) => h !== handler);
      },
      async goto() {
        // Simulate request events for stream URLs (default emits one
        // .m3u8 to exercise the happy path; pass {} to force the
        // STREAM_NOT_FOUND branch).
        for (const h of listeners.request) {
          for (const url of streamEmissions) {
            h({ url: () => url });
          }
        }
      },
      async waitForSelector() {
        return { catch: () => {} };
      },
      async title() {
        return 'Skyline at Sundown';
      },
      async evaluate(fn, _arg) {
        if (typeof fn === 'function') {
          // We cannot run the browser function in Node; return a fixed snapshot
          // keyed by the function body so the various evaluate() calls get
          // sensible values.
          const body = fn.toString();
          if (body.includes('__NEXT_DATA__')) {
            return resultOverrides.nextData || null;
          }
          if (body.includes('application/ld+json')) {
            return resultOverrides.jsonLd || [];
          }
          if (body.includes('documentElement')) {
            return resultOverrides.outerHtml || '<html></html>';
          }
          // Mock-fragility fix (closes the regression surfaced by the
          // verify-artlist-live smoke on commit 2443a633c): the
          // streamsFromPerf evaluate in detail-page.js fetches an iterable
          // (performance.getEntriesByType(...)) that is later consumed via
          // [...streamsFromPerf, videoSrc] array spread. Returning a plain
          // `{}` default would TypeError ("object is not iterable") under
          // the spread operator, which bubbles up to fetchClipDetails'
          // outer catch and produces a null return. Returning [] here keeps
          // the spread iterable for any test that exercises the no-stream
          // branch (STREAM_NOT_FOUND contract test plus any future
          // regression net that mocks performance entries to [] each).
          if (body.includes('getEntriesByType')) {
            return resultOverrides.performanceEntries || [];
          }
          // DOM metadata extraction.
          return resultOverrides.domMetadata || {};
        }
        return null;
      },
      async close() {},
    };

    return {
      async newPage() {
        return page;
      },
    };
  }

  test('returns a structured clip object with provider and streams', async () => {
    const browser = createMockBrowser({
      nextData: { props: { pageProps: {} } },
      jsonLd: [],
      domMetadata: {},
      outerHtml: '<html><body></body></html>',
    });
    const result = await fetchClipDetails(browser, CLIP_PAGE_URL);
    assert.ok(result);
    assert.equal(result.provider, 'artlist');
    assert.equal(result.clip_id, '123456');
    assert.equal(result.clip_page_url, CLIP_PAGE_URL);
    assert.equal(Array.isArray(result.stream_urls), true);
  });

  test('returns null on Cloudflare block', async () => {
    const browser = {
      async newPage() {
        return {
          async setViewport() {},
          async setUserAgent() {},
          on() {},
          async goto() {},
          async waitForSelector() {
            return { catch: () => {} };
          },
          async title() {
            return 'Just a moment...';
          },
          async close() {},
        };
      },
    };
    const result = await fetchClipDetails(browser, CLIP_PAGE_URL);
    assert.equal(result, null);
  });

  // closes the reviewer's coverage gap on commit 6358d0c40 (second
  // half): the STREAM_NOT_FOUND clip.ok=false path MUST emit the exact
  // contract that tests/operational/artlist/03_detail_stream.sh Gate 1
  // Phase 3 probes (jq asserted shape: .ok==false and .error=='STREAM_NOT_FOUND'
  // and (.clip_id|length>0) and (.stream_urls|length==0)). Without
  // stream candidates scraped from the page (no m3u8, no mp4, no
  // /manifest, no /playlist), buildResult falls through to the
  // STREAM_NOT_FOUND branch — verify the JSON shape is exactly what
  // the smoke test asserts.
  test('STREAM_NOT_FOUND clip.ok=false path matches Gate 1 Phase 3 contract', async () => {
    const browser = createMockBrowser(
      {
        // No .m3u8/.mp4/manifest/playlist emissions anywhere —
        // outerHtml is empty, perf entries are empty, video is absent,
        // and goto() emits nothing via {streamEmissions: []} below.
        nextData: { props: { pageProps: {} } },
        jsonLd: [],
        domMetadata: {},
        outerHtml: '<html><body><div>no streams here</div></body></html>',
      },
      { streamEmissions: [] }
    );
    const result = await fetchClipDetails(browser, CLIP_PAGE_URL);
    // Sanity — the function returned a body, not a Cloudflare-null.
    assert.ok(result, 'fetchClipDetails returned null (Cloudflare block?)');
    // Gate 1 Phase 3 jq assertions, ported verbatim:
    assert.equal(result.ok, false);
    assert.equal(result.error, 'STREAM_NOT_FOUND');
    assert.ok((result.clip_id || '').length > 0, 'clip_id must be non-empty');
    assert.equal((result.stream_urls || []).length, 0);
    // Additional contract fields documented in
    // node-scraper/README.md §2.2:
    assert.equal(result.provider, 'artlist');
    assert.equal(result.page_url, CLIP_PAGE_URL);
    assert.equal(result.clip_page_url, CLIP_PAGE_URL);
    assert.ok(result.raw_metadata, 'raw_metadata must be present for operator debug');
  });

  test('builds a full result from all metadata sources', async () => {
    const browser = createMockBrowser({
      nextData: {
        props: {
          pageProps: {
            clip: {
              id: '123456',
              title: 'Next Title',
              creator: 'John Richter',
              country: 'Spain',
              tags: ['Skyline', 'Evening'],
              thumbnailUrl: 'https://cdn.artlist.io/next.jpg',
            },
          },
        },
      },
      jsonLd: [
        {
          innerHTML: JSON.stringify({
            '@type': 'VideoObject',
            name: 'JSON-LD Title',
            description: 'JSON-LD description',
          }),
        },
      ],
      domMetadata: {
        title: 'DOM Title',
        creator: 'DOM Creator',
        country: 'DOM Country',
        location: 'DOM Location',
        tags: ['DOM Tag'],
        categories: ['DOM Category'],
        thumbnail_url: 'https://cdn.artlist.io/dom.jpg',
        preview_url: 'https://cdn.artlist.io/dom.mp4',
      },
      outerHtml: '<html><body></body></html>',
    });
    const result = await fetchClipDetails(browser, CLIP_PAGE_URL);
    assert.ok(result);
    // API/Next.js data wins over JSON-LD and DOM.
    assert.equal(result.title, 'Next Title');
    assert.equal(result.creator, 'John Richter');
    assert.equal(result.country, 'Spain');
    assert.deepEqual(result.tags, ['Skyline', 'Evening']);
    assert.equal(result.thumbnail_url, 'https://cdn.artlist.io/next.jpg');
    // JSON-LD description is preserved because Next.js did not provide one.
    assert.equal(result.description, 'JSON-LD description');
    assert.equal(result.provider, 'artlist');
    assert.equal(result.clip_id, '123456');
    assert.ok(result.raw_metadata);
  });
});
