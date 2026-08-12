// Pure metadata extraction and merge helpers for Artlist detail pages.

import { looksLikeStreamUrl } from './detail-streams.js';

const PROVIDER = 'artlist';
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
export function findClipObject(obj) {
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
export function extractFromApiObject(raw, clipPageUrl) {
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
export function extractFromDom(documentOrPageUrl, maybeClipPageUrl) {
  const runningInPage = typeof documentOrPageUrl === 'string';
  const document = runningInPage ? globalThis.document : documentOrPageUrl;
  const clipPageUrl = runningInPage ? documentOrPageUrl : maybeClipPageUrl;
  if (!document) return {};

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
export function buildResult({
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
    preview_url: preferredPreview || preferredPrimary || preferredStream || preferredVideoSrc || primaryUrl,
    primary_url: primaryUrl,
    stream_urls: streams,
    raw_metadata: metadata.raw_metadata || {},
  };
}


