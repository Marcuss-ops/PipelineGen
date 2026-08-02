// src/server/http.js — HTTP server bootstrap + browser lifecycle +
// process signal handling for the Artlist scraper. The 489-line
// artlist_server.js was split in the
// `chore(node-scraper): introduce src/{driver,scrape,server}` refactor
// so the server bucket has one file per concern:
//
//   src/server/http.js    — bootstrap, lifecycle, process-level signals
//                           (THIS FILE — owns state + lifecycle + exit codes)
//   src/server/routes.js  — per-endpoint handlers / dispatch (no shared state)
//
// This module owns:
//   - env-derived config (PORT, BIND, PROFILE_DIR, DEFAULT_LIMIT, MAX_LIMIT,
//                         HB_INTERVAL_MS, HB_FRESH_WINDOW_MS)
//   - process-level state (globalBrowser, lastLaunchError, ...)
//   - browser lifecycle (getBrowser, cleanupBrowser, runBootWarmup)
//   - heartbeat (startHeartbeat, stopHeartbeat)
//   - preflight (runBrowserPreflight) — fail-closed browser resolution
//   - HTTP server bootstrap (createServer + listen + signal handlers)
//
// Per-endpoint handlers live in src/server/routes.js; this module
// exports startArtlistServer() which builds the ctx object (config +
// state mutators + injected deps) and dispatches every incoming
// request via routes.dispatchRequest.
import http from 'node:http';
import fs from 'node:fs';

import { openBrowser, evaluateBrowserPreflight } from '../driver/browser.js';
import { searchArtlist } from '../../artlist_search.js';
import { searchArtlistGateway } from '../../artlist/gateway-search.js';
import { downloadClipVideo } from './download.js';
import { fetchClipDetails } from '../scrape/detail-page.js';
import { computeHealthVerdict } from './health.js';
import { dispatchRequest } from './routes.js';

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

// PR-HEALTHCHECK-FAILFAST (P2, July 2026): /health freshness contract.
//   HB_INTERVAL_MS       = 30s heartbeat setInterval cadence.
//   HB_FRESH_WINDOW_MS   = 60s — anything older than this in
//                         lastSessionAliveAt drops recentSessionAlive
//                         to false, so /health flips to 503.
// Heartbeat interval < fresh-window guarantees at least ONE fresh
// check during any window in steady state.
const HB_INTERVAL_MS = 30_000;
const HB_FRESH_WINDOW_MS = 60_000;

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
let globalBrowserUserDataDir = null;
let globalBrowserOwnsUserDataDir = false;

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
  const {
    browser,
    connected,
    launchError,
    userDataDir,
    ownsUserDataDir,
  } = await openBrowser(PROFILE_DIR);
  globalBrowser = browser;
  globalBrowserConnected = connected;
  globalBrowserUserDataDir = userDataDir || null;
  globalBrowserOwnsUserDataDir = ownsUserDataDir === true;
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
      if (globalBrowserOwnsUserDataDir && globalBrowserUserDataDir) {
        await fs.promises.rm(globalBrowserUserDataDir, { recursive: true, force: true }).catch(() => {});
      }
      globalBrowserUserDataDir = null;
      globalBrowserOwnsUserDataDir = false;
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

// ─── Preflight (PR-FIX-SCRAPER-BROWSER, July 2026) ────────────────────────────
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

// ─── Boot warmup (PR-HEALTHCHECK-FAILFAST, P2, July 2026) ─────────────────────
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

// ─── Heartbeat (PR-HEALTHCHECK-FAILFAST, P2, July 2026) ────────────────────────
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

// ─── ctx factory ─────────────────────────────────────────────────────────────
//
// The ctx object flows through every dispatch from createServer into
// routes.dispatchRequest. Config is frozen because the env-derived
// values do not change after boot; state uses getters + explicit
// mutators so handlers cannot reach into module-level state without
// the accessor channels defined here. Deps are frozen because they
// are wiring (not data) and never reassigned.
function createCtx() {
  return {
    config: Object.freeze({
      PORT,
      BIND,
      PROFILE_DIR,
      DEFAULT_LIMIT,
      MAX_LIMIT,
      HB_INTERVAL_MS,
      HB_FRESH_WINDOW_MS,
    }),
    state: {
      get requestCount() { return requestCount; },
      incRequest() { return ++requestCount; },
      setLastSearchAt(iso) { lastSearchAt = iso; },
      setLastLaunchError(msg) { lastLaunchError = msg; },
      get startedAt() { return startedAt; },
      get globalBrowser() { return globalBrowser; },
      get lastLaunchError() { return lastLaunchError; },
      get lastSearchAt() { return lastSearchAt; },
      get globalBrowserPid() { return globalBrowserPid; },
      get lastSessionAliveAt() { return lastSessionAliveAt; },
    },
    deps: Object.freeze({
      getBrowser,
      searchArtlist,
      downloadClipVideo,
      fetchClipDetails,
      computeHealthVerdict,
      searchArtlistGateway,
    }),
  };
}

// ─── Public entry point — used by root artlist_server.js ─────────────────────
export async function startArtlistServer() {
  runBrowserPreflight();
  await runBootWarmup();
  startHeartbeat();

  const ctx = createCtx();
  const server = http.createServer(async (req, res) => {
    await dispatchRequest(req, res, ctx);
  });

  server.listen(PORT, BIND, () => {
    console.log(`[artlist-server] Listening on http://${BIND}:${PORT} (bind via ARTLIST_SCRAPER_BIND env var)`);
    console.log(`[artlist-server] Endpoints: POST /search, POST /v1/clips/search, POST /detail, POST /download, POST /discover-api, GET /health, GET /v1/health`);
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

  return server;
}
