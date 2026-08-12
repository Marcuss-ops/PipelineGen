import { normalizeLinks, extractClipId } from './url.js';
import { importCookies } from '../driver/cookies.js';
import { addCandidateStream, looksLikeStreamUrl } from './detail-streams.js';
import {
  buildResult,
  extractFromApiObject,
  extractFromDom,
  extractFromJsonLd,
  extractFromNextData,
  findClipObject,
  mergeMetadata,
} from './detail-metadata.js';

export { extractFromDom, extractFromJsonLd, extractFromNextData, mergeMetadata } from './detail-metadata.js';
export { looksLikeStreamUrl } from './detail-streams.js';

// ── Constants ───────────────────────────────────────────────────────────

/** @type {string} */
const PROVIDER = 'artlist';

/** Default timeout for detail page navigation. */
const DEFAULT_NAV_TIMEOUT = 60000;

/** Default wait after navigation for dynamic content. */
const DEFAULT_SETTLE_MS = 300;

// ── Main entry point ─────────────────────────────────────────────────────

/**
 * Fetches details for a single clip from its detail page.
 * @param {object} browser Puppeteer browser instance
 * @param {string} clipPageUrl
 * @returns {Promise<object|null>}
 */
export async function fetchClipDetails(browser, clipPageUrl) {
  const detailPage = await browser.newPage();
  await detailPage.setViewport({ width: 1440, height: 900 });
  await detailPage.setUserAgent(
    'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 ' +
      '(KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36'
  );

  const cookiePath = process.env.ARTLIST_COOKIE_FILE?.trim() || '';
  await importCookies(detailPage, cookiePath);

  const streamSet = new Set();
  const apiResponses = [];

  const capture = (url) => {
    if (typeof url === 'string' && url.includes('.m3u8')) {
      streamSet.add(url.replace(/\\+$/, ''));
    }
  };

  const onRequest = (req) => capture(req.url());
  const onResponse = (res) => capture(res.url());

  detailPage.on('request', onRequest);
  detailPage.on('response', onResponse);

  // Also capture JSON API responses on the detail page.
  const apiHandler = async (response) => {
    try {
      const contentType = response.headers()['content-type'] || '';
      const url = response.url();
      if (
        (contentType.includes('json') || contentType.includes('text/plain')) &&
        (url.includes('artlist') || url.includes('/graphql') || url.includes('/api/'))
      ) {
        try {
          const json = await response.json();
          apiResponses.push({ url, data: json });
        } catch {
          // Non-JSON responses are expected.
        }
      }
    } catch {
      // Ignore header access errors.
    }
  };
  detailPage.on('response', apiHandler);

  try {
    // Artlist detail pages keep player/HLS requests open, so waiting for
    // networkidle2 can consume the full navigation timeout even after the
    // DOM and stream metadata are available. DOM readiness is sufficient;
    // the selector wait and settle delay below preserve player hydration.
    await detailPage.goto(clipPageUrl, {
      waitUntil: 'domcontentloaded',
      timeout: DEFAULT_NAV_TIMEOUT,
    });
    await detailPage
      .waitForSelector('video, [class*="player"], [class*="video"]', { timeout: 10000 })
      .catch(() => {});
    await new Promise((resolve) => setTimeout(resolve, DEFAULT_SETTLE_MS));

    // Artlist loads its HLS stream lazily. Trigger the player before we
    // read back the network/resource hints so the .m3u8 request is emitted.
    await detailPage
      .evaluate(() => {
        const video = document.querySelector('video');
        if (video) {
          try {
            video.scrollIntoView({ behavior: 'instant', block: 'center' });
          } catch {
            video.scrollIntoView();
          }
          video.play().catch(() => {
            const player = video.closest('[class*="player"]') || video.parentElement;
            if (player && typeof player.click === 'function') player.click();
          });
        }
        const playBtn = document.querySelector('[class*="play"], [class*="Play"], [aria-label*="play"], [aria-label*="Play"]');
        if (playBtn && typeof playBtn.click === 'function') playBtn.click();
      })
      .catch(() => {});
    await new Promise((resolve) => setTimeout(resolve, 3000));

    const title = await detailPage.title();
    if (title.includes('Just a moment')) {
      console.error(`[artlist] Cloudflare block detected for ${clipPageUrl}`);
      return null;
    }

    // 1. Try intercepted API responses first.
    let apiMetadata = {};
    for (const { data } of apiResponses) {
      const found = findClipObject(data);
      if (found) {
        apiMetadata = extractFromApiObject(found, clipPageUrl);
        break;
      }
    }

    // 2. __NEXT_DATA__
    const nextData = await detailPage
      .evaluate(() => {
        try {
          return window.__NEXT_DATA__ || null;
        } catch {
          return null;
        }
      })
      .catch(() => null);

    // 3. JSON-LD scripts
    const jsonLdScripts = await detailPage
      .evaluate(() =>
        Array.from(document.querySelectorAll('script[type="application/ld+json"]')).map((s) => ({
          innerHTML: s.innerHTML,
        }))
      )
      .catch(() => []);

    // 4. DOM selectors. The exported helper is self-contained so it can
    // run both in Node tests and as a serialized Puppeteer evaluate callback.
    const domMetadata = await detailPage
      .evaluate(extractFromDom, clipPageUrl)
      .catch(() => ({}));

    // 5. Build stream list from network interception + HTML scraping.
    const html = (await detailPage.evaluate(() => document.documentElement.outerHTML))
      .replace(/\\\//g, '/')
      .replace(/\\u0026/g, '&');
    const streamsFromPerf = await detailPage
      .evaluate(() =>
        performance
          .getEntriesByType('resource')
          .map((entry) => entry?.name || '')
          .filter(Boolean)
      )
      .catch(() => []);
    const videoSrc = await detailPage.evaluate(() => {
      const video = document.querySelector('video');
      if (!video) return '';
      const source = document.querySelector('video source[src]');
      return video.src || video.currentSrc || source?.src || '';
    });
    for (const url of [...streamsFromPerf, videoSrc]) {
      addCandidateStream(streamSet, url, clipPageUrl);
    }
    const streamsFromHtml = normalizeLinks([
      ...streamSet,
      ...((html.match(/https?:\/\/[^"'\\s>]+\.m3u8[^"'\\s>]*/g) || [])),
      ...((html.match(/https?:\/\/[^"'\\s>]+\.mp4[^"'\\s>]*/g) || [])),
      ...((html.match(/https?:\/\/[^"'\\s>]+(?:manifest|playlist)[^"'\\s>]*/g) || [])),
    ]).filter((url) => looksLikeStreamUrl(url) && url !== clipPageUrl);

    // Merge sources in priority order (lowest → highest):
    // textual page title < DOM < JSON-LD < __NEXT_DATA__ < API/GraphQL.
    const merged = mergeMetadata([
      { title: title || '' },
      domMetadata,
      extractFromJsonLd(jsonLdScripts, clipPageUrl),
      extractFromNextData(nextData, clipPageUrl),
      apiMetadata,
    ]);

    const clipId = extractClipId(clipPageUrl);

    const rawMetadata = {
      api: apiResponses.map(({ data }) => data),
      next: nextData,
      jsonLd: jsonLdScripts.map((s) => s.innerHTML),
      domSnapshot: {
        title,
        tagCount: domMetadata.tags?.length ?? 0,
        categoryCount: domMetadata.categories?.length ?? 0,
      },
    };

    const result = buildResult({
      clipPageUrl,
      clipId,
      title: merged.title || title,
      streams: streamsFromHtml,
      videoSrc,
      metadata: { ...merged, raw_metadata: rawMetadata },
    });

    if (result.stream_urls.length === 0 || result.primary_url === clipPageUrl || !looksLikeStreamUrl(result.primary_url)) {
      return {
        ok: false,
        error: 'STREAM_NOT_FOUND',
        provider: PROVIDER,
        clip_id: clipId,
        page_url: clipPageUrl,
        clip_page_url: clipPageUrl,
        stream_urls: result.stream_urls || [],
        raw_metadata: rawMetadata,
      };
    }

    return result;
  } catch (e) {
    console.error(`[artlist] failed to fetch detail for ${clipPageUrl}:`, e.message);
    return null;
  } finally {
    try {
      detailPage.removeListener('request', onRequest);
      detailPage.removeListener('response', onResponse);
      detailPage.removeListener('response', apiHandler);
    } catch {
      /* ignore */
    }
    await detailPage.close().catch(() => {});
  }
}
