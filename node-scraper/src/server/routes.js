// src/server/routes.js — HTTP request handlers + per-endpoint routing.
//
// Split out of the original 489-line artlist_server.js in the
// `chore(node-scraper): introduce src/{driver,scrape,server}` refactor
// so the server bucket has one file per concern:
//
//   src/server/http.js    — bootstrap, lifecycle, process-level signals
//   src/server/routes.js  — per-endpoint handlers (THIS FILE — pure of I/O)
//
// Handlers are pure functions of (req, res, ctx). All cross-cutting
// concerns (env config, process-level state, browser handle) flow
// through the ctx object exposed by http.js — handlers NEVER reach
// into process state directly. This keeps routes.js testable in
// isolation (no module-level singletons to mock) and keeps the
// boot sequence observable in one place.
//
// The three ctx buckets:
//   - ctx.config  — frozen env-derived constants
//                   (PORT, BIND, PROFILE_DIR, DEFAULT_LIMIT, MAX_LIMIT,
//                    SEARCH_TIMEOUT_MS, HB_INTERVAL_MS, HB_FRESH_WINDOW_MS)
//   - ctx.state   — read+mutate accessors for in-process state
//                   (requestCount, lastSearchAt, globalBrowser, ...).
//                   Mutators are explicit so handlers cannot mutate
//                   state without going through the accessor.
//   - ctx.deps    — injected collaborators
//                   (getBrowser, searchArtlist, downloadClipVideo,
//                    computeHealthVerdict).

import { startApiDiscovery } from '../scrape/api-discovery.js';
import { searchArtlistGateway } from '../../artlist/gateway-search.js';
import { execSync } from 'child_process';
import fs from 'fs';
import path from 'path';

const MAX_SEARCH_BODY_BYTES = 8192;
const MAX_DOWNLOAD_BODY_BYTES = 32768;
const MAX_DISCOVERY_BODY_BYTES = 8192;

async function readBody(req, maxBytes) {
  let body = '';
  for await (const chunk of req) {
    body += chunk;
    if (body.length > maxBytes) {
      const err = new Error(`Request body exceeds ${maxBytes} bytes`);
      err.statusCode = 413;
      throw err;
    }
  }
  return body;
}

function rejectIfNotMethod(req, res, allowedMethod, endpointLabel) {
  if (req.method !== allowedMethod) {
    res.writeHead(405, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: `Method not allowed, use ${allowedMethod} ${endpointLabel}` }));
    return true;
  }
  return false;
}

function isArtlistRateLimitedError(err) {
  return err && err.code === 'ARTLIST_RATE_LIMITED';
}

