// Package retrieved — provider_registry.go declares Step 8's
// RetrievalProvider interface and the canonical provider list for
// the retrieved-image territory.
//
// Per the July 2026 image-restructuring plan, retrieval sources fall
// into four named providers per the ImageProvider taxonomy in
// internal/kernel/asset/image_taxonomy.go:
//
//   - Wikipedia  (provider.ProviderWikipedia)
//   - SearXNG    (provider.ProviderSearXNG)
//   - DuckDuckGo (provider.ProviderDuckDuckGo)
//   - Drive      (provider.ProviderDrive)
//
// Each provider owns one network/disk round-trip for a given query.
// The RetrievalProviderRegistry composes them in fallback order and
// exposes SearchAll — so callers can request a query once and let
// the registry orchestrate the Wikipedia → SearXNG → DuckDuckGo →
// Drive fallback chain. Step 8 replaces the imperative if-cascade
// in storage_search.go with this registry.
//
// FASE 8 (July 2026): the per-call DTOs (RetrievalSearchOptions +
// RetrievalSearchResult) moved to internal/application/images/routing
// to break the routing↔retrieved import cycle. The retrieved
// subpackage keeps the concrete provider implementations and the
// registry; provider Search methods now accept routing types directly.
//
// File layout (PR-IMG-SPLIT-3, July 2026):
//
//	provider.go              — RetrievalProvider interface + httpDoer
//	storage_bridge.go        — StorageBridge interface
//	provider_wikipedia.go    — WikipediaProvider
//	provider_searxng.go      — SearXNGProvider
//	provider_duckduckgo.go   — DuckDuckGoProvider
//	provider_drive.go        — DriveImageProvider
//	provider_registry.go     — RetrievalProviderRegistry
package retrieved
