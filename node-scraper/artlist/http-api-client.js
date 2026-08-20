import fs from 'node:fs/promises';
import crypto from 'node:crypto';
import { normalizeArtlistClip, findLargestClipArray } from './normalize.js';

const endpointState = new Map();
const GRAPHQL_PAGE_SIZE = 50;
const DEFAULT_MAX_CATALOG_PAGES = 20_000;
const DEFAULT_MAX_INCREMENTAL_PAGES = 2_000;
const DEFAULT_KNOWN_STREAK = 50;
const DEFAULT_REQUEST_TIMEOUT_MS = 90_000;

const CLIP_LIST_QUERY = `
query ClipList($filterCategories: [Int!], $searchTerms: [String], $sortType: Int, $queryType: Int, $page: Int, $durationMin: Int, $durationMax: Int, $orientation: ClipOrientation, $frameRate: ClipFPS, $includeAIContent: Boolean) {
  clipList(filterCategories: $filterCategories, searchTerms: $searchTerms, sortType: $sortType, queryType: $queryType, page: $page, durationMin: $durationMin, durationMax: $durationMax, orientation: $orientation, frameRate: $frameRate, includeAIContent: $includeAIContent) {
    totalExact
    totalSimilar
    totalLexicalResults
    totalSemanticResults
    exactResults {
      id clipName clipNameForUrl thumbnailUrl clipPath filmMakerDisplayName
      storyName duration width height orientation isMadeWithAi
      availableFormats { id displayName }
      filmMakerId filmMakerNameForUrl storyId storyNameForURL
    }
    similarResults { id clipName clipNameForUrl thumbnailUrl clipPath filmMakerDisplayName storyName duration width height orientation }
  }
}`;

function buildRequestBody(endpoint, term, page, limit, filters) {
  if (endpoint.kind === 'graphql') {
    const normalizedTerm = String(term ?? '').trim();
    const searchTerms = normalizedTerm ? [normalizedTerm] : []; 
    const visitorId = crypto.randomUUID();
    return {
      operationName: endpoint.operationName || 'ClipList',
      query: endpoint.query || CLIP_LIST_QUERY,
      variables: {
        page,
        queryType: 1,
        filterCategories: [],
        searchTerms,
        sortType: filters.sortType ?? 1,
        includeAIContent: filters.includeAIContent ?? true,
        ...(filters.orientation ? { orientation: filters.orientation } : {}),
        ...(filters.durationMin != null ? { durationMin: filters.durationMin } : {}),
        ...(filters.durationMax != null ? { durationMax: filters.durationMax } : {}),
        ...(filters.frameRate ? { frameRate: filters.frameRate } : {}),
      },
      __visitorId: visitorId,
    };
  }
  return { query: term, term, page, limit, filters, ...filters };
}

async function cookieHeader(cookieFile) {
  const direct = String(process.env.ARTLIST_COOKIE_HEADER || '').trim();
  if (direct) return direct;
  if (!cookieFile) return '';
  try {
    const raw = await fs.readFile(cookieFile, 'utf8');
    return raw.split('\n')
      .filter((line) => line && !line.startsWith('#'))
      .map((line) => line.split('\t'))
      .filter((fields) => fields.length >= 7 && fields[5] && fields[6])
      .map((fields) => `${fields[5]}=${fields[6]}`)
      .join('; ');
  } catch {
    return '';
  }
}

/**
 * Direct catalog client. It deliberately runs outside Puppeteer and only
 * uses an endpoint explicitly supplied by the operator in the registry.
 */
export class ArtlistHttpApiClient {
  constructor({ endpoint, logger = console, cookieFile = process.env.ARTLIST_COOKIE_FILE || '', ratePerSecond = 2, circuitCooldownMs = 60_000, requestTimeoutMs = DEFAULT_REQUEST_TIMEOUT_MS }) {
    this.endpoint = endpoint;
    this.logger = logger;
    this.cookieFile = cookieFile;
    this.ratePerSecond = Math.max(1, Number(ratePerSecond) || 2);
    this.intervalMs = 1000 / this.ratePerSecond;
    this.state = endpointState.get(endpoint.url) || { nextRequestAt: 0, circuitOpenUntil: 0 };
    endpointState.set(endpoint.url, this.state);
    this.circuitCooldownMs = circuitCooldownMs;
    this.requestTimeoutMs = Math.max(1_000, Number(requestTimeoutMs) || DEFAULT_REQUEST_TIMEOUT_MS);
  }

