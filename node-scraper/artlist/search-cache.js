// Artlist search cache (SQLite-backed) with catalog sync, query relationships
// and durable search responses.
//
// The implementation lives in sibling files — each physical file stays under
// 400 lines while this module keeps the public surface and the single
// ArtlistSearchCache class:
//   search-cache-util.js     — constants + pure helpers (shared, exported)
//   search-cache-db.js       — ensureDatabase (schema/migrations)
//   search-cache-catalog.js  — upsertCatalog + catalog sync page recording
//   search-cache-sync.js     — sync lifecycle (complete/fail/resume/backfill)
//   search-cache-schedule.js — scheduled catalog-sync runs
//   search-cache-query.js    — search/query/get/put/delete surface
import {
  DEFAULT_DB_PATH,
  DEFAULT_TTL_MS,
} from './search-cache-util.js';
import { searchCacheCatalogMethods } from './search-cache-catalog.js';
import { searchCacheDbMethods } from './search-cache-db.js';
import { searchCacheQueryMethods } from './search-cache-query.js';
import { searchCacheScheduleMethods } from './search-cache-schedule.js';
import { searchCacheSyncMethods } from './search-cache-sync.js';

export {
  buildSearchCacheKey,
  buildSearchQueryKey,
  isTransientArtlistUrl,
} from './search-cache-util.js';

class ArtlistSearchCache {
  constructor(dbPath = DEFAULT_DB_PATH, ttlMs = DEFAULT_TTL_MS) {
    this.dbPath = dbPath;
    this.ttlMs = ttlMs;
    this.items = new Map();
    this.db = null;
    this.ensureDatabase();
  }
}

// Methods are grouped by concern across the sibling files above and merged
// onto the prototype so the class keeps one identity and `this` semantics.
Object.assign(
  ArtlistSearchCache.prototype,
  searchCacheDbMethods,
  searchCacheCatalogMethods,
  searchCacheSyncMethods,
  searchCacheScheduleMethods,
  searchCacheQueryMethods,
);

let sharedCache = null;

export function getSearchCache() {
  if (!sharedCache) {
    sharedCache = new ArtlistSearchCache();
  }
  return sharedCache;
}

export function createSearchCache(dbPath, ttlMs) {
  return new ArtlistSearchCache(dbPath, ttlMs);
}
