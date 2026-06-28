import http from 'node:http';
import path from 'node:path';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { openBrowser } from './src/artlist/browser.js';
import { searchArtlist } from './artlist_search.js';
import { downloadClipVideo } from './src/artlist/download.js';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// ─── Config ──────────────────────────────────────────────────────────────────
const PORT = parseInt(process.env.ARTLIST_SCRAPER_PORT || '9123', 10);
// HOST controls the bind address. Defaults to '0.0.0.0' so the
// scraper is reachable from sibling Docker containers
// (e.g. http://artlist-scraper:9123 from the pipelinegen-server).
// Set HOST=127.0.0.1 in operator-only setups where the scraper is
// intentionally looped back to its own container.
// Phase 1 audit fix (PR ref fix/artlist-container-runtime): the
// legacy hardcoded '127.0.0.1' made the container unreachable
// from any peer on the `backend` Docker network.
const HOST = process.env.HOST || '0.0.0.0';
const PROFILE_DIR = process.env.CHROME_PROFILE_DIR || '';
const DEFAULT_LIMIT = 8;
const MAX_LIMIT = 50;

// ─── State ────────────────────────────────────────────────────────────────────
let requestCount = 0;
const startedAt = new Date().toISOString();
let globalBrowser = null;
let globalBrowserConnected = false;

// ─── Browser Lifecycle ────────────────────────────────────────────────────────
async function getBrowser() {
  if (globalBrowser) {
    try {
      // Check if browser is still responsive
      await globalBrowser.version();
      return globalBrowser;
    } catch {
      console.warn('[artlist-server] Browser disconnected or dead, restarting...');
      await cleanupBrowser();
    }
  }

  console.log('[artlist-server] Launching persistent Chromium browser...');
  const { browser, connected } = await openBrowser(PROFILE_DIR);
  globalBrowser = browser;
  globalBrowserConnected = connected;
  return globalBrowser;
}

async function cleanupBrowser() {
  if (globalBrowser) {
    try {
      if (globalBrowserConnected && globalBrowser.disconnect) {
        await globalBrowser.disconnect();
      } else if (globalBrowser.close) {
        await globalBrowser.close();
      }
    } catch (err) {
      console.error('[artlist-server] Error closing browser:', err.message);
    } finally {
      globalBrowser = null;
      globalBrowserConnected = false;
    }
  }
}



// ─── Request handler ──────────────────────────────────────────────────────────
async function handleSearch(req, res) {
  if (req.method !== 'POST') {
    res.writeHead(405, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'Method not allowed, use POST /search' }));
    return;
  }

  let body = '';
  for await (const chunk of req) {
    body += chunk;
    if (body.length > 8192) {
      res.writeHead(413, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ ok: false, error: 'Request too large' }));
      return;
    }
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

  const limit = Math.min(Math.max(parseInt(payload.limit || DEFAULT_LIMIT, 10), 1), MAX_LIMIT);
  requestCount++;
  const reqId = requestCount;

  console.log(`[${new Date().toISOString()}] #${reqId} SEARCH term="${term}" limit=${limit}`);
  const t0 = Date.now();

  try {
    const browser = await getBrowser();
    
    // searchArtlist accetta un browser esistente (param 4) per riusare Chromium.
    const result = await searchArtlist(term, limit, PROFILE_DIR, browser);
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
    res.writeHead(500, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: err.message || String(err) }));
  }
}

async function handleDownload(req, res, getBrowserFn) {
  if (req.method !== 'POST') {
    res.writeHead(405, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'Method not allowed, use POST /download' }));
    return;
  }

  let body = '';
  for await (const chunk of req) {
    body += chunk;
    if (body.length > 32768) {
      res.writeHead(413, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ ok: false, error: 'Request too large' }));
      return;
    }
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

  const clipId = payload.clip_id || 'unknown';
  const outputDir = payload.output_dir || '/tmp/artlist_downloads';

  requestCount++;
  const reqId = requestCount;
  console.log(`[${new Date().toISOString()}] #${reqId} DOWNLOAD clip="${clipId}" url="${clipUrl.substring(0,80)}"`);
  const t0 = Date.now();

  try {
    const browser = await getBrowserFn();
    const result = await downloadClipVideo(browser, clipUrl, clipId, outputDir);
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

function handleHealth(req, res) {
  // Liveness probe only — must NOT touch Chromium (which may take
  // seconds to launch). K8s / Docker restart loops hit /health on
  // every tick; we want a fast, no-side-effect 200 so the container
  // is kept up while Chromium warms in the background.
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({
    ok: true,
    uptime_seconds: Math.floor(process.uptime()),
    requests_served: requestCount,
    started_at: startedAt,
    port: PORT,
    browser_running: globalBrowser !== null,
  }));
}

// Readiness probe — Phase 1 audit fix (PR ref fix/artlist-container-runtime).
// Actively asks Chromium to confirm it is alive (browser.version()
// round-trips over the DevTools protocol). Returns 200 ONLY once
// the browser is up; 503 otherwise. This replaces the legacy
// "/health returns 200 even when Chromium is dead" footgun which
// caused the server to mark artlist-scraper as healthy in
// docker-compose healthcheck checks before the first search worked.
async function handleReady(req, res) {
  let browserConnected = false;
  let lastError = null;
  try {
    const browser = await getBrowser();
    // browser.version() performs a live CDP roundtrip; if Chromium
    // is dead, this throws and we surface 503.
    await browser.version();
    browserConnected = browser.isConnected();
  } catch (err) {
    lastError = err && err.message ? err.message : String(err);
  }
  const ready = browserConnected && lastError === null;
  res.writeHead(ready ? 200 : 503, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({
    ok: ready,
    ready,
    browser_connected: browserConnected,
    error: lastError,
    port: PORT,
  }));
}

// ─── HTTP server ──────────────────────────────────────────────────────────────
const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`);
  if (url.pathname === '/search') {
    await handleSearch(req, res);
  } else if (url.pathname === '/download') {
    await handleDownload(req, res, getBrowser);
  } else if (url.pathname === '/health') {
    handleHealth(req, res);
  } else if (url.pathname === '/ready') {
    await handleReady(req, res);
  } else {
    res.writeHead(404, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: `Unknown path: ${url.pathname}` }));
  }
});

server.listen(PORT, HOST, () => {
  console.log(`[artlist-server] Listening on http://${HOST}:${PORT}`);
  console.log(`[artlist-server] Endpoints: POST /search, POST /download, GET /health, GET /ready`);
  console.log(`[artlist-server] Browser will warm up on first request`);
});

server.on('error', (err) => {
  console.error('[artlist-server] Server error:', err.message);
  process.exit(1);
});

// Graceful shutdown
process.on('SIGTERM', async () => {
  console.log('[artlist-server] SIGTERM received, closing browser & shutting down...');
  await cleanupBrowser();
  server.close(() => process.exit(0));
});
process.on('SIGINT', async () => {
  await cleanupBrowser();
  server.close(() => process.exit(0));
});
