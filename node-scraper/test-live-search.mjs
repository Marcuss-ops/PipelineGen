#!/usr/bin/env node
/**
 * Live Artlist direct-search diagnostic.
 *
 * Purpose:
 *   - navigate directly to /stock-footage/search?terms=<keyword>&sortId=3
 *   - observe JS/network failures instead of treating HTTP 200 as success
 *   - collect clip links across repeated scroll rounds until the DOM stabilizes
 *   - dedupe by clip_id
 *   - compare DOM coverage with any intercepted Artlist JSON/API clip IDs
 *   - save a JSON receipt; on zero-result runs also save HTML + screenshot
 *
 * This is an operator diagnostic only. It is intentionally not part of
 * `npm test` because it requires live Artlist access and a real browser.
 *
 * Usage:
 *   node test-live-search.mjs [keyword]
 *
 * Environment:
 *   ARTLIST_COOKIE_FILE=/path/to/cookies.txt   optional explicit session
 *   ARTLIST_LIVE_MAX_SCROLL_ROUNDS=40         safety cap (default 40)
 *   ARTLIST_LIVE_STABLE_ROUNDS=3              unchanged rounds before stop
 *   ARTLIST_LIVE_SCROLL_WAIT_MS=1200           wait after each scroll
 *   ARTLIST_LIVE_INITIAL_WAIT_MS=20000         selector wait budget
 *   ARTLIST_LIVE_TEST_OUTDIR=/tmp              receipt/screenshot directory
 */

import fs from 'node:fs';
import path from 'node:path';
import { createBrowserPage, closeBrowserHandle } from './src/driver/browser.js';
import { importCookies } from './src/driver/cookies.js';
import { setupApiInterception, extractClipsFromApiResponses } from './artlist/search-api.js';
import { extractClipId } from './src/scrape/url.js';

const keyword = String(process.argv[2] || 'electricity').trim();
if (!keyword) {
  console.error('keyword is required');
  process.exit(2);
}

