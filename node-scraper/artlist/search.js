// Hybrid Artlist search orchestrator.
//
// Order of operations:
//   1. Open the search page (delegates to browser-session).
//   2. Set up response interception on the page (delegates to search-api).
//   3. Navigate to the search URL.
//   4. shouldUseFastPath(intercepted, limit)? return intercepted clips with
//      primary_url != clip_page_url (FAST PATH).
//   5. Otherwise: scroll DOM (delegates to search-dom), open detail tabs in
//      chunks (delegates to detail-fetcher), score + sort by relevance,
//      return top `limit` clips (FALLBACK PATH).
//
// Public surface:
//
//   shouldUseFastPath(intercepted, limit)  — pure branching decision
//   fetchClipDetailsBatch(browser, urls, n) — chunked detail fetcher
//   searchArtlist(term, limit, profileDir, existingBrowser?)  — main entry
//   searchArtlistPreview(term, limit, profileDir)            — quick preview

import {
  createBrowserPage,
  closeBrowserHandle,
} from '../src/driver/browser.js';
import { fetchClipDetails } from './detail-fetcher.js';
import {
  relevanceOverfetch,
  DEFAULT_MAX_FETCH_PAGES,
} from '../src/scrape/relevance-overfetch.js';
import { exportCookiesForYtDlp, importCookies, DEFAULT_COOKIE_FILE_PATH } from '../src/driver/cookies.js';
import { extractClipId } from '../src/scrape/url.js';
import {
  setupApiInterception,
  extractClipsFromApiResponses,
} from './search-api.js';
import {
  chunkArray,
  collectClipLinksFromHrefs,
} from './search-dom.js';

/** Minimum intercepted clips required to short-circuit to the API fast path. */
const FAST_PATH_MIN_CLIPS = 2;

/** Default per-chunk concurrency when opening detail tabs in parallel. */
const DEFAULT_DETAIL_CONCURRENCY = 8;

/**
 * Decides whether the intercepted API responses satisfy the fast-path
 * criterion: enough clips with a resolvable primary URL to skip the
 * DOM fallback.
 *
 * Pure function — safe to unit-test under `node --test` without
 * Puppeteer, network, or filesystem.
 *
 * Contract (kept in sync with the legacy behavior in
 * src/scrape/search-page.js before this refactor): the FAST PATH
 * fires only when at least `min(limit, FAST_PATH_MIN_CLIPS)` clips
 * have BOTH a primary_url AND that primary_url differs from their
 * clip_page_url (otherwise the page_url itself is the only URL,
 * which doesn't carry stream metadata).
 *
 * @param {Array<{primary_url?: string, clip_page_url?: string}>} intercepted
 * @param {number} limit
 * @returns {boolean}
 */
export function shouldUseFastPath(intercepted, limit) {
  if (!Array.isArray(intercepted)) return false;
  if (!Number.isFinite(limit) || limit <= 0) return false;
  const clipsWithStreams = intercepted.filter(
    (c) => c && c.primary_url && c.primary_url !== c.clip_page_url
  );
  return clipsWithStreams.length >= Math.min(limit, FAST_PATH_MIN_CLIPS);
}

/**
 * Opens reachable detail pages for `clipPageUrls` in chunks of
 * `concurrency`. Uses `chunkArray` from search-dom.js so the chunking
 * logic is testable in isolation.
 *
 * Null entries (failed fetches) are dropped before returning.
 *
 * @param {object} browser — puppeteer Browser instance
 * @param {string[]} clipPageUrls
 * @param {number} concurrency — chunk size; default DEFAULT_DETAIL_CONCURRENCY
 * @returns {Promise<Array<object>>}
 */
export async function fetchClipDetailsBatch(
  browser,
  clipPageUrls,
  concurrency = DEFAULT_DETAIL_CONCURRENCY
) {
  const out = [];
  for (const chunk of chunkArray(clipPageUrls, concurrency)) {
    const results = await Promise.all(
      chunk.map((url) => fetchClipDetails(browser, url))
    );
    for (const r of results) if (r) out.push(r);
  }
  return out;
}

/**
 * Hybrid search (FAST PATH + DOM FALLBACK). Exits with a JSON-shaped
 * payload {term, search_url, clips: Array<Clip>}.
 *
 * @param {string} term
 * @param {number} limit
 * @param {string} profileDir
 * @param {object|null} existingBrowser — optional puppeteer Browser to reuse
 * @returns {Promise<{term: string, search_url: string, clips: Array<object>}>}
 */
