#!/usr/bin/env node

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import puppeteer from 'puppeteer-core';
import { categories, searchTerms, videoLinks, closeDB } from './src/db.js';

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

function makeTempBrowserDir() {
  const baseDir = fs.existsSync('/dev/shm') ? '/dev/shm' : os.tmpdir();
  return fs.mkdtempSync(path.join(baseDir, 'velox-chrome-'));
}

function normalizeLinks(values) {
  return [...new Set(values.filter(Boolean).map((value) => String(value).trim().replace(/\\+$/, '')))];
}

function extractClipId(url) {
  const match = String(url || '').match(/\/clip\/[^/]+\/(\d+)/);
  return match ? match[1] : '';
}

function normalizeQuery(value) {
  return String(value || '')
    .toLowerCase()
    .normalize('NFKD')
    .replace(/[\u0300-\u036f]/g, '')
    .replace(/[^a-z0-9]+/g, ' ')
    .trim();
}

function tokenizeQuery(value) {
  return normalizeQuery(value)
    .split(/\s+/)
    .map((part) => part.trim())
    .filter((part) => part.length > 2);
}

function scoreClipRelevance(term, clip) {
  const tokens = tokenizeQuery(term);
  if (tokens.length === 0) return 0;

  const haystack = normalizeQuery([
    clip?.title,
    clip?.clip_page_url,
    clip?.primary_url,
    clip?.stream_urls?.join(' '),
  ].filter(Boolean).join(' '));

  let hits = 0;
  for (const token of tokens) {
    if (haystack.includes(token)) {
      hits += 1;
    }
  }

  if (hits === 0) return 0;
  if (tokens.length === 1) return hits >= 1 ? 100 : 0;

  const score = Math.round((hits / tokens.length) * 100);
  return score;
}

function isRelevantClip(term, clip) {
  return scoreClipRelevance(term, clip) >= (tokenizeQuery(term).length > 1 ? 60 : 100);
}

function resolveChromeProfile(profileDir) {
  if (profileDir && fs.existsSync(profileDir)) {
    return profileDir;
  }
  return makeTempBrowserDir();
}

async function openBrowser(profileDir) {
  const browserWs = process.env.BROWSER_WS || process.env.LIGHTPANDA_WS || process.env.CHROME_WS || '';
  if (browserWs) {
    const browser = await puppeteer.connect({
      browserWSEndpoint: browserWs,
    });
    return { browser, connected: true };
  }

  const userDataDir = resolveChromeProfile(profileDir);
  const browser = await puppeteer.launch({
    executablePath: process.env.CHROME_EXECUTABLE || '/usr/bin/google-chrome',
    headless: 'new',
    userDataDir,
    args: [
      '--no-sandbox',
      '--disable-gpu',
      '--disable-blink-features=AutomationControlled',
      '--no-first-run',
      '--no-default-browser-check',
    ],
  });
  return { browser, connected: false };
}

async function createBrowserPage(profileDir) {
  const { browser, connected } = await openBrowser(profileDir);
  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  return { browser, connected, context, page };
}

async function closeBrowserHandle(handle) {
  try {
    if (handle?.page) {
      await handle.page.close().catch(() => {});
    }
    if (handle?.context) {
      await handle.context.close().catch(() => {});
    }
  } finally {
    if (handle?.browser) {
      if (handle.connected && handle.browser.disconnect) {
        await handle.browser.disconnect().catch(() => {});
      } else if (handle.browser.close) {
        await handle.browser.close().catch(() => {});
      }
    }
  }
}

async function searchArtlist(term, limit, profileDir) {
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
    const concurrency = 4; // Fetch 4 clips at a time

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
            ...((html.match(/https?:\/\/[^"'\\s>]+\.m3u8[^"'\\s>]*/g) || [])),
            ...((html.match(/https?:\/\/[^"'\\s>]+\.mp4[^"'\\s>]*/g) || [])),
            ...((html.match(/https?:\/\/[^"'\\s>]+cdn[^"'\\s>]*/g) || [])),
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

async function exportCookiesForYtDlp(page, outputPath) {
  const cookies = await page.cookies();

  const lines = [
    '# Netscape HTTP Cookie File',
    '# Generated by PipelineGen Artlist scraper',
  ];

  for (const c of cookies) {
    const domain = c.domain || '';
    const includeSubdomains = domain.startsWith('.') ? 'TRUE' : 'FALSE';
    const path = c.path || '/';
    const secure = c.secure ? 'TRUE' : 'FALSE';
    const expires = c.expires && c.expires > 0 ? Math.floor(c.expires) : 0;
    const name = c.name;
    const value = c.value;

    lines.push([domain, includeSubdomains, path, secure, expires, name, value].join('\t'));
  }

  fs.writeFileSync(outputPath, lines.join('\n') + '\n', 'utf8');
  console.error(`[artlist] exported ${cookies.length} cookies to ${outputPath}`);
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
