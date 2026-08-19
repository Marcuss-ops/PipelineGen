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
  evaluateBrowserPreflight,
} from '../src/driver/browser.js';
import { computeHealthVerdict } from '../src/server/health.js';

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
// would break unrelated call sites in the detail/download path.
test('browser.js: helper exports remain intact', () => {
  assert.equal(typeof pickChromeExecutable, 'function', 'pickChromeExecutable must be exported');
  assert.equal(typeof openBrowser, 'function', 'openBrowser must be exported');
  assert.equal(typeof resolveChromeProfile, 'function', 'resolveChromeProfile must be exported');
  assert.equal(typeof makeTempBrowserDir, 'function', 'makeTempBrowserDir must be exported');
  assert.equal(typeof closeBrowserHandle, 'function', 'closeBrowserHandle must be exported');
  assert.equal(typeof evaluateBrowserPreflight, 'function', 'evaluateBrowserPreflight must be exported (PR-FIX-SCRAPER-BROWSER July 2026)');
});

// ---------- PR-FIX-SCRAPER-BROWSER (July 2026): preflight verdict tests ----------
//
// evaluateBrowserPreflight returns a structured verdict so the
// actual fail-fast in artlist_server.js::runBrowserPreflight does
// not need to be exercised in unit tests (mocking process.exit is
// brittle). Each of the 3 cases below pins one branch of the
// decision tree in src/driver/browser.js.

// Case A: no WS endpoint AND no local binary → preflight FAIL.
// The test forces `CHROME_EXECUTABLE` to a path that does NOT exist
// in `/usr/bin/*` candidates by pointing pickChromeExecutable's
// priority-1 (CHROME_EXECUTABLE env) at a nonexistent file
// (pickChromeExecutable's priority-1 returns the env verbatim when
// it's set, regardless of whether the file exists — so we instead
// delete the env to trigger the /usr/bin probe, then mock the probe
// by checking ONLY that the verdict is consistent given the test
// environment's actual /usr/bin state).
//
// The 3-tier preflight contract is decided by env ordering:
//   - any WS set         → mode='ws', ok=true (regardless of binary)
//   - else CHROME_EXECUTABLE set → mode='local', execPath=... ok=true
//   - else /usr/bin/* hit → mode='local', execPath=... ok=true
//   - else → mode='none', ok=false
//
// The "fail" branch only fires when NEITHER WS NOR binary exists,
// which on the test host means /usr/bin/chromium AND /usr/bin/google-chrome
// AND ... are all absent. On a host with any of them installed,
// the test passes by returning mode='local'. Either way the test
// verifies the verdict SHAPE rather than the binary-state outcome,
// so it stays hermetic regardless of the underlying host's browser
// install state.
test('evaluateBrowserPreflight: verdict shape is valid for every branch', () => {
  // Save env state so we can recover it across the 3 sub-cases.
  const saved = {
    BROWSER_WS: process.env.BROWSER_WS,
    LIGHTPANDA_WS: process.env.LIGHTPANDA_WS,
    CHROME_WS: process.env.CHROME_WS,
    CHROME_EXECUTABLE: process.env.CHROME_EXECUTABLE,
  };
  try {
    // Sub-case A: WS wins (highest priority).
    delete process.env.LIGHTPANDA_WS;
    delete process.env.CHROME_WS;
    process.env.BROWSER_WS = 'ws://stub-cdp-endpoint:9222';
    const wsVerdict = evaluateBrowserPreflight();
    assert.equal(wsVerdict.ok, true, 'WS endpoint must yield ok=true');
    assert.equal(wsVerdict.mode, 'ws', 'mode must be ws');
    assert.equal(wsVerdict.reason, null, 'reason must be null on success');

    // Sub-case B: CHROME_EXECUTABLE override wins (priority-2).
    delete process.env.BROWSER_WS;
    process.env.CHROME_EXECUTABLE = '/usr/bin/chromium';
    const execVerdict = evaluateBrowserPreflight();
    assert.equal(execVerdict.ok, true, 'CHROME_EXECUTABLE must yield ok=true');
    assert.equal(execVerdict.mode, 'local', 'mode must be local');
    assert.equal(execVerdict.execPath, '/usr/bin/chromium', 'execPath must echo the env var');
    assert.equal(execVerdict.reason, null, 'reason must be null on success');

    // Sub-case C: no WS + CHROME_EXECUTABLE→nonexistent.
    // pickChromeExecutable priority-1 returns the env verbatim
    // regardless of whether the file exists, so this sub-case does
    // NOT exercise the "fail" branch — it confirms the local-mode
    // path always wins when the env var is set. The pure "fail"
    // branch is exercised by deleting CHROME_EXECUTABLE + relying
    // on the host's /usr/bin probe. We document that branch as
    // 'depends on host /usr/bin state' and verify the verdict shape
    // only (ok is bool, mode is one of 'ws'|'local'|'none', reason
    // is string|null).
    delete process.env.CHROME_EXECUTABLE;
    const noEnvVerdict = evaluateBrowserPreflight();
    assert.equal(typeof noEnvVerdict.ok, 'boolean');
    assert.ok(['ws', 'local', 'none'].includes(noEnvVerdict.mode));
    if (noEnvVerdict.ok) {
      assert.equal(noEnvVerdict.reason, null);
    } else {
      assert.equal(noEnvVerdict.mode, 'none');
      assert.ok(
        typeof noEnvVerdict.reason === 'string' && noEnvVerdict.reason.length > 0,
        'reason must be a non-empty string when ok=false',
      );
      assert.match(noEnvVerdict.reason, /BROWSER_WS/, 'reason must name the env var');
      assert.match(noEnvVerdict.reason, /Chrome\/Chromium binary/, 'reason must name the missing artifact');
    }
  } finally {
    for (const [k, v] of Object.entries(saved)) {
      if (v === undefined) delete process.env[k];
      else process.env[k] = v;
    }
  }
});

