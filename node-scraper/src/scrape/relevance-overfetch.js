// Hybrid Artlist search — relevance overfetch loop.
//
// PR-P2-RELEVANCE-FILTER (July 2026) — User-spec literal:
//
//   "se i risultati rilevanti sono meno del `limit`, NON fare
//    fallback sui non filtrati. Invece overfetch (apri più pagine
//    dettaglio finché non raggiungi il limite o il budget di
//    pagine) e poi restituisci solo i rilevanti. Limita a
//    MAX_FETCH_PAGES per evitare costi incontrollati."
//
// godlike/06 SSOT: this helper owns the SINGLE canonical dispatch
// decision for the relevance overfetch loop. The orchestrator
// (artlist/search.js) wires the puppeteer-runtime hooks (fetch
// details, DOM-scroll) via the function arguments below; the helper
// stays pure (no Puppeteer / filesystem / network) so the synthetic
// dataset tests in node-scraper/test/relevance-overfetch.test.js
// run under `node --test` without Chromium.
//
// godlike/07 no-fake-availability: at EVERY halt path the helper
// returns ONLY the accumulated relevant clips — it NEVER pads with
// unfiltered. The legacy anti-pattern
//   (scored.length >= limit ? scored : fallback)
// is RETIRED: the caller in artlist/search.js now wraps this
// helper and discards the unfiltered branch entirely.
//
// Halt modes:
//   - 'limit'  → relevant-cumulative reached `limit`.
//   - 'budget' → max-fetch-pages exhausted without reaching `limit`.
//   - 'nomore' → discoverMore returned [] (the results page is
//                exhausted; we can't surface more URLs to fetch).
//
// Determinism: at every halt, the relevant accumulator is sorted
// descending by score (tiebreaker: clip_id asc) before slicing so
// the top-`limit` is the highest-scored, regardless of arrival
// order. This pins the user-spec invariant "se i risultati sono <
// limit restituisci i rilevanti" — never silently drop a
// higher-scored late arrival to favor a lower-scored early one.

import { scoreClipRelevance, isRelevantClip } from './scoring.js';

/** Default upper bound on detail-page fetches per search invocation. */
export const DEFAULT_MAX_FETCH_PAGES = 20;

/** Default per-iteration fanout (= chunks of detail tabs opened in parallel). */
export const DEFAULT_BATCH_SIZE = 8;

/**
 * Sort the relevant accumulator by descending score (tiebreaker on
 * clip_id asc). Determinism is a contract — operators see the same
 * ordering regardless of which iteration produced which clip.
 *
 * @param {Array<{score: number, clip_id: string}>} arr
 */
function sortByScoreDesc(arr) {
  arr.sort(
    (a, b) => b.score - a.score || String(a.clip_id).localeCompare(String(b.clip_id))
  );
}

/**
 * Run the relevance overfetch loop.
 *
 * @param {Object} opts
 * @param {string} opts.term - Search term (consumed by scoreClipRelevance + isRelevantClip).
 * @param {number} opts.limit - Target relevant-count. Positive finite.
 * @param {number} [opts.maxFetchPages=DEFAULT_MAX_FETCH_PAGES] - HARD cap on detail-page fetches per call.
 * @param {number} [opts.batchSize=DEFAULT_BATCH_SIZE] - Per-iteration fanout (= fetchBatch invocation count).
 * @param {(n: number) => Promise<Array<object|null>>} opts.fetchBatch - Detail-page fetcher. Receives n (clamped to remaining budget). Returns Clip[] — null/undefined entries denote failed fetches and are filtered.
 * @param {(seenClipIds: Set<string>) => Promise<Array<string>>} opts.discoverMore - Surface NEW clip-page URLs not yet seen. Return [] (or non-Array) to halt in 'nomore'.
 * @returns {Promise<{clips: Array<object>, fetchedCount: number, haltedAt: 'limit'|'budget'|'nomore'}>}
 */
