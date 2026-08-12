import { startApiDiscovery } from '../scrape/api-discovery.js';
import { MAX_DISCOVERY_BODY_BYTES, readBody, rejectIfNotMethod } from './route-utils.js';

export async function handleDiscoverApi(req, res, ctx) {
  if (rejectIfNotMethod(req, res, 'POST', '/discover-api')) return;

  let body;
  try {
    body = await readBody(req, MAX_DISCOVERY_BODY_BYTES);
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

  const term = String(payload.term || '').trim();
  if (!term) {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'term is required' }));
    return;
  }

  const reqId = ctx.state.incRequest();
  const t0 = Date.now();
  const browser = await ctx.deps.getBrowser();
  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  const discovery = startApiDiscovery(page);

  try {
    const searchUrl = `https://artlist.io/stock-footage/search?terms=${encodeURIComponent(term)}`;
    await page.goto(searchUrl, {
      waitUntil: 'networkidle2',
      timeout: 120_000,
    });
    await new Promise((resolve) => setTimeout(resolve, 1500));

    const requests = discovery.stop();
    const elapsed = Date.now() - t0;

    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      ok: true,
      term,
      requests,
      _meta: { request_id: reqId, elapsed_ms: elapsed },
    }));
  } catch (err) {
    const elapsed = Date.now() - t0;
    res.writeHead(500, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      ok: false,
      error: err.message || String(err),
      _meta: { request_id: reqId, elapsed_ms: elapsed },
    }));
  } finally {
    await page.close().catch(() => {});
    await context.close().catch(() => {});
  }
}

// ─── /search ──────────────────────────────────────────────────────────────────
