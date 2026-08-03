import { normalizeLinks, extractClipId } from './url.js';
import { importCookies } from '../driver/cookies.js';

/**
 * ArtlistDetailHydrator — extracts structured metadata from an Artlist
 * clip detail page.  The hydrator follows a strict fallback chain:
 *
 *   1. Intercepted API / GraphQL responses while the page loads.
 *   2. window.__NEXT_DATA__ (Next.js app data).
 *   3. JSON-LD scripts in the page.
 *   4. DOM selectors and meta tags.
 *   5. Controlled textual fallback (page title, canonical URL).
 *
 * The function keeps the original return shape and adds new canonical
 * fields: description, creator, country, location, tags, categories,
 * thumbnail_url, preview_url, and raw_metadata.
 */

// ── Constants ───────────────────────────────────────────────────────────

/** @type {string} */
const PROVIDER = 'artlist';

/** Default timeout for detail page navigation. */
const DEFAULT_NAV_TIMEOUT = 60000;

/** Default wait after navigation for dynamic content. */
const DEFAULT_SETTLE_MS = 300;

// exported so the unit-test net (detail-page.test.js) can probe the
// happy-path matcher in isolation; otherwise it would be unreachable
// from outside the IIFE-shaped fetchClipDetails function body.
export function looksLikeStreamUrl(url) {
  const trimmed = toString(url);
  if (!trimmed) return false;
  return (
    /\.m3u8(?:\?|$)/i.test(trimmed) ||
    /\.mp4(?:\?|$)/i.test(trimmed) ||
    /manifest/i.test(trimmed) ||
    /playlist/i.test(trimmed)
  );
}

function addCandidateStream(streamSet, url, clipPageUrl) {
  if (typeof url !== 'string') return;
  const trimmed = url.trim().replace(/\\+$/, '');
  if (!trimmed || trimmed === clipPageUrl) return;
  if (looksLikeStreamUrl(trimmed)) {
    streamSet.add(trimmed);
  }
}

// ── Pure helper: array/string normalization ─────────────────────────────

/**
 * Ensures a value is an array of non-empty strings.
 * @param {any} value
 * @returns {string[]}
 */
function toStringArray(value) {
  if (value == null || value === '') return [];
  if (typeof value === 'string') return value.split(',').map((s) => s.trim()).filter(Boolean);
  if (Array.isArray(value)) {
    return value
      .filter((v) => v != null && v !== '')
      .map((v) => (typeof v === 'string' ? v.trim() : String(v).trim()))
      .filter(Boolean);
  }
  return [];
}

/**
 * Trims and coerces a value to a string, returning empty string for null/undefined.
 * @param {any} value
 * @returns {string}
 */
function toString(value) {
  if (value == null) return '';
  return String(value).trim();
}

// ── Extraction from intercepted API / GraphQL responses ─────────────────

/**
 * Recursively walks an object looking for the first clip-like object that
 * carries enough Artlist detail fields.
 * @param {any} obj
 * @returns {object|null}
 */
function findClipObject(obj) {
  if (obj == null || typeof obj !== 'object') return null;

  // Direct match: object with id/title and extra detail fields.
  if ((obj.id || obj.clipId || obj._id) && (obj.title || obj.name) && (Array.isArray(obj.tags) || obj.description || obj.creator || obj.author)) {
    return obj;
  }

  // Array branch: walk up to a reasonable depth.
  if (Array.isArray(obj)) {
    for (const item of obj) {
      const found = findClipObject(item);
      if (found) return found;
    }
    return null;
  }

  // Object branch.
  for (const key of Object.keys(obj)) {
    // Skip large string payloads and obvious non-clip collections.
    if (key === '__next' || key === 'runtime' || key === 'env') continue;
    const found = findClipObject(obj[key]);
    if (found) return found;
  }
  return null;
}

/**
 * Builds a normalized metadata object from a raw API/GraphQL clip object.
 * @param {object} raw
 * @param {string} clipPageUrl
 * @returns {object}
 */
function extractFromApiObject(raw, clipPageUrl) {
  if (!raw) return {};

  const tags = toStringArray(raw.tags || raw.keywords);
  const categories = toStringArray(raw.categories);

  return {
    title: toString(raw.title || raw.name),
    description: toString(raw.description),
    creator: toString(raw.creator || raw.author || raw.artist || raw.contributor),
    country: toString(raw.country || raw.countryName),
    location: toString(raw.location || raw.shootingLocation || raw.place),
    tags,
    categories,
    thumbnail_url: toString(raw.thumbnailUrl || raw.thumbnail_url || raw.image),
    preview_url: toString(raw.previewUrl || raw.preview_url || raw.video),
    clip_page_url: toString(raw.clipPageUrl || raw.clip_page_url || clipPageUrl),
  };
}

// ── Extraction from __NEXT_DATA__ ──────────────────────────────────────

/**
 * Extracts clip metadata from Next.js __NEXT_DATA__.
 * @param {object|null} nextData
 * @param {string} clipPageUrl
 * @returns {object}
 */
