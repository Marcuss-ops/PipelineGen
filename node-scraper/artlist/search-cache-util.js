import crypto from 'node:crypto';
import path from 'node:path';

const DEFAULT_TTL_MS = 12 * 60 * 60 * 1000;
const DEFAULT_INCREMENTAL_INTERVAL_MS = 24 * 60 * 60 * 1000;
const DEFAULT_RECONCILIATION_INTERVAL_MS = 7 * 24 * 60 * 60 * 1000;
// Query-to-clip knowledge is durable. The legacy expires_at column remains
// for schema compatibility but no longer controls relationship visibility.
const PERMANENT_RELATION_EXPIRES_AT = '9999-12-31T23:59:59.999Z';
const DEFAULT_DB_PATH = process.env.ARTLIST_SEARCH_CACHE_DB || path.join(process.cwd(), 'data', 'artlist-search-cache.sqlite');

function sortObject(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return value;
  }

  return Object.fromEntries(
    Object.entries(value)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, nested]) => [key, sortObject(nested)]),
  );
}

const TRANSIENT_METADATA_KEY = /(?:download|stream|primary|preview|video|hls|manifest|playlist|signed|token)/i;
const TRANSIENT_MEDIA_URL = /(?:\.(?:m3u8|mp4|mov|webm|mkv)(?:[?#]|$)|\/(?:hls|manifest|playlist|stream|download)(?:[/?#]|$)|(?:[?&](?:token|signature|expires|x-amz-signature)=))/i;

function isTransientArtlistUrl(value) {
  const url = String(value || '').trim();
  return /^https?:\/\//i.test(url) && TRANSIENT_MEDIA_URL.test(url);
}

function stableCatalogUrl(value) {
  const url = String(value || '').trim();
  return url && !isTransientArtlistUrl(url) ? url : '';
}

function sanitizeDurableMetadata(value, key = '') {
  if (value == null) return value;
  if (typeof value === 'string') return isTransientArtlistUrl(value) ? undefined : value;
  if (Array.isArray(value)) return value.map((item) => sanitizeDurableMetadata(item, key)).filter((item) => item !== undefined);
  if (typeof value !== 'object') return value;
  if (key && TRANSIENT_METADATA_KEY.test(key)) return undefined;

  const result = {};
  for (const [childKey, childValue] of Object.entries(value)) {
    const sanitized = sanitizeDurableMetadata(childValue, childKey);
    if (sanitized !== undefined) result[childKey] = sanitized;
  }
  return result;
}

function buildSearchCacheKey({ query, filters = {}, page = 1, limit = 24 }) {
  const canonical = JSON.stringify({
    query: String(query || '').trim().toLowerCase(),
    filters: sortObject(filters || {}),
    page: Number.isFinite(Number(page)) ? Number(page) : 1,
    limit: Number.isFinite(Number(limit)) ? Number(limit) : 24,
  });
  return crypto.createHash('sha256').update(canonical).digest('hex');
}

function buildSearchQueryKey({ query, filters = {} }) {
  return crypto.createHash('sha256').update(JSON.stringify({
    query: String(query || '').trim().toLowerCase(),
    filters: sortObject(filters || {}),
  })).digest('hex');
}

export {
  DEFAULT_DB_PATH,
  DEFAULT_INCREMENTAL_INTERVAL_MS,
  DEFAULT_RECONCILIATION_INTERVAL_MS,
  DEFAULT_TTL_MS,
  PERMANENT_RELATION_EXPIRES_AT,
  buildSearchCacheKey,
  buildSearchQueryKey,
  isTransientArtlistUrl,
  sanitizeDurableMetadata,
  sortObject,
  stableCatalogUrl,
};