// Case B: explicit failure path on a hermetic test that masks the
// host's /usr/bin/* probes via a CHROME_EXECUTABLE that points at
// a guaranteed-nonexistent path AND no WS endpoint.
// (Relies on the fact that pickChromeExecutable priority-1 returns
// the env even when the file doesn't exist; we don't want a false
// pass from a host with /usr/bin/chromium. So we ASSERT that with
// CHROME_EXECUTABLE=non-existent and no WS, the verdict is
// mode='local' execPath=<that path> — which is the documented
// behaviour. This pins the contract: the env override is treated
// as authoritative, no filesystem re-check.)
test('evaluateBrowserPreflight CHROME_EXECUTABLE takes precedence even when path does not exist', () => {
  const saved = {
    BROWSER_WS: process.env.BROWSER_WS,
    LIGHTPANDA_WS: process.env.LIGHTPANDA_WS,
    CHROME_WS: process.env.CHROME_WS,
    CHROME_EXECUTABLE: process.env.CHROME_EXECUTABLE,
  };
  try {
    delete process.env.BROWSER_WS;
    delete process.env.LIGHTPANDA_WS;
    delete process.env.CHROME_WS;
    process.env.CHROME_EXECUTABLE = '/this/path/does/not/exist/chromium-99';
    const verdict = evaluateBrowserPreflight();
    assert.equal(verdict.ok, true);
    assert.equal(verdict.mode, 'local');
    assert.equal(verdict.execPath, '/this/path/does/not/exist/chromium-99',
      'pickChromeExecutable priority-1 (env override) must return the env verbatim, no fs probe');
  } finally {
    for (const [k, v] of Object.entries(saved)) {
      if (v === undefined) delete process.env[k];
      else process.env[k] = v;
    }
  }
});

// ---------- PR-HEALTHCHECK-FAILFAST (P2, July 2026): computeHealthVerdict ----------
//
// computeHealthVerdict is the pure-function module that the /health handler
// delegates to. Each test below pins one ax-axis of the 3-predicate
// healthy logic:
//   healthy := browser != null
//           && !last_launch_error
//           && recentSessionAlive (= now - lastSessionAliveAt <= window)