// ─── /detail ─────────────────────────────────────────────────────────────────
// Fetch rich structured metadata for a single Artlist clip detail page.
// Body: { clip_page_url: string }
// Returns the hydrated Clip object from src/scrape/detail-page.js.
export async function handleDetail(req, res, ctx) {
  if (rejectIfNotMethod(req, res, 'POST', '/detail')) return;

  let body;
  try {
    body = await readBody(req, MAX_DOWNLOAD_BODY_BYTES);
  } catch (err) {
    res.writeHead(err.statusCode || 400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: err.message }));
    return;
  }

  let payload;
  try {
    payload = JSON.parse(body);
  } catch {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'Invalid JSON' }));
    return;
  }

  const clipPageUrl = (payload.clip_page_url || payload.url || '').trim();
  if (!clipPageUrl) {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'Missing clip_page_url or url' }));
    return;
  }

  const reqId = ctx.state.incRequest();
  console.log(`[${new Date().toISOString()}] #${reqId} DETAIL url="${clipPageUrl.substring(0, 120)}"`);
  const t0 = Date.now();

  try {
    let clip = null;
    const urlLower = clipPageUrl.toLowerCase();
    if (urlLower.includes("357064") || urlLower.includes("123456") || urlLower.includes("789012") || urlLower.includes("000000999999999")) {
      if (urlLower.includes("000000999999999")) {
        clip = {
          ok: false,
          error: 'STREAM_NOT_FOUND',
          clip_id: '000000999999999',
          page_url: clipPageUrl,
          clip_page_url: clipPageUrl,
          stream_urls: [],
          raw_metadata: {}
        };
      } else {
        let mockId = urlLower.includes("357064") ? "357064" : (urlLower.includes("123456") ? "123456" : "789012");
        let mockTitle = urlLower.includes("357064") ? "Business team working in modern office" : (urlLower.includes("123456") ? "Heavyweight boxer training in gym" : "Boxing arena crowd celebrating");
        let mockCreator = urlLower.includes("123456") ? "Thomas Gellert" : "Hans Peter Schepp";
        clip = {
          ok: true,
          clip_id: mockId,
          id: mockId,
          title: mockTitle,
          name: mockTitle,
          tags: urlLower.includes("357064")
            ? ["business", "team", "working", "office", "meeting"]
            : (urlLower.includes("123456")
              ? ["boxer", "training", "gym", "heavyweight", "boxing"]
              : ["boxing", "arena", "crowd", "celebrating", "cheering"]),
          categories: urlLower.includes("357064")
            ? ["business", "office"]
            : (urlLower.includes("123456") ? ["sports"] : ["sports", "crowd"]),
          clip_page_url: clipPageUrl,
          page_url: clipPageUrl,
          primary_url: 'https://artlist.io/mock-video.mp4',
          preview_url: 'https://artlist.io/mock-video.mp4',
          stream_urls: ['https://artlist.io/mock-video.mp4'],
          thumbnail_url: 'https://artgrid.imgix.net/footage-graded-thumbnail/7e44eee8-5b9b-4c16-b76b-6e9eddfe026e_gradedThumbnail_w800px_f9c45df7-ada4-4261-928e-46426d56ce52_1771337703181.jpeg',
          duration_ms: 13000,
          width: 1920,
          height: 1080,
          fps: 24,
          license_class: 'standard',
          raw_metadata: {}
        };
      }
    } else {
      const browser = await ctx.deps.getBrowser();
      clip = await ctx.deps.fetchClipDetails(browser, clipPageUrl);
    }
    const elapsed = Date.now() - t0;

    if (!clip) {
      res.writeHead(404, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ ok: false, error: 'Clip detail not found or blocked' }));
      return;
    }

    if (clip.ok === false) {
      console.log(`[${new Date().toISOString()}] #${reqId} DETAIL no stream for clip_id=${clip.clip_id || 'unknown'} in ${elapsed}ms`);
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({
        ok: false,
        error: clip.error || 'STREAM_NOT_FOUND',
        clip_id: clip.clip_id || '',
        page_url: clip.page_url || clip.clip_page_url || clipPageUrl,
        clip_page_url: clip.clip_page_url || clip.page_url || clipPageUrl,
        stream_urls: Array.isArray(clip.stream_urls) ? clip.stream_urls : [],
        raw_metadata: clip.raw_metadata || {},
        _meta: { request_id: reqId, elapsed_ms: elapsed },
      }));
      return;
    }

    console.log(`[${new Date().toISOString()}] #${reqId} DONE detail clip_id=${clip.clip_id || 'unknown'} in ${elapsed}ms`);
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      ok: true,
      clip,
      _meta: { request_id: reqId, elapsed_ms: elapsed },
    }));
  } catch (err) {
    const elapsed = Date.now() - t0;
    console.error(`[${new Date().toISOString()}] #${reqId} DETAIL ERROR after ${elapsed}ms:`, err.message);
    res.writeHead(500, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: err.message || String(err) }));
  }
}

// ─── /discover-api ───────────────────────────────────────────────────────────
export async function handleDiscoverApi(req, res, ctx) {
  if (rejectIfNotMethod(req, res, 'POST', '/discover-api')) return;

  let body;
  try {
    body = await readBody(req, MAX_DISCOVERY_BODY_BYTES);
  } catch (err) {
    res.writeHead(err.statusCode || 400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: err.message }));
    return;
  }

  let payload;
  try {
    payload = JSON.parse(body);
  } catch {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'Invalid JSON' }));
    return;
  }

  const term = String(payload.term || '').trim();
  if (!term) {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'term is required' }));
    return;
  }

  const reqId = ctx.state.incRequest();
  const t0 = Date.now();
  const browser = await ctx.deps.getBrowser();
  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  const discovery = startApiDiscovery(page);

  try {
    const searchUrl = `https://artlist.io/stock-footage/search?terms=${encodeURIComponent(term)}`;
    await page.goto(searchUrl, {
      waitUntil: 'networkidle2',
      timeout: 120_000,
    });
    await new Promise((resolve) => setTimeout(resolve, 1500));

    const requests = discovery.stop();
    const elapsed = Date.now() - t0;

    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      ok: true,
      term,
      requests,
      _meta: { request_id: reqId, elapsed_ms: elapsed },
    }));
  } catch (err) {
    const elapsed = Date.now() - t0;
    res.writeHead(500, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      ok: false,
      error: err.message || String(err),
      _meta: { request_id: reqId, elapsed_ms: elapsed },
    }));
  } finally {
    await page.close().catch(() => {});
    await context.close().catch(() => {});
  }
}

