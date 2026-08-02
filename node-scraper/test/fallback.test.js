// Test 5: Puppeteer fallback decision — shouldUseFastPath.
//
// Covers the user-requested "fallback Puppeteer" test area as a
// pure branching-decision function (no Puppeteer / network / fs).
//
// shouldUseFastPath determines whether the API interception has
// yielded enough clips with primary_url != clip_page_url to
// SHORT-CIRCUIT the per-tab detail fallback. The expected behavior
// matches the legacy inline predicate in src/scrape/search-page.js
// from before the modularization:
//
//   - intercepted must be a non-empty array
//   - limit must be a positive number
//   - clipsWithStreams.length >= min(limit, 2) is the gate
//
// These tests pin all four edge cases that motivated extracting
// the logic in Commit A.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { classifyChallengePage, shouldUseFastPath } from '../artlist/search.js';

describe('shouldUseFastPath', () => {
  test('returns false for empty intercepted array', () => {
    assert.equal(shouldUseFastPath([], 8), false);
  });

  test('returns false for non-array intercepted', () => {
    assert.equal(shouldUseFastPath(null, 8), false);
    assert.equal(shouldUseFastPath('not-array', 8), false);
    assert.equal(shouldUseFastPath(undefined, 8), false);
  });

  test('returns false when limit is 0 / negative / NaN', () => {
    const clips = [{ primary_url: 'p', clip_page_url: 'c' }];
    assert.equal(shouldUseFastPath(clips, 0), false);
    assert.equal(shouldUseFastPath(clips, -1), false);
    assert.equal(shouldUseFastPath(clips, Number.NaN), false);
  });

  test('single clip with primary != page URL: below the 2-clip threshold', () => {
    const clips = [{ primary_url: 'p', clip_page_url: 'c' }];
    assert.equal(shouldUseFastPath(clips, 8), false);
  });

  test('two clips with primary != page URL: hits the fast path', () => {
    const clips = [
      { primary_url: 'p1', clip_page_url: 'c1' },
      { primary_url: 'p2', clip_page_url: 'c2' },
    ];
    assert.equal(shouldUseFastPath(clips, 8), true);
  });

  test('clip with primary_url == clip_page_url is NOT counted as fast-path-eligible', () => {
    const clips = [
      { primary_url: 'p', clip_page_url: 'p' },
      { primary_url: 'q', clip_page_url: 'q' },
    ];
    // Both filtered out by `primary_url !== clip_page_url`, so clipsWithStreams.length === 0 < min(limit, 2) === 2
    assert.equal(shouldUseFastPath(clips, 8), false);
  });

  test('mixed clips: only those with primary != page count', () => {
    const clips = [
      { primary_url: 'p1', clip_page_url: 'p1' }, // excluded
      { primary_url: 'p2', clip_page_url: 'c2' }, // eligible
      { primary_url: 'p3', clip_page_url: 'c3' }, // eligible
    ];
    assert.equal(shouldUseFastPath(clips, 8), true);
  });

  test('limit = 1 lowers the gate to min(limit, 2) = 1', () => {
    // With limit=1, the gate collapses to min(1, 2) = 1 — so a single
    // eligible clip already satisfies the fast-path predicate.
    const clips = [{ primary_url: 'p', clip_page_url: 'c' }];
    assert.equal(shouldUseFastPath(clips, 1), true);
  });

  test('limit > 2 still only requires min(limit, 2) eligible clips', () => {
    const clips = [
      { primary_url: 'p1', clip_page_url: 'c1' },
      { primary_url: 'p2', clip_page_url: 'c2' },
    ];
    // 2 eligible clips, gate is min(100, 2) = 2 → true
    assert.equal(shouldUseFastPath(clips, 100), true);
  });

  test('clip missing primary_url is excluded', () => {
    const clips = [
      { clip_page_url: 'c1' },
      { primary_url: 'p2', clip_page_url: 'c2' },
    ];
    // Only 1 eligible; gate is min(8, 2) = 2 → false
    assert.equal(shouldUseFastPath(clips, 8), false);
  });

  test('clip with falsy clip_page_url still eligible when primary_url differs', () => {
    const clips = [
      { primary_url: 'p1', clip_page_url: '' },
      { primary_url: 'p2', clip_page_url: undefined },
    ];
    // filter: (c.primary_url && c.primary_url !== c.clip_page_url)
    //   - {primary:p1, page:''}  → 'p1' !== '' → eligible
    //   - {primary:p2, page:undef} → 'p2' !== undefined → eligible
    // count = 2, gate = min(8, 2) = 2 → true
    assert.equal(shouldUseFastPath(clips, 8), true);
  });
});

describe('classifyChallengePage', () => {
  test('classifies a Cloudflare 429 interstitial', () => {
    assert.deepEqual(
      classifyChallengePage({
        status: 429,
        title: 'Just a moment...',
        bodyText: 'Performing security verification',
      }),
      { code: 'ARTLIST_RATE_LIMITED', reason: 'Artlist returned an anti-bot or rate-limit challenge page' },
    );
  });

  test('classifies a challenge page even when the transport status is 200', () => {
    const result = classifyChallengePage({
      status: 200,
      title: 'Just a moment...',
      bodyText: 'This website uses a security service to protect against malicious bots',
    });
    assert.equal(result?.code, 'ARTLIST_RATE_LIMITED');
  });

  test('does not classify a normal Artlist result page', () => {
    assert.equal(
      classifyChallengePage({ status: 200, title: 'Artlist search', bodyText: 'Mountain footage' }),
      null,
    );
  });
});
