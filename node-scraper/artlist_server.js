import http from 'node:http';
import path from 'node:path';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { openBrowser, pickChromeExecutable, evaluateBrowserPreflight } from './src/artlist/browser.js';
import { searchArtlist } from './artlist_search.js';
import { downloadClipVideo } from './src/artlist/download.js';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// ─── Config ──────────────────────────────────────────────────────────────────
const PORT = parseInt(process.env.ARTLIST_SCRAPER_PORT || '9123', 10);
// P3 (July 2026): BIND defaults to '0.0.0.0' so the container is reachable
// from sibling containers via the docker-compose `backend` network bridge.
// The historical default '127.0.0.1' made /health unreachable from
// pipelinegen-server (only the scraper's own loopback). Operators can
// still pin to loopback for unit-tests/local dev via ARTLIST_SCRAPER_BIND.
const BIND = process.env.ARTLIST_SCRAPER_BIND || '0.0.0.0';
const PROFILE_DIR = process.env.CHROME_PROFILE_DIR || '';
const DEFAULT_LIMIT = 8;
const MAX_LIMIT = 50;

// ─── State ────────────────────────────────────────────────────────────────────
let requestCount = 0;
const startedAt = new Date().toISOString();
let globalBrowser = null;
let globalBrowserConnected = false;
// FASE 9 (June 2026): last launch error exposed via /health so operators
// can triage the restart-loop / browser_running=false case from
// `docker logs` and `curl /health` without grepping Puppeteer debug
// output. Reset only on a successful openBrowser (preserved across
// cleanupBrowser so the diagnostic survives browser death).
let lastLaunchError = null;

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
  const { browser, connected, launchError } = await openBrowser(PROFILE_DIR);
  globalBrowser = browser;
  globalBrowserConnected = connected;
  // FASE 9 (June 2026): a successful launch clears the previous
  // launchError; a failed launch preserves the message for /health.
  if (browser !== null && launchError === null) {
    lastLaunchError = null;
  } else if (launchError) {
    lastLaunchError = launchError;
  }
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
      // do NOT reset lastLaunchError on cleanup — the diagnostic value
      // is precisely to surface the error after the browser is gone,
      // so cleanup should preserve it until the next successful launch.
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
  // FASE 9 (June 2026): /health now distinguishes the three
  // browser-process states operators care about:
  //   - browser_running=true + last_launch_error=null → healthy;
  //   - browser_running=false + last_launch_error=null → never launched
  //     yet, normal for cold-start;
  //   - browser_running=false + last_launch_error=<msg> → launch failed,
  //     the message names the binary path + args + root cause.
  // The `ok` flag remains true so docker-compose's HEALTHCHECK command
  // (curl -fsS http://127.0.0.1:9123/health) keeps reporting "healthy"
  // for the purpose of the container-restart-on-unhealthy decision;
  // operators read last_launch_error via docker logs / curl to triage.
  const browserRunning = globalBrowser !== null;
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({
    ok: true,
    uptime_seconds: Math.floor(process.uptime()),
    requests_served: requestCount,
    started_at: startedAt,
    port: PORT,
    browser_running: browserRunning,
    last_launch_error: lastLaunchError,
  }));
}

// ─── Preflight (PR-FIX-SCRAPER-BROWSER, July 2026) ────────────────────────────────────────────────────────────────────
//
// Godlike/07 fail-closed: the scraper must NOT silently degrade to
// "server running, every /search request returning 500 because the
// browser cannot launch" — that mode requires operators to grep
// docker logs to realize something is misconfigured. runBrowserPreflight
// runs once at startup, BEFORE server.listen, and exits with EX_CONFIG
// (78) when no browser source is reachable. The deterministic
// exit-on-miss converts the silent-degradation foot-gun into a
// crashloop the operator can fix in one config edit.
//
// Three resolution paths (logged at INFO when the preflight passes):
//   1. WS endpoint via BROWSER_WS / LIGHTPANDA_WS / CHROME_WS
//      (operator-pinned CDP socket for an external browser farm).
//   2. Local Chromium/Chrome picked up via CHROME_EXECUTABLE override
//      or the /usr/bin/{google-chrome,chromium,...} filesystem probe.
//   3. FAIL — script exits 78 with actionable fix hints (logged at
//      ERROR to stderr so docker logs surfaces them).
function runBrowserPreflight() {
  const verdict = evaluateBrowserPreflight();
  if (verdict.ok) {
    console.log(
      `[artlist-server] Preflight OK (mode=${verdict.mode}` +
      (verdict.execPath ? `, exec=${verdict.execPath}` : '') +
      ')',
    );
    return;
  }
  console.error('[artlist-server] FATAL preflight (PR-FIX-SCRAPER-BROWSER, July 2026):');
  console.error(`  ${verdict.reason}`);
  console.error('  Resolution paths (pick one):');
  console.error('    (1) apt-get install -y chromium   (or set CHROME_EXECUTABLE=/path/to/your-browser)');
  console.error('    (2) BROWSER_WS=ws://your-cdp-endpoint   (or LIGHTPANDA_WS / CHROME_WS)');
  console.error('  Exiting with code 78 (EX_CONFIG) so docker-compose surfaces the misconfiguration immediately.');
  process.exit(78);
}

runBrowserPreflight();

// ─── HTTP server ──────────────────────────────────────────────────────────────
const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`);
  if (url.pathname === '/search') {
    await handleSearch(req, res);
  } else if (url.pathname === '/download') {
    await handleDownload(req, res, getBrowser);
  } else if (url.pathname === '/health') {
    handleHealth(req, res);
  } else {
    res.writeHead(404, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: `Unknown path: ${url.pathname}` }));
  }
});

server.listen(PORT, BIND, () => {
  console.log(`[artlist-server] Listening on http://${BIND}:${PORT} (bind via ARTLIST_SCRAPER_BIND env var)`);
  console.log(`[artlist-server] Endpoints: POST /search, POST /download, GET /health`);
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
