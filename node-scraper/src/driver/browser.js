import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import puppeteerCore from 'puppeteer-core';
import puppeteerExtra from 'puppeteer-extra';
import StealthPlugin from 'puppeteer-extra-plugin-stealth';

puppeteerExtra.use(StealthPlugin());

/**
 * Creates a temporary directory for browser profile.
 * @returns {string} Path to temp directory
 */
export function makeTempBrowserDir() {
  // Chromium is launched with --disable-dev-shm-usage, so keeping the user
  // data directory in /dev/shm only makes abandoned sessions consume the
  // finite shared-memory mount. Use the regular temp filesystem and let the
  // owning browser handle remove it during cleanup.
  return fs.mkdtempSync(path.join(os.tmpdir(), 'velox-chrome-'));
}

/**
 * Resolves Chrome profile directory.
 * @param {string} profileDir - Custom profile directory
 * @returns {string} Profile directory path
 */
export function resolveChromeProfile(profileDir) {
  if (profileDir && fs.existsSync(profileDir)) {
    return profileDir;
  }
  return makeTempBrowserDir();
}

/**
 * Canonical Chromium/Chrome executable path lookup.
 *
 * Priority:
 *   1. process.env.CHROME_EXECUTABLE — operator-pinned path (always wins).
 *   2. Filesystem probe of common Linux install paths
 *      (google-chrome, google-chrome-stable, chromium, chromium-browser).
 *   3. return null — the caller's try/catch around puppeteer.launch
 *      will produce a descriptive error.
 *
 * FASE 9 (June 2026) — diagnostic: the previous implementation
 * (executablePath: process.env.CHROME_EXECUTABLE || '/usr/bin/google-chrome')
 * silently fell-back to a default that did not exist on the host
 * when PUPPETEER_SKIP_DOWNLOAD=true was set (the Dockerfile.scraper
 * default), so puppeteer.launch returned a generic "Failed to launch
 * the browser process" error that the server-side healthcheck reported
 * as `browser_running: false` without surfacing the underlying cause.
 *
 * @returns {string|null} Absolute path to the Chrome binary, or null
 *   when no candidate is present on the host.
 */
export function pickChromeExecutable() {
  const explicit = process.env.CHROME_EXECUTABLE;
  if (explicit) return explicit;
  const candidates = [
    '/usr/bin/google-chrome',
    '/usr/bin/google-chrome-stable',
    '/usr/bin/chromium',
    '/usr/bin/chromium-browser',
  ];
  for (const candidate of candidates) {
    try {
      if (fs.existsSync(candidate)) return candidate;
    } catch (_probeErr) {
      // Permission errors or transient FS races during the probe are
      // non-fatal — move on to the next candidate.
    }
  }
  return null;
}

/**
 * Evaluate the browser preflight (PR-FIX-SCRAPER-BROWSER, July 2026).
 *
 * Returns a structured verdict describing which browser sourcing path
 * is available. The caller (artlist_server.js::runBrowserPreflight)
 * decides whether to fail-fast (process.exit 78) or continue. The
 * pure-function shape keeps preflight logic hermetic to unit tests
 * (test/browser.test.mjs) without needing to spawn child processes or
 * mock process.exit.
 *
 * Decision tree (matches the documented operator resolution paths in
 * Dockerfile.scraper + artlist_server.js::runBrowserPreflight):
 *
 *   1. If any of BROWSER_WS / LIGHTPANDA_WS / CHROME_WS is set,
 *      return {ok:true, mode:'ws'}. The WS endpoint takes priority
 *      even if a local Chromium is also installed — operators pin
 *      the WS path when running against an external browser farm
 *      (Lightpanda, Chrome-WS service, Playwright-on-host).
 *   2. Else call pickChromeExecutable() (which honours CHROME_EXECUTABLE
 *      first, then scans /usr/bin/* candidates). If a path is found,
 *      return {ok:true, mode:'local', execPath}.
 *   3. Else return {ok:false, mode:'none', reason:'...'} so the caller
 *      can fail-closed at server startup.
 *
 * @returns {{ok: boolean, mode: 'ws'|'local'|'none', execPath?: string, reason?: string}}
 */
export function evaluateBrowserPreflight() {
  const hasWs = !!(process.env.BROWSER_WS || process.env.LIGHTPANDA_WS || process.env.CHROME_WS);
  if (hasWs) {
    return { ok: true, mode: 'ws', reason: null };
  }
  const execPath = pickChromeExecutable();
  if (execPath) {
    return { ok: true, mode: 'local', execPath, reason: null };
  }
  return {
    ok: false,
    mode: 'none',
    reason: 'no remote browser WS endpoint (BROWSER_WS / LIGHTPANDA_WS / CHROME_WS) AND no local Chromium/Chrome binary found',
  };
}

