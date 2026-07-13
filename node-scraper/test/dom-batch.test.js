// Test 6: Concurrency limit + DOM scroll-batch caps — chunkArray + MAX_SCROLL_ROUNDS.
//
// Covers the user-requested "limite concorrenza" test area via the
// pure helper `chunkArray` and the named constant `MAX_SCROLL_ROUNDS`.
// Both are the testable seams of `artlist/search-dom.js`.
//
// The legacy logic is in artlist/search.js::fetchClipDetailsBatch,
// which feeds chunkArray-slice chunks into Promise.all — the test
// pins the chunk-size guarantee so the per-tab concurrency cap
// (=8 in production) is verifiable without launching Chromium.
//
// MAX_SCROLL_ROUNDS caps the number of DOM-scroll rounds the
// FALLBACK path performs before giving up. The constant is the
// testable form of the legacy inline `Math.min(8, ...)`.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  chunkArray,
  MAX_SCROLL_ROUNDS,
} from '../artlist/search-dom.js';

describe('chunkArray', () => {
  test('chunks an array into fixed-size slices', () => {
    assert.deepEqual(
      chunkArray([1, 2, 3, 4, 5, 6, 7, 8, 9, 10], 3),
      [[1, 2, 3], [4, 5, 6], [7, 8, 9], [10]]
    );
  });

  test('exact-split array → all chunks full size, no trailing partial', () => {
    assert.deepEqual(
      chunkArray([1, 2, 3, 4, 5, 6], 3),
      [[1, 2, 3], [4, 5, 6]]
    );
  });

  test('size greater than length → single chunk of whole array', () => {
    assert.deepEqual(chunkArray([1, 2, 3], 10), [[1, 2, 3]]);
  });

  test('empty array → empty chunks', () => {
    assert.deepEqual(chunkArray([], 8), []);
  });

  test('size = 0 → single chunk of the input array (collapse)', () => {
    assert.deepEqual(chunkArray([1, 2, 3], 0), [[1, 2, 3]]);
  });

  test('size = -5 → single chunk of the input array (collapse)', () => {
    assert.deepEqual(chunkArray([1, 2, 3], -5), [[1, 2, 3]]);
  });

  test('size = NaN → single chunk of the input array (collapse)', () => {
    assert.deepEqual(chunkArray([1, 2, 3], Number.NaN), [[1, 2, 3]]);
  });

  test('non-array input → empty array', () => {
    assert.deepEqual(chunkArray(null, 3), []);
    assert.deepEqual(chunkArray(undefined, 3), []);
    assert.deepEqual(chunkArray('not-an-array', 3), []);
  });

  test('preserves element types (objects / mixed)', () => {
    const a = { x: 1 };
    const b = { y: 2 };
    const c = { z: 3 };
    assert.deepEqual(chunkArray([a, b, c], 2), [[a, [b, c][0]], [c]]);
    // Note: b is wrapped in a one-element array by .slice()
    // Last chunk is [c], first is [[b, c][0]] which is [b]. So:
    //   chunkArray([a,b,c], 2) === [[a], [b,c]]
  });

  test('production concurrency cap (8) on 20 URLs → 3 chunks', () => {
    const urls = Array.from({ length: 20 }, (_, i) => `https://artlist.io/c/${i}`);
    const chunks = chunkArray(urls, 8);
    assert.equal(chunks.length, 3);
    assert.equal(chunks[0].length, 8);
    assert.equal(chunks[1].length, 8);
    assert.equal(chunks[2].length, 4);
  });

  test('production concurrency cap (8) on 8 URLs → 1 full chunk', () => {
    const urls = Array.from({ length: 8 }, (_, i) => `https://artlist.io/c/${i}`);
    const chunks = chunkArray(urls, 8);
    assert.equal(chunks.length, 1);
    assert.equal(chunks[0].length, 8);
  });

  test('production concurrency cap (8) on 0 URLs → 0 chunks', () => {
    assert.deepEqual(chunkArray([], 8), []);
  });
});

describe('MAX_SCROLL_ROUNDS', () => {
  test('is the documented cap of 8 (legacy from inline `Math.min(8, ...)`)', () => {
    assert.equal(MAX_SCROLL_ROUNDS, 8);
  });
});