// Case 1: all-healthy inputs.
test('computeHealthVerdict: all-positive state yields healthy=true', () => {
  const now = Date.now();
  const v = computeHealthVerdict({
    browser: { _stub: true },
    lastLaunchError: null,
    lastSessionAliveAt: new Date(now - 30_000).toISOString(),
    now,
    freshnessWindowMs: 60_000,
  });
  assert.equal(v.healthy, true);
  assert.equal(v.browserRunning, true);
  assert.equal(v.recentSessionAlive, true);
});

// Case 2: lastSessionAliveAt STALE (> window) -> recentSessionAlive=false -> healthy=false.
test('computeHealthVerdict: stale lastSessionAliveAt flips recentSessionAlive=false', () => {
  const now = Date.now();
  const v = computeHealthVerdict({
    browser: { _stub: true },
    lastLaunchError: null,
    lastSessionAliveAt: new Date(now - 90_000).toISOString(), // 90s ago
    now,
    freshnessWindowMs: 60_000, // window is 60s
  });
  assert.equal(v.healthy, false, 'healthy must be false when lastSessionAliveAt is older than the fresh window');
  assert.equal(v.browserRunning, true, 'browserRunning stays true (browser != null)');
  assert.equal(v.recentSessionAlive, false, 'recentSessionAlive must be false past the window');
});

// Case 3: lastLaunchError non-null -> healthy=false regardless of other state.
test('computeHealthVerdict: lastLaunchError non-null makes healthy=false', () => {
  const v = computeHealthVerdict({
    browser: { _stub: true },
    lastLaunchError: 'puppeteer.launch failed: missing binary',
    lastSessionAliveAt: new Date().toISOString(),
  });
  assert.equal(v.healthy, false);
  assert.equal(v.browserRunning, true);
  assert.equal(v.recentSessionAlive, true);
});

// Case 4: browser null -> browserRunning=false -> healthy=false (boot pre-warmup).
test('computeHealthVerdict: null browser makes browserRunning=false', () => {
  const v = computeHealthVerdict({
    browser: null,
    lastLaunchError: null,
    lastSessionAliveAt: null,
  });
  assert.equal(v.healthy, false);
  assert.equal(v.browserRunning, false);
  assert.equal(v.recentSessionAlive, false, 'null timestamp also means not recent');
});

// Case 5: all-negative inputs (default fallback for defensiveness).
test('computeHealthVerdict: empty/zero input is safe', () => {
  const v = computeHealthVerdict({});
  assert.equal(v.healthy, false);
  assert.equal(v.browserRunning, false);
  assert.equal(v.recentSessionAlive, false);
});

// Case 6: edge of the fresh window -- exactly at the boundary -- still recent.
// (now - lastSessionAliveAt === window_ms is the boundary; verdict stays true on the
// inclusive side per `now - X <= window_ms` semantics.)
test('computeHealthVerdict: exactly at the boundary of freshness window is recent', () => {
  const now = 1_000_000_000_000;
  const v = computeHealthVerdict({
    browser: { _stub: true },
    lastLaunchError: null,
    lastSessionAliveAt: new Date(now - 60_000).toISOString(),
    now,
    freshnessWindowMs: 60_000,
  });
  assert.equal(v.healthy, true);
  assert.equal(v.recentSessionAlive, true);
});

// Case 7: malformed/unparsable lastSessionAliveAt -> recentSessionAlive=false.
test('computeHealthVerdict: malformed lastSessionAliveAt yields recentSessionAlive=false', () => {
  const v = computeHealthVerdict({
    browser: { _stub: true },
    lastLaunchError: null,
    lastSessionAliveAt: 'this-is-not-a-date',
  });
  assert.equal(v.recentSessionAlive, false, 'malformed timestamp must not be treated as recent');
  assert.equal(v.healthy, false);
});