export async function searchArtlist(
  term,
  limit,
  profileDir,
  existingBrowser = null
) {
  let handle = null;
  let browser = existingBrowser;
  let page = null;

  if (existingBrowser) {
    const context = await existingBrowser.createBrowserContext();
    page = await context.newPage();
    handle = { browser: existingBrowser, context, page, connected: true };
  } else {
    handle = await createBrowserPage(profileDir);
    browser = handle.browser;
    page = handle.page;
  }

  await page.setViewport({ width: 1440, height: 900 });
  await page.setUserAgent(
    'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 ' +
      '(KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36'
  );

  // PR-ARTLIST-COOKIE-IMPORT (July 2026): inject a session cookie file
  // (JSON or Netscape) before touching Artlist. Without this the scraper
  // cannot reach authenticated streams / API responses.
  const cookiePath = process.env.ARTLIST_COOKIE_FILE || DEFAULT_COOKIE_FILE_PATH;
  await importCookies(page, cookiePath);

  const searchUrl = `https://artlist.io/stock-footage/search?terms=${encodeURIComponent(term)}`;

  // ─── Phase 1: API Interception ─────────────────────────────────────────
  const apiResponses = [];
  const responseHandler = setupApiInterception(page, apiResponses);

  try {
    await page.goto(searchUrl, { waitUntil: 'networkidle2', timeout: 120000 });
    await new Promise((resolve) => setTimeout(resolve, 800));

    const intercepted = extractClipsFromApiResponses(apiResponses, term);

    if (shouldUseFastPath(intercepted, limit)) {
      const clipsWithStreams = intercepted.filter(
        (c) => c.primary_url && c.primary_url !== c.clip_page_url
      );
      try {
        await exportCookiesForYtDlp(page, cookiePath);
      } catch {
        /* ignore cookie export failures on the fast path */
      }
      return {
        term,
        search_url: searchUrl,
        clips: clipsWithStreams.slice(0, limit),
      };
    }

    // ─── Phase 2: Fallback — Relevance overfetch loop ─────────────────────
    //
    // PR-P2-RELEVANCE-FILTER (July 2026). User-spec literal:
    //   "se i risultati rilevanti sono meno del `limit`, NON fare
    //    fallback sui non filtrati. Invece overfetch (apri più
    //    pagine dettaglio finché non raggiungi il limite o il
    //    budget di pagine) e poi restituisci solo i rilevanti.
    //    Limita a MAX_FETCH_PAGES per evitare costi incontrollati."
    //
    // The dispatch decision (limit / budget / nomore) is owned by
    // relevanceOverfetch — a pure helper (godlike/06 SSOT) that
    // NEVER pads the result set with unfiltered (godlike/07
    // no-fake-availability). The pre-PR branch
    //   `(scored.length >= limit ? scored : fallback)`
    // is RETIRED: the legacy anti-pattern silently hid the
    // relevance-filter deficit from the operator. With overfetch,
    // the scraper either returns ≥ `limit` relevant clips, or it
    // returns the best subset up to MAX_FETCH_PAGES with a
    // `haltedAt: 'budget'` observable in tests.
    //
    // Wiring:
    //   - pendingUrls  : queue of clip-page URLs awaiting a detail
    //                    fetch. Initially populated from intercepted
    //                    API responses; discoverMore appends more.
    //   - fetchBatch   : closure that drains up to `n` URLs from
    //                    pendingUrls → fetchClipDetailsBatch →
    //                    Clip[]. Returns [] when queue is empty.
    //   - discoverMore : closure that surfaces NEW clip-page URLs
    //                    via DOM scroll. Returns [] when the page
    //                    is exhausted (driver of 'nomore' halt).
    const interceptedPageUrls = intercepted
      .filter((c) => c.clip_page_url)
      .map((c) => c.clip_page_url);
    const seenUrls = new Set(interceptedPageUrls);
    const pendingUrls = [...interceptedPageUrls];

    const fetchBatch = async (n) => {
      if (n <= 0 || pendingUrls.length === 0) return [];
      const slice = pendingUrls.splice(0, n);
      const clips = await fetchClipDetailsBatch(
        browser,
        slice,
        Math.min(slice.length, DEFAULT_DETAIL_CONCURRENCY)
      );
      return Array.isArray(clips) ? clips : [];
    };

    const discoverMore = async () => {
      // Surface fresh clip-page URLs from the search-results page.
      // The underlying collectClipLinksFromHrefs loops until its
      // internal cap (`targetCandidates`) is reached OR the page is
      // exhausted. We surface up to `targetCandidates` fresh URLs
      // per discoverMore() call to keep the budget bounded.
      const targetCandidates = DEFAULT_DETAIL_CONCURRENCY;
      const fresh = [];
      await collectClipLinksFromHrefs(
        page,
        (newlyFound) => {
          for (const href of newlyFound) {
            if (seenUrls.has(href) || fresh.length >= targetCandidates) continue;
            seenUrls.add(href);
            fresh.push(href);
          }
        },
        targetCandidates
      );
      pendingUrls.push(...fresh);
      return fresh;
    };

    const overfetch = await relevanceOverfetch({
      term,
      limit: Math.max(limit, 1),
      maxFetchPages: DEFAULT_MAX_FETCH_PAGES,
      batchSize: DEFAULT_DETAIL_CONCURRENCY,
      fetchBatch,
      discoverMore,
    });

    try {
      await exportCookiesForYtDlp(page, cookiePath);
    } catch (e) {
      // eslint-disable-next-line no-console
      console.error('[artlist] cookie export failed:', e.message);
    }

    return {
      term,
      search_url: searchUrl,
      clips: overfetch.clips.map(({ score, ...clip }) => clip),
    };
  } finally {
    try {
      page.removeListener('response', responseHandler);
    } catch {
      /* ignore */
    }
    if (existingBrowser) {
      if (page) await page.close().catch(() => {});
      if (handle?.context) await handle.context.close().catch(() => {});
    } else {
      await closeBrowserHandle(handle);
    }
  }
}