export async function relevanceOverfetch({
  term,
  limit,
  maxFetchPages = DEFAULT_MAX_FETCH_PAGES,
  batchSize = DEFAULT_BATCH_SIZE,
  fetchBatch,
  discoverMore,
}) {
  if (!term || typeof term !== 'string') {
    throw new TypeError('relevanceOverfetch: term is required (non-empty string)');
  }
  if (!Number.isFinite(limit) || limit <= 0) {
    throw new TypeError(
      `relevanceOverfetch: limit must be a positive finite number (got ${limit})`
    );
  }
  if (!Number.isFinite(maxFetchPages) || maxFetchPages <= 0) {
    throw new TypeError(
      `relevanceOverfetch: maxFetchPages must be a positive finite number (got ${maxFetchPages})`
    );
  }
  if (!Number.isFinite(batchSize) || batchSize <= 0) {
    throw new TypeError(
      `relevanceOverfetch: batchSize must be a positive finite number (got ${batchSize})`
    );
  }
  if (typeof fetchBatch !== 'function') {
    throw new TypeError('relevanceOverfetch: fetchBatch must be a function (n) => Promise<Clip[]>');
  }
  if (typeof discoverMore !== 'function') {
    throw new TypeError(
      'relevanceOverfetch: discoverMore must be a function (seenIds) => Promise<string[]>'
    );
  }

  const relevant = [];
  const seenClipIds = new Set();
  let pagesFetched = 0;

  // Each iteration consumes up to min(batchSize, remaining budget) fetches.
  while (pagesFetched < maxFetchPages) {
    const remainingBudget = maxFetchPages - pagesFetched;
    const thisBatch = Math.min(batchSize, remainingBudget);
    const rawClips = await fetchBatch(thisBatch);
    pagesFetched += thisBatch;

    for (const clip of rawClips || []) {
      // Skip null/undefined (failed detail fetches) and missing-clip_id (malformed).
      if (!clip || !clip.clip_id) continue;
      // Dedupe across scroll rounds (same clip surfaced twice).
      if (seenClipIds.has(clip.clip_id)) continue;
      seenClipIds.add(clip.clip_id);

      if (isRelevantClip(term, clip)) {
        relevant.push({ ...clip, score: scoreClipRelevance(term, clip) });
      }
    }

    // ── Halt path A: relevant-count reached `limit`. ──────────────
    // Sort the WHOLE accumulator (not just the latest batch) so a
    // late-arriving higher-scored clip is correctly preferred over
    // earlier lower-scored ones when the slice picks the top-`limit`.
    if (relevant.length >= limit) {
      sortByScoreDesc(relevant);
      return {
        clips: relevant.slice(0, limit),
        fetchedCount: pagesFetched,
        haltedAt: 'limit',
      };
    }

    // ── Halt path B: budget exhausted without reaching `limit`. ──
    // Return whatever relevant we have — NEVER pad with unfiltered
    // (this is the user-spec literal "ritorni SOLO i rilevanti" +
    // godlike/07 no-fake-availability contract).
    if (pagesFetched >= maxFetchPages) {
      sortByScoreDesc(relevant);
      return {
        clips: relevant,
        fetchedCount: pagesFetched,
        haltedAt: 'budget',
      };
    }

    // ── Halt path C: request more clip-page URLs. Empty → no more. ─
    const more = await discoverMore(seenClipIds);
    if (!Array.isArray(more) || more.length === 0) {
      sortByScoreDesc(relevant);
      return {
        clips: relevant,
        fetchedCount: pagesFetched,
        haltedAt: 'nomore',
      };
    }
    // (The orchestrator's fetchBatch closure drains a pendingUrls
    // queue that discoverMore appends to — no explicit merge here.)
  }

  // Defensive fallback for type-checkers. Unreachable: every halt
  // path above returns. Keeping a budget-halt return so the function
  // never returns undefined under future refactor drift.
  sortByScoreDesc(relevant);
  return { clips: relevant, fetchedCount: pagesFetched, haltedAt: 'budget' };
}

export const __testing = { DEFAULT_MAX_FETCH_PAGES, DEFAULT_BATCH_SIZE };