  async acquireToken() {
    const now = Date.now();
    if (this.state.circuitOpenUntil > now) {
      const error = new Error('Artlist HTTP circuit is open; session refresh required');
      error.code = 'ARTLIST_CIRCUIT_OPEN';
      throw error;
    }
    const waitMs = Math.max(0, this.state.nextRequestAt - now);
    this.state.nextRequestAt = Math.max(now, this.state.nextRequestAt) + this.intervalMs;
    if (waitMs > 0) await new Promise((resolve) => setTimeout(resolve, waitMs));
  }

  openCircuit() {
    this.state.circuitOpenUntil = Date.now() + this.circuitCooldownMs;
  }

  resetCircuit() {
    this.state.circuitOpenUntil = 0;
  }

  async searchFootage({ term, page = 1, limit = 24, filters = {} }) {
    await this.acquireToken();
    const endpoint = this.endpoint;
    const method = (endpoint.method || 'POST').toUpperCase();
    const body = buildRequestBody(endpoint, term, page, limit, filters);
    const visitorId = body.__visitorId;
    if (visitorId) delete body.__visitorId;
    const headers = {
      accept: 'application/json',
      'accept-language': 'en-US,en;q=0.9,it;q=0.8',
      'user-agent': 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36',
      'sec-ch-ua': '"Chromium";v="124", "Google Chrome";v="124", "Not-A.Brand";v="99"',
      'sec-ch-ua-mobile': '?0',
      'sec-ch-ua-platform': '"Linux"',
      'sec-fetch-dest': 'empty',
      'sec-fetch-mode': 'cors',
      'sec-fetch-site': 'same-origin',
      referer: 'https://artlist.io/',
      origin: 'https://artlist.io',
      ...(visitorId ? {
        'x-anonymous-id': visitorId,
        'x-visitor-id': visitorId,
        'x-user-status': 'guest',
      } : {}),
      ...(endpoint.headers || {}),
    };
    const cookies = await cookieHeader(this.cookieFile);
    if (cookies && !headers.cookie && !headers.Cookie) headers.cookie = cookies;

    const init = { method, headers, redirect: 'follow' };
    if (method !== 'GET') {
      headers['content-type'] ||= 'application/json';
      init.body = JSON.stringify(body);
    }
    let response;
    try {
      response = await fetch(endpoint.url, {
        ...init,
        signal: AbortSignal.timeout(this.requestTimeoutMs),
      });
    } catch (error) {
      if (error?.name === 'TimeoutError' || error?.name === 'AbortError') {
        const timeoutError = new Error(`Artlist HTTP API request timed out after ${this.requestTimeoutMs}ms`);
        timeoutError.code = 'ARTLIST_API_TIMEOUT';
        throw timeoutError;
      }
      throw error;
    }
    const text = await response.text();
    let data;
    try { data = JSON.parse(text); } catch { data = null; }
    if (response.status === 401 || response.status === 403) {
      this.openCircuit();
      const error = new Error(`Artlist HTTP session unavailable: HTTP ${response.status}`);
      error.code = 'SESSION_EXPIRED';
      throw error;
    }
    if (response.status === 429) {
      this.openCircuit();
      const error = new Error('Artlist HTTP API request was rate limited');
      error.code = 'ARTLIST_RATE_LIMITED';
      throw error;
    }
    if (!response.ok) {
      const error = new Error(`Artlist HTTP API request failed: HTTP ${response.status}`);
      error.code = 'ARTLIST_API_ERROR';
      error.status = response.status;
      throw error;
    }
    this.resetCircuit();
    if (Array.isArray(data?.errors) && data.errors.length > 0) {
      const error = new Error(`Artlist GraphQL returned ${data.errors.length} error(s)`);
      error.code = 'ARTLIST_GRAPHQL_ERROR';
      error.graphqlErrors = data.errors.slice(0, 5);
      throw error;
    }
    const clipList = data?.data?.clipList;
    const total = Number(clipList?.totalExact);
    return {
      status: response.status,
      data,
      rawText: data ? null : text.slice(0, 2000),
      pagination: Number.isFinite(total) ? {
        total,
        has_next_page: Array.isArray(clipList?.exactResults) && clipList.exactResults.length > 0 && page * GRAPHQL_PAGE_SIZE < total,
        ...(Number.isFinite(total) && page * GRAPHQL_PAGE_SIZE < total ? { next_page: page + 1 } : {}),
      } : {},
    };
  }

