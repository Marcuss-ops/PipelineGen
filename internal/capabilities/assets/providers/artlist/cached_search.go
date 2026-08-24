package artlist

// cached_search.go is the split-by-capability (Phase 7) CAPABILITY
// DECLARATION file for the "cached search" bucket.
//
// The actual implementation lives in search_cache.go as the unexported
// `liveSearchCache` struct (constructor: newLiveSearchCache), which
// implements a two-level (in-memory L1 + SQLite L2) cache for Artlist
// live search results to avoid re-launching Playwright for recently-
// searched terms (PR-ARTLIST-CACHE-L1).
//
// This file exists to:
//  1. Make the capability discoverable in the architecture graph
//     (godlike/07 SSOT for machine-readable ownership).
//  2. Document the cache surface for future cross-component consumers
//     (e.g., a P2 retry-fallback chain that wants to reuse the cache
//     without coupling to the unexported liveSearchCache type).
//  3. House any future capability-level helpers (TTL refresh policies,
//     eviction strategies, scoped-prefix warming for related terms).
//
// Current Service facade exposure: Service.liveCache (unexported
// field, same-package access only). Composition root wires the cache
// implicitly via newLiveSearchCache() inside NewService; persistent
// extensions route through the typed ArtlistSearchCachePort per
// godlike/07 zero-legacy (P0-3, July 2026):
//
//	newPersistentLiveSearchCache(
//	    NewSQLiteArtlistSearchCacheAdapter(db, log),  // returns artlist.ArtlistSearchCachePort
//	    log,
//	)
//
// The two-arg wiring IS the post-migration contract — direct *sql.DB
// usage in the application layer is forbidden by
// scripts/ci-architectural-checks.sh Check 42 + the canonical
// app-sql-imports-allowlist owner + deadline discipline.
//
// Phase 7 manifest: this file marks the canonical location of the
// cached-search capability in the split-by-capability refactor of
// service.go (lookup / normalizer / cached search / retry/fallback).