/**
 * Realistic stealth headers mimicking Chrome 124 on Linux.
 * Used both for WS-connected pages (stealth plugin doesn't apply)
 * and as the canonical header set for Artlist detail/download requests.
 */
export const STEALTH_HEADERS = {
  'accept-language': 'en-US,en;q=0.9,it;q=0.8',
  'sec-ch-ua': '"Chromium";v="124", "Google Chrome";v="124", "Not-A.Brand";v="99"',
  'sec-ch-ua-mobile': '?0',
  'sec-ch-ua-platform': '"Linux"',
  'sec-fetch-dest': 'document',
  'sec-fetch-mode': 'navigate',
  'sec-fetch-site': 'none',
  'sec-fetch-user': '?1',
  'upgrade-insecure-requests': '1',
};

/**
 * Applies stealth-level evasions to a puppeteer Page instance.
 *
 * For locally-launched browsers, the puppeteer-extra stealth plugin
 * already handles these evasions globally. This function is needed
 * for pages obtained from WS-connected browsers (CDP endpoints,
 * Lightpanda, external browser farms) where the stealth plugin's
 * `evaluateOnNewDocument` injections do not apply.
 *
 * @param {import('puppeteer-core').Page} page
 */
export async function applyStealthEvasions(page) {
  if (typeof page.evaluateOnNewDocument === 'function') {
    await page.evaluateOnNewDocument(() => {
      // 1. Remove navigator.webdriver flag
      Object.defineProperty(navigator, 'webdriver', { get: () => false });

      // 2. Realistic navigator.plugins (Chrome on Linux has 5 plugins)
      Object.defineProperty(navigator, 'plugins', {
        get: () => {
          const plugins = [
            { name: 'Chrome PDF Plugin', filename: 'internal-pdf-viewer', description: 'Portable Document Format' },
            { name: 'Chrome PDF Viewer', filename: 'mhjfbmdgcfjbbpaeojofohoefgiehjai', description: '' },
            { name: 'Native Client', filename: 'internal-nacl-plugin', description: '' },
          ];
          plugins.length = 3;
          return plugins;
        },
      });

      // 3. Realistic navigator.languages
      Object.defineProperty(navigator, 'languages', { get: () => ['en-US', 'en', 'it'] });

      // 4. Override permissions API to return 'granted' for notifications
      const originalQuery = window.navigator.permissions.query;
      window.navigator.permissions.query = (parameters) =>
        parameters.name === 'notifications'
          ? Promise.resolve({ state: Notification.permission })
          : originalQuery(parameters);

      // 5. Prevent detection of CDP via chrome.runtime
      if (window.chrome) {
        window.chrome.runtime = undefined;
      }

      // 6. WebGL vendor/renderer spoofing (common fingerprint vector)
      const getParameter = WebGLRenderingContext.prototype.getParameter;
      WebGLRenderingContext.prototype.getParameter = function (param) {
        if (param === 37445) return 'Intel Inc.';
        if (param === 37446) return 'Intel Iris OpenGL Engine';
        return getParameter.call(this, param);
      };
    });
  }

  // Set realistic headers on every navigation
  if (typeof page.setExtraHTTPHeaders === 'function') {
    await page.setExtraHTTPHeaders(STEALTH_HEADERS);
  }
}

/**
 * Opens browser instance (local or remote).
 *
 * Local launches use puppeteer-extra with the stealth plugin to
 * automatically evade bot detection (navigator.webdriver, headless
 * traces, automation flags). Remote WS connections get stealth
 * evasions applied manually via applyStealthEvasions() on each page.
 *
 * Return shape extended in FASE 9 (June 2026) to carry `launchError`
 * so the artlist-server /health endpoint can surface the underlying
 * cause when browser_running transitions from false (unlaunched) to
 * false (crashed after a brief launch). Callers that ignore the new
 * field continue to compile because destructuring is optional.
 *
 * @param {string} profileDir - Profile directory
 * @returns {Promise<{browser: object, connected: boolean, launchError: (string|null)}>}
 */