  // Fetches newest pages in provider order until a safety streak of clips
  // already present in the local catalog is reached. This must remain
  // sequential: fetching pages concurrently could cross the stop boundary.
  async searchNewestUntilKnown({
    term = '',
    filters = {},
    isKnown = null,
    knownClipIds = [],
    knownStreak = DEFAULT_KNOWN_STREAK,
    maxPages = DEFAULT_MAX_INCREMENTAL_PAGES,
    onPage = null,
    collectPages = false,
  } = {}) {
    const startedAt = Date.now();
    const normalizedTerm = String(term ?? '').trim();
    const threshold = Math.max(1, Number(knownStreak) || DEFAULT_KNOWN_STREAK);
    const requestedMaxPages = Math.max(1, Number(maxPages) || DEFAULT_MAX_INCREMENTAL_PAGES);
    const knownSeed = new Set((Array.isArray(knownClipIds) ? knownClipIds : [knownClipIds])
      .map((id) => String(id || '').trim()).filter(Boolean));
    const currentSeen = new Set();
    const knownSeen = new Set();
    const newSeen = new Set();
    const pages = [];
    let rawResults = 0;
    let duplicates = 0;
    let pageRequestMs = 0;
    let normalizeMs = 0;
    let knownStreakCount = 0;
    let fetchedPages = 0;
    let stopReason = 'max_pages';
    let providerTotal = null;

    const fetchPage = async (page) => {
      let lastError;
      for (let attempt = 1; attempt <= 3; attempt += 1) {
        try {
          return await this.searchFootage({ term: normalizedTerm, page, limit: GRAPHQL_PAGE_SIZE, filters });
        } catch (error) {
          lastError = error;
          error.page = page;
          const transient = error?.status === 429
            || error?.status >= 500
            || error?.code === 'ARTLIST_RATE_LIMITED'
            || error?.code === 'ARTLIST_API_TIMEOUT'
            || error?.code === 'ECONNRESET';
          if (!transient || attempt === 3) throw error;
          await new Promise((resolve) => setTimeout(resolve, 250 * (2 ** (attempt - 1))));
        }
      }
      throw lastError;
    };

    for (let pageNumber = 1; pageNumber <= requestedMaxPages; pageNumber += 1) {
      const requestStartedAt = Date.now();
      const response = await fetchPage(pageNumber);
      const requestMs = Date.now() - requestStartedAt;
      const normalizeStartedAt = Date.now();
      const clips = extractHttpClips(response.data);
      fetchedPages += 1;
      const pageNormalizeMs = Date.now() - normalizeStartedAt;
      pageRequestMs += requestMs;
      normalizeMs += pageNormalizeMs;
      providerTotal ??= Number.isFinite(Number(response.pagination?.total))
        ? Number(response.pagination.total)
        : null;

      if (clips.length === 0) {
        stopReason = 'provider_empty';
        break;
      }

      let pageKnownIds;
      try {
        pageKnownIds = typeof isKnown === 'function'
          ? await isKnown(clips, pageNumber)
          : knownSeed;
      } catch (error) {
        error.page = pageNumber;
        throw error;
      }
      const pageKnown = new Set((Array.isArray(pageKnownIds) ? pageKnownIds : [...(pageKnownIds || [])])
        .map((id) => String(id || '').trim()).filter(Boolean));
      rawResults += clips.length;

      for (const clip of clips) {
        const clipId = String(clip.clip_id || '').trim();
        if (!clipId) continue;
        const isProviderKnown = pageKnown.has(clipId) || knownSeed.has(clipId);
        if (isProviderKnown) {
          knownSeen.add(clipId);
          knownStreakCount += 1;
        } else {
          newSeen.add(clipId);
          knownStreakCount = 0;
        }
        if (currentSeen.has(clipId)) duplicates += 1;
        currentSeen.add(clipId);
      }

      const normalizedPage = {
        page: pageNumber,
        response,
        clips,
        known_clip_ids: [...pageKnown],
        timings: { request_ms: requestMs, normalize_ms: pageNormalizeMs },
      };
      try {
        if (typeof onPage === 'function') await onPage(normalizedPage);
      } catch (error) {
        error.page = pageNumber;
        throw error;
      }
      if (collectPages) pages.push(normalizedPage);

      if (knownStreakCount >= threshold) {
        stopReason = 'known_streak';
        break;
      }
    }

    return {
      pages,
      clips: collectPages ? pages.flatMap((page) => page.clips) : [],
      query: normalizedTerm,
      total: providerTotal,
      page_count: fetchedPages,
      pages_fetched: fetchedPages,
      raw_results: rawResults,
      unique_clip_ids: currentSeen.size,
      new_clip_ids: newSeen.size,
      known_clip_ids: knownSeen.size,
      duplicates,
      known_streak: knownStreakCount,
      stopped_on_known: stopReason === 'known_streak',
      stop_reason: stopReason,
      timings: {
        total_ms: Date.now() - startedAt,
        page_request_ms: pageRequestMs,
        normalize_ms: normalizeMs,
      },
    };
  }

