// node-scraper/src/artlist/health.js
//
// PR-HEALTHCHECK-FAILFAST (July 2026, P2) — pure-function health
// verdict helper. The /health handler in artlist_server.js delegates
// to computeHealthVerdict() so this logic lives in one place
// (AGENTS.md godlike/06 SSOT) and is pure-function-testable from
// node-scraper/test/browser.test.mjs without spawning the actual
// server.
//
// Healthy predicate (exactly the operator spec from the P2 ticket):
//
//   healthy := browser_running            // puppeteer.browser is non-null
//          && last_launch_error === null // runtime hasn't recorded a failure
//          && recentSessionAlive        // now - lastSessionAliveAt <= freshness window
//
// The mapping to HTTP is handled by the caller (artlist_server.js):
//   healthy=true  → 200 OK
//   healthy=false → 503 Service Unavailable (Docker HEALTHCHECK
//                    curl -f catches this and the container's
//                    restart: unless-stopped policy replaces the
//                    stale process).

/**
 * Compute the scraper's health verdict.
 *
 * @param {object} input - All state required for the verdict.
 * @param {object|null} input.browser - Puppeteer browser handle (or null).
 * @param {string|null} input.lastLaunchError - Last launch error message (or null on clean state).
 * @param {string|null} input.lastSessionAliveAt - ISO timestamp of the last successful browser.version() (or null if never checked).
 * @param {number} [input.now] - Reference timestamp for "recent" computation (defaults to Date.now(); hookable for testability).
 * @param {number} [input.freshnessWindowMs] - Maximum grace period before `lastSessionAliveAt` is treated as stale (defaults to 60000ms = 60s).
 * @returns {{healthy: boolean, browserRunning: boolean, recentSessionAlive: boolean}}
 *   - healthy: composite verdict the caller maps to HTTP 200/503.
 *   - browserRunning: mirror of (browser != null). Surfaces in logs without forcing caller to re-derive.
 *   - recentSessionAlive: mirror of the freshness-window check. Surface so operators can read whether the heartbeat is current without inspecting the timestamp directly.
 */
export function computeHealthVerdict({
  browser,
  lastLaunchError,
  lastSessionAliveAt,
  now,
  freshnessWindowMs,
}) {
  const refNow = typeof now === 'number' ? now : Date.now();
  const windowMs = typeof freshnessWindowMs === 'number' ? freshnessWindowMs : 60_000;

  const browserRunning = browser !== null && browser !== undefined;
  const recentSessionAlive =
    typeof lastSessionAliveAt === 'string' &&
    lastSessionAliveAt.length > 0 &&
    Number.isFinite(Date.parse(lastSessionAliveAt)) &&
    refNow - Date.parse(lastSessionAliveAt) <= windowMs;
  const healthy = browserRunning && !lastLaunchError && recentSessionAlive;
  return { healthy, browserRunning, recentSessionAlive };
}
