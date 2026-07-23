import { searchArtlist as legacySearchArtlist } from './search.js';
import { ArtlistBrowserApiClient } from './browser-api-client.js';
import { findLargestClipArray, normalizeArtlistClip } from './normalize.js';
import { buildSearchCacheKey, getSearchCache } from './search-cache.js';
import {
  getFootageSearchEndpoint,
  loadArtlistEndpointRegistry,
  resolveArtlistEndpointRegistryPath,
} from './endpoint-registry.js';

function clampLimit(value, fallback = 24) {
  const num = Number.parseInt(value, 10);
  if (!Number.isFinite(num) || num <= 0) {
    return fallback;
  }
  return Math.min(num, 50);
}

function clampPage(value, fallback = 1) {
  const num = Number.parseInt(value, 10);
  if (!Number.isFinite(num) || num <= 0) {
    return fallback;
  }
  return num;
}

function makeStableEnvelope({
  query,
  page,
  limit,
  searchUrl,
  clips,
  source,
  cacheHit,
}) {
  return {
    ok: true,
    provider: 'artlist',
    query,
    term: query,
    page,
    limit,
    search_url: searchUrl,
    cache_hit: cacheHit,
    source,
    results: clips,
    clips,
    saved: 0,
  };
}

function coerceStableClips(value) {
  if (!Array.isArray(value)) {
    return [];
  }

  const seen = new Set();
  const clips = [];

  for (const item of value) {
    const normalized = normalizeArtlistClip(item);
    const key = normalized.clip_id || normalized.clip_page_url || normalized.primary_url || normalized.preview_url;
    if (!key || seen.has(key)) {
      continue;
    }
    seen.add(key);
    clips.push(normalized);
  }

  return clips;
}

async function searchViaBrowserApi({
  browser,
  registry,
  query,
  page,
  limit,
  filters,
  logger,
}) {
  const endpoint = getFootageSearchEndpoint(registry);
  if (!endpoint) {
    return null;
  }

  const client = new ArtlistBrowserApiClient({
    browser,
    registry: { footage_search: endpoint },
    logger,
  });

  const response = await client.searchFootage({
    term: query,
    page,
    limit,
    filters,
  });

  const clipArray = findLargestClipArray(response.data);
  const clips = coerceStableClips(clipArray);
  if (!clips.length) {
    return null;
  }

  return makeStableEnvelope({
    query,
    page,
    limit,
    searchUrl: `https://artlist.io/stock-footage/search?terms=${encodeURIComponent(query)}`,
    clips,
    source: 'browser_api',
    cacheHit: false,
  });
}

async function searchViaLegacyFallback({ query, limit, profileDir, browser }) {
  const legacy = await legacySearchArtlist(query, limit, profileDir, browser);
  const clips = coerceStableClips(legacy.clips || []);
  return makeStableEnvelope({
    query,
    page: 1,
    limit,
    searchUrl: legacy.search_url || `https://artlist.io/stock-footage/search?terms=${encodeURIComponent(query)}`,
    clips,
    source: 'legacy',
    cacheHit: false,
  });
}

export async function searchArtlistGateway({
  browser,
  query,
  page = 1,
  limit = 24,
  filters = {},
  forceRefresh = false,
  profileDir = '',
  registryPath = resolveArtlistEndpointRegistryPath(),
  logger = console,
}) {
  const normalizedQuery = String(query || '').trim();
  if (!normalizedQuery) {
    throw new Error('query is required');
  }

  const normalizedPage = clampPage(page, 1);
  const normalizedLimit = clampLimit(limit, 24);
  const cache = getSearchCache();
  const cacheKey = buildSearchCacheKey({
    query: normalizedQuery,
    filters,
    page: normalizedPage,
    limit: normalizedLimit,
  });

  if (!forceRefresh) {
    const cached = cache.get(cacheKey);
    if (cached) {
      return {
        ...cached,
        cache_hit: true,
        source: cached.source || 'sqlite',
      };
    }
  }

  const registry = await loadArtlistEndpointRegistry(registryPath);

  let envelope = null;
  try {
    envelope = await searchViaBrowserApi({
      browser,
      registry,
      query: normalizedQuery,
      page: normalizedPage,
      limit: normalizedLimit,
      filters,
      logger,
    });
  } catch (err) {
    if (err && err.code === 'SESSION_EXPIRED') {
      throw err;
    }
    if (err && err.code === 'ARTLIST_ENDPOINT_INVALID') {
      throw err;
    }
    if (logger && typeof logger.warn === 'function') {
      logger.warn('artlist browser API search failed, falling back to legacy search', {
        error: err && err.message ? err.message : String(err),
      });
    }
  }

  if (!envelope) {
    envelope = await searchViaLegacyFallback({
      query: normalizedQuery,
      limit: normalizedLimit,
      profileDir,
      browser,
    });
  }

  cache.put(cacheKey, {
    query: normalizedQuery,
    filters,
    page: normalizedPage,
    limit: normalizedLimit,
  }, envelope);

  return envelope;
}