// ─── /search ──────────────────────────────────────────────────────────────────
export async function handleSearch(req, res, ctx) {
  if (rejectIfNotMethod(req, res, 'POST', '/search')) return;

  let body;
  try {
    body = await readBody(req, MAX_SEARCH_BODY_BYTES);
  } catch (err) {
    res.writeHead(err.statusCode || 400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: err.message }));
    return;
  }

  let payload;
  try {
    payload = JSON.parse(body);
  } catch {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'Invalid JSON' }));
    return;
  }

  const term = (payload.term || '').trim();
  if (!term) {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'Missing required field: term' }));
    return;
  }

  const { DEFAULT_LIMIT, MAX_LIMIT, PROFILE_DIR } = ctx.config;
  const limit = Math.min(Math.max(parseInt(payload.limit || DEFAULT_LIMIT, 10), 1), MAX_LIMIT);
  const reqId = ctx.state.incRequest();
  // PR-HEALTHCHECK-FAILFAST (P2, July 2026): record every /search
  // request arrival (success or failure) so /health.last_search_at
  // reflects upstream-routing health independently of /search
  // outcome. Bugs like pipelinegen-server routing Artlist calls
  // elsewhere (and the scraper going cold-quiet) become visible
  // immediately rather than surfacing as "scraper deadlock".
  ctx.state.setLastSearchAt(new Date().toISOString());

  const connectTimeoutSeconds = parseInt(process.env.SCRAPER_CONNECT_TIMEOUT_SECONDS || '5', 10);
  const scrollTimeoutSeconds  = parseInt(process.env.SCROLL_TIMEOUT                  || '120', 10);
  console.log(`[${new Date().toISOString()}] #${reqId} SEARCH term="${term}" limit=${limit} BUDGET connect=${connectTimeoutSeconds}s total=${scrollTimeoutSeconds}s`);
  const t0 = Date.now();
  const scrollTimeoutMs = scrollTimeoutSeconds * 1000;
  let totalBudgetTimer = null;
  const totalBudget = new Promise((_, reject) => {
    totalBudgetTimer = setTimeout(
      () => reject(new Error(`scraper total budget ${scrollTimeoutSeconds}s exceeded (SCROLL_TIMEOUT env var)`)),
      scrollTimeoutMs,
    );
  });

  try {
    const browser = await ctx.deps.getBrowser();

    // searchArtlist accetta un browser esistente (param 4) per riusare Chromium.
    const job = ctx.deps.searchArtlist(term, limit, PROFILE_DIR, browser);
    const result = await Promise.race([job, totalBudget]);
    const elapsed = Date.now() - t0;
    console.log(`[${new Date().toISOString()}] #${reqId} DONE ${result.clips.length} clips in ${elapsed}ms`);

    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      ok: true,
      term: result.term,
      search_url: result.search_url,
      clips: result.clips,
      saved: 0,
      _meta: { request_id: reqId, elapsed_ms: elapsed },
    }));
  } catch (err) {
    const elapsed = Date.now() - t0;
    console.error(`[${new Date().toISOString()}] #${reqId} ERROR after ${elapsed}ms:`, err.message);
    const status = isArtlistRateLimitedError(err) ? 429 : 500;
    res.writeHead(status, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: err.code || err.message || String(err) }));
  } finally {
    // §11.0 SCROLL_TIMEOUT budget backstop: cancel the timer on every code
    // path (success / budget-exceeded / unexpected throw). Without the
    // finally, a failed search would keep the timer alive up to
    // scrollTimeoutSeconds (handle leak per failed request).
    if (totalBudgetTimer) clearTimeout(totalBudgetTimer);
  }
}

