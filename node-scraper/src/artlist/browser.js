import fs from 'node:fs';
import os from 'node:os';
import puppeteer from 'puppeteer-core';

/**
 * Creates a temporary directory for browser profile.
 * @returns {string} Path to temp directory
 */
export function makeTempBrowserDir() {
  const baseDir = fs.existsSync('/dev/shm') ? '/dev/shm' : os.tmpdir();
  return fs.mkdtempSync(path.join(baseDir, 'velox-chrome-'));
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
 * Opens browser instance (local or remote).
 * @param {string} profileDir - Profile directory
 * @returns {Promise<{browser: object, connected: boolean}>}
 */
export async function openBrowser(profileDir) {
  const browserWs = process.env.BROWSER_WS || process.env.LIGHTPANDA_WS || process.env.CHROME_WS || '';
  if (browserWs) {
    const browser = await puppeteer.connect({
      browserWSEndpoint: browserWs,
    });
    return { browser, connected: true };
  }

  const userDataDir = resolveChromeProfile(profileDir);
  const browser = await puppeteer.launch({
    executablePath: process.env.CHROME_EXECUTABLE || '/usr/bin/google-chrome',
    headless: 'new',
    userDataDir,
    args: [
      '--no-sandbox',
      '--disable-gpu',
      '--disable-blink-features=AutomationControlled',
      '--no-first-run',
      '--no-default-browser-check',
    ],
  });
  return { browser, connected: false };
}

/**
 * Creates browser page with context.
 * @param {string} profileDir - Profile directory
 * @returns {Promise<{browser: object, connected: boolean, context: object, page: object}>}
 */
export async function createBrowserPage(profileDir) {
  const { browser, connected } = await openBrowser(profileDir);
  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  return { browser, connected, context, page };
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
  }
}
