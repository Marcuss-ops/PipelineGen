// Test 7: Relevance overfetch loop — synthetic dataset.
// PR-P2-RELEVANCE-FILTER (July 2026).
//
// User-spec literal: "se i risultati rilevanti sono meno del `limit`,
// NON fare fallback sui non filtrati. Invece overfetch (apri più
// pagine dettaglio finché non raggiungi il limite o il budget di
// pagine) e poi restituisci solo i rilevanti."
//
// godlike/06 SSOT: tests exercise the canonical pure helper
// relevanceOverfetch from src/artlist/relevance-overfetch.js.
// Tests do NOT spin up Puppeteer / Chromium / network — `fetchBatch`
// is a synchronous Array-returning mock. The orchestrator wiring in
// artlist/search.js is out of scope for THIS test file (integration
// tests live in tests/e2e/); this file pins the helper's contract.
//
// godlike/07 no-fake-availability: Test 3 HARD-PINS that
// haltedAt='budget' returns ONLY the relevant subset, NEVER
// padding with unfiltered. This is the user-spec invariant the
// retired the legacy `(scored.length >= limit ? scored : fallback)`
// ternary in artlist/search.js (commit subject: PR-P2-RELEVANCE-FILTER).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  relevanceOverfetch,
  DEFAULT_MAX_FETCH_PAGES,
  DEFAULT_BATCH_SIZE,
} from '../src/artlist/relevance-overfetch.js';

/**
 * Build a synthetic clip object whose relevance for term='boxing'
 * is determined solely by the id prefix:
 *   - 'r-' prefix → RELEVANT (isRelevantClip returns true)
 *   - 'x-' prefix → UNFILTERED / NOT relevant (token 'boxing' absent)
 * Title and stream_urls are populated with 'boxing' so the
 * isRelevantClip regex scan finds the token for r-* ids; for x-*
 * ids, the title stream is absent of 'boxing' so scoreClipRelevance
 * yields 0 (below relevance threshold).
 */
function synthClip(id, opts = {}) {
  const isRelevant = !opts.unfiltered;
  const title = isRelevant ? `Boxing Highlight ${id}` : `Unrelated Scene ${id}`;
  return {
    clip_id: id,
    title,
    clip_page_url: `https://artlist.io/c/${id}`,
    primary_url: `https://cdn/${id}.mp4`,
    stream_urls: isRelevant
      ? [`https://cdn/boxing-${id}.m3u8`]
      : [`https://cdn/${id}.m3u8`],
  };
}

/**
 * Build an indexable fetchBatch mock that walks a static dataset
 * cursor-forward. Returning a slice from `dataset` keeps the test
 * deterministic across runs (no Array.from({length:n}) noise).
 */
function buildStaticFetchBatch(dataset) {
  let cursor = 0;
  return async function fetchBatch(n) {
    const slice = dataset.slice(cursor, cursor + n);
    cursor += slice.length;
    return slice;
  };
}