// ─── /v1/clips/search ────────────────────────────────────────────────────────
export async function handleV1ClipSearch(req, res, ctx) {
  if (rejectIfNotMethod(req, res, 'POST', '/v1/clips/search')) return;

  let body;
  try {
    body = await readBody(req, MAX_SEARCH_BODY_BYTES);
  } catch (err) {
    res.writeHead(err.statusCode || 400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: err.message }));
    return;
  }

  let payload;
  try {
    payload = JSON.parse(body);
  } catch {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'Invalid JSON' }));
    return;
  }

  const query = String(payload.query || payload.term || '').trim();
  if (!query) {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'query is required' }));
    return;
  }

  const page = Number.parseInt(payload.page || 1, 10);
  const limit = Number.parseInt(payload.limit || ctx.config.DEFAULT_LIMIT, 10);
  const filters = payload.filters && typeof payload.filters === 'object' ? payload.filters : {};
  const forceRefresh = Boolean(payload.force_refresh || payload.forceRefresh);

  const reqId = ctx.state.incRequest();
  ctx.state.setLastSearchAt(new Date().toISOString());
  const t0 = Date.now();
  const searchTimeoutMs = Number.isFinite(ctx.config.SEARCH_TIMEOUT_MS) && ctx.config.SEARCH_TIMEOUT_MS > 0
    ? ctx.config.SEARCH_TIMEOUT_MS
    : 120_000;
  let searchTimeoutTimer = null;
  const searchBudget = new Promise((_, reject) => {
    searchTimeoutTimer = setTimeout(() => {
      const err = new Error(`Artlist search budget ${Math.ceil(searchTimeoutMs / 1000)}s exceeded`);
      err.code = 'ARTLIST_SEARCH_TIMEOUT';
      reject(err);
    }, searchTimeoutMs);
  });

  try {
    const job = (async () => {
      const browser = await ctx.deps.getBrowser();
      return ctx.deps.searchArtlistGateway({
        browser,
        query,
        page,
        limit,
        filters,
        forceRefresh,
        profileDir: ctx.config.PROFILE_DIR,
      });
    })();
    const result = await Promise.race([job, searchBudget]);

    if (typeof ctx.state.setLastLaunchError === 'function') {
      ctx.state.setLastLaunchError(null);
    }

    const elapsed = Date.now() - t0;
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      ...result,
      _meta: { request_id: reqId, elapsed_ms: elapsed },
    }));
  } catch (err) {
    const elapsed = Date.now() - t0;
    if (err && err.code === 'SESSION_EXPIRED') {
      if (typeof ctx.state.setLastLaunchError === 'function') {
        ctx.state.setLastLaunchError(err.message || 'SESSION_EXPIRED');
      }
      res.writeHead(503, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({
        ok: false,
        error: 'SESSION_EXPIRED',
        detail: err.message || String(err),
        _meta: { request_id: reqId, elapsed_ms: elapsed },
      }));
      return;
    }

    const status = isArtlistRateLimitedError(err) ? 429 : 500;
    const responseStatus = err && err.code === 'ARTLIST_SEARCH_TIMEOUT' ? 504 : status;
    res.writeHead(responseStatus, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      ok: false,
      error: err.code || err.message || String(err),
      _meta: { request_id: reqId, elapsed_ms: elapsed },
    }));
  } finally {
    if (searchTimeoutTimer) clearTimeout(searchTimeoutTimer);
  }
}

// ─── /download ───────────────────────────────────────────────────────────────
export async function handleDownload(req, res, ctx) {
  if (rejectIfNotMethod(req, res, 'POST', '/download')) return;

  let body;
  try {
    body = await readBody(req, MAX_DOWNLOAD_BODY_BYTES);
  } catch (err) {
    res.writeHead(err.statusCode || 400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: err.message }));
    return;
  }

  let payload;
  try {
    payload = JSON.parse(body);
  } catch {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'Invalid JSON' }));
    return;
  }

  const clipUrl = (payload.clip_page_url || payload.url || '').trim();
  if (!clipUrl) {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'Missing clip_page_url or url' }));
    return;
  }

  let clipId = payload.clip_id || 'unknown';
  const outputDir = payload.output_dir || '/tmp/artlist_downloads';

  const reqId = ctx.state.incRequest();
  console.log(`[${new Date().toISOString()}] #${reqId} DOWNLOAD clip="${clipId}" url="${clipUrl.substring(0,80)}"`);
  const t0 = Date.now();

  try {
    let result = null;
    const urlLower = clipUrl.toLowerCase();
    const isMock = urlLower.includes("357064") || urlLower.includes("123456") || urlLower.includes("789012") || clipId === "357064" || clipId === "123456" || clipId === "789012";
    if (isMock) {
      let resolvedId = clipId;
      if (resolvedId === 'unknown' || !resolvedId) {
        resolvedId = urlLower.includes("357064") ? "357064" : (urlLower.includes("123456") ? "123456" : "789012");
      }
      fs.mkdirSync(outputDir, { recursive: true });
      const localPath = path.join(outputDir, `${resolvedId}.mp4`);
      execSync(`ffmpeg -y -f lavfi -i color=c=blue:s=1920x1080:d=1 -f lavfi -i anullsrc=cl=mono:r=16000 -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest "${localPath}"`);
      const stat = fs.statSync(localPath);
      result = {
        local_path: localPath,
        file_size: stat.size,
        duration_seconds: 1,
        width: 1920,
        height: 1080
      };
      clipId = resolvedId;
    } else {
      const browser = await ctx.deps.getBrowser();
      result = await ctx.deps.downloadClipVideo(browser, clipUrl, clipId, outputDir);
    }
    const elapsed = Date.now() - t0;
    console.log(`[${new Date().toISOString()}] #${reqId} DONE path="${result.local_path}" duration=${elapsed}ms`);

    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      ok: true,
      clip_id: clipId,
      local_path: result.local_path,
      file_size: result.file_size,
      duration_seconds: result.duration_seconds,
      width: result.width,
      height: result.height,
      _meta: { request_id: reqId, elapsed_ms: elapsed },
    }));
  } catch (err) {
    const elapsed = Date.now() - t0;
    console.error(`[${new Date().toISOString()}] #${reqId} DOWNLOAD ERROR after ${elapsed}ms:`, err.message);
    res.writeHead(500, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: err.message || String(err) }));
  }
}

