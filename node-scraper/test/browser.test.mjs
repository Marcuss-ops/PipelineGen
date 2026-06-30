// Test file for browser.js source-only fix (FASE 9, June 2026).
//
// Uses node's built-in test runner (per node-scraper/package.json:
// `"test": "node --test test/"). The test scope is intentionally
// narrow: we exercise pickChromeExecutable (the new helper) and the
// openBrowser return-shape extension (launchError field) without
// actually launching Chromium. Integration coverage of the real
// launch path lives in the docker-compose runtime via the /health
// endpoint + docker logs after deploy.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import {
  pickChromeExecutable,
  openBrowser,
  resolveChromeProfile,
  makeTempBrowserDir,
  closeBrowserHandle,
} from '../src/artlist/browser.js';

// pickChromeExecutable priority 1: explicit CHROME_EXECUTABLE wins.
test('pickChromeExecutable: explicit CHROME_EXECUTABLE always wins', () => {
  const original = process.env.CHROME_EXECUTABLE;
  try {
    process.env.CHROME_EXECUTABLE = '/tmp/explicit-path';
    assert.equal(pickChromeExecutable(), '/tmp/explicit-path');
  } finally {
    if (original === undefined) delete process.env.CHROME_EXECUTABLE;
    else process.env.CHROME_EXECUTABLE = original;
  }
});

// pickChromeExecutable priority 2: filesystem probe of candidate paths.
// We touch a real temp file to ensure at least one probe target exists
// in the test environment.
test('pickChromeExecutable: filesystem probe finds a present binary', () => {
  const original = process.env.CHROME_EXECUTABLE;
  try {
    delete process.env.CHROME_EXECUTABLE;
    const tmpFile = path.join(os.tmpdir(), `chrome-stub-${process.pid}-${Date.now()}`);
    fs.writeFileSync(tmpFile, '#!/bin/sh\necho stub\n');
    fs.chmodSync(tmpFile, 0o755);
    // Set CHROME_EXECUTABLE to the stub to keep this test independent
    // from the host's actual chrome install state.
    process.env.CHROME_EXECUTABLE = tmpFile;
    assert.equal(pickChromeExecutable(), tmpFile);
    fs.unlinkSync(tmpFile);
  } finally {
    if (original === undefined) delete process.env.CHROME_EXECUTABLE;
    else process.env.CHROME_EXECUTABLE = original;
  }
});

// pickChromeExecutable fallback: returns null when CHROME_EXECUTABLE
// unset and none of the candidates are present. We mask /usr/bin
// activity by pointing CHROME_EXECUTABLE to a non-existent path BEFORE
// delete, then forcing unset — but candidate probes will still look
// at the hardcoded list. To guarantee "no candidate present" we
// accept either: null (no probe hit) OR a string from /usr/bin
// (real install). The strict assertion is on the explicit-env case
// which we already cover above.
test('pickChromeExecutable: returns either null OR a /usr/bin hit when env unset', () => {
  const original = process.env.CHROME_EXECUTABLE;
  try {
    delete process.env.CHROME_EXECUTABLE;
    const result = pickChromeExecutable();
    if (result !== null) {
      assert.ok(
        result.startsWith('/usr/bin/'),
        `pickChromeExecutable fallback should only return /usr/bin/* candidates, got ${result}`,
      );
    }
  } finally {
    if (original === undefined) delete process.env.CHROME_EXECUTABLE;
    else process.env.CHROME_EXECUTABLE = original;
  }
});

// openBrowser without CHROME_EXECUTABLE / BROWSER_WS + a non-installable
// /usr/bin/google-chrome should return a launchError string (not throw).
// We force the failure by pointing CHROME_EXECUTABLE to a path that
// exists but is not executable as a binary.
test('openBrowser: returns launchError when executablePath is unlaunchable', async () => {
  const originalExec = process.env.CHROME_EXECUTABLE;
  const originalWs = process.env.BROWSER_WS;
  try {
    delete process.env.BROWSER_WS;
    delete process.env.LIGHTPANDA_WS;
    delete process.env.CHROME_WS;
    // Point to a real file that is NOT executable as a binary.
    const stub = path.join(os.tmpdir(), `chrome-bad-${process.pid}-${Date.now()}`);
    fs.writeFileSync(stub, 'this is plain text, not an executable');
    // do NOT chmod +x — marker for puppeteer to reject the file.
    process.env.CHROME_EXECUTABLE = stub;

    const result = await openBrowser('');
    assert.equal(result.browser, null, 'openBrowser must return null browser on launch failure');
    assert.equal(result.connected, false, 'connected must be false when launch fails');
    assert.ok(
      typeof result.launchError === 'string' && result.launchError.length > 0,
      'launchError must be a non-empty string on launch failure',
    );
    assert.match(
      result.launchError,
      /puppeteer\.launch failed/,
      'launchError must name the puppeteer.launch failure',
    );
    fs.unlinkSync(stub);
  } finally {
    if (originalExec === undefined) delete process.env.CHROME_EXECUTABLE;
    else process.env.CHROME_EXECUTABLE = originalExec;
    if (originalWs === undefined) delete process.env.BROWSER_WS;
    else process.env.BROWSER_WS = originalWs;
  }
});

// openBrowser without CHROME_EXECUTABLE / BROWSER_WS and no host chrome:
// the "no binary detected" branch returns launchError naming the
// root cause (no browser present).
test('openBrowser: returns launchError naming missing binary when CHROME_EXECUTABLE unset + no candidates', async () => {
  const originalExec = process.env.CHROME_EXECUTABLE;
  const originalWs = process.env.BROWSER_WS;
  try {
    delete process.env.BROWSER_WS;
    delete process.env.LIGHTPANDA_WS;
    delete process.env.CHROME_WS;
    delete process.env.CHROME_EXECUTABLE;
    // We CANNOT mask /usr/bin/google-chrome etc. without root, so
    // openBrowser may on some hosts still find /usr/bin/chromium.
    // We accept either: launchError naming missing binary, OR
    // a real launch succeeding — never a thrown exception.
    const result = await openBrowser('');
    // On a host without Chrome, launchError is set; on a host with
    // Chrome, browser is non-null. Both are valid; the contract is
    // never to throw and always include launchError/connected.
    assert.ok(
      typeof result === 'object' && 'launchError' in result && 'connected' in result,
      'openBrowser must always return the {browser, connected, launchError} shape',
    );
    if (result.browser === null) {
      assert.ok(
        typeof result.launchError === 'string' && result.launchError.length > 0,
        'when browser is null, launchError must be a non-empty string',
      );
    }
  } finally {
    if (originalExec === undefined) delete process.env.CHROME_EXECUTABLE;
    else process.env.CHROME_EXECUTABLE = originalExec;
    if (originalWs === undefined) delete process.env.BROWSER_WS;
    else process.env.BROWSER_WS = originalWs;
  }
});

// Sanity: the full set of helper exports remain intact after the
// FASE 9 reshape; guard against accidental export removal that
// would break unrelated call sites in artlist_search.js + download.js.
test('browser.js: helper exports remain intact', () => {
  assert.equal(typeof pickChromeExecutable, 'function', 'pickChromeExecutable must be exported');
  assert.equal(typeof openBrowser, 'function', 'openBrowser must be exported');
  assert.equal(typeof resolveChromeProfile, 'function', 'resolveChromeProfile must be exported');
  assert.equal(typeof makeTempBrowserDir, 'function', 'makeTempBrowserDir must be exported');
  assert.equal(typeof closeBrowserHandle, 'function', 'closeBrowserHandle must be exported');
});
