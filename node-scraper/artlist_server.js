#!/usr/bin/env node
// artlist_server.js — root entrypoint for the Artlist scraper HTTP server.
//
// THIN WRAPPER. The original 489-line monolith was split in the
// `chore(node-scraper): introduce src/{driver,scrape,server}` refactor
// so the server bucket has one file per concern:
//
//   src/server/http.js   — bootstrap, browser lifecycle, process signals,
//                          port-bind, env config, ctx assembly.
//   src/server/routes.js — per-endpoint handlers / dispatch (pure of I/O,
//                          driven by the ctx object that http.js builds).
//   src/server/download.js, src/server/health.js — already existed (clip
//                          download + composite health verdict).
//
// CLI invocation preserved exactly:
//   node artlist_server.js        ← historical (back-compat)
//   npx artlist-server / bin/     ← canonical (future)
//
// Endpoints unchanged: POST /search, POST /download, GET /health.
// Env vars unchanged: PORT, ARTLIST_SCRAPER_PORT, ARTLIST_SCRAPER_BIND,
// CHROME_PROFILE_DIR, SCRAPER_CONNECT_TIMEOUT_SECONDS, SCROLL_TIMEOUT,
// BROWSER_WS, LIGHTPANDA_WS, CHROME_WS, CHROME_EXECUTABLE.
import { startArtlistServer } from './src/server/http.js';

await startArtlistServer();
