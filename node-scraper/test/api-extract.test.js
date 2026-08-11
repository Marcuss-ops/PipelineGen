// Test 4: API response parsing — extractClipsFromApiResponses.
//
// Covers two of the user-requested test areas:
//
//   - "parsing API response" — the JSON-recursion heuristic finds clip
//     arrays nested under GraphQL `data` keys, REST `items` keys, or
//     arbitrary depth (up to the depth=5 budget). clip-like objects are
//     identified by id / title / url fields. The 50-result cap holds.
//
//   - "deduplica" — the API-level half of the broader dedup test area:
//     seenIds Set dedups by id (or by url when id missing), preventing
//     duplicate clips across multiple intercepted responses. The
//     URL-side dedup is in url-strings.test.js.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { extractClipsFromApiResponses } from '../src/scrape/api-interception.js';

const TERM = 'sunrise';

function fakeResp(url, data) {
  return { url, data };
}

describe('extractClipsFromApiResponses', () => {
  test('returns empty array for empty input', () => {
    assert.deepEqual(extractClipsFromApiResponses([], TERM), []);
  });

  test('skips non-object / null data entries', () => {
    assert.deepEqual(
      extractClipsFromApiResponses(
        [fakeResp('https://api/x', null), fakeResp('https://api/y', 'string')],
        TERM
      ),
      []
    );
  });

  test('extracts flat top-level clip array by id + title + url heuristic', () => {
    const data = {
      items: [
        { id: 1, title: 'Sunrise Over Mountains', url: 'https://cdn/sunrise.mp4' },
        { id: 2, title: 'Sunset Beach', url: 'https://cdn/sunset.mp4' },
      ],
    };
    const out = extractClipsFromApiResponses([fakeResp('https://api/items', data)], TERM);
    assert.equal(out.length, 2);
    assert.equal(out[0].clip_id, '1');
    assert.equal(out[0].title, 'Sunrise Over Mountains');
    assert.equal(out[0].primary_url, 'https://cdn/sunrise.mp4');
    assert.equal(out[0].stream_urls.length, 1);
  });

  test('extracts clips nested under GraphQL data wrapper', () => {
    const data = {
      data: {
        searchClips: {
          results: [
            { id: 42, name: 'Sunrise', src: 'https://cdn/42.m3u8' },
          ],
        },
      },
    };
    const out = extractClipsFromApiResponses([fakeResp('https://graphql', data)], TERM);
    assert.equal(out.length, 1);
    assert.equal(out[0].clip_id, '42');
    assert.equal(out[0].title, 'Sunrise');
    assert.equal(out[0].primary_url, 'https://cdn/42.m3u8');
  });

  test('dedupes by id across multiple responses', () => {
    const data1 = { items: [{ id: 5, title: 'A', url: 'https://a' }] };
    const data2 = { items: [{ id: 5, title: 'A dup', url: 'https://a' }] };
    const data3 = { items: [{ id: 6, title: 'B', url: 'https://b' }] };
    const out = extractClipsFromApiResponses(
      [
        fakeResp('https://api/1', data1),
        fakeResp('https://api/2', data2),
        fakeResp('https://api/3', data3),
      ],
      TERM
    );
    assert.equal(out.length, 2);
    const ids = out.map((c) => c.clip_id);
    assert.deepEqual(ids.sort(), ['5', '6']);
  });

  test('dedupes by url when id is missing', () => {
    const data = {
      items: [
        { title: 'X', url: 'https://cdn/x.mp4' },
        { title: 'X dup', url: 'https://cdn/x.mp4' },
      ],
    };
    const out = extractClipsFromApiResponses([fakeResp('https://api', data)], TERM);
    assert.equal(out.length, 1);
    assert.equal(out[0].primary_url, 'https://cdn/x.mp4');
  });

  test('skips entries with no id AND no url', () => {
    // The production heuristic inspects obj[0] to decide whether the
    // outer array is clip-shaped. To exercise the per-entry skip, the
    // first entry must have clip-like keys (id+title+url); the missing
    // entry (no id, no url) is dropped in the per-item loop.
    const data = {
      items: [
        { id: 999, title: 'Shape-determining entry with all keys', url: 'https://cdn/999' },
        { id: 0, title: 'No id and no url', url: '' },
      ],
    };
    const out = extractClipsFromApiResponses([fakeResp('https://api', data)], TERM);
    assert.equal(out.length, 1);
    assert.equal(out[0].clip_id, '999');
  });

  test('falls back to term when title is empty', () => {
    const data = { items: [{ id: 1, url: 'https://cdn/1' }] };
    const out = extractClipsFromApiResponses([fakeResp('https://api', data)], 'sunrise');
    assert.equal(out[0].title, 'sunrise');
  });

  test('honors 50-clip cap (per-response and overall)', () => {
    const items = [];
    for (let i = 0; i < 60; i += 1) {
      items.push({ id: i, title: `Clip ${i}`, url: `https://cdn/${i}` });
    }
    const data = { items };
    const out = extractClipsFromApiResponses([fakeResp('https://api', data)], TERM);
    assert.equal(out.length, 50);
  });

  test('extracts clip_page_url when present', () => {
    const data = {
      items: [
        {
          id: 1,
          title: 'A',
          url: 'https://cdn/1',
          clipPageUrl: 'https://artlist.io/stock-footage/clip/abc/1',
        },
      ],
    };
    const out = extractClipsFromApiResponses([fakeResp('https://api', data)], TERM);
    assert.equal(out[0].clip_page_url, 'https://artlist.io/stock-footage/clip/abc/1');
  });

  test('does not expose a non-media API url as a stream', () => {
    const clips = extractClipsFromApiResponses([{
      url: 'https://artlist.io/api/search',
      data: {
        items: [{
          id: '123',
          title: 'Business meeting',
          url: 'https://artlist.io/site.webmanifest?v=1',
          clipPageUrl: 'https://artlist.io/stock-footage/clip/business-meeting/123',
        }],
      },
    }], 'business meeting');
    assert.equal(clips[0].primary_url, clips[0].clip_page_url);
    assert.deepEqual(clips[0].stream_urls, []);
  });

  test('stream_urls contains the primary_url when present', () => {
    const data = { items: [{ id: 1, title: 'A', url: 'https://cdn/1' }] };
    const out = extractClipsFromApiResponses([fakeResp('https://api', data)], TERM);
    assert.deepEqual(out[0].stream_urls, ['https://cdn/1']);
  });

  test('stream_urls is empty when neither url nor src is present', () => {
    const data = { items: [{ id: 1, title: 'A' }] };
    const out = extractClipsFromApiResponses([fakeResp('https://api', data)], TERM);
    assert.deepEqual(out[0].stream_urls, []);
  });
});
