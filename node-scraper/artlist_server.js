import http from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { openBrowser } from './src/artlist/browser.js';
import { searchArtlist } from './artlist_search.js';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// ─── Config ──────────────────────────────────────────────────────────────────
const PORT = parseInt(process.env.ARTLIST_SCRAPER_PORT || '9123', 10);
const PROFILE_DIR = process.env.CHROME_PROFILE_DIR || '';
const DEFAULT_LIMIT = 8;
const MAX_LIMIT = 50;

// ─── State ────────────────────────────────────────────────────────────────────
let requestCount = 0;
const startedAt = new Date().toISOString();
let globalBrowser = null;
let globalBrowserConnected = false;

// ─── Browser Lifecycle ────────────────────────────────────────────────────────
async function getBrowser() {
  if (globalBrowser) {
    try {
      // Check if browser is still responsive
      await globalBrowser.version();
      return globalBrowser;
    } catch {
      console.warn('[artlist-server] Browser disconnected or dead, restarting...');
      await cleanupBrowser();
    }
  }

  console.log('[artlist-server] Launching persistent Chromium browser...');
  const { browser, connected } = await openBrowser(PROFILE_DIR);
  globalBrowser = browser;
  globalBrowserConnected = connected;
  return globalBrowser;
}

async function cleanupBrowser() {
  if (globalBrowser) {
    try {
      if (globalBrowserConnected && globalBrowser.disconnect) {
        await globalBrowser.disconnect();
      } else if (globalBrowser.close) {
        await globalBrowser.close();
      }
    } catch (err) {
      console.error('[artlist-server] Error closing browser:', err.message);
    } finally {
      globalBrowser = null;
      globalBrowserConnected = false;
    }
  }
}

// ─── Search Runner with persistent browser ─────────────────────────────────────
async function runSearchWithPersistentBrowser(term, limit) {
  const browser = await getBrowser();
  // Create page inside its own transient context for cookie/session isolation
  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  await page.setViewport({ width: 1440, height: 900 });
  await page.setUserAgent('Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36');

  // We temporarily patch or call searchArtlist logic using our open context/page.
  // Since searchArtlist calls createBrowserPage internally, we can temporarily bypass it 
  // or pass a custom browser handle, or simply run the search script but reuse page.
  // Let's modify searchArtlist to accept an optional existing browser/page handle,
  // or write a lightweight wrapper. Let's look at searchArtlist in artlist_search.js first
  // to see if we can adapt it or pass the browser.
}

// ─── Request handler ──────────────────────────────────────────────────────────
async function handleSearch(req, res) {
  if (req.method !== 'POST') {
    res.writeHead(405, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'Method not allowed, use POST /search' }));
    return;
  }

  let body = '';
  for await (const chunk of req) {
    body += chunk;
    if (body.length > 8192) {
      res.writeHead(413, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ ok: false, error: 'Request too large' }));
      return;
    }
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

  const limit = Math.min(Math.max(parseInt(payload.limit || DEFAULT_LIMIT, 10), 1), MAX_LIMIT);
  requestCount++;
  const reqId = requestCount;

  console.log(`[${new Date().toISOString()}] #${reqId} SEARCH term="${term}" limit=${limit}`);
  const t0 = Date.now();

  try {
    const browser = await getBrowser();
    
    // Instead of launching a new browser inside searchArtlist, let's call the search.
    // However, searchArtlist is hardcoded to do createBrowserPage. 
    // We will export a modified version of searchArtlist, or patch it to accept custom browser page handle.
    // Let's look at artlist_search.js to make it support passing an existing page or browser.
    const result = await searchArtlist(term, limit, PROFILE_DIR, browser);
    const elapsed = Date.now() - t0;
    console.log(`[${new Date().toISOString()}] #${reqId} DONE ${result.clips.length} clips in ${elapsed}ms`);

    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      ok: true,
      term: result.term,
      search_url: result.search_url,
      clips: result.clips,
      saved: 0,
      _meta: { request_id: reqId, elapsed_ms: elapsed },
    }));
  } catch (err) {
    const elapsed = Date.now() - t0;
    console.error(`[${new Date().toISOString()}] #${reqId} ERROR after ${elapsed}ms:`, err.message);
    res.writeHead(500, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: err.message || String(err) }));
  }
}

function handleHealth(req, res) {
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({
    ok: true,
    uptime_seconds: Math.floor(process.uptime()),
    requests_served: requestCount,
    started_at: startedAt,
    port: PORT,
    browser_running: globalBrowser !== null,
  }));
}

// ─── HTTP server ──────────────────────────────────────────────────────────────
const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`);
  if (url.pathname === '/search') {
    await handleSearch(req, res);
  } else if (url.pathname === '/health') {
    handleHealth(req, res);
  } else {
    res.writeHead(404, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: `Unknown path: ${url.pathname}` }));
  }
});

server.listen(PORT, '127.0.0.1', () => {
  console.log(`[artlist-server] Listening on http://127.0.0.1:${PORT}`);
  console.log(`[artlist-server] Endpoints: POST /search, GET /health`);
  console.log(`[artlist-server] Browser will warm up on first request`);
});

server.on('error', (err) => {
  console.error('[artlist-server] Server error:', err.message);
  process.exit(1);
});

// Graceful shutdown
process.on('SIGTERM', async () => {
  console.log('[artlist-server] SIGTERM received, closing browser & shutting down...');
  await cleanupBrowser();
  server.close(() => process.exit(0));
});
process.on('SIGINT', async () => {
  await cleanupBrowser();
  server.close(() => process.exit(0));
});