function positiveInt(value, fallback) {
  const parsed = Number.parseInt(String(value || ''), 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function safeFileToken(value) {
  return String(value || 'query')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 80) || 'query';
}

const maxScrollRounds = positiveInt(process.env.ARTLIST_LIVE_MAX_SCROLL_ROUNDS, 40);
const stableRoundsRequired = positiveInt(process.env.ARTLIST_LIVE_STABLE_ROUNDS, 3);
const scrollWaitMs = positiveInt(process.env.ARTLIST_LIVE_SCROLL_WAIT_MS, 1200);
const initialWaitMs = positiveInt(process.env.ARTLIST_LIVE_INITIAL_WAIT_MS, 20_000);
const outDir = process.env.ARTLIST_LIVE_TEST_OUTDIR?.trim() || '/tmp';
const searchURL = new URL('https://artlist.io/stock-footage/search');
searchURL.searchParams.set('terms', keyword);
searchURL.searchParams.set('sortId', '3');

fs.mkdirSync(outDir, { recursive: true });

async function collectVisibleClipCards(page) {
  return page.evaluate(() => {
    const rows = [];
    const seen = new Set();

    for (const el of document.querySelectorAll('a[href*="/stock-footage/clip/"]')) {
      const href = el.href || el.getAttribute('href') || '';
      if (!href || seen.has(href)) continue;
      seen.add(href);

      const imgAlt = (el.querySelector('img')?.getAttribute('alt') || '').trim();
      const heading = el.querySelector('h1, h2, h3, h4, h5, h6, [class*="title"], [class*="Title"]');
      const headingText = (heading?.textContent || '').trim();
      const rawText = (el.textContent || '').trim().replace(/\s+/g, ' ').slice(0, 240);

      rows.push({
        href,
        title: imgAlt || headingText || rawText,
      });
    }

    return rows;
  });
}

async function clickLoadMoreIfPresent(page) {
  return page.evaluate(() => {
    const candidates = Array.from(document.querySelectorAll('button, [role="button"]'));
    const match = candidates.find((el) => {
      const text = (el.textContent || '').trim().toLowerCase();
      return text === 'load more' || text === 'show more' || text === 'see more';
    });
    if (!match) return false;
    match.click();
    return true;
  }).catch(() => false);
}

function clipIdFromCandidate(candidate) {
  return String(candidate?.clip_id || candidate?.id || extractClipId(candidate?.clip_page_url || candidate?.page_url || candidate?.primary_url || '') || '').trim();
}

let handle = null;
let responseHandler = null;
let requestFailedHandler = null;
let statusHandler = null;
let consoleHandler = null;
let pageErrorHandler = null;

const apiResponses = [];
const requestFailures = [];
const httpFailures = [];
const consoleErrors = [];
const pageErrors = [];
const observedSearchApiURLs = new Set();

try {
  console.log('=== Artlist direct-search live diagnostic ===');
  console.log(`keyword     : ${keyword}`);
  console.log(`search_url  : ${searchURL.toString()}`);
  console.log(`scroll cap  : ${maxScrollRounds}`);

  handle = await createBrowserPage('');
  const { page } = handle;
  await page.setViewport({ width: 1440, height: 900 });

  const configuredUserAgent = process.env.ARTLIST_USER_AGENT?.trim();
  if (configuredUserAgent) {
    await page.setUserAgent(configuredUserAgent);
  }

  const cookiePath = process.env.ARTLIST_COOKIE_FILE?.trim() || '';
  const importedCookies = await importCookies(page, cookiePath);
  console.log(`cookies     : ${importedCookies} imported`);

  responseHandler = setupApiInterception(page, apiResponses);

  requestFailedHandler = (request) => {
    const failure = request.failure?.();
    requestFailures.push({
      url: request.url(),
      method: request.method(),
      error_text: failure?.errorText || '',
    });
  };
  page.on('requestfailed', requestFailedHandler);

  statusHandler = (response) => {
    const url = response.url();
    const status = response.status();
    if (url.includes('search-api.artlist.io')) observedSearchApiURLs.add(url);
    if (status >= 400) {
      httpFailures.push({ status, url });
    }
  };
  page.on('response', statusHandler);

  consoleHandler = (message) => {
    if (message.type() === 'error') {
      consoleErrors.push(message.text());
    }
  };
  page.on('console', consoleHandler);

  pageErrorHandler = (error) => {
    pageErrors.push(error?.message || String(error));
  };
  page.on('pageerror', pageErrorHandler);

  const mainResponse = await page.goto(searchURL.toString(), {
    waitUntil: 'domcontentloaded',
    timeout: 60_000,
  });

  const mainStatus = mainResponse?.status?.() || 0;
  console.log(`http_status : ${mainStatus}`);
  console.log(`final_url   : ${page.url()}`);

  await page.waitForSelector('a[href*="/stock-footage/clip/"]', {
    timeout: initialWaitMs,
  }).catch(() => {});

  const collected = new Map();
  let stableRounds = 0;
  let lastCount = -1;
  let roundsPerformed = 0;

  for (let round = 1; round <= maxScrollRounds; round++) {
    const visible = await collectVisibleClipCards(page);
    for (const row of visible) {
      const id = extractClipId(row.href);
      const key = id || row.href;
      if (!collected.has(key)) {
        collected.set(key, {
          clip_id: id,
          title: row.title,
          clip_page_url: row.href,
        });
      }
    }

    const count = collected.size;
    roundsPerformed = round;
    console.log(`round ${String(round).padStart(2, '0')}: unique clips=${count}`);

    if (count === lastCount) {
      stableRounds += 1;
    } else {
      stableRounds = 0;
    }
    lastCount = count;

    if (stableRounds >= stableRoundsRequired) {
      break;
    }

    const clickedLoadMore = await clickLoadMoreIfPresent(page);
    await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
    await delay(clickedLoadMore ? Math.max(scrollWaitMs, 1800) : scrollWaitMs);
  }

  // Give asynchronous response.json() handlers a final chance to settle.
  await delay(500);

  const domClips = [...collected.values()];
  const domIds = new Set(domClips.map((clip) => clip.clip_id).filter(Boolean));
  const duplicateURLCount = domClips.length - new Set(domClips.map((clip) => clip.clip_page_url)).size;
  const invalidIDCount = domClips.filter((clip) => !clip.clip_id).length;

  const intercepted = extractClipsFromApiResponses(apiResponses, keyword);
  const apiIDs = new Set(intercepted.map(clipIdFromCandidate).filter(Boolean));
  const apiMissingInDOM = [...apiIDs].filter((id) => !domIds.has(id));
  const domOnlyIDs = [...domIds].filter((id) => !apiIDs.has(id));

  const pageState = await page.evaluate(() => ({
    title: document.title || '',
    body_preview: (document.body?.innerText || '').slice(0, 1200),
    anchors: document.querySelectorAll('a').length,
    scripts: document.scripts.length,
    next_data_present: !!document.querySelector('#__NEXT_DATA__'),
    ready_state: document.readyState,
    webdriver: navigator.webdriver,
  }));

  const zeroResults = domClips.length === 0;
  const apiCoverageCertified = apiIDs.size > 0 && apiMissingInDOM.length === 0;
  const obviousFailure = mainStatus >= 400 || zeroResults || invalidIDCount > 0 || duplicateURLCount > 0;

  const receipt = {
    ok: !obviousFailure,
    keyword,
    search_url: searchURL.toString(),
    final_url: page.url(),
    http_status: mainStatus,
    imported_cookie_count: importedCookies,
    rounds_performed: roundsPerformed,
    stable_rounds_required: stableRoundsRequired,
    dom_unique_clips: domClips.length,
    dom_unique_clip_ids: domIds.size,
    api_intercepted_responses: apiResponses.length,
    api_unique_clip_ids: apiIDs.size,
    api_coverage_certified: apiCoverageCertified,
    api_missing_in_dom: apiMissingInDOM,
    dom_only_ids: domOnlyIDs,
    invalid_dom_clip_ids: invalidIDCount,
    duplicate_dom_urls: duplicateURLCount,
    observed_search_api_urls: [...observedSearchApiURLs],
    request_failures: requestFailures.slice(0, 100),
    http_failures: httpFailures.slice(0, 100),
    console_errors: consoleErrors.slice(0, 100),
    page_errors: pageErrors.slice(0, 100),
    page_state: pageState,
    clips: domClips,
  };

  const token = safeFileToken(keyword);
  const receiptPath = path.join(outDir, `artlist-live-search-${token}.json`);
  fs.writeFileSync(receiptPath, JSON.stringify(receipt, null, 2) + '\n', 'utf8');

  console.log('\n=== result ===');
  console.log(`dom_unique_clips       : ${domClips.length}`);
  console.log(`api_unique_clip_ids    : ${apiIDs.size}`);
  console.log(`api_missing_in_dom     : ${apiMissingInDOM.length}`);
  console.log(`invalid_dom_clip_ids   : ${invalidIDCount}`);
  console.log(`duplicate_dom_urls     : ${duplicateURLCount}`);
  console.log(`search-api URLs seen   : ${observedSearchApiURLs.size}`);
  console.log(`request failures       : ${requestFailures.length}`);
  console.log(`HTTP >=400 responses   : ${httpFailures.length}`);
  console.log(`console errors         : ${consoleErrors.length}`);
  console.log(`page errors            : ${pageErrors.length}`);
  console.log(`API coverage certified : ${apiCoverageCertified}`);
  console.log(`receipt                 : ${receiptPath}`);

  if (zeroResults) {
    const htmlPath = path.join(outDir, `artlist-live-search-${token}.html`);
    const screenshotPath = path.join(outDir, `artlist-live-search-${token}.png`);
    fs.writeFileSync(htmlPath, await page.content(), 'utf8');
    await page.screenshot({ path: screenshotPath, fullPage: true }).catch(() => {});
    console.error('\nZERO RESULTS: saved diagnostics');
    console.error(`html       : ${htmlPath}`);
    console.error(`screenshot : ${screenshotPath}`);
    console.error(`body       : ${pageState.body_preview}`);
  }

  // Do not claim "all provider results" unless the provider exposed an API
  // set we can compare against. DOM-only success is useful, but not a proof
  // of complete provider pagination.
  if (domClips.length > 0 && apiIDs.size === 0) {
    console.warn('coverage note: DOM clips were found, but no provider API ID set was captured; full provider coverage is not certified.');
  }

  if (obviousFailure) {
    process.exitCode = 1;
  }
} catch (error) {
  console.error(`\nFATAL: ${error?.message || String(error)}`);
  if (error?.stack) console.error(error.stack);
  process.exitCode = 1;
} finally {
  if (handle?.page) {
    if (responseHandler) handle.page.removeListener('response', responseHandler);
    if (requestFailedHandler) handle.page.removeListener('requestfailed', requestFailedHandler);
    if (statusHandler) handle.page.removeListener('response', statusHandler);
    if (consoleHandler) handle.page.removeListener('console', consoleHandler);
    if (pageErrorHandler) handle.page.removeListener('pageerror', pageErrorHandler);
  }
  if (handle) {
    await closeBrowserHandle(handle);
  }
}
