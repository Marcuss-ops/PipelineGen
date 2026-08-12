import { MAX_SEARCH_BODY_BYTES, isArtlistRateLimitedError, readBody, rejectIfNotMethod } from './route-utils.js';

export async function handleV1ClipSearch(req, res, ctx) {
  if (rejectIfNotMethod(req, res, 'POST', '/v1/clips/search')) return;

  let body;
  try {
    body = await readBody(req, MAX_SEARCH_BODY_BYTES);
  } catch (err) {
    res.writeHead(err.statusCode || 400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: err.message }));
    return;
  }

  let payload;
  try {
    payload = JSON.parse(body);
  } catch {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'Invalid JSON' }));
    return;
  }

  const query = String(payload.query || payload.term || '').trim();
  if (!query) {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'query is required' }));
    return;
  }

  const page = Number.parseInt(payload.page || 1, 10);
  const limit = Number.parseInt(payload.limit || ctx.config.DEFAULT_LIMIT, 10);
  const filters = payload.filters && typeof payload.filters === 'object' ? payload.filters : {};
  const forceRefresh = Boolean(payload.force_refresh || payload.forceRefresh);

  const reqId = ctx.state.incRequest();
  ctx.state.setLastSearchAt(new Date().toISOString());
  const t0 = Date.now();
  const searchTimeoutMs = Number.isFinite(ctx.config.SEARCH_TIMEOUT_MS) && ctx.config.SEARCH_TIMEOUT_MS > 0
    ? ctx.config.SEARCH_TIMEOUT_MS
    : 120_000;
  let searchTimeoutTimer = null;
  const searchBudget = new Promise((_, reject) => {
    searchTimeoutTimer = setTimeout(() => {
      const err = new Error(`Artlist search budget ${Math.ceil(searchTimeoutMs / 1000)}s exceeded`);
      err.code = 'ARTLIST_SEARCH_TIMEOUT';
      reject(err);
    }, searchTimeoutMs);
  });

  try {
    const job = (async () => {
      const browser = await ctx.deps.getBrowser();
      return ctx.deps.searchArtlistGateway({
        browser,
        query,
        page,
        limit,
        filters,
        forceRefresh,
        profileDir: ctx.config.PROFILE_DIR,
      });
    })();
    const result = await Promise.race([job, searchBudget]);

    if (typeof ctx.state.setLastLaunchError === 'function') {
      ctx.state.setLastLaunchError(null);
    }

    const elapsed = Date.now() - t0;
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      ...result,
      _meta: { request_id: reqId, elapsed_ms: elapsed },
    }));
  } catch (err) {
    const elapsed = Date.now() - t0;
    if (err && err.code === 'SESSION_EXPIRED') {
      if (typeof ctx.state.setLastLaunchError === 'function') {
        ctx.state.setLastLaunchError(err.message || 'SESSION_EXPIRED');
      }
      res.writeHead(503, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({
        ok: false,
        error: 'SESSION_EXPIRED',
        detail: err.message || String(err),
        _meta: { request_id: reqId, elapsed_ms: elapsed },
      }));
      return;
    }

    const status = isArtlistRateLimitedError(err) ? 429 : 500;
    const responseStatus = err && err.code === 'ARTLIST_SEARCH_TIMEOUT' ? 504 : status;
    res.writeHead(responseStatus, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      ok: false,
      error: err.code || err.message || String(err),
      _meta: { request_id: reqId, elapsed_ms: elapsed },
    }));
  } finally {
    if (searchTimeoutTimer) clearTimeout(searchTimeoutTimer);
  }
}

// ─── /download ───────────────────────────────────────────────────────────────
