package artlist

// retry_fallback.go is the split-by-capability (Phase 7) CAPABILITY
// DECLARATION file for the "retry/fallback" bucket.
//
// The actual orchestration lives inside SearchService
// (internal/application/assets/providers/artlist/search_service.go):
// when a live search for term T does not yield sufficient results
// from the canonical Artlist source, the chain fans out to
// scraperSearcher → pixabaySearcher → pexelsSearcher based on the
// wired ArtlistSearchStrategy (PR-AUDIT-5, July 2026). Retry
// semantics, back-off timing, and circuit-breaker behavior are
// encapsulated there.
//
// This file exists to:
//  1. Make the retry/fallback surface discoverable in the architecture
//     graph (godlike/07 SSOT for machine-readable ownership).
//  2. Document the 3-Searcher triplet semantics for future
//     cross-component consumers (e.g., new artlist fallthrough
//     providers per PR-AUDIT-5 expansion).
//  3. House any future retry-policy helpers (rate-limit pacers,
//     idempotent-retry keys, fallback-result aggregation contracts).
//
// Current Service facade exposure: Service.Searchers() accessor
// (defined in service_delegates.go) returns the
// (scraper, pixabay, pexels) triplet; composition root wires each
// Searcher port at build_bundles_artlist.go (WireArtlist →
// artlist.NewService(ServiceDeps{ServicePorts: ServicePorts{
// ScraperSearcher: ..., PixabaySearcher: ..., PexelsSearcher: ...}})).
//
// searchStrategy (an ArtlistSearchStrategy string) is the canonical
// user-overridable knob: zero-value defaults to artlist_only (no
// external stock sources without explicit operator opt-in).
//
// Phase 7 manifest: this file marks the canonical location of the
// retry/fallback capability in the split-by-capability refactor of
// service.go (lookup / normalizer / cached search / retry/fallback).
