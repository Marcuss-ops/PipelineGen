// HTTP server entrypoint and route-context wiring for the Artlist scraper.
import http from 'node:http';
import {
  BIND,
  DEFAULT_LIMIT,
  HB_FRESH_WINDOW_MS,
  HB_INTERVAL_MS,
  MAX_LIMIT,
  PORT,
  PROFILE_DIR,
  SEARCH_TIMEOUT_MS,
} from './http-config.js';
import {
  cleanupBrowser,
  createStateAccessors,
  getBrowser,
  runBootWarmup,
  runBrowserPreflight,
  startHeartbeat,
  stopHeartbeat,
} from './http-lifecycle.js';
import { searchArtlistAllPages, searchArtlistGateway } from '../../artlist/gateway-search.js';
import { createCatalogSyncJobManager } from './catalog-sync-job.js';
import { downloadClipVideo } from './download.js';
import { downloadDirectClip } from './download-direct.js';
import { fetchClipDetails } from '../scrape/detail-page.js';
import { computeHealthVerdict } from './health.js';
import { dispatchRequest } from './routes.js';

function createCtx() {
  const catalogSyncJobs = createCatalogSyncJobManager({ syncCatalog: searchArtlistAllPages });
  catalogSyncJobs.startScheduler({
    pollIntervalMs: Number(process.env.ARTLIST_CATALOG_SCHEDULER_POLL_MS) || 60_000,
    incrementalIntervalMs: Number(process.env.ARTLIST_INCREMENTAL_INTERVAL_MS) || 24 * 60 * 60 * 1000,
    reconciliationIntervalMs: Number(process.env.ARTLIST_RECONCILIATION_INTERVAL_MS) || 7 * 24 * 60 * 60 * 1000,
  });
  return {
    config: Object.freeze({
      PORT,
      BIND,
      PROFILE_DIR,
      DEFAULT_LIMIT,
      MAX_LIMIT,
      SEARCH_TIMEOUT_MS,
      HB_INTERVAL_MS,
      HB_FRESH_WINDOW_MS,
    }),
    state: createStateAccessors(),
    deps: Object.freeze({
      getBrowser,
      downloadClipVideo,
      downloadDirectClip,
      fetchClipDetails,
      computeHealthVerdict,
      searchArtlistGateway,
      syncArtlistCatalog: searchArtlistAllPages,
      enqueueArtlistCatalogSync: (input) => catalogSyncJobs.enqueue(input),
      getArtlistCatalogSyncStatus: (syncId) => catalogSyncJobs.getStatus(syncId),
      getArtlistCatalogSyncSchedule: () => catalogSyncJobs.getSchedule(),
      stopArtlistCatalogScheduler: () => catalogSyncJobs.stopScheduler(),
    }),
  };
}

export async function startArtlistServer() {
  runBrowserPreflight();
  await runBootWarmup();
  startHeartbeat();

  const ctx = createCtx();
  const server = http.createServer(async (req, res) => {
    await dispatchRequest(req, res, ctx);
  });

  server.listen(PORT, BIND, () => {
    console.log(`[artlist-server] Listening on http://${BIND}:${PORT} (bind via ARTLIST_SCRAPER_BIND env var)`);
    console.log('[artlist-server] Endpoints: POST /search, POST /v1/clips/search, POST /v1/catalog/sync, POST /detail, POST /download, POST /discover-api, GET /health, GET /v1/health');
    console.log('[artlist-server] Browser will warm up on first request');
  });

  server.on('error', (err) => {
    console.error('[artlist-server] Server error:', err.message);
    process.exit(1);
  });

  process.on('SIGTERM', async () => {
    console.log('[artlist-server] SIGTERM received, closing browser & shutting down...');
    stopHeartbeat();
    ctx.deps.stopArtlistCatalogScheduler();
    await cleanupBrowser();
    server.close(() => process.exit(0));
  });
  process.on('SIGINT', async () => {
    stopHeartbeat();
    ctx.deps.stopArtlistCatalogScheduler();
    await cleanupBrowser();
    server.close(() => process.exit(0));
  });

  return server;
}
