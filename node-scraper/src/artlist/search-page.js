import { createBrowserPage, closeBrowserHandle } from './browser.js';
import { fetchClipDetails } from './detail-page.js';
import { scoreClipRelevance, isRelevantClip } from './scoring.js';
import { exportCookiesForYtDlp } from './cookies.js';
import { extractClipId } from './url.js';
import { setupApiInterception, extractClipsFromApiResponses } from './api-interception.js';

/**
 * Extracts clip links from the search page DOM by scrolling and collecting
 * anchor elements pointing to Artlist clip detail pages.
 */
async function collectClipLinks(page, targetCount) {
  const clipPages = [];
  const seen = new Set();
  const maxScrollRounds = Math.max(1, Math.min(8, Math.ceil(targetCount / 2) + 1));

  for (let round = 0; round < maxScrollRounds && clipPages.length < targetCount; round++) {
    await new Promise((resolve) => setTimeout(resolve, 300));

    const newlyFound = await page.evaluate(() => {
      const found = [];
      const seenLocal = new Set();
      document.querySelectorAll('a[href*=\"/stock-footage/clip/\"]').forEach((el) => {
        const href = el.href || el.getAttribute('href') || '';
        if (!href || seenLocal.has(href)) return;
        seenLocal.add(href);
        found.push(href);
      });
      return found;
    });

    for (const href of newlyFound) {
      if (seen.has(href)) continue;
      seen.add(href);
      clipPages.push(href);
      if (clipPages.length >= targetCount) break;
    }

    if (clipPages.length >= targetCount) break;
    await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
  }

  return clipPages;
}

/**
 * Fetches clip details (stream URLs) for a batch of clip page URLs with
 * configurable concurrency.
 */
async function fetchClipDetailsBatch(browser, clipPageUrls, concurrency = 8) {
  const clips = [];
  for (let i = 0; i < clipPageUrls.length; i += concurrency) {
    const chunk = clipPageUrls.slice(i, i + concurrency);
    const results = await Promise.all(chunk.map((url) => fetchClipDetails(browser, url)));
    clips.push(...results.filter(Boolean));
  }
  return clips;
}

/**
 * Searches Artlist for stock footage using a hybrid approach:
 * 1. Intercept Artlist API responses (GraphQL/XHR) on the search page
 *    to extract preview URLs directly — eliminating individual clip tabs.
 * 2. If interception yields enough clips with valid stream URLs, return
 *    them immediately (~1-2s instead of 5-10s).
 * 3. Otherwise fall back to per-tab detail pages (existing approach).
 */
export async function searchArtlist(term, limit, profileDir, existingBrowser = null) {
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
  await page.setUserAgent('Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36');

  const searchUrl = `https://artlist.io/stock-footage/search?terms=${encodeURIComponent(term)}`;

  // ─── Phase 1: API Interception ───────────────────────────────────────────
  const apiResponses = [];
  const responseHandler = setupApiInterception(page, apiResponses);

  try {
    await page.goto(searchUrl, { waitUntil: 'networkidle2', timeout: 120000 });
    await new Promise((resolve) => setTimeout(resolve, 800));

    // Try API interception first
    let clips = extractClipsFromApiResponses(apiResponses, term);
    const clipsWithStreams = clips.filter(c => c.primary_url && c.primary_url !== c.clip_page_url);

    if (clipsWithStreams.length >= Math.min(limit, 2)) {
      // FAST PATH: return clips directly from intercepted API responses
      try {
        await exportCookiesForYtDlp(page, '/tmp/artlist_cookies.txt');
      } catch (e) {
        console.error('[artlist] cookie export failed:', e.message);
      }

      return {
        term,
        search_url: searchUrl,
        clips: clipsWithStreams.slice(0, limit),
      };
    }

    // ─── Phase 2: Fallback — scroll DOM + open detail tabs ──────────────────
    const interceptedPageUrls = clips.filter(c => c.clip_page_url).map(c => c.clip_page_url);
    const targetCandidates = Math.max(limit, 1);
    let clipPageUrls = [...interceptedPageUrls];

    // Scroll the search page to discover more clips
    const seen = new Set(clipPageUrls);
    const moreUrls = await collectClipLinks(page, targetCandidates);
    for (const url of moreUrls) {
      if (!seen.has(url) && clipPageUrls.length < targetCandidates) {
        clipPageUrls.push(url);
        seen.add(url);
      }
    }

    // Open detail pages for each clip to capture stream URLs
    clips = await fetchClipDetailsBatch(browser, clipPageUrls.slice(0, targetCandidates), 8);

    const scored = clips
      .map((clip) => ({ ...clip, score: scoreClipRelevance(term, clip) }))
      .filter((clip) => isRelevantClip(term, clip))
      .sort((a, b) => b.score - a.score || String(a.clip_id).localeCompare(String(b.clip_id)));

    const fallback = clips
      .map((clip) => ({ ...clip, score: scoreClipRelevance(term, clip) }))
      .sort((a, b) => b.score - a.score || String(a.clip_id).localeCompare(String(b.clip_id)));

    try {
      await exportCookiesForYtDlp(page, '/tmp/artlist_cookies.txt');
    } catch (e) {
      console.error('[artlist] cookie export failed:', e.message);
    }

    return {
      term,
      search_url: searchUrl,
      clips: (scored.length >= limit ? scored : fallback).slice(0, limit).map(({ score, ...clip }) => clip),
    };
  } finally {
    try { page.removeListener('response', responseHandler); } catch (e) { /* ignore */ }
    if (existingBrowser) {
      if (page) await page.close().catch(() => {});
      if (handle?.context) await handle.context.close().catch(() => {});
    } else {
      await closeBrowserHandle(handle);
    }
  }
}

/**
 * Performs a quick preview search without fetching clip details.
 */
export async function searchArtlistPreview(term, limit, profileDir) {
  const handle = await createBrowserPage(profileDir);
  const { page } = handle;
  await page.setViewport({ width: 1440, height: 900 });
  await page.setUserAgent('Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36');

  const searchUrl = `https://artlist.io/stock-footage/search?terms=${encodeURIComponent(term)}`;
  try {
    await page.goto(searchUrl, { waitUntil: 'domcontentloaded', timeout: 120000 });
    await page.waitForSelector('a[href*=\"/stock-footage/clip/\"]', { timeout: 60000 }).catch(() => {});
    await new Promise((resolve) => setTimeout(resolve, 1500));

    const clips = await page.evaluate((maxResults) => {
      const found = [];
      const seen = new Set();
      document.querySelectorAll('a[href*=\"/stock-footage/clip/\"]').forEach((el) => {
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