export function extractFromNextData(nextData, clipPageUrl) {
  if (!nextData || typeof nextData !== 'object') return {};

  const props = nextData.props || nextData;
  const pageProps = props.pageProps || props;
  const initialProps = pageProps.initialProps || pageProps;

  // Common shapes seen in Next.js detail pages: clip, asset, product, etc.
  const candidates = [
    pageProps.clip,
    pageProps.asset,
    pageProps.product,
    pageProps.data,
    initialProps.clip,
    initialProps.asset,
    initialProps.product,
    initialProps.data,
  ].filter(Boolean);

  for (const candidate of candidates) {
    if (typeof candidate === 'object') {
      const found = findClipObject(candidate);
      if (found) {
        const meta = extractFromApiObject(found, clipPageUrl);
        if (meta.title) return meta;
      }
    }
  }

  // Fallback: search the whole tree up to a shallow depth.
  const found = findClipObject(pageProps);
  if (found) return extractFromApiObject(found, clipPageUrl);
  return {};
}

// ── Extraction from JSON-LD ───────────────────────────────────────────────

/**
 * Parses JSON-LD script contents and extracts video metadata.
 * @param {Array<{innerHTML?: string}>} jsonLdScripts
 * @param {string} clipPageUrl
 * @returns {object}
 */
export function extractFromJsonLd(jsonLdScripts, clipPageUrl) {
  const result = {};
  if (!Array.isArray(jsonLdScripts)) return result;

  for (const script of jsonLdScripts) {
    const html = script?.innerHTML || '';
    if (!html.trim()) continue;
    try {
      const data = JSON.parse(html);
      const graph = Array.isArray(data['@graph']) ? data['@graph'] : [data];

      for (const item of graph) {
        if (!item || typeof item !== 'object') continue;
        const type = item['@type'] || '';
        if (type === 'VideoObject' || item.name || item.description || item.contentUrl) {
          result.title = result.title || toString(item.name);
          result.description = result.description || toString(item.description);
          result.thumbnail_url = result.thumbnail_url || toString(item.thumbnailUrl || item.thumbnail?.url);
          result.preview_url = result.preview_url || toString(item.contentUrl || item.embedUrl);
          result.clip_page_url = result.clip_page_url || toString(item.url) || clipPageUrl;
        }
      }
    } catch {
      // Ignore malformed JSON-LD.
    }
  }

  return result;
}

// ── Extraction from DOM (runs in browser context) ─────────────────────────

/**
 * Extracts metadata from the live DOM.  This function is designed to be
 * passed to page.evaluate(); it only uses standard Web APIs so it can be
 * tested with a lightweight document mock.
 * @param {Document} document
 * @param {string} clipPageUrl
 * @returns {object}
 */
export function extractFromDom(document, clipPageUrl) {
  if (!document) return {};

  const meta = (name) => {
    const el = document.querySelector(`meta[property="${name}"], meta[name="${name}"]`);
    return el ? el.getAttribute('content') || '' : '';
  };

  const queryText = (selectors) => {
    for (const selector of selectors) {
      const el = document.querySelector(selector);
      if (el && el.textContent) return el.textContent.trim();
    }
    return '';
  };

  const queryMany = (selectors) => {
    const out = [];
    for (const selector of selectors) {
      try {
        const nodes = document.querySelectorAll(selector);
        for (const node of nodes) {
          const text = node.textContent?.trim();
          if (text) out.push(text);
        }
      } catch {
        // Ignore invalid selectors.
      }
    }
    return out;
  };

  const title = document.title?.trim() || queryText(['h1', '[data-testid="clip-title"]', '.clip-title']) || '';
  const description = meta('og:description') || meta('description') || queryText(['[data-testid="clip-description"]', '.clip-description']) || '';
  const creator = queryText([
    '[data-testid="creator-name"]',
    '[data-testid="artist-name"]',
    '.creator-name',
    '.artist-name',
    '[itemprop="author"]',
  ]);
  const country = queryText(['[data-testid="country"]', '.country', '[itemprop="location"]']);
  const location = queryText(['[data-testid="location"]', '.location', '[itemprop="place"]']) || country;

  const tagSelectors = [
    '[data-testid="clip-tag"], [data-testid="tag"], .clip-tag, a[href*="/tag/"], a[href*="/stock-footage/tag/"]',
  ];
  const categorySelectors = [
    '[data-testid="clip-category"], .clip-category, a[href*="/category/"], a[href*="/stock-footage/category/"]',
  ];

  const tags = toStringArray(queryMany(tagSelectors));
  const categories = toStringArray(queryMany(categorySelectors));

  const thumbnail_url = meta('og:image') || meta('twitter:image') || '';
  const preview_url = meta('og:video') || meta('twitter:player') || '';

  return {
    title,
    description,
    creator,
    country,
    location,
    tags,
    categories,
    thumbnail_url,
    preview_url,
    clip_page_url: clipPageUrl,
  };
}

// ── Metadata merging ────────────────────────────────────────────────────

/**
 * Merges metadata sources in priority order.  Later sources override
 * earlier ones only when they provide a non-empty value.
 * @param {Array<object>} sources
 * @returns {object}
 */
