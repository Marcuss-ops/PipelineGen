import { createBrowserPage, closeBrowserHandle } from './browser.js';
import { fetchClipDetails } from './detail-page.js';
import { scoreClipRelevance, isRelevantClip } from './scoring.js';
import { exportCookiesForYtDlp } from './cookies.js';
import { extractClipId } from './url.js';

/**
 * Searches Artlist for stock footage.
 * @param {string} term 
 * @param {number} limit 
 * @param {string} profileDir 
 * @returns {Promise<object>}
 */
export async function searchArtlist(term, limit, profileDir) {
  const handle = await createBrowserPage(profileDir);
  const { browser, page } = handle;
  await page.setViewport({ width: 1440, height: 900 });
  await page.setUserAgent('Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36');

  const searchUrl = `https://artlist.io/stock-footage/search?terms=${encodeURIComponent(term)}`;
  try {
    await page.goto(searchUrl, { waitUntil: 'domcontentloaded', timeout: 120000 });
    await page.waitForSelector('a[href*="/stock-footage/clip/"]', { timeout: 60000 }).catch(() => {});
    
    const clipPages = [];
    const seen = new Set();
    const targetCandidates = Math.max(limit, 1);
    const maxScrollRounds = Math.max(1, Math.min(8, Math.ceil(targetCandidates / 2) + 1));

    for (let round = 0; round < maxScrollRounds && clipPages.length < targetCandidates; round++) {
      await new Promise((resolve) => setTimeout(resolve, 300));

      const newlyFound = await page.evaluate(() => {
        const found = [];
        const seenLocal = new Set();
        document.querySelectorAll('a[href*="/stock-footage/clip/"]').forEach((el) => {
          const href = el.href || el.getAttribute('href') || '';
          if (!href || seenLocal.has(href)) {
            return;
          }
          seenLocal.add(href);
          found.push(href);
        });
        return found;
      });

      for (const href of newlyFound) {
        if (seen.has(href)) {
          continue;
        }
        seen.add(href);
        clipPages.push(href);
        if (clipPages.length >= targetCandidates) {
          break;
        }
      }

      if (clipPages.length >= targetCandidates) {
        break;
      }

      await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
    }

    const clips = [];
    const clipQueue = clipPages.slice(0, targetCandidates);
    const concurrency = 8; // Fetch 8 clips at a time

    for (let i = 0; i < clipQueue.length; i += concurrency) {
      const chunk = clipQueue.slice(i, i + concurrency);
      const results = await Promise.all(chunk.map((clipPageUrl) => fetchClipDetails(browser, clipPageUrl)));
      clips.push(...results.filter(Boolean));
    }

    const scored = clips
      .map((clip) => ({
        ...clip,
        score: scoreClipRelevance(term, clip),
      }))
      .filter((clip) => isRelevantClip(term, clip))
      .sort((a, b) => {
        if (b.score !== a.score) return b.score - a.score;
        return String(a.clip_id).localeCompare(String(b.clip_id));
      });

    const fallback = clips
      .map((clip) => ({
        ...clip,
        score: scoreClipRelevance(term, clip),
      }))
      .sort((a, b) => {
        if (b.score !== a.score) return b.score - a.score;
        return String(a.clip_id).localeCompare(String(b.clip_id));
      });

    // Export cookies for yt-dlp/ffmpeg after successful search
    try {
      const cookiePath = '/tmp/artlist_cookies.txt';
      await exportCookiesForYtDlp(page, cookiePath);
    } catch (e) {
      console.error('[artlist] cookie export failed:', e.message);
    }

    return {
      term,
      search_url: searchUrl,
      clips: (scored.length >= limit ? scored : fallback).slice(0, limit).map(({ score, ...clip }) => clip),
    };
  } finally {
    await closeBrowserHandle(handle);
  }
}

/**
 * Performs a quick preview search without fetching clip details.
 * @param {string} term 
 * @param {number} limit 
 * @param {string} profileDir 
 * @returns {Promise<object>}
 */
export async function searchArtlistPreview(term, limit, profileDir) {
  const handle = await createBrowserPage(profileDir);
  const { page } = handle;
  await page.setViewport({ width: 1440, height: 900 });
  await page.setUserAgent('Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36');

  const searchUrl = `https://artlist.io/stock-footage/search?terms=${encodeURIComponent(term)}`;
  try {
    await page.goto(searchUrl, { waitUntil: 'domcontentloaded', timeout: 120000 });
    await page.waitForSelector('a[href*="/stock-footage/clip/"]', { timeout: 60000 }).catch(() => {});
    await new Promise((resolve) => setTimeout(resolve, 1500));

    const clips = await page.evaluate((maxResults) => {
      const found = [];
      const seen = new Set();
      document.querySelectorAll('a[href*="/stock-footage/clip/"]').forEach((el) => {
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
