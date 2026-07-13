import http from 'node:http';
import path from 'node:path';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { openBrowser, pickChromeExecutable, evaluateBrowserPreflight } from './src/artlist/browser.js';
import { searchArtlist } from './artlist_search.js';
import { downloadClipVideo } from './src/artlist/download.js';
import { computeHealthVerdict } from './src/artlist/health.js';

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
// PR-HEALTHCHECK-FAILFAST (P2, July 2026): timestamp of the last
// successful `await globalBrowser.version()` call (boot warmup or
// heartbeat). Used by computeHealthVerdict to test recentSessionAlive
// against a 60s freshness window — without it /health would silently
// report healthy=true on a crashed browser that hasn't received a
// request in 10 minutes (the existing getBrowser() check fires only
// on /search arrival).
let lastSessionAliveAt = null;
// PR-HEALTHCHECK-FAILFAST (P2, July 2026): ISO timestamp of the last
// /search request observed (success or failure). Surfaced via
// /health.last_search_at so operators can correlate scraper
// quietness with upstream issues (e.g. pipelinegen-server routing
// the request elsewhere).
let lastSearchAt = null;
// PR-HEALTHCHECK-FAILFAST (P2, July 2026): pid of the Chromium
// process spawned by puppeteer.launch (or null when not running).
// Surfaced via /health.browser_pid so operators can run `ps`,
// `kill -9`, or signal the spawned process directly.
let globalBrowserPid = null;

