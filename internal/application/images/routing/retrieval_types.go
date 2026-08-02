// Package routing — retrieval_types.go hosts the shared DTOs
// that the retrieved subpackage's SearchAll signature uses.
//
// Per the FASE 8 (image-territories action plan, July 2026)
// cycle-break design: the canonical home of these types is the
// routing package because routing's `RetrievalSearchBackend`
// port references them. Owning the type at the port-side avoids
// the routing → retrieved import edge that completes the
// pre-FASE-8 cycle.
//
// The retrieved subpackage continues to import routing (one-way
// dependency) and references routing.RetrievalSearchOptions /
// routing.RetrievalSearchResult in its `Search` / `SearchAll`
// signatures. Concrete providers (WikipediaProvider, SearXNGProvider,
// DuckDuckGoProvider, DriveImageProvider) live in the retrieved
// package and use these types via the package import.
package routing

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// RetrievalSearchOptions are the per-call options that control how
// each RetrievalProvider executes a query. Carried instead of inline
// parameters so providers remain signature-stable when new options
// are added.
//
// FASE 8 (July 2026): relocated from internal/application/images/retrieved
// to break the routing↔retrieved import cycle. Wire shape unchanged
// (struct fields, JSON tags) so on-wire callers and YAML files keep
// working.
type RetrievalSearchOptions struct {
	// Lang is the BCP-47 language tag used by Wikipedia/SearXNG/etc.
	// Empty means the provider default ("en").
	Lang string
	// Limit caps the number of details returned per provider (0 = no cap).
	Limit int
	// Timeout is the per-provider HTTP round-trip timeout (0 = use default 10s).
	Timeout time.Duration
}

// RetrievalSearchResult is a single candidate image produced by a
// provider. PreviewURL is the source image URL; the storage layer
// is responsible for downloading + ingesting it into media_assets.
// Provider, License, Author are populated from provider-specific
// knowledge (e.g. Wikipedia → CC-BY-SA-4.0).
//
// FASE 8 (July 2026): relocated from internal/application/images/retrieved
// to break the routing↔retrieved import cycle. Wire shape unchanged.
type RetrievalSearchResult struct {
	Provider   asset.ImageProvider
	Origin     asset.ImageOrigin
	PreviewURL string
	PageURL    string
	Width      int
	Height     int
	Title      string
	License    string
	Author     string
	// StyleID is reserved for Step 9 (ImageAsset.Style) and is empty
	// for all current retrieval providers.
	StyleID string
}