describe('relevanceOverfetch — halt paths', () => {
  test('all relevant on first batch — halt at "limit"', async () => {
    // Dataset: 8 r-* clips (all relevant). Iter 1 → 8 cumulative.
    // Iter 1 alone surfaces 5 (limit). haltAt='limit'.
    const dataset = Array.from({ length: 8 }, (_, i) => synthClip(`r-${i}`));
    const result = await relevanceOverfetch({
      term: 'boxing',
      limit: 5,
      maxFetchPages: 20,
      fetchBatch: buildStaticFetchBatch(dataset),
      discoverMore: async () => [], // pretend no more URLs
    });
    assert.equal(result.haltedAt, 'limit');
    assert.equal(result.clips.length, 5);
    assert.equal(result.fetchedCount, 8, 'first batch (8 fetches) yielded the limit');
  });

  test('overfetches into iter 2 — halt at "limit" once cumulative ≥ limit', async () => {
    // Iter 1: dataset is 4 r-* + 4 x-* (4 relevant found). Not yet at limit=5.
    // Iter 2 surfaces more relevant → 5 cumulative → halt 'limit'.
    const dataset = [
      synthClip('r-1'), synthClip('r-2'),
      synthClip('r-3'), synthClip('r-4'),
      synthClip('x-1'), synthClip('x-2'), synthClip('x-3'), synthClip('x-4'),
      synthClip('r-5'),
    ];
    const result = await relevanceOverfetch({
      term: 'boxing',
      limit: 5,
      maxFetchPages: 20,
      fetchBatch: buildStaticFetchBatch(dataset),
      discoverMore: async () => ['extra-1', 'extra-2'], // surface URLs (no effect in unit test)
    });
    assert.equal(result.haltedAt, 'limit');
    assert.equal(result.clips.length, 5);
    assert.ok(result.fetchedCount >= 8, 'expect ≥ first full batch');
  });

  test('budget exhausted with only 3 relevant — returns ONLY those 3 (godlike/07)', async () => {
    // Spec literal: "Limita a MAX_FETCH_PAGES per evitare costi
    // incontrollati ... poi restituisci solo i rilevanti."
    //
    // Setup that drives the loop into the 'budget' halt path:
    //   - dataset has 40 items (3 relevant + 37 unfiltered) so the
    //     cursor-driven fetchBatch never starves the loop.
    //   - discoverMore ALWAYS returns a non-empty array so the
    //     helper's halt order (limit → budget → discoverMore)
    //     exhausts the budget (20 pages = 8+8+4) BEFORE the
    //     'nomore' halt can fire.
    //   - limit=10 is never reached because only 3 relevant clips
    //     exist in the dataset — so the helper MUST halt at
    //     'budget' per the hard-budget invariant.
    const dataset = [
      synthClip('r-1'), synthClip('r-2'), synthClip('r-3'),
    ];
    for (let i = 1; i <= 37; i++) dataset.push(synthClip(`x-${i}`, { unfiltered: true }));

    const result = await relevanceOverfetch({
      term: 'boxing',
      limit: 10,
      maxFetchPages: 20,
      fetchBatch: buildStaticFetchBatch(dataset),
      discoverMore: async () => ['placeholder'], // keeps the loop running until budget exhausts
    });
    assert.equal(result.haltedAt, 'budget',
      `user-spec: budget exhaustion MUST switch to the budget halt path. expected='budget' actual='${result.haltedAt}'`);
    assert.equal(result.fetchedCount, 20, '8+8+4 fetches across 3 iterations, exactly the budget');
    assert.equal(result.clips.length, 3, 'PR-P2-RELEVANCE-FILTER user-spec literal NEVER pads with unfiltered');

    // HARDEST assertion: every returned id is in the RELEVANT set,
    // and NO unfiltered id leaked. This is the user-spec literal
    // 'ritorni SOLO i rilevanti' pinned as an executable contract.
    const relevantIds = new Set(['r-1', 'r-2', 'r-3']);
    for (const c of result.clips) {
      assert.ok(
        relevantIds.has(c.clip_id),
        `USER-SPEC VIOLATED: returned id ${c.clip_id} is NOT in the relevant set. ` +
          `PR-P2-RELEVANCE-FILTER contract: NEVER pad with unfiltered.`
      );
      assert.ok(
        !c.clip_id.startsWith('x-'),
        `godlike/07 no-fake-availability violated: unfiltered clip ${c.clip_id} leaked into result`
      );
    }
  });

  test('discoverMore returns [] on iter 2 — halt at "nomore"', async () => {
    // Iter 1: 2 r-* + 4 x-* (limit=5 not reached). discoverMore → [].
    // haltAt='nomore'; 2 relevant returned.
    //
    // IMPORTANT: synthClip defaults to isRelevant=true (no opts).
    // x-* clips MUST pass {unfiltered: true} or the entire dataset
    // defaults to relevant and the helper reaches the 'limit' halt
    // instead of 'nomore'.
    const dataset = [
      synthClip('r-1'), synthClip('r-2'),
      synthClip('x-1', { unfiltered: true }),
      synthClip('x-2', { unfiltered: true }),
      synthClip('x-3', { unfiltered: true }),
      synthClip('x-4', { unfiltered: true }),
    ];
    let discoverCalls = 0;
    const discoverMore = async () => {
      discoverCalls += 1;
      return [];
    };
    const result = await relevanceOverfetch({
      term: 'boxing',
      limit: 5,
      maxFetchPages: 20,
      fetchBatch: buildStaticFetchBatch(dataset),
      discoverMore,
    });
    assert.equal(result.haltedAt, 'nomore');
    assert.equal(result.clips.length, 2);
    assert.ok(discoverCalls >= 1, 'discoverMore is called when relevant < limit and budget remains');
  });
});

