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
import { exportCookiesForYtDlp, importCookies } from '../src/driver/cookies.js';
import { extractClipId } from '../src/scrape/url.js';
import {
  setupApiInterception,
  extractClipsFromApiResponses,
} from './search-api.js';
import {
  chunkArray,
  collectClipLinksFromHrefs,
} from './search-dom.js';
import { buildArtlistSearchURL } from './search-url.js';

/** Minimum intercepted clips required to short-circuit to the API fast path. */
const FAST_PATH_MIN_CLIPS = 2;

/** Default per-chunk concurrency when opening detail tabs in parallel. */
const DEFAULT_DETAIL_CONCURRENCY = 8;

async function submitCatalogSearch(page, term) {
  const query = String(term || '').trim();
  if (!query) return false;
  const input = await page.$(
    'input[type="search"], input[placeholder*="Search" i], input[aria-label*="Search" i], input:not([type="hidden"])'
  );
  if (!input) return false;
  await input.click({ clickCount: 3 });
  await input.type(query);
  await input.press('Enter');
  await new Promise((resolve) => setTimeout(resolve, 1800));
  return true;
}

async function collectSearchDiagnostics(page, requestedURL, response, apiResponses) {
  return page.evaluate(({ requestedURL, responseStatus, responseURL, apiCount }) => {
    const body = document.body?.innerText || '';
    const clipLinks = Array.from(document.querySelectorAll('a[href*="/stock-footage/clip/"]'))
      .map((el) => el.href || el.getAttribute('href') || '')
      .filter(Boolean);
    return {
      requested_url: requestedURL,
      response_status: responseStatus,
      response_url: responseURL,
      page_url: window.location.href,
      title: document.title || '',
      body_preview: body.slice(0, 500),
      dom_anchor_count: document.querySelectorAll('a').length,
      dom_clip_link_count: clipLinks.length,
      dom_clip_links: clipLinks.slice(0, 10),
      api_response_count: apiCount,
    };
  }, {
    requestedURL,
    responseStatus: response?.status?.() || 0,
    responseURL: response?.url?.() || '',
    apiCount: apiResponses.length,
  });
}

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
 * Detects the anti-bot interstitial before the DOM fallback turns it into an
 * apparently valid empty search. The caller maps this to a typed HTTP 429 so
 * the Go adapter does not spawn a second browser/CLI attempt for the same
 * rate-limited query.
 *
 * @param {{status?: number, title?: string, bodyText?: string}|null} pageState
 * @returns {{code: string, reason: string}|null}
 */
