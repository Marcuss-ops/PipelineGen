#!/usr/bin/env node

import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { scoreClipRelevance, isRelevantClip } from './src/artlist/scoring.js';
import { extractClipId, normalizeLinks } from './src/artlist/url.js';
import { exportCookiesForYtDlp } from './src/artlist/cookies.js';
import { createBrowserPage, closeBrowserHandle } from './src/artlist/browser.js';
import { setupApiInterception, extractClipsFromApiResponses } from './src/artlist/api-interception.js';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

function parseArgs(argv) {
  const args = {
    term: '',
    limit: 8,
    profileDir: process.env.CHROME_PROFILE_DIR || '',
  };

  for (let i = 2; i < argv.length; i++) {
    const arg = argv[i];
    const next = argv[i + 1];
    if (arg === '--term' || arg === '-t') {
      args.term = next || '';
      i++;
    } else if (arg === '--limit' || arg === '-l') {
      args.limit = Number.parseInt(next || '8', 10) || 8;
      i++;
    } else if (arg === '--profile-dir') {
      args.profileDir = next || args.profileDir;
      i++;
    }
  }

  return args;
}

/**
 * Searches Artlist for stock footage using a hybrid approach:
 * 1. Intercept Artlist API responses (GraphQL/XHR) on the search page
 *    to extract preview URLs directly — eliminates opening individual tabs.
 * 2. If interception yields enough clips with stream URLs, return them (~1-2s).
 * 3. Otherwise fall back to per-tab detail pages (existing behavior).
 */
async function searchArtlist(term, limit, profileDir, existingBrowser = null) {
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

  // ─── Phase 1: API Interception ─────────────────────────────────────────
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
      } catch (e) { /* ignore */ }

      return {
        term,
        search_url: searchUrl,
        clips: clipsWithStreams.slice(0, limit),
      };
    }

    // ─── Phase 2: Fallback — scroll DOM + open detail tabs ──────────────
    const interceptedPageUrls = clips.filter(c => c.clip_page_url).map(c => c.clip_page_url);
    const targetCandidates = Math.max(limit, 1);
    let clipPageUrls = [...interceptedPageUrls];

    // Scroll the search page to discover more clips
    const maxScrollRounds = Math.max(1, Math.min(8, Math.ceil(targetCandidates / 2) + 1));
    const seen = new Set(clipPageUrls);

    for (let round = 0; round < maxScrollRounds && clipPageUrls.length < targetCandidates; round++) {
      await new Promise((resolve) => setTimeout(resolve, 300));

      const newlyFound = await page.evaluate(() => {
        const found = [];
        const localSeen = new Set();
        document.querySelectorAll('a[href*=\"/stock-footage/clip/\"]').forEach((el) => {
          const href = el.href || el.getAttribute('href') || '';
          if (!href || localSeen.has(href)) return;
          localSeen.add(href);
          found.push(href);
        });
        return found;
      });

      for (const href of newlyFound) {
        if (seen.has(href)) continue;
        seen.add(href);
        clipPageUrls.push(href);
        if (clipPageUrls.length >= targetCandidates) break;
      }

      if (clipPageUrls.length >= targetCandidates) break;
      await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
    }

    // Open detail pages for each clip to capture stream URLs
    clips = [];
    const clipQueue = clipPageUrls.slice(0, targetCandidates);
    const concurrency = 8;

    for (let i = 0; i < clipQueue.length; i += concurrency) {
      const chunk = clipQueue.slice(i, i + concurrency);
      const results = await Promise.all(chunk.map(async (clipPageUrl) => {
        const detailPage = await browser.newPage();
        await detailPage.setViewport({ width: 1440, height: 900 });
        await detailPage.setUserAgent('Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36');

        const streamSet = new Set();
        const capture = (url) => {
          if (typeof url === 'string' && url.includes('.m3u8')) {
            streamSet.add(url.replace(/\\+$/, ''));
          }
        };
        detailPage.on('request', (req) => capture(req.url()));
        detailPage.on('response', (res) => capture(res.url()));

        try {
          await detailPage.goto(clipPageUrl, { waitUntil: 'networkidle2', timeout: 60000 });
          await detailPage.waitForSelector('video, [class*="player"], [class*="video"]', { timeout: 10000 }).catch(() => {});
          await new Promise((resolve) => setTimeout(resolve, 300));
          const title = await detailPage.title();

          if (title.includes('Just a moment')) return null;

          const html = (await detailPage.evaluate(() => document.documentElement.outerHTML))
            .replace(/\\\\\//g, '/')
            .replace(/\\u0026/g, '&');
          const streams = normalizeLinks([
            ...streamSet,
            ...((html.match(/https?:\/\/[^"'\s>]+\.m3u8[^"'\s>]*/g) || [])),
            ...((html.match(/https?:\/\/[^"'\s>]+\.mp4[^"'\s>]*/g) || [])),
            ...((html.match(/https?:\/\/[^"'\s>]+cdn[^"'\s>]*/g) || [])),
          ]);
          const videoSrc = await detailPage.evaluate(() => {
            const video = document.querySelector('video');
            return video ? (video.src || video.currentSrc || '') : '';
          });
          if (videoSrc && !streams.includes(videoSrc)) streams.push(videoSrc);

          return {
            title,
            clip_page_url: clipPageUrl,
            stream_urls: streams,
            primary_url: streams[0] || videoSrc || clipPageUrl,
            clip_id: extractClipId(clipPageUrl),
          };
        } catch (e) {
          console.error(`[artlist] failed to fetch detail for ${clipPageUrl}:`, e.message);
          return null;
        } finally {
          await detailPage.close().catch(() => {});
        }
      }));
      clips.push(...results.filter(Boolean));
    }

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

async function searchArtlistPreview(term, limit, profileDir) {
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

async function main() {
  const args = parseArgs(process.argv);
  if (!args.term) {
    console.error(JSON.stringify({ ok: false, error: 'missing --term' }));
    process.exit(2);
  }

  const result = await searchArtlist(args.term, args.limit, args.profileDir);

  console.log(JSON.stringify({
    ok: true,
    term: result.term,
    search_url: result.search_url,
    saved: 0,
    clips: result.clips,
  }, null, 2));
}

const isMain = process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url);

if (isMain) {
  main().catch((err) => {
    console.error(JSON.stringify({
      ok: false,
      error: err?.message || String(err),
    }));
    process.exit(1);
  });
}

export {
  extractClipId,
  isRelevantClip,
  normalizeLinks,
  scoreClipRelevance,
  searchArtlist,
  searchArtlistPreview,
};