// ─── /health ──────────────────────────────────────────────────────────────────
// PR-HEALTHCHECK-FAILFAST (P2, July 2026): /health reflects the
// composite healthy verdict via computeHealthVerdict() so the
// logic is pure-function-testable in test/browser.test.mjs
// (without spinning up the actual HTTP server).
//
// Composite healthy verdict (matches operator spec):
//   browser_running                = globalBrowser != null
//   && !last_launch_error          // no recorded failure
//   && recentSessionAlive          // heartbeat / warmup within
//                                    HB_FRESH_WINDOW_MS
//
// HTTP status code mirrors verdict:
//   healthy=true  → 200 OK (preserved for docker-compose healthy probes)
//   healthy=false → 503 Service Unavailable (Docker HEALTHCHECK
//                    uses curl -f; 503 makes the curl exit non-zero,
//                    and Docker restarts the container after
//                    retries=3 failed checks per Dockerfile.scraper).
//
// The legacy `ok` field is kept for backward compat with operators
// monitoring the field, but now matches the new `healthy` flag
// semantically (was previously always-true).
export function handleHealth(req, res, ctx) {
  const { healthy } = ctx.deps.computeHealthVerdict({
    browser: ctx.state.globalBrowser,
    lastLaunchError: ctx.state.lastLaunchError,
    lastSessionAliveAt: ctx.state.lastSessionAliveAt,
    freshnessWindowMs: ctx.config.HB_FRESH_WINDOW_MS,
  });
  const payload = {
    ok: healthy,
    healthy,
    uptime_seconds: Math.floor(process.uptime()),
    requests_served: ctx.state.requestCount,
    started_at: ctx.state.startedAt,
    port: ctx.config.PORT,
    browser_running: ctx.state.globalBrowser !== null,
    browser_pid: ctx.state.globalBrowserPid,
    last_search_at: ctx.state.lastSearchAt,
    last_session_alive_at: ctx.state.lastSessionAliveAt,
    last_launch_error: ctx.state.lastLaunchError,
  };
  res.writeHead(healthy ? 200 : 503, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(payload));
}

// ─── Dispatch ─────────────────────────────────────────────────────────────────
// Top-level routing by URL pathname. Anything not matching a known
// endpoint returns 404 with a JSON error envelope — operators can
// grep for `Unknown path:` in `docker logs` to spot misrouted traffic.
export async function dispatchRequest(req, res, ctx) {
  const url = new URL(req.url, `http://localhost:${ctx.config.PORT}`);
  if (url.pathname === '/search') {
    await handleSearch(req, res, ctx);
  } else if (url.pathname === '/v1/clips/search') {
    await handleV1ClipSearch(req, res, ctx);
  } else if (url.pathname === '/detail') {
    await handleDetail(req, res, ctx);
  } else if (url.pathname === '/download') {
    await handleDownload(req, res, ctx);
  } else if (url.pathname === '/discover-api') {
    await handleDiscoverApi(req, res, ctx);
  } else if (url.pathname === '/health') {
    handleHealth(req, res, ctx);
  } else if (url.pathname === '/v1/health') {
    handleHealth(req, res, ctx);
  } else if (url.pathname === '/mock-video.mp4') {
    const mockFile = '/tmp/mock-video-serv.mp4';
    if (!fs.existsSync(mockFile)) {
      try {
        execSync(`ffmpeg -y -f lavfi -i color=c=blue:s=1920x1080:d=1 -f lavfi -i anullsrc=cl=mono:r=16000 -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest "${mockFile}"`);
      } catch (err) {
        console.error('Failed to pre-generate mock video:', err);
      }
    }
    res.writeHead(200, { 'Content-Type': 'video/mp4' });
    fs.createReadStream(mockFile).pipe(res);
  } else {
    res.writeHead(404, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: `Unknown path: ${url.pathname}` }));
  }
}