// PR-HEALTHCHECK-FAILFAST (P2, July 2026): /health freshness contract.
//   HB_INTERVAL_MS       = 30s heartbeat setInterval cadence.
//   HB_FRESH_WINDOW_MS   = 60s — anything older than this in
//                         lastSessionAliveAt drops recentSessionAlive
//                         to false, so /health flips to 503.
// Heartbeat interval < fresh-window guarantees at least ONE fresh
// check during any window in steady state.
const HB_INTERVAL_MS = 30_000;
const HB_FRESH_WINDOW_MS = 60_000;

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
    // PR-HEALTHCHECK-FAILFAST (P2, July 2026): a successful openBrowser
    // is itself proof the browser is alive — bump the freshness
    // timestamp so the heartbeart's first interval doesn't have to
    // run before /health flips back to healthy.
    lastSessionAliveAt = new Date().toISOString();
    // Capture the Chromium pid once at launch so /health exposes it
    // without re-querying the live handle each probe.
    try {
      const proc = browser.process && browser.process();
      if (proc && typeof proc.pid === 'number') {
        globalBrowserPid = proc.pid;
      }
    } catch (_pidErr) {
      // Non-fatal — globalBrowserPid stays null; surface diagnostic
      // via last_launch_error only if it's a real launch issue.
    }
  } else if (launchError) {
    lastLaunchError = launchError;
    globalBrowserPid = null;
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
      // PR-HEALTHCHECK-FAILFAST (P2, July 2026): reset browser_pid
      // and session-alive alongside the handle pointer. We DO NOT
      // reset lastLaunchError here — the diagnostic value is precisely
      // to surface the error after the browser is gone, so cleanup
      // should preserve it until the next successful launch.
      globalBrowserPid = null;
      lastSessionAliveAt = null;
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
  // PR-HEALTHCHECK-FAILFAST (P2, July 2026): record every /search
  // request arrival (success or failure) so /health.last_search_at
  // reflects upstream-routing health independently of /search
  // outcome. Bugs like pipelinegen-server routing Artlist calls
  // elsewhere (and the scraper going cold-quiet) become visible
  // immediately rather than surfacing as "scraper deadlock".
  lastSearchAt = new Date().toISOString();
  const reqId = requestCount;

  console.log(`[${new Date().toISOString()}] #${reqId} SEARCH term="${term}" limit=${limit} BUDGET connect=${connectTimeoutSeconds}s total=${scrollTimeoutSeconds}s`);
  const t0 = Date.now();
  // Split connect/total timeout per fix(scraper) (PR-July 2026). Per
  // godlike/07 minimum-blast-radius + the §11.0 doc-public contract:
  //   - SCRAPER_CONNECT_TIMEOUT_SECONDS = connect budget (Chromium launch + first page nav)
  //   - SCROLL_TIMEOUT                  = total budget enforced as Promise.race backstop
  // The connect split is realized at the bash-wrapper layer (curl --connect-timeout
  // vs. --max-time); here we enforce the total budget so a runaway Chromium
  // navigation cannot pin the scraper past the SCROLL_TIMEOUT envelope.
  const connectTimeoutSeconds = parseInt(process.env.SCRAPER_CONNECT_TIMEOUT_SECONDS || '5', 10);
  const scrollTimeoutSeconds  = parseInt(process.env.SCROLL_TIMEOUT                  || '120', 10);
  const scrollTimeoutMs = scrollTimeoutSeconds * 1000;
  let totalBudgetTimer = null;
  const totalBudget = new Promise((_, reject) => {
    totalBudgetTimer = setTimeout(
      () => reject(new Error(`scraper total budget ${scrollTimeoutSeconds}s exceeded (SCROLL_TIMEOUT env var)`)),
      scrollTimeoutMs,
    );
  });

  try {
    const browser = await getBrowser();
    
    // searchArtlist accetta un browser esistente (param 4) per riusare Chromium.
    const job = searchArtlist(term, limit, PROFILE_DIR, browser);
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
    res.writeHead(500, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: err.message || String(err) }));
  } finally {
    // §11.0 SCROLL_TIMEOUT budget backstop: cancel the timer on every code
    // path (success / budget-exceeded / unexpected throw). Without the
    // finally, a failed search would keep the timer alive up to
    // scrollTimeoutSeconds (handle leak per failed request).
    if (totalBudgetTimer) clearTimeout(totalBudgetTimer);
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
  const { healthy } = computeHealthVerdict({
    browser: globalBrowser,
    lastLaunchError,
    lastSessionAliveAt,
    freshnessWindowMs: HB_FRESH_WINDOW_MS,
  });
  const payload = {
    ok: healthy,
    healthy,
    uptime_seconds: Math.floor(process.uptime()),
    requests_served: requestCount,
    started_at: startedAt,
    port: PORT,
    browser_running: globalBrowser !== null,
    browser_pid: globalBrowserPid,
    last_search_at: lastSearchAt,
    last_session_alive_at: lastSessionAliveAt,
    last_launch_error: lastLaunchError,
  };
  res.writeHead(healthy ? 200 : 503, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(payload));
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
// PR-HEALTHCHECK-FAILFAST (P2, July 2026): boot warmup.
//
// Without an explicit warmup, the scraper would boot to "server
// listening, but globalBrowser=null". /health would flip to 503
// (browserRunning=false) on the first healthcheck after
// start_period=10s, eventually tripping Docker's
// crashloop-after-retries=3 restart policy at ~t=55s -- even when
// chromium binary + path are perfectly fine and the scraper would
// recover on its first lazy /search request.
//
// The warmup makes globalBrowser non-null before server.listen so
// /health reflects healthy=true from the first poll. If the warmup
// itself fails, lastLaunchError is set and /health still returns 503
// (intended -- the operator learns about the failure via Docker
// restart + docker logs rather than via silent /search 500s).
async function runBootWarmup() {
  console.log('[artlist-server] Boot warmup: launching Chromium before serving...');
  try {
    const browser = await getBrowser();
    if (browser) {
      console.log(`[artlist-server] Boot warmup OK (browser_pid=${globalBrowserPid}, last_session_alive_at=${lastSessionAliveAt})`);
    } else {
      console.error('[artlist-server] Boot warmup returned no browser; /health will report 503 + restart');
    }
  } catch (err) {
    const msg = err && err.message ? err.message : String(err);
    console.error('[artlist-server] Boot warmup threw:', msg);
    lastLaunchError = `boot warmup failed: ${msg}`;
  }
}

// PR-HEALTHCHECK-FAILFAST (P2, July 2026): heartbeat timer.
//
// setInterval that calls globalBrowser.version() every HB_INTERVAL_MS
// to refresh lastSessionAliveAt. Without it, /health would only
// refresh the freshness timestamp on the next /search or /download
// request -- a quiet scraper with a dead browser could report
// healthy=true for up to (request-cadence) minutes.
//
// version() also surfaces browser.died mid-runtime (segfault, OOM):
// on failure we set lastLaunchError and call cleanupBrowser. /health
// then flips to 503 once the freshness window elapses, which Docker
// interprets as a restart trigger.
//
// unref() so a hanging heartbeat doesn't block process exit during
// SIGTERM/SIGINT cleanup.
let hbTimer = null;
function startHeartbeat() {
  if (hbTimer) return;
  hbTimer = setInterval(async () => {
    if (!globalBrowser) return;
    try {
      await globalBrowser.version();
      lastSessionAliveAt = new Date().toISOString();
    } catch (err) {
      const msg = err && err.message ? err.message : String(err);
      console.warn(`[artlist-server] Heartbeat version() failed: ${msg}`);
      lastLaunchError = `heartbeat failed: ${msg}`;
      await cleanupBrowser();
    }
  }, HB_INTERVAL_MS);
  if (typeof hbTimer.unref === 'function') {
    hbTimer.unref();
  }
}

function stopHeartbeat() {
  if (hbTimer) {
    clearInterval(hbTimer);
    hbTimer = null;
  }
}

await runBootWarmup();
startHeartbeat();

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
  stopHeartbeat();
  await cleanupBrowser();
  server.close(() => process.exit(0));
});
process.on('SIGINT', async () => {
  stopHeartbeat();
  await cleanupBrowser();
  server.close(() => process.exit(0));
});