export function mergeMetadata(sources) {
  const merged = {};
  for (const source of sources) {
    if (!source || typeof source !== 'object') continue;
    for (const [key, value] of Object.entries(source)) {
      if (value == null) continue;
      if (Array.isArray(value)) {
        if (value.length > 0) merged[key] = value;
      } else if (typeof value === 'string') {
        if (value !== '') merged[key] = value;
      } else {
        merged[key] = value;
      }
    }
  }
  return merged;
}

// ── Result builder ───────────────────────────────────────────────────────

/**
 * Builds the canonical detail result object.
 * @param {object} param
 * @returns {object}
 */
function buildResult({
  clipPageUrl,
  clipId,
  title,
  streams,
  videoSrc,
  metadata,
}) {
  const preferredStream = streams.find((u) => looksLikeStreamUrl(u)) || '';
  const preferredVideoSrc = looksLikeStreamUrl(videoSrc) ? videoSrc : '';
  const preferredPreview = looksLikeStreamUrl(metadata.preview_url) ? metadata.preview_url : '';
  const preferredPrimary = looksLikeStreamUrl(metadata.primary_url) ? metadata.primary_url : '';
  const primaryUrl = preferredStream || preferredVideoSrc || preferredPreview || preferredPrimary || clipPageUrl;

  return {
    ok: true,
    provider: PROVIDER,
    clip_id: clipId,
    title: title || metadata.title || clipPageUrl,
    description: metadata.description || '',
    creator: metadata.creator || '',
    country: metadata.country || '',
    location: metadata.location || metadata.country || '',
    tags: toStringArray(metadata.tags),
    categories: toStringArray(metadata.categories),
    page_url: clipPageUrl,
    clip_page_url: clipPageUrl,
    thumbnail_url: metadata.thumbnail_url || '',
    preview_url: preferredPreview || preferredPrimary || preferredStream || preferredVideoSrc || metadata.preview_url || primaryUrl,
    primary_url: primaryUrl,
    stream_urls: streams,
    raw_metadata: metadata.raw_metadata || {},
  };
}

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

    // 4. DOM selectors
    // NOTE: the logic below is intentionally self-contained (it runs in
    // the browser context) and mirrors the exported `extractFromDom`
    // helper used by the unit tests.  Keep the two implementations in
    // sync; a future refactor can inject the same function source into
    // both contexts.
    const domMetadata = await detailPage
      .evaluate((pageUrl) => {
        // Re-import the helper in browser context via a self-contained function.
        // We inline the minimal logic to avoid module-loading issues in Puppeteer.
        const toStringArray = (value) => {
          if (value == null || value === '') return [];
          if (typeof value === 'string') return value.split(',').map((s) => s.trim()).filter(Boolean);
          if (Array.isArray(value)) {
            return value
              .filter((v) => v != null && v !== '')
              .map((v) => (typeof v === 'string' ? v.trim() : String(v).trim()))
              .filter(Boolean);
          }
          return [];
        };

        const meta = (name) => {
          const el = document.querySelector(`meta[property="${name}"], meta[name="${name}"]`);
          return el ? el.getAttribute('content') || '' : '';
        };

        const queryText = (selectors) => {
          for (const selector of selectors) {
            const el = document.querySelector(selector);
            if (el && el.textContent) return el.textContent.trim();
          }
          return '';
        };

        const queryMany = (selectors) => {
          const out = [];
          for (const selector of selectors) {
            try {
              const nodes = document.querySelectorAll(selector);
              for (const node of nodes) {
                const text = node.textContent?.trim();
                if (text) out.push(text);
              }
            } catch {
              // Ignore invalid selectors.
            }
          }
          return out;
        };

        const title = document.title?.trim() || '';
        const description = meta('og:description') || meta('description') || queryText(['[data-testid="clip-description"]', '.clip-description']) || '';
        const creator = queryText([
          '[data-testid="creator-name"]',
          '[data-testid="artist-name"]',
          '.creator-name',
          '.artist-name',
          '[itemprop="author"]',
        ]);
        const country = queryText(['[data-testid="country"]', '.country', '[itemprop="location"]']);
        const location = queryText(['[data-testid="location"]', '.location', '[itemprop="place"]']) || country;

        const tags = toStringArray(queryMany([
          '[data-testid="clip-tag"]', '[data-testid="tag"]', '.clip-tag',
          'a[href*="/tag/"]', 'a[href*="/stock-footage/tag/"]',
        ]));
        const categories = toStringArray(queryMany([
          '[data-testid="clip-category"]', '.clip-category',
          'a[href*="/category/"]', 'a[href*="/stock-footage/category/"]',
        ]));

        const thumbnail_url = meta('og:image') || meta('twitter:image') || '';
        const preview_url = meta('og:video') || meta('twitter:player') || '';

        return {
          title,
          description,
          creator,
          country,
          location,
          tags,
          categories,
          thumbnail_url,
          preview_url,
          clip_page_url: pageUrl,
        };
      }, clipPageUrl)
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
