import fs from 'node:fs/promises';
import crypto from 'node:crypto';
import { normalizeArtlistClip, findLargestClipArray } from './normalize.js';

const endpointState = new Map();
const GRAPHQL_PAGE_SIZE = 50;

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
    const visitorId = crypto.randomUUID();
    return {
      operationName: endpoint.operationName || 'ClipList',
      query: endpoint.query || CLIP_LIST_QUERY,
      variables: {
        page,
        queryType: 1,
        filterCategories: [],
        searchTerms: [term],
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
  constructor({ endpoint, logger = console, cookieFile = process.env.ARTLIST_COOKIE_FILE || '', ratePerSecond = 2, circuitCooldownMs = 60_000 }) {
    this.endpoint = endpoint;
    this.logger = logger;
    this.cookieFile = cookieFile;
    this.ratePerSecond = Math.max(1, Number(ratePerSecond) || 2);
    this.intervalMs = 1000 / this.ratePerSecond;
    this.state = endpointState.get(endpoint.url) || { nextRequestAt: 0, circuitOpenUntil: 0 };
    endpointState.set(endpoint.url, this.state);
    this.circuitCooldownMs = circuitCooldownMs;
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
    const response = await fetch(endpoint.url, init);
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

  // Fetches the complete provider result set with bounded parallelism after
  // the first page establishes the authoritative total. Each page retains
  // its own timings so callers can distinguish network, normalization and
  // persistence costs.
  async searchAllPages({ term, filters = {}, concurrency = 4, maxPages = 100 }) {
    const startedAt = Date.now();
    const firstStartedAt = Date.now();
    const fetchPage = async (page) => {
      let lastError;
      for (let attempt = 1; attempt <= 3; attempt += 1) {
        try {
          return await this.searchFootage({ term, page, limit: 50, filters });
        } catch (error) {
          lastError = error;
          const transient = error?.status === 429 || error?.status >= 500 || error?.code === 'ARTLIST_RATE_LIMITED';
          if (!transient || attempt === 3) throw error;
          await new Promise((resolve) => setTimeout(resolve, 250 * (2 ** (attempt - 1))));
        }
      }
      throw lastError;
    };
    const first = await fetchPage(1);
    const firstRequestMs = Date.now() - firstStartedAt;
    const total = Number(first.pagination?.total || 0);
    const pageCount = Math.min(maxPages, total > 0 ? Math.ceil(total / GRAPHQL_PAGE_SIZE) : 1);
    const firstNormalizeStartedAt = Date.now();
    const firstClips = extractHttpClips(first.data);
    const firstNormalizeMs = Date.now() - firstNormalizeStartedAt;
    const pages = [{
      page: 1,
      response: first,
      clips: firstClips,
      timings: { request_ms: firstRequestMs, normalize_ms: firstNormalizeMs },
    }];
    const pending = [];
    for (let page = 2; page <= pageCount; page += 1) pending.push(page);
    const workerCount = Math.max(1, Math.min(Number(concurrency) || 4, pending.length || 1));
    let cursor = 0;
    const worker = async () => {
      while (cursor < pending.length) {
        const page = pending[cursor++];
        const pageStartedAt = Date.now();
        const response = await fetchPage(page);
        const requestMs = Date.now() - pageStartedAt;
        const normalizeStartedAt = Date.now();
        const clips = extractHttpClips(response.data);
        const normalizeMs = Date.now() - normalizeStartedAt;
        pages.push({ page, response, clips, timings: { request_ms: requestMs, normalize_ms: normalizeMs } });
      }
    };
    await Promise.all(Array.from({ length: workerCount }, worker));
    pages.sort((left, right) => left.page - right.page);
    const seen = new Set();
    const clips = [];
    for (const page of pages) {
      for (const clip of page.clips) {
        const key = clip.clip_id || clip.clip_page_url || clip.preview_url;
        if (!key || seen.has(key)) continue;
        seen.add(key);
        clips.push(clip);
      }
    }
    return {
      pages,
      clips,
      total,
      page_count: pageCount,
      raw_results: pages.reduce((sum, page) => sum + page.clips.length, 0),
      unique_clip_ids: clips.length,
      missing: Math.max(0, total - clips.length),
      duplicates: Math.max(0, pages.reduce((sum, page) => sum + page.clips.length, 0) - clips.length),
      timings: {
        total_ms: Date.now() - startedAt,
        first_request_ms: firstRequestMs,
        page_request_ms: pages.reduce((sum, page) => sum + (page.timings.request_ms || 0), 0),
        normalize_ms: pages.reduce((sum, page) => sum + (page.timings.normalize_ms || 0), 0),
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