/**
 * Quick preview search — collects clip links from the search page DOM
 * without opening detail tabs. Returns at most `limit` clips with a
 * page_url + placeholder stream_urls (caller can decide whether to
 * fetch details in a follow-up call).
 *
 * @param {string} term
 * @param {number} limit
 * @param {string} profileDir
 * @returns {Promise<{term: string, search_url: string, clips: Array<object>}>}
 */
export async function searchArtlistPreview(term, limit, profileDir) {
  const handle = await createBrowserPage(profileDir);
  const { page } = handle;
  await page.setViewport({ width: 1440, height: 900 });
  await page.setUserAgent(
    'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 ' +
      '(KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36'
  );

  // PR-ARTLIST-COOKIE-IMPORT (July 2026): preview path also needs session
  // cookies so that search-result pages show licensed previews.
  const cookiePath = process.env.ARTLIST_COOKIE_FILE || DEFAULT_COOKIE_FILE_PATH;
  await importCookies(page, cookiePath);

  const searchUrl = `https://artlist.io/stock-footage/search?terms=${encodeURIComponent(term)}`;
  try {
    await page.goto(searchUrl, { waitUntil: 'domcontentloaded', timeout: 120000 });
    await page
      .waitForSelector('a[href*="/stock-footage/clip/"]', { timeout: 60000 })
      .catch(() => {});
    await new Promise((resolve) => setTimeout(resolve, 1500));

    const clips = await page.evaluate((maxResults) => {
      const found = [];
      const seen = new Set();
      document
        .querySelectorAll('a[href*="/stock-footage/clip/"]')
        .forEach((el) => {
          const href = el.href || el.getAttribute('href') || '';
          if (!href || seen.has(href)) return;
          const title = (el.textContent || el.getAttribute('aria-label') || '').trim();
          seen.add(href);
          found.push({
            title,
            clip_page_url: href,
            primary_url: href,
            stream_urls: [],
          });
        });
      return found.slice(0, maxResults);
    }, limit);

    return {
      term,
      search_url: searchUrl,
      clips: clips.map((clip) => ({
        ...clip,
        clip_id: extractClipId(clip.clip_page_url),
      })),
    };
  } finally {
    await closeBrowserHandle(handle);
  }
}

export const __testing = {
  FAST_PATH_MIN_CLIPS,
  DEFAULT_DETAIL_CONCURRENCY,
  DEFAULT_MAX_FETCH_PAGES,
};
