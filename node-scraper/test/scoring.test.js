// Test 3: Scoring — normalizeQuery + tokenizeQuery + scoreClipRelevance + isRelevantClip.
//
// Covers the "scoring" user-requested test area. Verifies:
//
//   - normalizeQuery does NFKD unicode-strip + lowercase + punctuation-to-space
//   - tokenizeQuery skips tokens of length <= 2 (legacy behavior)
//   - scoreClipRelevance scores 100 for single-token hit, 0 for miss,
//     and round(hits / total * 100) for multi-token partial matches
//   - scoreClipRelevance returns 0 for missing/empty term or missing/empty clip
//   - isRelevantClip uses threshold 100 for single-token queries and
//     threshold 60 for multi-token queries (per the legacy contract)

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  normalizeQuery,
  tokenizeQuery,
  scoreClipRelevance,
  isRelevantClip,
} from '../src/artlist/scoring.js';

describe('normalizeQuery', () => {
  test('lowercases uppercase input', () => {
    assert.equal(normalizeQuery('SUNRISE'), 'sunrise');
  });

  test('strips NFKD diacritics', () => {
    assert.equal(normalizeQuery('café résumé'), 'cafe resume');
  });

  test('collapses non-alphanumeric to single space', () => {
    assert.equal(normalizeQuery('foo-bar.baz_qux'), 'foo bar baz qux');
  });

  test('trims leading and trailing whitespace', () => {
    assert.equal(normalizeQuery('  hello  '), 'hello');
  });

  test('returns empty for empty input', () => {
    assert.equal(normalizeQuery(''), '');
  });

  test('returns empty for null / undefined', () => {
    assert.equal(normalizeQuery(null), '');
    assert.equal(normalizeQuery(undefined), '');
  });

  test('keeps digits', () => {
    assert.equal(normalizeQuery('4k drone shot 2025'), '4k drone shot 2025');
  });
});

describe('tokenizeQuery', () => {
  test('splits normalized query on whitespace', () => {
    assert.deepEqual(tokenizeQuery('foo bar baz'), ['foo', 'bar', 'baz']);
  });

  test('drops tokens of length <= 2', () => {
    assert.deepEqual(tokenizeQuery('a foo an bar'), ['foo', 'bar']);
  });

  test('returns empty array for empty input', () => {
    assert.deepEqual(tokenizeQuery(''), []);
  });

  test('single-token query yields single-token list', () => {
    assert.deepEqual(tokenizeQuery('sunrise'), ['sunrise']);
  });

  test('strips diacritics before tokenizing', () => {
    assert.deepEqual(tokenizeQuery('café résumé'), ['cafe', 'resume']);
  });
});

describe('scoreClipRelevance — single token', () => {
  test('returns 100 when token matches title', () => {
    assert.equal(
      scoreClipRelevance('sunrise', { title: 'Beautiful Sunrise Over Mountains' }),
      100
    );
  });

  test('returns 100 when token matches primary_url', () => {
    assert.equal(
      scoreClipRelevance('sunrise', { primary_url: 'https://x/sunrise-clip.mp4' }),
      100
    );
  });

  test('returns 100 when token matches stream_urls', () => {
    assert.equal(
      scoreClipRelevance('sunrise', {
        stream_urls: ['https://cdn/sunrise.m3u8'],
      }),
      100
    );
  });

  test('returns 0 when single token does not match', () => {
    assert.equal(scoreClipRelevance('sunrise', { title: 'Rainy Day' }), 0);
  });

  test('returns 0 when term is empty', () => {
    assert.equal(scoreClipRelevance('', { title: 'Anything' }), 0);
  });

  test('returns 0 when clip has no recognizable field', () => {
    assert.equal(scoreClipRelevance('sunrise', {}), 0);
  });
});

describe('scoreClipRelevance — multi token', () => {
  test('round(hits/total * 100) for partial match', () => {
    // term has 2 tokens [sunrise, mountain], 1 hit → 50
    assert.equal(
      scoreClipRelevance('sunrise mountain', { title: 'Beautiful Sunrise' }),
      50
    );
  });

  test('all tokens hit → 100', () => {
    assert.equal(
      scoreClipRelevance('sunrise mountain', {
        title: 'Sunrise over a Mountain',
      }),
      100
    );
  });

  test('no token hits → 0', () => {
    assert.equal(
      scoreClipRelevance('sunrise mountain', { title: 'Rainy Day' }),
      0
    );
  });

  test('token hit in stream_urls counts', () => {
    assert.equal(
      scoreClipRelevance('sunrise mountain', {
        stream_urls: ['https://cdn/mountain.m3u8'],
      }),
      50
    );
  });

  test('empty term with multi-token-shape yields 0', () => {
    assert.equal(scoreClipRelevance('   ', { title: 'Whatever' }), 0);
  });

  test('tokenize then score: filter behavior verified end-to-end', () => {
    // 'a foo an bar' tokenizes to ['foo', 'bar'], clip has 'foo' → 1/2 = 50
    assert.equal(
      scoreClipRelevance('a foo an bar', { title: 'foo something' }),
      50
    );
  });
});

describe('isRelevantClip', () => {
  test('multi-token term with all hits → relevant', () => {
    assert.equal(
      isRelevantClip('sunrise mountain', {
        title: 'Sunrise over a Mountain',
      }),
      true
    );
  });

  test('multi-token term with all hits → above 60% threshold', () => {
    // 2 tokens, 1 hit = 50 — below 60 threshold
    assert.equal(
      isRelevantClip('sunrise mountain', { title: 'Beautiful Sunrise' }),
      false
    );
  });

  test('multi-token term with >60% hits → relevant', () => {
    // 3 tokens, 2 hits = 67 → >= 60
    assert.equal(
      isRelevantClip('sunrise mountain forest', { title: 'sunrise in forest' }),
      true
    );
  });

  test('single-token term requires 100 (exact) to be relevant', () => {
    assert.equal(
      isRelevantClip('sunrise', { title: 'Beautiful Sunrise' }),
      true
    );
    assert.equal(
      isRelevantClip('sunrise', { title: 'Rainy Day' }),
      false
    );
  });
});