  // Fetches the complete provider result set with bounded parallelism after
  // the first page establishes the authoritative total. Each page retains
  // its own timings so callers can distinguish network, normalization and
  // persistence costs.
  async searchAllPages({
    term = '',
    filters = {},
    concurrency = 4,
    maxPages = DEFAULT_MAX_CATALOG_PAGES,
    resumeFromPage = 1,
    onPage = null,
    collectPages = !onPage,
  }) {
    const startedAt = Date.now();
    const normalizedTerm = String(term ?? '').trim();
    const pageSize = GRAPHQL_PAGE_SIZE;
    const requestedMaxPages = Number.isFinite(Number(maxPages)) ? Number(maxPages) : DEFAULT_MAX_CATALOG_PAGES;
    const requestedResumePage = Math.max(1, Number(resumeFromPage) || 1);
    const fetchPage = async (page) => {
      let lastError;
      for (let attempt = 1; attempt <= 3; attempt += 1) {
        try {
          return await this.searchFootage({ term: normalizedTerm, page, limit: pageSize, filters });
        } catch (error) {
          lastError = error;
          error.page = page;
          const transient = error?.status === 429
            || error?.status >= 500
            || error?.code === 'ARTLIST_RATE_LIMITED'
            || error?.code === 'ARTLIST_API_TIMEOUT'
            || error?.code === 'ECONNRESET';
          if (!transient || attempt === 3) throw error;
          await new Promise((resolve) => setTimeout(resolve, 250 * (2 ** (attempt - 1))));
        }
      }
      throw lastError;
    };

    const firstStartedAt = Date.now();
    const first = await fetchPage(1);
    const firstRequestMs = Date.now() - firstStartedAt;
    const total = Number(first.pagination?.total);
    if (!Number.isFinite(total) || total < 0) {
      const error = new Error('Artlist provider did not return a valid totalExact for catalog sync');
      error.code = 'ARTLIST_INVALID_PROVIDER_TOTAL';
      throw error;
    }

    const pageCount = Math.max(1, total > 0 ? Math.ceil(total / pageSize) : 1);
    if (pageCount > requestedMaxPages) {
      const error = new Error(`Artlist catalog requires ${pageCount} pages, exceeding maxPages=${requestedMaxPages}`);
      error.code = 'ARTLIST_MAX_PAGES_EXCEEDED';
      error.providerTotal = total;
      error.pageCount = pageCount;
      throw error;
    }

    const normalizePage = (page, requestMs) => {
      const normalizeStartedAt = Date.now();
      const clips = extractHttpClips(page.response.data);
      const normalizeMs = Date.now() - normalizeStartedAt;
      if (page.page < pageCount && clips.length === 0) {
        const error = new Error(`Artlist returned an empty page ${page.page} before the expected final page ${pageCount}`);
        error.code = 'ARTLIST_EMPTY_PAGE';
        error.page = page.page;
        throw error;
      }
      return {
        page: page.page,
        response: page.response,
        clips,
        timings: { request_ms: requestMs, normalize_ms: normalizeMs },
      };
    };

    const pages = collectPages ? [] : [];
    const collectedClips = [];
    let uniqueClipCount = 0;
    const seen = new Set();
    let rawResults = 0;
    let duplicates = 0;
    let pageRequestMs = 0;
    let normalizeMs = 0;
    const processPage = async (page) => {
      rawResults += page.clips.length;
      pageRequestMs += page.timings.request_ms || 0;
      normalizeMs += page.timings.normalize_ms || 0;
      for (const clip of page.clips) {
        const key = String(clip.clip_id || '').trim();
        if (!key) continue;
        if (seen.has(key)) {
          duplicates += 1;
          continue;
        }
        seen.add(key);
        uniqueClipCount += 1;
        if (collectPages) collectedClips.push(clip);
      }
      if (collectPages) pages.push(page);
      if (typeof onPage === 'function') await onPage(page);
    };

    const firstPage = normalizePage({ page: 1, response: first }, firstRequestMs);
    if (requestedResumePage <= 1) {
      try {
        await processPage(firstPage);
      } catch (error) {
        error.page = 1;
        throw error;
      }
    }
    const pending = [];
    for (let page = Math.max(2, requestedResumePage); page <= pageCount; page += 1) pending.push(page);
    const workerCount = Math.max(1, Math.min(Number(concurrency) || 4, pending.length || 1));
    let cursor = 0;
    const worker = async () => {
      while (cursor < pending.length) {
        const page = pending[cursor++];
        const pageStartedAt = Date.now();
        const response = await fetchPage(page);
        const normalizedPage = normalizePage({ page, response }, Date.now() - pageStartedAt);
        try {
          await processPage(normalizedPage);
        } catch (error) {
          error.page = page;
          throw error;
        }
      }
    };
    await Promise.all(Array.from({ length: workerCount }, worker));

    if (collectPages) pages.sort((left, right) => left.page - right.page);
    return {
      pages,
      clips: collectedClips,
      query: normalizedTerm,
      total,
      page_count: pageCount,
      resume_from_page: requestedResumePage,
      raw_results: rawResults,
      unique_clip_ids: uniqueClipCount,
      missing: Math.max(0, total - uniqueClipCount),
      duplicates,
      complete: uniqueClipCount === total,
      timings: {
        total_ms: Date.now() - startedAt,
        first_request_ms: firstRequestMs,
        page_request_ms: pageRequestMs,
        normalize_ms: normalizeMs,
        max_concurrency: workerCount,
      },
    };
  }
}

export function extractHttpClips(data) {
  const exactResults = data?.data?.clipList?.exactResults;
  const values = Array.isArray(exactResults) ? exactResults : findLargestClipArray(data);
  const seen = new Set();
  return (Array.isArray(values) ? values : []).map((item) => {
    if (data?.data?.clipList?.exactResults) {
      const id = item.id == null ? '' : String(item.id);
      const slug = String(item.clipNameForUrl || item.clipName || id)
        .toLowerCase().trim().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
      return normalizeArtlistClip({
        ...item,
        title: item.clipName,
        name: item.clipName,
        creator: item.filmMakerDisplayName,
        previewUrl: item.clipPath,
        duration_seconds: Number(item.duration || 0) / 1000,
        page_url: id ? `https://artlist.io/stock-footage/clip/${slug}/${id}` : '',
        clip_page_url: id ? `https://artlist.io/stock-footage/clip/${slug}/${id}` : '',
      });
    }
    return normalizeArtlistClip(item);
  }).filter((clip) => {
    const key = clip.clip_id || clip.clip_page_url || clip.primary_url || clip.preview_url;
    if (!key || seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}