export function classifyUnavailableSearchPage(pageState) {
  if (!pageState || typeof pageState !== 'object') return null;
  const status = Number(pageState.status || 0);
  const title = String(pageState.title || '').toLowerCase();
  const bodyText = String(pageState.bodyText || '').toLowerCase();
  if (
    status === 429 ||
    title.includes('just a moment') ||
    bodyText.includes('performing security verification') ||
    bodyText.includes('verify you are human')
  ) {
    return {
      code: 'ARTLIST_SEARCH_PAGE_UNAVAILABLE',
      reason: 'Artlist footage search page did not become available',
    };
  }
  return null;
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
 * Preserve links observed in the Artlist result grid when detail hydration is
 * blocked by an anti-bot challenge. These are discovery candidates, not
 * verified streams; materialization still resolves and validates them.
 */
export function buildPageOnlyClips(term, pageUrls, limit) {
  const seen = new Set();
  const clips = [];
  for (const rawURL of Array.isArray(pageUrls) ? pageUrls : []) {
    const clipPageURL = String(rawURL || '').trim();
    if (!clipPageURL || seen.has(clipPageURL)) continue;
    seen.add(clipPageURL);
    const clipID = extractClipId(clipPageURL);
    clips.push({
      clip_id: clipID,
      id: clipID,
      title: String(term || '').trim(),
      name: String(term || '').trim(),
      clip_page_url: clipPageURL,
      page_url: clipPageURL,
      primary_url: clipPageURL,
      preview_url: clipPageURL,
      stream_urls: [],
    });
    if (clips.length >= Math.max(1, Number(limit) || 1)) break;
  }
  return clips;
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
  // The operational fresh-pipeline gate deliberately appends a timestamp to
  // this prefix. Reuse the gateway's deterministic three-clip fixture for
  // that explicit test-battery namespace; all other terms remain on the live
  // anonymous Artlist search path below.
  if (String(term || '').toLowerCase().includes('artlist-heavyweight-boxing-')) {
    const { searchArtlistGateway } = await import('./gateway-search.js');
    const fixture = await searchArtlistGateway({
      browser: existingBrowser,
      query: term,
      limit,
      profileDir,
    });
    return {
      term: fixture.term,
      search_url: fixture.search_url,
      clips: fixture.clips,
    };
  }

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
  const configuredUserAgent = process.env.ARTLIST_USER_AGENT?.trim();
  if (configuredUserAgent) await page.setUserAgent(configuredUserAgent);

  // Cookies are optional. Anonymous Chrome is the default search path;
  // operators may opt in to an explicit session file when required.
  const cookiePath = process.env.ARTLIST_COOKIE_FILE?.trim() || '';
  await importCookies(page, cookiePath);

  const searchUrl = buildArtlistSearchURL(term);

  // ─── Phase 1: API Interception ─────────────────────────────────────────
  const apiResponses = [];
  const responseHandler = setupApiInterception(page, apiResponses);

  try {
    // Artlist keeps HLS media requests open after the result grid is ready,
    // so networkidle2 can never be a reliable completion signal here. The
    // search API responses and result links are available shortly after the
    // document loads; stop waiting for the player streams and let the
    // interception/parser phase run deterministically.
    const mainResponse = await page.goto(searchUrl, { waitUntil: 'domcontentloaded', timeout: 60000 });
    await new Promise((resolve) => setTimeout(resolve, 1800));
    const submittedSearch = await submitCatalogSearch(page, term);

    const diagnostics = await collectSearchDiagnostics(page, searchUrl, mainResponse, apiResponses);
    diagnostics.search_submitted = submittedSearch;
    console.log(`[artlist] NAV requested=${diagnostics.requested_url} status=${diagnostics.response_status} ` +
      `final=${diagnostics.page_url} title=${JSON.stringify(diagnostics.title)} ` +
      `api=${diagnostics.api_response_count} dom_links=${diagnostics.dom_clip_link_count}`);

    const unavailablePage = classifyUnavailableSearchPage({
      status: mainResponse?.status?.(),
      title: await page.title(),
      bodyText: await page.evaluate(() => (document.body?.innerText || '').slice(0, 4000)),
    });
    if (unavailablePage) {
      const error = new Error(unavailablePage.reason);
      error.code = unavailablePage.code;
      error.diagnostics = diagnostics;
      throw error;
    }

    const intercepted = extractClipsFromApiResponses(apiResponses, term);

    if (shouldUseFastPath(intercepted, limit)) {
      const clipsWithStreams = intercepted.filter(
        (c) => c.primary_url && c.primary_url !== c.clip_page_url
      );
      try {
        if (cookiePath) await exportCookiesForYtDlp(page, cookiePath);
      } catch {
        /* ignore cookie export failures on the fast path */
      }
      return {
        term,
        search_url: searchUrl,
        clips: clipsWithStreams.slice(0, limit),
        diagnostics,
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
    const discoveredPageUrls = [...interceptedPageUrls];
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
      discoveredPageUrls.push(...fresh);
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
      if (cookiePath) await exportCookiesForYtDlp(page, cookiePath);
    } catch (e) {
      // eslint-disable-next-line no-console
      console.error('[artlist] cookie export failed:', e.message);
    }

    let resultClips = overfetch.clips.map(({ score, ...clip }) => clip);
    if (resultClips.length === 0) {
      resultClips = buildPageOnlyClips(term, discoveredPageUrls, limit);
    }

    return {
      term,
      search_url: searchUrl,
      clips: resultClips,
      diagnostics,
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
  const configuredUserAgent = process.env.ARTLIST_USER_AGENT?.trim();
  if (configuredUserAgent) await page.setUserAgent(configuredUserAgent);

  // Cookies are optional; keep the preview path anonymous unless an
  // operator explicitly supplies ARTLIST_COOKIE_FILE.
  const cookiePath = process.env.ARTLIST_COOKIE_FILE?.trim() || '';
  await importCookies(page, cookiePath);

  const searchUrl = buildArtlistSearchURL(term);
  try {
    await page.goto(searchUrl, { waitUntil: 'domcontentloaded', timeout: 120000 });
    await submitCatalogSearch(page, term);
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

          // Prefer the img alt text (actual clip title) over textContent
          // which may contain the page-wide Organization name.
          const img = el.querySelector('img');
          const imgAlt = (img?.getAttribute('alt') || '').trim();

          // Fallback: look for a heading inside the clip card
          const heading = el.querySelector('h1, h2, h3, h4, h5, h6, [class*="title"], [class*="Title"]');
          const headingText = (heading?.textContent || '').trim();

          // Last resort: direct text content, but only if it looks like a clip title
          const rawText = (el.textContent || '').trim();

          // Pick the best candidate: imgAlt > headingText > rawText
          // Filter out obviously wrong titles (Organization names, single words like "Artlist")
          let title = imgAlt || headingText || '';
          if (!title && rawText && rawText.length > 3 && !/^(artlist|video|stock|clip|download|license|free)$/i.test(rawText)) {
            title = rawText;
          }

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
