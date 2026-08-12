import fs from 'node:fs';
import { evaluateBrowserPreflight, openBrowser } from '../driver/browser.js';
import { HB_INTERVAL_MS, PROFILE_DIR } from './http-config.js';

let requestCount = 0;
const startedAt = new Date().toISOString();
let globalBrowser = null;
let globalBrowserConnected = false;
let lastLaunchError = null;
let lastSessionAliveAt = null;
let lastSearchAt = null;
let globalBrowserPid = null;
let globalBrowserUserDataDir = null;
let globalBrowserOwnsUserDataDir = false;

export async function getBrowser() {
  if (globalBrowser) {
    try {
      await globalBrowser.version();
      return globalBrowser;
    } catch {
      console.warn('[artlist-server] Browser disconnected or dead, restarting...');
      await cleanupBrowser();
    }
  }

  console.log('[artlist-server] Launching persistent Chromium browser...');
  const { browser, connected, launchError, userDataDir, ownsUserDataDir } = await openBrowser(PROFILE_DIR);
  globalBrowser = browser;
  globalBrowserConnected = connected;
  globalBrowserUserDataDir = userDataDir || null;
  globalBrowserOwnsUserDataDir = ownsUserDataDir === true;
  if (browser !== null && launchError === null) {
    lastLaunchError = null;
    lastSessionAliveAt = new Date().toISOString();
    try {
      const proc = browser.process && browser.process();
      if (proc && typeof proc.pid === 'number') globalBrowserPid = proc.pid;
    } catch (_pidErr) {
      // Non-fatal; the diagnostic remains available through /health.
    }
  } else if (launchError) {
    lastLaunchError = launchError;
    globalBrowserPid = null;
  }
  return globalBrowser;
}

export async function cleanupBrowser() {
  if (!globalBrowser) return;
  try {
    if (globalBrowserConnected && globalBrowser.disconnect) await globalBrowser.disconnect();
    else if (globalBrowser.close) await globalBrowser.close();
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
    globalBrowserPid = null;
    lastSessionAliveAt = null;
  }
}

export function runBrowserPreflight() {
  const verdict = evaluateBrowserPreflight();
  if (verdict.ok) {
    console.log(`[artlist-server] Preflight OK (mode=${verdict.mode}${verdict.execPath ? `, exec=${verdict.execPath}` : ''})`);
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

export async function runBootWarmup() {
  console.log('[artlist-server] Boot warmup: launching Chromium before serving...');
  try {
    const browser = await getBrowser();
    if (browser) console.log(`[artlist-server] Boot warmup OK (browser_pid=${globalBrowserPid}, last_session_alive_at=${lastSessionAliveAt})`);
    else console.error('[artlist-server] Boot warmup returned no browser; /health will report 503 + restart');
  } catch (err) {
    const msg = err && err.message ? err.message : String(err);
    console.error('[artlist-server] Boot warmup threw:', msg);
    lastLaunchError = `boot warmup failed: ${msg}`;
  }
}

let hbTimer = null;
export function startHeartbeat() {
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
  if (typeof hbTimer.unref === 'function') hbTimer.unref();
}

export function stopHeartbeat() {
  if (hbTimer) {
    clearInterval(hbTimer);
    hbTimer = null;
  }
}

export function createStateAccessors() {
  return {
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
  };
}
