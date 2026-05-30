#!/usr/bin/env node

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import puppeteer from 'puppeteer-core';
import { categories, searchTerms, videoLinks, closeDB } from './src/db.js';
import { normalizeQuery, tokenizeQuery, scoreClipRelevance, isRelevantClip } from './src/artlist/scoring.js';
import { extractClipId, normalizeLinks } from './src/artlist/url.js';
import { exportCookiesForYtDlp } from './src/artlist/cookies.js';
import { makeTempBrowserDir, resolveChromeProfile, openBrowser, createBrowserPage, closeBrowserHandle } from './src/artlist/browser.js';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

function parseArgs(argv) {
  const args = {
    term: '',
    limit: 8,
    saveDb: false,
    dbPath: path.join(__dirname, 'artlist_videos.db'),
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
    } else if (arg === '--save-db') {
      args.saveDb = true;
    } else if (arg === '--db-path') {
      args.dbPath = next || args.dbPath;
      i++;
    } else if (arg === '--profile-dir') {
      args.profileDir = next || args.profileDir;
      i++;
    }
  }

  return args;
}

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
  try {
    await page.goto(searchUrl, { waitUntil: 'domcontentloaded', timeout: 120000 });
    await page.waitForSelector('a[href*="/stock-footage/clip/"]', { timeout: 60000 }).catch(() => {});
    const clipPages = [];
    const seen = new Set();
    const targetCandidates = Math.max(limit, 1);
    const maxScrollRounds = Math.max(1, Math.min(8, Math.ceil(targetCandidates / 2) + 1));

    for (let round = 0; round < maxScrollRounds && clipPages.length < targetCandidates; round++) {
      await new Promise((resolve) => setTimeout(resolve, 1000));

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
    const concurrency = 4;

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
        const onRequest = (req) => capture(req.url());
        const onResponse = (res) => capture(res.url());
        detailPage.on('request', onRequest);
        detailPage.on('response', onResponse);

        try {
          await detailPage.goto(clipPageUrl, { waitUntil: 'networkidle2', timeout: 60000 });
          await detailPage.waitForSelector('video, [class*="player"], [class*="video"]', { timeout: 10000 }).catch(() => {});
          await new Promise((resolve) => setTimeout(resolve, 1000));
          const title = await detailPage.title();
          
          if (title.includes('Just a moment')) {
            console.error(`[artlist] Cloudflare block detected for ${clipPageUrl}`);
            return null;
          }

          const html = (await detailPage.evaluate(() => document.documentElement.outerHTML))
            .replace(/\\\//g, '/')
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
          if (videoSrc && !streams.includes(videoSrc)) {
            streams.push(videoSrc);
          }
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

async function main() {
  const args = parseArgs(process.argv);
  if (!args.term) {
    console.error(JSON.stringify({ ok: false, error: 'missing --term' }));
    process.exit(2);
  }

  const result = await searchArtlist(args.term, args.limit, args.profileDir);

  let saved = 0;
  if (args.saveDb) {
    categories.add('Artlist', 'Imported Artlist search results');
    searchTerms.addMultiple('Artlist', [args.term]);
    const urls = [];
    const meta = [];
    for (const clip of result.clips) {
      if (!clip.primary_url) continue;
      urls.push(clip.primary_url);
      meta.push({
        video_id: clip.clip_id || clip.primary_url,
        file_name: clip.title || clip.clip_id || '',
        width: 0,
        height: 0,
        duration: 0,
      });
    }
    saved = urls.length > 0 ? videoLinks.addMultipleWithSource('Artlist', args.term, urls, 'artlist', meta) : 0;
    closeDB();
  }

  console.log(JSON.stringify({
    ok: true,
    term: result.term,
    search_url: result.search_url,
    saved,
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
