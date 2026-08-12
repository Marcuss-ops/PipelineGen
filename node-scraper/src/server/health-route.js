export function handleHealth(req, res, ctx) {
  const { healthy } = ctx.deps.computeHealthVerdict({
    browser: ctx.state.globalBrowser,
    lastLaunchError: ctx.state.lastLaunchError,
    lastSessionAliveAt: ctx.state.lastSessionAliveAt,
    freshnessWindowMs: ctx.config.HB_FRESH_WINDOW_MS,
  });
  const payload = {
    ok: healthy,
    healthy,
    uptime_seconds: Math.floor(process.uptime()),
    requests_served: ctx.state.requestCount,
    started_at: ctx.state.startedAt,
    port: ctx.config.PORT,
    browser_running: ctx.state.globalBrowser !== null,
    browser_pid: ctx.state.globalBrowserPid,
    last_search_at: ctx.state.lastSearchAt,
    last_session_alive_at: ctx.state.lastSessionAliveAt,
    last_launch_error: ctx.state.lastLaunchError,
  };
  res.writeHead(healthy ? 200 : 503, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(payload));
}

// ─── Dispatch ─────────────────────────────────────────────────────────────────
// Top-level routing by URL pathname. Anything not matching a known
// endpoint returns 404 with a JSON error envelope — operators can
// grep for `Unknown path:` in `docker logs` to spot misrouted traffic.