export async function openBrowser(profileDir) {
  const browserWs = process.env.BROWSER_WS || process.env.LIGHTPANDA_WS || process.env.CHROME_WS || '';
  if (browserWs) {
    try {
      const browser = await puppeteerCore.connect({
        browserWSEndpoint: browserWs,
      });
      return { browser, connected: true, launchError: null };
    } catch (err) {
      // FASE 9 (June 2026): redact the raw browserWS URL from the return
      // value to avoid a token/PII leak on GET /health if the operator
      // misconfigures a CDP endpoint with credentials. The full URL is
      // emitted to stderr (operator-only log stream) for triage.
      console.error(`[artlist-browser] puppeteer.connect over CDP failed: ${err && err.message ? err.message : String(err)} (browserWS=${browserWs})`);
      const safeMsg = '[artlist-browser] puppeteer.connect over CDP failed; see operator logs for the browserWS endpoint detail';
      return { browser: null, connected: false, launchError: safeMsg };
    }
  }

  const ownsUserDataDir = !profileDir || !fs.existsSync(profileDir);
  const userDataDir = resolveChromeProfile(profileDir);
  const executablePath = pickChromeExecutable();
  if (!executablePath) {
    const msg = '[artlist-browser] no Chrome/Chromium binary detected on this host. ' +
                'Install chromium via "apt-get install -y chromium" or set CHROME_EXECUTABLE=/path/to/chrome. ' +
                'For the docker-compose service, the image must include a Chromium install (FASE 10 followup).';
    console.error(msg);
    return { browser: null, connected: false, launchError: msg };
  }
  const args = [
    '--no-sandbox',
    '--disable-setuid-sandbox',
    '--disable-dev-shm-usage',
    '--disable-gpu',
    '--no-first-run',
    '--no-default-browser-check',
  ];
  try {
    const headless = process.env.ARTLIST_HEADLESS === 'false' ? false : 'new';
    const browser = await puppeteerExtra.launch({
      executablePath,
      headless,
      userDataDir,
      args,
    });
    return {
      browser,
      connected: false,
      launchError: null,
      userDataDir,
      ownsUserDataDir,
    };
  } catch (err) {
    const msg = `[artlist-browser] puppeteer.launch failed: ${err && err.message ? err.message : String(err)} ` +
                `(executablePath=${executablePath}, args=${args.join(' ')})`;
    console.error(msg);
    if (ownsUserDataDir) {
      try {
        fs.rmSync(userDataDir, { recursive: true, force: true });
      } catch (_cleanupErr) {
        // Preserve the launch diagnostic; the next startup can retry cleanup.
      }
    }
    return {
      browser: null,
      connected: false,
      launchError: msg,
      userDataDir: null,
      ownsUserDataDir: false,
    };
  }
}

/**
 * Creates browser page with context.
 *
 * FASE 9 (June 2026) — null-safety: openBrowser now returns
 * `{browser: null, launchError: <string>}` instead of throwing on
 * launch failure (the return shape change lets the /health endpoint
 * surface the underlying cause). createBrowserPage preserves the
 * throw contract callers have historically depended on by re-throwing
 * the diagnostic message — callers that tried openBrowser directly
 * used to get a puppeteer Error; we keep the throw shape but
 * promote the descriptive launchError so triage is no worse than
 * before the FASE 9 reshape.
 *
 * @param {string} profileDir - Profile directory
 * @returns {Promise<{browser: object, connected: boolean, context: object, page: object}>}
 */
export async function createBrowserPage(profileDir) {
  const {
    browser,
    connected,
    launchError,
    userDataDir,
    ownsUserDataDir,
  } = await openBrowser(profileDir);
  if (!browser) {
    throw new Error(launchError || '[artlist-browser] openBrowser returned no browser (no diagnostic available)');
  }
  const context = await browser.createBrowserContext();
  const page = await context.newPage();

  // For WS-connected browsers (stealth plugin doesn't inject evaluateOnNewDocument),
  // apply evasions manually on every new page so Artlist's JS probes see a
  // clean fingerprint (navigator.webdriver=false, realistic plugins, headers).
  if (connected) {
    await applyStealthEvasions(page);
  }

  return {
    browser,
    connected,
    context,
    page,
    userDataDir,
    ownsUserDataDir,
  };
}

/**
 * Closes browser and associated resources.
 * @param {object} handle - Browser handle
 */
export async function closeBrowserHandle(handle) {
  try {
    if (handle?.page) {
      await handle.page.close().catch(() => {});
    }
    if (handle?.context) {
      await handle.context.close().catch(() => {});
    }
  } finally {
    if (handle?.browser) {
      if (handle.connected && handle.browser.disconnect) {
        await handle.browser.disconnect().catch(() => {});
      } else if (handle.browser.close) {
        await handle.browser.close().catch(() => {});
      }
    }
    if (handle?.ownsUserDataDir && handle.userDataDir) {
      await fs.promises.rm(handle.userDataDir, { recursive: true, force: true }).catch(() => {});
    }
  }
}