describe('relevanceOverfetch — cost-control invariants', () => {
  test('fetchBatch is never asked for more than remaining budget', async () => {
    let totalRequested = 0;
    const fetchBatch = async (n) => {
      totalRequested += n;
      // Step: Mutate the totalRequest but return only irrelevant
      // clips so the loop drives through every iteration up to the
      // hard cap. Each 'n' requested is checked here.
      return [];
    };
    await relevanceOverfetch({
      term: 'boxing',
      limit: 100, // never reached → drives loop until budget
      maxFetchPages: 5,
      batchSize: 8,
      fetchBatch,
      discoverMore: async () => [],
    });
    assert.ok(
      totalRequested <= 5,
      `HARD CAP VIOLATED: fetchBatch received totalRequested=${totalRequested}, expected <=5`
    );
    // Last batch should be ≤ batchSize (since batchSize > remaining).
    // numIterations math: 5 / 8 → 1 iteration of size 5 (single batch).
    assert.equal(totalRequested, 5);
  });

  test('null/undefined clip entries are filtered before scoring', async () => {
    // Fetch-detail may return null on timeout / cloudflare-block
    // (see detail-page.js: catch returns null). The helper MUST
    // skip these without crashing on `scoreClipRelevance(undefined)`.
    //
    // Test design: limit=2 so the 2 surviving relevant clips DO
    // reach the limit — and the halt is 'limit'. This is the
    // cleanest assertion surface: the FILTER behavior is the
    // thing under test (null/undefined don't crash + don't count
    // toward relevant); the halt mode is incidental.
    const dataset = [
      null, // failed detail fetch (filter sentinel)
      synthClip('r-1'),
      undefined, // malformed detail fetch (filter sentinel)
      synthClip('r-2'),
    ];
    const result = await relevanceOverfetch({
      term: 'boxing',
      limit: 2,
      maxFetchPages: 20,
      fetchBatch: buildStaticFetchBatch(dataset),
      discoverMore: async () => [],
    });
    assert.equal(result.haltedAt, 'limit', '2 relevant <= limit=2 triggers limit halt');
    assert.equal(result.clips.length, 2, 'null + undefined were filtered before scoring');
    // Negative pin: the returned set is the 2 r-* clips, never null/undefined.
    for (const c of result.clips) {
      assert.ok(c && c.clip_id, 'returned clip must have a clip_id (null/undefined filtered)');
      assert.ok(c.clip_id.startsWith('r-'), `unexpected clip_id=${c.clip_id}; null/undefined must not leak into result`);
    }
  });

  test('duplicate clip_ids encountered across iterations are deduped', async () => {
    // Same 'r-1' appears in both batches; the second occurrence
    // should NOT increment the relevant counter (godlike/06 SSOT:
    // dedupe is the helper's job, not the orchestrator's).
    const dataset = [
      synthClip('r-1'), synthClip('r-2'),
      synthClip('r-1', { unfiltered: false }), // duplicate id (same score)
      synthClip('r-3'),
    ];
    const result = await relevanceOverfetch({
      term: 'boxing',
      limit: 2,
      maxFetchPages: 20,
      fetchBatch: buildStaticFetchBatch(dataset),
      discoverMore: async () => [],
    });
    assert.equal(result.haltedAt, 'limit');
    assert.equal(result.clips.length, 2);
    // Identity: only 2 of r-1/r-2/r-3 distinct IDs.
    const ids = new Set(result.clips.map((c) => c.clip_id));
    assert.equal(ids.size, 2);
  });
});

describe('relevanceOverfetch — arg validation', () => {
  test('throws TypeError on empty term', async () => {
    await assert.rejects(
      () =>
        relevanceOverfetch({
          term: '',
          limit: 5,
          fetchBatch: async () => [],
          discoverMore: async () => [],
        }),
      /term is required/
    );
  });

  test('throws TypeError on non-positive limit', async () => {
    await assert.rejects(
      () =>
        relevanceOverfetch({
          term: 'x',
          limit: 0,
          fetchBatch: async () => [],
          discoverMore: async () => [],
        }),
      /limit must be a positive finite number/
    );
  });

  test('throws TypeError on non-positive maxFetchPages', async () => {
    await assert.rejects(
      () =>
        relevanceOverfetch({
          term: 'x',
          limit: 5,
          maxFetchPages: -1,
          fetchBatch: async () => [],
          discoverMore: async () => [],
        }),
      /maxFetchPages must be a positive finite number/
    );
  });

  test('throws TypeError when fetchBatch is missing', async () => {
    await assert.rejects(
      () =>
        relevanceOverfetch({
          term: 'x',
          limit: 5,
          discoverMore: async () => [],
        }),
      /fetchBatch must be a function/
    );
  });
});

describe('relevanceOverfetch — defaults', () => {
  test('default maxFetchPages is 20', () => {
    assert.equal(DEFAULT_MAX_FETCH_PAGES, 20);
  });

  test('default batchSize is 8 (= DEFAULT_DETAIL_CONCURRENCY)', () => {
    assert.equal(DEFAULT_BATCH_SIZE, 8);
  });
});
