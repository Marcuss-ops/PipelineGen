// Test 2: URL helpers — extractClipId + normalizeLinks.
//
// Covers two of the user-requested test areas:
//
//   - "estrazione clip_id" — `extractClipId(url)` matches the canonical
//     `/clip/<slug>/<id>` pattern from Artlist URL templates and returns
//     the trailing digit sequence. Edge cases: missing id segments,
//     non-numeric slugs, empty / null input, missing leading slash,
//     and the per-malformed-input fallback to ''.
//
//   - "normalizzazione URL" — `normalizeLinks(values)` deduplicates,
//     trims whitespace, strips trailing backslashes, filters falsy
//     entries, and preserves first-seen order. This is the URL-side
//     half of the broader "deduplica" test area; the API-side half
//     lives in api-extract.test.js.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { extractClipId, normalizeLinks } from '../src/scrape/url.js';

describe('extractClipId', () => {
  test('extracts numeric id from canonical clip URL', () => {
    assert.equal(
      extractClipId('https://artlist.io/stock-footage/clip/some-slug/123456'),
      '123456'
    );
  });

  test('extracts id even when slug carries digits', () => {
    assert.equal(
      extractClipId('https://artlist.io/stock-footage/clip/4k-drone-shot/987654321'),
      '987654321'
    );
  });

  test('extracts id from bare /clip/<id> URLs', () => {
    assert.equal(
      extractClipId('https://artlist.io/stock-footage/clip/987654321'),
      '987654321'
    );
  });

  test('returns last numeric group for additional trailing segments', () => {
    assert.equal(
      extractClipId('https://artlist.io/stock-footage/clip/foo/42/extra'),
      '42'
    );
  });

  test('returns empty string when /clip/ segment is missing', () => {
    assert.equal(extractClipId('https://artlist.io/stock-footage/'), '');
  });

  test('returns empty string when slug has no numeric tail', () => {
    assert.equal(
      extractClipId('https://artlist.io/stock-footage/clip/no-numbers'),
      ''
    );
  });

  test('returns empty string for empty input', () => {
    assert.equal(extractClipId(''), '');
  });

  test('returns empty string for null / undefined input', () => {
    assert.equal(extractClipId(null), '');
    assert.equal(extractClipId(undefined), '');
  });

  test('returns empty string for non-string input (number)', () => {
    assert.equal(extractClipId(42), '');
  });

  test('matches a URL with a deep /clip/.../<digits> path', () => {
    assert.equal(
      extractClipId('https://artlist.io/stock-footage/clip/abc-def/7'),
      '7'
    );
  });
});

describe('normalizeLinks', () => {
  test('deduplicates repeated URLs', () => {
    assert.deepEqual(
      normalizeLinks(['a', 'b', 'a', 'c']),
      ['a', 'b', 'c']
    );
  });

  test('strips trailing backslashes (legacy escape sequences in HTML)', () => {
    assert.deepEqual(
      normalizeLinks(['https://x.m3u8\\', 'https://x.m3u8']),
      ['https://x.m3u8']
    );
  });

  test('trims surrounding whitespace', () => {
    assert.deepEqual(
      normalizeLinks(['  https://x.m3u8  ', 'https://y.m3u8']),
      ['https://x.m3u8', 'https://y.m3u8']
    );
  });

  test('filters falsy entries (null, undefined, empty string)', () => {
    assert.deepEqual(
      normalizeLinks(['a', null, undefined, '', 'a']),
      ['a']
    );
  });

  test('returns empty array for empty input', () => {
    assert.deepEqual(normalizeLinks([]), []);
  });

  test('returns empty array for non-array input', () => {
    assert.deepEqual(normalizeLinks(null), []);
    assert.deepEqual(normalizeLinks('not an array'), []);
  });

  test('preserves first-seen order when deduping', () => {
    assert.deepEqual(
      normalizeLinks(['z', 'a', 'm', 'a', 'z', 'b']),
      ['z', 'a', 'm', 'b']
    );
  });

  test('strips multiple trailing backslashes (defensive)', () => {
    assert.deepEqual(
      normalizeLinks(['https://x.m3u8\\\\\\\\']),
      ['https://x.m3u8']
    );
  });

  test('does not strip internal backslashes', () => {
    assert.deepEqual(
      normalizeLinks(['https://x.m3u8?a=1\\&b=2']),
      ['https://x.m3u8?a=1\\&b=2']
    );
  });

  test('coerces non-string values to string before normalize', () => {
    assert.deepEqual(
      normalizeLinks([42, 'a', 42, 'a']),
      ['42', 'a']
    );
  });
});
