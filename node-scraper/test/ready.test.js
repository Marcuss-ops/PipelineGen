// node-scraper/test/ready.test.js
//
// Phase 1 audit fix (PR ref fix/artlist-container-runtime).
// Smoke test for the new /ready endpoint contract:
//   - GET /ready returns JSON with `ok`, `ready`, `browser_connected`,
//     `error`, `port` fields.
//   - Without Chromium installed (the test environment), the
//     handler MUST return 503 with ok=false, ready=false.
//   - GET /health remains liveness-only — always returns 200.
//
// Uses node:test (built-in test runner; ships with Node 22, no
// new dependency) so we can rely on it in CI without bumping
// package.json deps. The test starts the server in a subprocess on
// an ephemeral port (ARTLIST_SCRAPER_PORT=0 so the OS picks) and
// uses the standard `node:http` client to query /ready and
// /health.
//
// We deliberately do NOT spin up a real Chromium — Chromium
// installation is linter-/build-level (Docker phase) and out of
// scope for the unit test. The /ready contract is "fail closed
// when chromium is missing", which is exactly what this test pins.

import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const SCRAPER_PATH = path.resolve(__dirname, '..', 'artlist_server.js');

// Phase 1 fix uses a fixed test port. We deliberately do NOT use
// ARTLIST_SCRAPER_PORT=0 (ephemeral) because artlist_server.js
// logs the *configured* PORT (parseInt(env)), not the OS-assigned
// bound port from server.address().port — so waitForListen on the
// "Listening on" regex match would yield 0 if the env var were 0,
// which http.request then defaults to :80 (ECONNREFUSED on most
// CI runners). Pinning 18791 keeps the test deterministic and
// avoids the parseInt(0) footgun.
const TEST_PORT = 18791;

function startServer(env) {
  const child = spawn(process.execPath, [SCRAPER_PATH], {
    env: { ...process.env, ...env, HOST: '127.0.0.1' },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  return child;
}

function stopServer(child) {
  if (!child || child.killed) return Promise.resolve();
  return new Promise((resolve) => {
    child.once('exit', () => resolve());
    child.kill('SIGTERM');
    setTimeout(() => {
      if (!child.killed) child.kill('SIGKILL');
    }, 3000);
  });
}

function get(port, urlPath) {
  return new Promise((resolve, reject) => {
    const req = http.request(
      { host: '127.0.0.1', port, path: urlPath, method: 'GET', timeout: 5000 },
      (res) => {
        let body = '';
        res.on('data', (chunk) => (body += chunk));
        res.on('end', () =>
          resolve({ status: res.statusCode, body, json: safeJson(body) })
        );
      }
    );
    req.on('error', reject);
    req.on('timeout', () => req.destroy(new Error('request timeout')));
    req.end();
  });
}

function safeJson(s) {
  try {
    return JSON.parse(s);
  } catch {
    return null;
  }
}

// Wait until the server prints "Listening on" so we know the
// socket is bound. Sniffs stdout for the canonical log line.
async function waitForListen(child) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      cleanup();
      reject(new Error('server did not print "Listening on" within 8s'));
    }, 8000);
    let buf = '';

    function onChunk(chunk) {
      buf += chunk.toString('utf8');
      const m = buf.match(/Listening on http:\/\/127\.0\.0\.1:(\d+)/);
      if (m) {
        cleanup();
        resolve(parseInt(m[1], 10));
      }
    }
    function cleanup() {
      clearTimeout(timer);
      child.stdout.off('data', onChunk);
    }
    child.stdout.on('data', onChunk);
    child.stderr.on('data', () => {
      /* swallow; we don't care about stderr */
    });
  });
}

test('/health is liveness-only and returns 200 regardless of chromium', async (t) => {
  const child = startServer({ ARTLIST_SCRAPER_PORT: String(TEST_PORT) });
  t.after(() => stopServer(child));
  await waitForListen(child);
  const res = await get(TEST_PORT, '/health');
  assert.equal(res.status, 200, 'GET /health must be 200 even without chromium');
  assert.equal(res.json && res.json.ok, true);
  assert.ok(
    typeof res.json.uptime_seconds === 'number',
    '/health must include uptime_seconds'
  );
});

test('/ready fails closed (503 + ok=false) when chromium is missing', async (t) => {
  const child = startServer({
    ARTLIST_SCRAPER_PORT: String(TEST_PORT),
    // Point at a path that doesn't exist so getBrowser() throws.
    CHROME_EXECUTABLE: '/nonexistent/chromium-binary',
  });
  t.after(() => stopServer(child));
  await waitForListen(child);
  const res = await get(TEST_PORT, '/ready');
  // Phase 1 contract: 503 until chromium is genuinely ready.
  assert.equal(
    res.status,
    503,
    'GET /ready must be 503 when chromium is not reachable'
  );
  const j = res.json;
  assert.equal(j && j.ok, false, 'ok must be false');
  assert.equal(j && j.ready, false, 'ready must be false');
  assert.equal(j && j.browser_connected, false, 'browser_connected must be false');
  assert.ok(
    typeof j.error === 'string' && j.error.length > 0,
    'error must be a non-empty string so operators can grep docker logs'
  );
  assert.equal(j && j.port, TEST_PORT, 'port must echo the bound port');
});

test('/ready response shape is JSON with the documented keys', async (t) => {
  const child = startServer({
    ARTLIST_SCRAPER_PORT: String(TEST_PORT),
    CHROME_EXECUTABLE: '/nonexistent/chromium-binary',
  });
  t.after(() => stopServer(child));
  await waitForListen(child);
  const res = await get(TEST_PORT, '/ready');
  const j = res.json;
  // Defends against a regression where someone drops one of the
  // documented keys from /ready without updating docker healthcheck
  // expectations.
  assert.ok(j && typeof j === 'object', 'response must be JSON object');
  for (const k of ['ok', 'ready', 'browser_connected', 'error', 'port']) {
    assert.ok(k in j, `response must include key "${k}"`);
  }
});
