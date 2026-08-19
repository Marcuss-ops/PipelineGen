import { MAX_SEARCH_BODY_BYTES, isArtlistRateLimitedError, readBody, rejectIfNotMethod } from './route-utils.js';

export async function handleSearch(req, res, ctx) {
  if (rejectIfNotMethod(req, res, 'POST', '/search')) return;

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

  const term = (payload.term || '').trim();
  if (!term) {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'Missing required field: term' }));
    return;
  }

  const { DEFAULT_LIMIT, MAX_LIMIT } = ctx.config;
  const limit = Math.min(Math.max(parseInt(payload.limit || DEFAULT_LIMIT, 10), 1), MAX_LIMIT);
  const reqId = ctx.state.incRequest();
  // PR-HEALTHCHECK-FAILFAST (P2, July 2026): record every /search
  // request arrival (success or failure) so /health.last_search_at
  // reflects upstream-routing health independently of /search
  // outcome. Bugs like pipelinegen-server routing Artlist calls
  // elsewhere (and the scraper going cold-quiet) become visible
  // immediately rather than surfacing as "scraper deadlock".
  ctx.state.setLastSearchAt(new Date().toISOString());

  const connectTimeoutSeconds = parseInt(process.env.SCRAPER_CONNECT_TIMEOUT_SECONDS || '5', 10);
  const scrollTimeoutSeconds  = parseInt(process.env.SCROLL_TIMEOUT                  || '120', 10);
  console.log(`[${new Date().toISOString()}] #${reqId} SEARCH term="${term}" limit=${limit} BUDGET connect=${connectTimeoutSeconds}s total=${scrollTimeoutSeconds}s`);
  const t0 = Date.now();
  const scrollTimeoutMs = scrollTimeoutSeconds * 1000;
  let totalBudgetTimer = null;
  const totalBudget = new Promise((_, reject) => {
    totalBudgetTimer = setTimeout(
      () => reject(new Error(`scraper total budget ${scrollTimeoutSeconds}s exceeded (SCROLL_TIMEOUT env var)`)),
      scrollTimeoutMs,
    );
  });

  try {
    const job = ctx.deps.searchArtlistGateway({
      query: term,
      page: 1,
      limit,
      mode: 'live_required',
      forceRefresh: true,
      logger: console,
    });
    const result = await Promise.race([job, totalBudget]);
    const elapsed = Date.now() - t0;
    console.log(`[${new Date().toISOString()}] #${reqId} DONE ${result.clips.length} clips in ${elapsed}ms`);

    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      ok: true,
      term: result.term,
      search_url: result.search_url,
      clips: result.clips,
      diagnostics: result.diagnostics,
      saved: 0,
      _meta: { request_id: reqId, elapsed_ms: elapsed },
    }));
  } catch (err) {
    const elapsed = Date.now() - t0;
    console.error(`[${new Date().toISOString()}] #${reqId} ERROR after ${elapsed}ms:`, err.message);
    const status = isArtlistRateLimitedError(err) ? 429 : 500;
    res.writeHead(status, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      ok: false,
      error: err.code || err.message || String(err),
      diagnostics: err.diagnostics,
    }));
  } finally {
    // §11.0 SCROLL_TIMEOUT budget backstop: cancel the timer on every code
    // path (success / budget-exceeded / unexpected throw). Without the
    // finally, a failed search would keep the timer alive up to
    // scrollTimeoutSeconds (handle leak per failed request).
    if (totalBudgetTimer) clearTimeout(totalBudgetTimer);
  }
}

// ─── /v1/clips/search ────────────────────────────────────────────────────────
